package web

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/back-to-code/sante/internal/check"
	"github.com/back-to-code/sante/internal/config"
	"github.com/back-to-code/sante/internal/store"
)

const days = int(config.Retention / (24 * time.Hour))

type level string

const (
	levelOK      level = "ok"
	levelMinor   level = "minor"
	levelPartial level = "partial"
	levelMajor   level = "major"
	levelNone    level = "none"
)

func (l level) Label() string {
	switch l {
	case levelOK:
		return "No downtime"
	case levelMinor:
		return "Minor disruption"
	case levelPartial:
		return "Partial outage"
	case levelMajor:
		return "Major outage"
	}
	return "No data"
}

func (l level) Icon() string {
	switch l {
	case levelOK:
		return "check-bold"
	case levelMinor, levelPartial:
		return "priority"
	case levelMajor:
		return "close-bold"
	}
	return "schedule"
}

// Shapes is the sequence a status hero morphs through.
func (l level) Shapes() string {
	switch l {
	case levelOK:
		return "cookie12 flower sunny"
	case levelMinor, levelPartial:
		return "cookie9 clover4 burst"
	case levelMajor:
		return "burst softburst square"
	}
	return "puffy circle"
}

// Badge is the still shape beside a monitor or component.
func (l level) Badge() string {
	switch l {
	case levelOK:
		return "cookie9"
	case levelMinor, levelPartial:
		return "clover4"
	case levelMajor:
		return "burst"
	}
	return "circle"
}

// uptimeLevel buckets a day: any failure leaves green, 99% and 90% uptime
// separate minor from partial from major.
func uptimeLevel(checks, failures int) level {
	switch {
	case checks == 0:
		return levelNone
	case failures == 0:
		return levelOK
	}
	up := float64(checks-failures) / float64(checks)
	switch {
	case up >= 0.99:
		return levelMinor
	case up >= 0.90:
		return levelPartial
	}
	return levelMajor
}

var legend = []struct {
	Level level
	Note  string
}{
	{levelOK, ""},
	{levelMinor, "≥ 99%"},
	{levelPartial, "≥ 90%"},
	{levelMajor, "< 90%"},
	{levelNone, ""},
}

type layout struct {
	Title     string
	PageTitle string
	Asset     string
	Refresh   int
	Timezone  string
	Version   string
	Nav       string
	Authed    bool
	Login     loginForm
}

func (s *Server) layout(r *http.Request, pageTitle, nav string, refresh bool) layout {
	l := layout{
		Title:     s.cfg.UI.Title,
		PageTitle: pageTitle,
		Asset:     s.assetVersion,
		Timezone:  s.cfg.Location.String(),
		Version:   s.version,
		Nav:       nav,
		Authed:    s.authed(r),
		Login:     loginForm{Next: r.URL.RequestURI()},
	}
	if refresh {
		l.Refresh = int(s.cfg.UI.Refresh.Std().Seconds())
	}
	return l
}

type bar struct {
	Level   level
	Date    string
	Summary string
	Detail  string
	Today   bool
}

type stateView struct {
	Level     level
	Label     string
	CheckedAt time.Time
	ChangedAt time.Time
	Latency   string
	Message   string
}

type monitorRow struct {
	ID          string
	Name        string
	Description string
	Group       string
	Type        string
	State       stateView
	Uptime      string
	Bars        []bar
}

type group struct {
	Name     string
	Monitors []monitorRow
}

type count struct {
	N     int
	Label string
}

type overall struct {
	Level    level
	Headline string
	Counts   []count
	// Incident is the first monitor that is down, if any.
	Incident *monitorRow
	Down     int
}

func (o overall) IncidentLabel() string {
	if o.Down == 1 {
		return "View " + o.Incident.Name
	}
	return "View first incident"
}

type statusPage struct {
	layout
	Overall overall
	Groups  []group
	Legend  any
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	rows, err := s.monitorRows(r.Context(), time.Now())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page := statusPage{
		layout:  s.layout(r, "", "status", true),
		Overall: summarize(rows),
		Legend:  legend,
	}
	for _, row := range rows {
		if len(page.Groups) == 0 || page.Groups[len(page.Groups)-1].Name != row.Group {
			page.Groups = append(page.Groups, group{Name: row.Group})
		}
		g := &page.Groups[len(page.Groups)-1]
		g.Monitors = append(g.Monitors, row)
	}
	s.render(w, r, http.StatusOK, "status", page)
}

