// Package seed fills a store with synthetic but plausible check history, for
// demos and for trying out the UI without waiting 90 days.
package seed

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"math/rand/v2"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/back-to-code/sante/internal/config"
	"github.com/back-to-code/sante/internal/store"
)

// minSpacing keeps 90 days of history to about 26,000 results per monitor,
// however short its real interval is.
const minSpacing = 5 * time.Minute

// blipChance is the probability of an isolated failed check outside incidents.
const blipChance = 1.0 / 5000

// Run records synthetic history for each monitor, covering the retention
// period up to now, and returns the number of results written.
func Run(ctx context.Context, st *store.Store, monitors []config.Monitor, now time.Time) (int, error) {
	total := 0
	for _, m := range monitors {
		spacing := max(m.Interval.Std(), minSpacing)
		results := Generate(m, now.Add(-config.Retention).Add(spacing), now, spacing)
		if err := st.RecordBatch(ctx, results, spacing); err != nil {
			return total, fmt.Errorf("seeding %s: %w", m.ID, err)
		}
		total += len(results)
	}
	return total, nil
}

// Generate returns one result per spacing in [from, to], oldest first. A
// monitor id always produces the same incidents and latency pattern.
func Generate(m config.Monitor, from, to time.Time, spacing time.Duration) []store.Result {
	h := fnv.New64a()
	h.Write([]byte(m.ID))
	rng := rand.New(rand.NewPCG(h.Sum64(), 0x5a17e))

	p := profileFor(m)
	base := p.latency[0] + time.Duration(rng.Int64N(int64(p.latency[1]-p.latency[0])))
	phase := rng.Float64() * 2 * math.Pi
	incidents := makeIncidents(rng, from, to, len(p.failures))
	suffix := ""
	if *m.Retries > 0 {
		suffix = fmt.Sprintf(" (after %d attempts)", 1+*m.Retries)
	}

	results := make([]store.Result, 0, int(to.Sub(from)/spacing)+1)
	next := 0
	for t := from; !t.After(to); t = t.Add(spacing) {
		for next < len(incidents) && !incidents[next].end.After(t) {
			next++
		}
		inIncident := next < len(incidents) && !incidents[next].start.After(t)

		r := store.Result{MonitorID: m.ID, CheckedAt: t, Up: true, Message: p.ok}
		switch {
		case inIncident && rng.Float64() < 0.9:
			r.Up = false
			r.Message = p.failures[incidents[next].failure]
		case !inIncident && rng.Float64() < blipChance:
			r.Up = false
			r.Message = p.failures[rng.IntN(len(p.failures))]
		}
		if r.Up {
			r.Latency = latency(rng, base, t, phase)
		} else {
			r.Latency = failureLatency(rng, r.Message, base, m.Timeout.Std())
			r.Message += suffix
		}
		results = append(results, r)
	}
	return results
}

type incident struct {
	start, end time.Time
	failure    int
}

func makeIncidents(rng *rand.Rand, from, to time.Time, kinds int) []incident {
	incidents := make([]incident, rng.IntN(10))
	for i := range incidents {
		start := from.Add(time.Duration(rng.Int64N(int64(to.Sub(from)))))
		var d time.Duration
		switch x := rng.Float64(); {
		case x < 0.7:
			d = 5*time.Minute + time.Duration(rng.Int64N(int64(25*time.Minute)))
		case x < 0.95:
			d = 30*time.Minute + time.Duration(rng.Int64N(int64(90*time.Minute)))
		default:
			d = 2*time.Hour + time.Duration(rng.Int64N(int64(5*time.Hour)))
		}
		incidents[i] = incident{start: start, end: start.Add(d), failure: rng.IntN(kinds)}
	}
	slices.SortFunc(incidents, func(a, b incident) int { return a.start.Compare(b.start) })
	return incidents
}

// latency follows a daily rhythm peaking mid-afternoon UTC and a slow
// multi-week drift, with noise and occasional spikes.
func latency(rng *rand.Rand, base time.Duration, t time.Time, phase float64) time.Duration {
	hours := float64(t.UTC().Hour()) + float64(t.Minute())/60
	daily := 1 + 0.2*math.Sin(2*math.Pi*(hours-9)/24)
	days := float64(t.Unix()) / 86400
	drift := 1 + 0.12*math.Sin(2*math.Pi*days/40+phase)
	noise := max(0.5, 1+rng.NormFloat64()*0.08)
	f := daily * drift * noise
	if rng.Float64() < 0.01 {
		f *= 2 + 3*rng.Float64()
	}
	return time.Duration(float64(base) * f)
}

func failureLatency(rng *rand.Rand, message string, base, timeout time.Duration) time.Duration {
	switch {
	case message == msgTimeout:
		return timeout
	case strings.HasSuffix(message, msgRefused):
		return time.Duration(300+rng.IntN(1500)) * time.Microsecond
	}
	return time.Duration(float64(base) * (0.5 + rng.Float64()))
}

const (
	msgTimeout = "timed out"
	msgRefused = "connect: connection refused"
)

type profile struct {
	latency  [2]time.Duration
	ok       string
	failures []string
}

func profileFor(m config.Monitor) profile {
	refused := func(addr string) string { return "dial tcp " + addr + ": " + msgRefused }
	switch m.Type {
	case config.HTTP:
		ok := "200 OK"
		if len(m.ExpectStatus) > 0 {
			ok = fmt.Sprintf("%d %s", m.ExpectStatus[0], http.StatusText(m.ExpectStatus[0]))
		}
		host := "example.com:443"
		if u, err := url.Parse(m.URL); err == nil {
			host = u.Host
		}
		return profile{
			latency: [2]time.Duration{40 * time.Millisecond, 180 * time.Millisecond},
			ok:      ok,
			failures: []string{
				"unexpected status 503 Service Unavailable",
				"unexpected status 502 Bad Gateway",
				msgTimeout,
				refused(host),
			},
		}
	case config.TCP:
		return profile{
			latency:  [2]time.Duration{500 * time.Microsecond, 3 * time.Millisecond},
			ok:       "connected to " + m.Address,
			failures: []string{refused(m.Address), msgTimeout},
		}
	case config.Redis:
		return profile{
			latency:  [2]time.Duration{500 * time.Microsecond, 2 * time.Millisecond},
			ok:       "PONG",
			failures: []string{refused(m.Address), "LOADING Redis is loading the dataset in memory", msgTimeout},
		}
	case config.MySQL:
		return profile{
			latency:  [2]time.Duration{2 * time.Millisecond, 8 * time.Millisecond},
			ok:       dbOK(m),
			failures: []string{"Error 1040 (08004): Too many connections", msgRefused, msgTimeout},
		}
	}
	return profile{
		latency: [2]time.Duration{3 * time.Millisecond, 12 * time.Millisecond},
		ok:      dbOK(m),
		failures: []string{
			"FATAL: the database system is starting up (SQLSTATE 57P03)",
			"FATAL: sorry, too many clients already (SQLSTATE 53300)",
			msgRefused,
			msgTimeout,
		},
	}
}

func dbOK(m config.Monitor) string {
	if m.Query != "" {
		return "query ok"
	}
	return "ping ok"
}
