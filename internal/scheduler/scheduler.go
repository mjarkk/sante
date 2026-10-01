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

type Scheduler struct {
	store *store.Store
	jobs  []Job
	log   *slog.Logger

	lastCheck atomic.Int64
	running   atomic.Bool
}

func New(s *store.Store, jobs []Job, log *slog.Logger) *Scheduler {
	return &Scheduler{store: s, jobs: jobs, log: log}
}

// Run checks every monitor on its interval and prunes old results until ctx
// is cancelled, then waits for in-flight checks to be recorded.
func (s *Scheduler) Run(ctx context.Context) {
	s.running.Store(true)
	defer s.running.Store(false)

	var wg sync.WaitGroup
	for _, job := range s.jobs {
		wg.Go(func() { s.loop(ctx, job) })
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

func (s *Scheduler) loop(ctx context.Context, job Job) {
	interval := job.Monitor.Interval.Std()
	// Spread the first checks so a restart does not hit every target at once.
	startIn := rand.N(min(interval, 5*time.Second))
	select {
	case <-ctx.Done():
		return
	case <-time.After(startIn):
	}

	var wasUp *bool
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