// monitorRows lists the configured monitors with grouped monitors following
// the first monitor of their group, keeping config order otherwise.
func (s *Server) monitorRows(ctx context.Context, now time.Time) ([]monitorRow, error) {
	dates := s.dates(now)
	states, err := s.store.States(ctx)
	if err != nil {
		return nil, err
	}
	stats, err := s.store.Days(ctx, dates[0].Format(time.DateOnly))
	if err != nil {
		return nil, err
	}

	byGroup := map[string][]monitorRow{}
	for _, m := range s.cfg.Monitors {
		byGroup[m.Group] = append(byGroup[m.Group], s.monitorRow(m, states, stats[m.ID], dates))
	}
	rows := make([]monitorRow, 0, len(s.cfg.Monitors))
	for _, m := range s.cfg.Monitors {
		if g, ok := byGroup[m.Group]; ok {
			rows = append(rows, g...)
			delete(byGroup, m.Group)
		}
	}
	return rows, nil
}

func (s *Server) monitorRow(m config.Monitor, states map[string]store.State, stats []store.Day, dates []time.Time) monitorRow {
	row := monitorRow{
		ID:          m.ID,
		Name:        m.Name,
		Description: m.Description,
		Group:       m.Group,
		Type:        m.Type.Label(),
		State:       stateOf(states, m.ID),
	}
	byDay := make(map[string]store.Day, len(stats))
	var checks, failures int
	for _, d := range stats {
		byDay[d.Day] = d
		checks += d.Checks
		failures += d.Failures
	}
	row.Uptime = formatUptime(checks, failures)
	row.Bars = make([]bar, len(dates))
	for i, date := range dates {
		row.Bars[i] = dayBar(date, byDay[date.Format(time.DateOnly)])
	}
	row.Bars[len(row.Bars)-1].Today = true
	return row
}

func stateOf(states map[string]store.State, id string) stateView {
	st, ok := states[id]
	switch {
	case !ok:
		return stateView{Level: levelNone, Label: "Pending", Message: "Waiting for the first check"}
	case st.Up:
		return stateView{Level: levelOK, Label: "Operational", CheckedAt: st.CheckedAt, ChangedAt: st.ChangedAt, Latency: formatLatency(st.Latency), Message: st.Message}
	}
	return stateView{Level: levelMajor, Label: "Down", CheckedAt: st.CheckedAt, ChangedAt: st.ChangedAt, Latency: formatLatency(st.Latency), Message: st.Message}
}

func dayBar(date time.Time, d store.Day) bar {
	b := bar{Level: uptimeLevel(d.Checks, d.Failures), Date: date.Format("Mon, Jan 2, 2006")}
	switch b.Level {
	case levelNone:
		b.Summary = "No checks recorded"
	case levelOK:
		b.Summary = fmt.Sprintf("All %s checks passed", formatCount(d.Checks))
		b.Detail = "100% uptime · avg " + formatLatency(d.AvgLatency)
	default:
		b.Summary = fmt.Sprintf("%s of %s checks failed · ~%s down", formatCount(d.Failures), formatCount(d.Checks), formatDuration(d.Downtime))
		b.Detail = formatUptime(d.Checks, d.Failures) + " uptime"
		if d.Checks > d.Failures {
			b.Detail += " · avg " + formatLatency(d.AvgLatency)
		}
	}
	return b
}

func summarize(rows []monitorRow) overall {
	var o overall
	var up, pending int
	for i, r := range rows {
		switch r.State.Level {
		case levelOK:
			up++
		case levelMajor:
			if o.Down == 0 {
				o.Incident = &rows[i]
			}
			o.Down++
		default:
			pending++
		}
	}
	for _, c := range []count{{up, "operational"}, {o.Down, "down"}, {pending, "pending"}} {
		if c.N > 0 {
			o.Counts = append(o.Counts, c)
		}
	}
	switch {
	case len(rows) == 0:
		o.Level, o.Headline = levelNone, "No monitors configured"
	case o.Down == 0 && up == 0:
		o.Level, o.Headline = levelNone, "Waiting for the first checks"
	case o.Down == 0:
		o.Level, o.Headline = levelOK, "All systems operational"
	case up == 0 && pending == 0:
		o.Level, o.Headline = levelMajor, "Major outage"
	default:
		o.Level, o.Headline = levelPartial, "Partial outage"
	}
	return o
}

