package web

import (
	"testing"
	"time"

	"github.com/back-to-code/sante/internal/store"
)

func TestChartYTickLabels(t *testing.T) {
	for _, tc := range []struct {
		latency time.Duration
		want    []string
	}{
		{100 * time.Microsecond, []string{"0 ms", "0.025 ms", "0.05 ms", "0.075 ms", "0.1 ms"}},
		{300 * time.Millisecond, []string{"0 ms", "100 ms", "200 ms", "300 ms"}},
		{3 * time.Second, []string{"0 s", "1 s", "2 s", "3 s"}},
	} {
		to := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
		from := to.Add(-24 * time.Hour)
		c := buildChart([]store.Result{{CheckedAt: to.Add(-time.Minute), Up: true, Latency: tc.latency}}, from, to, time.UTC)
		var got []string
		for _, tick := range c.YTicks {
			got = append(got, tick.Label)
		}
		if len(got) != len(tc.want) {
			t.Errorf("%s: labels = %q, want %q", tc.latency, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: labels = %q, want %q", tc.latency, got, tc.want)
				break
			}
		}
	}
}
