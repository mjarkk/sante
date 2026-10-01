package scheduler

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/back-to-code/sante/internal/config"
	"github.com/back-to-code/sante/internal/store"
)

// sequence reports the given results in order, then repeats the last one.
type sequence struct {
	up    []bool
	calls atomic.Int32
}

func (s *sequence) Check(context.Context) (string, error) {
	n := int(s.calls.Add(1)) - 1
	if s.up[min(n, len(s.up)-1)] {
		return "ok", nil
	}
	return "", errors.New("connection refused")
}

type recorder struct {
	mu   sync.Mutex
	down []string
}

func (r *recorder) Down(_ context.Context, m config.Monitor, _ store.Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.down = append(r.down, m.ID)
}

func TestNotifiesWhenMonitorGoesDown(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sante.db"), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// Down before a restart: still being down afterwards is not news.
	if err := st.Record(t.Context(), store.Result{MonitorID: "known-down", CheckedAt: time.Now().Add(-time.Minute)}, time.Minute); err != nil {
		t.Fatal(err)
	}

	monitor := func(id string) config.Monitor {
		return config.Monitor{ID: id, Name: id, Interval: config.Duration(5 * time.Millisecond), Timeout: config.Duration(time.Second), Retries: new(0)}
	}
	flapping := &sequence{up: []bool{false, false, true, true, false, false, true}}
	knownDown := &sequence{up: []bool{false}}
	jobs := []Job{{monitor("flapping"), flapping}, {monitor("known-down"), knownDown}}
	notifier := &recorder{}
	sched := New(st, jobs, notifier, slog.New(slog.DiscardHandler))

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		sched.Run(ctx)
		close(done)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for (flapping.calls.Load() < 10 || knownDown.calls.Load() < 3) && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	if want := []string{"flapping", "flapping"}; !slices.Equal(notifier.down, want) {
		t.Errorf("notified for %v, want %v: once for the first failed check, once after recovering", notifier.down, want)
	}
}