// dates returns the displayed calendar days, oldest first, ending today. Each is
// at noon so a DST transition cannot shift it onto a neighbouring date.
func (s *Server) dates(now time.Time) []time.Time {
	y, m, d := now.In(s.cfg.Location).Date()
	out := make([]time.Time, days)
	for i := range out {
		out[i] = time.Date(y, m, d-(days-1-i), 12, 0, 0, 0, s.cfg.Location)
	}
	return out
}

type stat struct {
	Label string
	Value string
	Note  string
}

type logRow struct {
	At      time.Time
	Up      bool
	Latency string
	Message string
}

type monitorPage struct {
	layout
	Monitor      monitorRow
	Days         []bar
	Target       string
	Interval     string
	Timeout      string
	Retries      int
	Options      [][2]string
	Stats        []stat
	Chart        chart
	Log          []logRow
	FailuresOnly bool
	OlderURL     string
	NewestURL    string
	Legend       any
}

const logPageSize = 25

func (s *Server) handleMonitor(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idx := -1
	for i, m := range s.cfg.Monitors {
		if m.ID == r.PathValue("id") {
			idx = i
		}
	}
	if idx < 0 {
		http.NotFound(w, r)
		return
	}
	m := s.cfg.Monitors[idx]
	now := time.Now()

	dates := s.dates(now)
	states, err := s.store.States(ctx)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	stats, err := s.store.Days(ctx, dates[0].Format(time.DateOnly))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	row := s.monitorRow(m, states, stats[m.ID], dates)

	target, options := check.Describe(m)
	page := monitorPage{
		layout:   s.layout(r, m.Name, "status", true),
		Monitor:  row,
		Days:     newestFirst(row.Bars),
		Target:   target,
		Interval: m.Interval.String(),
		Timeout:  m.Timeout.String(),
		Retries:  *m.Retries,
		Options:  options,
		Legend:   legend,
	}

	windows := []struct {
		label string
		since time.Time
	}{
		{"24 hours", now.Add(-24 * time.Hour)},
		{"7 days", now.Add(-7 * 24 * time.Hour)},
		{"30 days", now.Add(-30 * 24 * time.Hour)},
	}
	since := make([]time.Time, len(windows))
	for i, win := range windows {
		since[i] = win.since
	}
	uptimes, err := s.store.Uptimes(ctx, m.ID, since...)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for i, u := range uptimes {
		page.Stats = append(page.Stats, stat{
			Label: "Uptime · " + windows[i].label,
			Value: formatUptime(u.Checks, u.Failures),
			Note:  fmt.Sprintf("%s checks, %s failed", formatCount(u.Checks), formatCount(u.Failures)),
		})
	}
	// From the daily rollup rather than raw results, so it matches the bars.
	var checks, failures int
	for _, d := range stats[m.ID] {
		checks += d.Checks
		failures += d.Failures
	}
	page.Stats = append(page.Stats, stat{
		Label: "Uptime · 90 days",
		Value: row.Uptime,
		Note:  fmt.Sprintf("%s checks, %s failed", formatCount(checks), formatCount(failures)),
	})

	recent, err := s.store.Between(ctx, m.ID, now.Add(-24*time.Hour), now)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	page.Chart = buildChart(recent, now.Add(-24*time.Hour), now, s.cfg.Location)
	page.Stats = append(page.Stats, stat{Label: "Avg response · 24 hours", Value: page.Chart.Average, Note: "successful checks only"})

	q := r.URL.Query()
	page.FailuresOnly = q.Get("failures") == "1"
	before := now.Add(time.Second)
	if ms, err := strconv.ParseInt(q.Get("before"), 10, 64); err == nil {
		before = time.UnixMilli(ms)
		page.NewestURL = logURL(m.ID, page.FailuresOnly, time.Time{})
	}
	results, err := s.store.Results(ctx, m.ID, before, page.FailuresOnly, logPageSize+1)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if len(results) > logPageSize {
		results = results[:logPageSize]
		page.OlderURL = logURL(m.ID, page.FailuresOnly, results[len(results)-1].CheckedAt)
	}
	for _, res := range results {
		page.Log = append(page.Log, logRow{At: res.CheckedAt, Up: res.Up, Latency: formatLatency(res.Latency), Message: res.Message})
	}

	s.render(w, r, http.StatusOK, "monitor", page)
}

