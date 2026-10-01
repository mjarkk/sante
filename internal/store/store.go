package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS check_results (
	id         INTEGER PRIMARY KEY,
	monitor_id TEXT    NOT NULL,
	checked_at INTEGER NOT NULL,
	up         INTEGER NOT NULL,
	latency_ms REAL    NOT NULL,
	message    TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS check_results_monitor_time ON check_results (monitor_id, checked_at);
CREATE INDEX IF NOT EXISTS check_results_time ON check_results (checked_at);

CREATE TABLE IF NOT EXISTS daily_stats (
	monitor_id     TEXT    NOT NULL,
	day            TEXT    NOT NULL,
	checks         INTEGER NOT NULL,
	failures       INTEGER NOT NULL,
	downtime_ms    INTEGER NOT NULL,
	latency_sum_ms REAL    NOT NULL,
	PRIMARY KEY (monitor_id, day)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS monitor_state (
	monitor_id TEXT PRIMARY KEY,
	checked_at INTEGER NOT NULL,
	up         INTEGER NOT NULL,
	latency_ms REAL    NOT NULL,
	message    TEXT    NOT NULL,
	changed_at INTEGER NOT NULL
) WITHOUT ROWID;
`

const maxMessageLen = 500

// Store is safe for concurrent use.
type Store struct {
	write *sql.DB
	read  *sql.DB
	loc   *time.Location
	path  string
}

type Result struct {
	MonitorID string
	CheckedAt time.Time
	Up        bool
	Latency   time.Duration
	Message   string
}

type State struct {
	Result
	// ChangedAt is when the monitor last went from up to down or back.
	ChangedAt time.Time
}

type Day struct {
	Day        string
	Checks     int
	Failures   int
	Downtime   time.Duration
	AvgLatency time.Duration
}

// Open creates the database file and its directory if needed. Days are
// bucketed in loc.
func Open(path string, loc *time.Location) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := func(extra string) string {
		return "file:" + (&url.URL{Path: path}).EscapedPath() +
			"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)" + extra
	}
	write, err := sql.Open("sqlite", dsn("&_txlock=immediate"))
	if err != nil {
		return nil, err
	}
	write.SetMaxOpenConns(1)
	if _, err := write.Exec(schema); err != nil {
		write.Close()
		return nil, fmt.Errorf("creating schema in %s: %w", path, err)
	}
	read, err := sql.Open("sqlite", dsn("&_pragma=query_only(1)"))
	if err != nil {
		write.Close()
		return nil, err
	}
	read.SetMaxOpenConns(4)
	return &Store{write: write, read: read, loc: loc, path: path}, nil
}

func (s *Store) Close() error {
	return errors.Join(s.read.Close(), s.write.Close())
}

func (s *Store) Path() string { return s.path }

func (s *Store) Size() int64 {
	var total int64
	for _, suffix := range []string{"", "-wal"} {
		if fi, err := os.Stat(s.path + suffix); err == nil {
			total += fi.Size()
		}
	}
	return total
}

func (s *Store) Ping(ctx context.Context) error {
	var one int
	return s.read.QueryRowContext(ctx, "SELECT 1").Scan(&one)
}

// DayKey is the calendar day t falls on in the store's time zone.
func (s *Store) DayKey(t time.Time) string {
	return t.In(s.loc).Format(time.DateOnly)
}

// Record stores r and folds it into the daily rollup and current state. A
// failed result adds interval to that day's downtime.
func (s *Store) Record(ctx context.Context, r Result, interval time.Duration) error {
	return s.RecordBatch(ctx, []Result{r}, interval)
}

// RecordBatch records results in one transaction, as Record does each. They
// must be in chronological order per monitor for the current state to be right.
func (s *Store) RecordBatch(ctx context.Context, results []Result, interval time.Duration) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	insert, err := tx.PrepareContext(ctx,
		`INSERT INTO check_results (monitor_id, checked_at, up, latency_ms, message) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	daily, err := tx.PrepareContext(ctx, `
		INSERT INTO daily_stats (monitor_id, day, checks, failures, downtime_ms, latency_sum_ms)
		VALUES (?, ?, 1, ?, ?, ?)
		ON CONFLICT (monitor_id, day) DO UPDATE SET
			checks = checks + 1,
			failures = failures + excluded.failures,
			downtime_ms = downtime_ms + excluded.downtime_ms,
			latency_sum_ms = latency_sum_ms + excluded.latency_sum_ms`)
	if err != nil {
		return err
	}
	state, err := tx.PrepareContext(ctx, `
		INSERT INTO monitor_state (monitor_id, checked_at, up, latency_ms, message, changed_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (monitor_id) DO UPDATE SET
			checked_at = excluded.checked_at,
			up = excluded.up,
			latency_ms = excluded.latency_ms,
			message = excluded.message,
			changed_at = CASE WHEN monitor_state.up = excluded.up THEN monitor_state.changed_at ELSE excluded.checked_at END`)
	if err != nil {
		return err
	}

	for _, r := range results {
		if len(r.Message) > maxMessageLen {
			r.Message = r.Message[:maxMessageLen]
		}
		at := r.CheckedAt.UnixMilli()
		latency := float64(r.Latency.Microseconds()) / 1000
		var failures, downtime int64
		var latencySum float64
		if r.Up {
			latencySum = latency
		} else {
			failures, downtime = 1, interval.Milliseconds()
		}
		if _, err := insert.ExecContext(ctx, r.MonitorID, at, r.Up, latency, r.Message); err != nil {
			return err
		}
		if _, err := daily.ExecContext(ctx, r.MonitorID, s.DayKey(r.CheckedAt), failures, downtime, latencySum); err != nil {
			return err
		}
		if _, err := state.ExecContext(ctx, r.MonitorID, at, r.Up, latency, r.Message, at); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) HasResults(ctx context.Context) (bool, error) {
	var exists bool
	err := s.read.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM check_results)`).Scan(&exists)
	return exists, err
}

// Delete removes all results, rollups and state of the given monitors.
func (s *Store) Delete(ctx context.Context, monitorIDs ...string) error {
	tx, err := s.write.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"check_results", "daily_stats", "monitor_state"} {
		for _, id := range monitorIDs {
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE monitor_id = ?`, id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Prune deletes results checked before cutoff and daily rollups for days before cutoff's day.
func (s *Store) Prune(ctx context.Context, cutoff time.Time) (int64, error) {
	res, err := s.write.ExecContext(ctx, `DELETE FROM check_results WHERE checked_at < ?`, cutoff.UnixMilli())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := s.write.ExecContext(ctx, `DELETE FROM daily_stats WHERE day < ?`, s.DayKey(cutoff)); err != nil {
		return n, err
	}
	return n, nil
}

func (s *Store) States(ctx context.Context) (map[string]State, error) {
	rows, err := s.read.QueryContext(ctx,
		`SELECT monitor_id, checked_at, up, latency_ms, message, changed_at FROM monitor_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := map[string]State{}
	for rows.Next() {
		var st State
		var at, changed int64
		var latency float64
		if err := rows.Scan(&st.MonitorID, &at, &st.Up, &latency, &st.Message, &changed); err != nil {
			return nil, err
		}
		st.CheckedAt, st.ChangedAt, st.Latency = time.UnixMilli(at), time.UnixMilli(changed), fromMillis(latency)
		states[st.MonitorID] = st
	}
	return states, rows.Err()
}

// Days returns the daily rollups from fromDay (inclusive) onwards, keyed by monitor id.
func (s *Store) Days(ctx context.Context, fromDay string) (map[string][]Day, error) {
	rows, err := s.read.QueryContext(ctx, `
		SELECT monitor_id, day, checks, failures, downtime_ms, latency_sum_ms
		FROM daily_stats WHERE day >= ? ORDER BY monitor_id, day`, fromDay)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	days := map[string][]Day{}
	for rows.Next() {
		var id string
		var d Day
		var downtime int64
		var latencySum float64
		if err := rows.Scan(&id, &d.Day, &d.Checks, &d.Failures, &downtime, &latencySum); err != nil {
			return nil, err
		}
		d.Downtime = time.Duration(downtime) * time.Millisecond
		if ok := d.Checks - d.Failures; ok > 0 {
			d.AvgLatency = fromMillis(latencySum / float64(ok))
		}
		days[id] = append(days[id], d)
	}
	return days, rows.Err()
}

type Uptime struct {
	Since    time.Time
	Checks   int
	Failures int
}

// Uptimes counts checks and failures of one monitor since each of the given times.
func (s *Store) Uptimes(ctx context.Context, monitorID string, since ...time.Time) ([]Uptime, error) {
	out := make([]Uptime, len(since))
	if len(since) == 0 {
		return out, nil
	}
	oldest := since[0]
	query := "SELECT "
	args := []any{}
	for i, t := range since {
		out[i].Since = t
		if t.Before(oldest) {
			oldest = t
		}
		if i > 0 {
			query += ", "
		}
		query += "COALESCE(SUM(checked_at >= ?), 0), COALESCE(SUM(checked_at >= ? AND up = 0), 0)"
		args = append(args, t.UnixMilli(), t.UnixMilli())
	}
	query += " FROM check_results WHERE monitor_id = ? AND checked_at >= ?"
	args = append(args, monitorID, oldest.UnixMilli())

	dest := make([]any, 0, 2*len(since))
	for i := range out {
		dest = append(dest, &out[i].Checks, &out[i].Failures)
	}
	return out, s.read.QueryRowContext(ctx, query, args...).Scan(dest...)
}

// Results returns up to limit results of one monitor checked before the given
// time, newest first.
func (s *Store) Results(ctx context.Context, monitorID string, before time.Time, failuresOnly bool, limit int) ([]Result, error) {
	query := `SELECT checked_at, up, latency_ms, message FROM check_results WHERE monitor_id = ? AND checked_at < ?`
	if failuresOnly {
		query += ` AND up = 0`
	}
	query += ` ORDER BY checked_at DESC LIMIT ?`
	return s.results(ctx, monitorID, query, monitorID, before.UnixMilli(), limit)
}

// Between returns the results of one monitor checked in [from, to), oldest first.
func (s *Store) Between(ctx context.Context, monitorID string, from, to time.Time) ([]Result, error) {
	return s.results(ctx, monitorID, `
		SELECT checked_at, up, latency_ms, message FROM check_results
		WHERE monitor_id = ? AND checked_at >= ? AND checked_at < ? ORDER BY checked_at`,
		monitorID, from.UnixMilli(), to.UnixMilli())
}

func (s *Store) results(ctx context.Context, monitorID, query string, args ...any) ([]Result, error) {
	rows, err := s.read.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Result
	for rows.Next() {
		r := Result{MonitorID: monitorID}
		var at int64
		var latency float64
		if err := rows.Scan(&at, &r.Up, &latency, &r.Message); err != nil {
			return nil, err
		}
		r.CheckedAt, r.Latency = time.UnixMilli(at), fromMillis(latency)
		out = append(out, r)
	}
	return out, rows.Err()
}

func fromMillis(ms float64) time.Duration {
	return time.Duration(ms * float64(time.Millisecond))
}
