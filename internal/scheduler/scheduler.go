package scheduler

import (
	"context"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"time"

	"github.com/back-to-code/sante/internal/check"
	"github.com/back-to-code/sante/internal/config"
	"github.com/back-to-code/sante/internal/store"
)

const (
	retryDelay    = time.Second
	pruneInterval = time.Hour
)

type Job struct {
	Monitor config.Monitor
	Checker check.Checker
}

// Notifier.Down runs in its own goroutine, possibly for several monitors at
// once, and Run waits for it to return.
type Notifier interface {
	Down(ctx context.Context, m config.Monitor, r store.Result)
}

type Scheduler struct {
	store    *store.Store
	jobs     []Job
	notifier Notifier
	log      *slog.Logger

	lastCheck atomic.Int64
	running   atomic.Bool
}

// New accepts a nil notifier.
func New(s *store.Store, jobs []Job, notifier Notifier, log *slog.Logger) *Scheduler {
	return &Scheduler{store: s, jobs: jobs, notifier: notifier, log: log}
}

// Run checks every monitor on its interval and prunes old results until ctx
// is cancelled, then waits for in-flight checks to be recorded and
// notifications to be sent.
func (s *Scheduler) Run(ctx context.Context) {
	s.running.Store(true)
	defer s.running.Store(false)

	states, err := s.store.States(ctx)
	if err != nil {
		s.log.Error("loading the last known states", "err", err)
	}
	var wg sync.WaitGroup
	for _, job := range s.jobs {
		var wasUp *bool
		if st, ok := states[job.Monitor.ID]; ok {
			wasUp = &st.Up
		}
		wg.Go(func() { s.loop(ctx, job, wasUp, &wg) })
	}
	wg.Go(func() { s.prune(ctx) })
	wg.Wait()
}

func (s *Scheduler) Running() bool { return s.running.Load() }

// LastCheck is when the most recent check result was stored, zero before the first.
func (s *Scheduler) LastCheck() time.Time {
	if ms := s.lastCheck.Load(); ms > 0 {
		return time.UnixMilli(ms)
	}
	return time.Time{}
}

// wasUp is the stored state, so a restart does not announce a monitor that
// was already down. Without one the monitor counts as up, so a monitor that
// fails its very first check is still announced.
func (s *Scheduler) loop(ctx context.Context, job Job, wasUp *bool, wg *sync.WaitGroup) {
	interval := job.Monitor.Interval.Std()
	// Spread the first checks so a restart does not hit every target at once.
	startIn := rand.N(min(interval, 5*time.Second))
	select {
	case <-ctx.Done():
		return
	case <-time.After(startIn):
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if r, ok := s.run(ctx, job); ok {
			if err := s.store.Record(context.WithoutCancel(ctx), r, interval); err != nil {
				s.log.Error("storing check result", "monitor", job.Monitor.ID, "err", err)
			} else {
				s.lastCheck.Store(r.CheckedAt.UnixMilli())
			}
			s.logResult(job.Monitor, r, wasUp)
			if !r.Up && (wasUp == nil || *wasUp) && s.notifier != nil {
				wg.Go(func() { s.notifier.Down(ctx, job.Monitor, r) })
			}
			wasUp = &r.Up
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// run performs one check including retries. ok is false when ctx was
// cancelled mid-check, since that failure says nothing about the target.
func (s *Scheduler) run(ctx context.Context, job Job) (r store.Result, ok bool) {
	m := job.Monitor
	attempts := 1 + *m.Retries
	for attempt := 1; ; attempt++ {
		started := time.Now()
		checkCtx, cancel := context.WithTimeout(ctx, m.Timeout.Std())
		msg, err := job.Checker.Check(checkCtx)
		cancel()
		if ctx.Err() != nil {
			return r, false
		}
		r = store.Result{MonitorID: m.ID, CheckedAt: started, Up: err == nil, Latency: time.Since(started), Message: msg}
		if err == nil {
			return r, true
		}
		r.Message = err.Error()
		if attempt == attempts {
			if attempts > 1 {
				r.Message = fmt.Sprintf("%s (after %d attempts)", r.Message, attempts)
			}
			return r, true
		}
		select {
		case <-ctx.Done():
			return r, false
		case <-time.After(retryDelay):
		}
	}
}

func (s *Scheduler) logResult(m config.Monitor, r store.Result, wasUp *bool) {
	attrs := []any{"monitor", m.ID, "latency", r.Latency.Round(time.Millisecond), "message", r.Message}
	switch {
	case wasUp == nil && !r.Up, wasUp != nil && *wasUp && !r.Up:
		s.log.Warn("monitor is down", attrs...)
	case wasUp != nil && !*wasUp && r.Up:
		s.log.Info("monitor recovered", attrs...)
	default:
		s.log.Debug("check", append(attrs, "up", r.Up)...)
	}
}

func (s *Scheduler) prune(ctx context.Context) {
	ticker := time.NewTicker(pruneInterval)
	defer ticker.Stop()
	for {
		cutoff := time.Now().Add(-config.Retention)
		if n, err := s.store.Prune(ctx, cutoff); err != nil && ctx.Err() == nil {
			s.log.Error("pruning old results", "err", err)
		} else if n > 0 {
			s.log.Info("pruned old results", "deleted", n, "before", cutoff.Format(time.DateTime))
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