func newestFirst(bars []bar) []bar {
	out := slices.Clone(bars)
	slices.Reverse(out)
	return out
}

func logURL(id string, failuresOnly bool, before time.Time) string {
	q := url.Values{}
	if failuresOnly {
		q.Set("failures", "1")
	}
	if !before.IsZero() {
		q.Set("before", strconv.FormatInt(before.UnixMilli(), 10))
	}
	u := "/monitors/" + url.PathEscape(id)
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u + "#log"
}

type apiMonitor struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Group     string    `json:"group,omitempty"`
	Type      string    `json:"type"`
	Status    string    `json:"status"`
	CheckedAt time.Time `json:"checked_at,omitzero"`
	ChangedAt time.Time `json:"changed_at,omitzero"`
	Message   string    `json:"message,omitempty"`
	Uptime90d string    `json:"uptime_90d"`
	Days      []apiDay  `json:"days"`
}

type apiDay struct {
	Date    string `json:"date"`
	Level   level  `json:"level"`
	Summary string `json:"summary"`
}

func (s *Server) handleAPIStatus(w http.ResponseWriter, r *http.Request) {
	rows, err := s.monitorRows(r.Context(), time.Now())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	dates := s.dates(time.Now())
	authed := s.authed(r)
	ov := summarize(rows)
	out := struct {
		Status   level        `json:"status"`
		Summary  string       `json:"summary"`
		Monitors []apiMonitor `json:"monitors"`
	}{Status: ov.Level, Summary: ov.Headline, Monitors: []apiMonitor{}}
	for _, row := range rows {
		am := apiMonitor{
			ID: row.ID, Name: row.Name, Group: row.Group, Type: row.Type,
			Status:    map[level]string{levelOK: "up", levelMajor: "down"}[row.State.Level],
			CheckedAt: row.State.CheckedAt, ChangedAt: row.State.ChangedAt,
			Uptime90d: row.Uptime,
		}
		// Check messages can name internal hosts.
		if authed {
			am.Message = row.State.Message
		}
		am.Status = cmp.Or(am.Status, "pending")
		for i, b := range row.Bars {
			am.Days = append(am.Days, apiDay{Date: dates[i].Format(time.DateOnly), Level: b.Level, Summary: b.Summary})
		}
		out.Monitors = append(out.Monitors, am)
	}
	s.json(w, r, http.StatusOK, out)
}

func formatUptime(checks, failures int) string {
	if checks == 0 {
		return "—"
	}
	switch failures {
	case 0:
		return "100%"
	case checks:
		return "0%"
	}
	// Floor, so a single failure never rounds up to a perfect 100.00%.
	pct := math.Floor(float64(checks-failures)/float64(checks)*10000) / 100
	return strconv.FormatFloat(pct, 'f', 2, 64) + "%"
}

func formatLatency(d time.Duration) string {
	ms := float64(d) / float64(time.Millisecond)
	switch {
	case d == 0:
		return "—"
	case ms < 10:
		return strconv.FormatFloat(ms, 'f', 1, 64) + " ms"
	case ms < 1000:
		return strconv.FormatFloat(ms, 'f', 0, 64) + " ms"
	}
	return strconv.FormatFloat(ms/1000, 'f', 2, 64) + " s"
}

func formatDuration(d time.Duration) string {
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Round(time.Minute).Minutes()))
	case d < 24*time.Hour:
		d = d.Round(time.Minute)
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	d = d.Round(time.Hour)
	return fmt.Sprintf("%dd %dh", int(d.Hours())/24, int(d.Hours())%24)
}

func formatCount(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
