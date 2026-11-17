package check

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/back-to-code/sante/internal/config"
)

// ErrPending is returned by a checker that has nothing to report yet; the
// scheduler records no result for it.
var ErrPending = errors.New("no result yet")

// HealthEndpoint is healthy while the monitored service calls Sante at least
// once per interval. Until the first call it reports ErrPending, however long
// that takes. It is safe for concurrent use.
type HealthEndpoint struct {
	interval time.Duration
	// last is the latest call in Unix nanoseconds, 0 before the first.
	last  atomic.Int64
	calls chan struct{}
}

func newHealthEndpoint(m config.Monitor) *HealthEndpoint {
	return &HealthEndpoint{interval: m.Interval.Std(), calls: make(chan struct{}, 1)}
}

func (h *HealthEndpoint) RecordCall() {
	h.last.Store(time.Now().UnixNano())
	select {
	case h.calls <- struct{}{}:
	default:
	}
}

// Calls receives after each call. Calls that arrive before it is drained
// are delivered as one.
func (h *HealthEndpoint) Calls() <-chan struct{} { return h.calls }

func (h *HealthEndpoint) Check(context.Context) (string, error) {
	last := h.last.Load()
	if last == 0 {
		return "", ErrPending
	}
	since := time.Since(time.Unix(0, last))
	if since > h.interval {
		return "", fmt.Errorf("last call %s ago, expected every %s", since.Round(time.Second), h.interval)
	}
	return fmt.Sprintf("last call %s ago", since.Round(time.Second)), nil
}
