package seed

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/back-to-code/sante/internal/config"
	"github.com/back-to-code/sante/internal/store"
)

func monitors(t *testing.T) []config.Monitor {
	t.Helper()
	var b strings.Builder
	b.WriteString("server:\n  auth_token: s3cret\nmonitors:\n")
	types := []string{"type: http\n    url: https://api.example.com/health", "type: tcp\n    address: db:5432",
		"type: redis\n    address: cache:6379", "type: mysql\n    dsn: u:p@tcp(db:3306)/x", "type: postgres\n    dsn: postgres://db/x"}
	for i := range 10 {
		fmt.Fprintf(&b, "  - name: m%d\n    retries: 1\n    %s\n", i, types[i%len(types)])
	}
	cfg, _, err := config.Parse([]byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Monitors
}

func TestGenerate(t *testing.T) {
	to := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	from := to.Add(-config.Retention)
	var up, down int
	for _, m := range monitors(t) {
		results := Generate(m, from, to, minSpacing)
		if want := int(config.Retention/minSpacing) + 1; len(results) != want {
			t.Fatalf("%s: %d results, want %d", m.ID, len(results), want)
		}
		if !slices.IsSortedFunc(results, func(a, b store.Result) int { return a.CheckedAt.Compare(b.CheckedAt) }) {
			t.Errorf("%s: results are not in chronological order", m.ID)
		}
		if again := Generate(m, from, to, minSpacing); !slices.Equal(results, again) {
			t.Errorf("%s: generating twice gave different history", m.ID)
		}
		for _, r := range results {
			switch {
			case r.Up:
				up++
			case !strings.HasSuffix(r.Message, "(after 2 attempts)"):
				t.Fatalf("%s: failure %q lacks the retry suffix", m.ID, r.Message)
			case strings.HasPrefix(r.Message, msgTimeout) && r.Latency != m.Timeout.Std():
				t.Fatalf("%s: timeout took %s, want the monitor timeout %s", m.ID, r.Latency, m.Timeout)
			default:
				down++
			}
			if r.Latency <= 0 {
				t.Fatalf("%s: non-positive latency %s", m.ID, r.Latency)
			}
		}
	}
	if ratio := float64(down) / float64(up+down); down == 0 || ratio > 0.02 {
		t.Errorf("%d of %d checks failed, want some failures but well under 2%%", down, up+down)
	}
}

func TestRun(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "sante.db"), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	ms := monitors(t)[:2]
	now := time.Now()
	n, err := Run(t.Context(), st, ms, now)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("nothing seeded")
	}

	days, err := st.Days(t.Context(), now.Add(-config.Retention).UTC().Format(time.DateOnly))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range ms {
		if got := len(days[m.ID]); got < 90 || got > 91 {
			t.Errorf("%s has %d days of rollups, want 90 or 91", m.ID, got)
		}
	}
	states, _ := st.States(t.Context())
	if len(states) != len(ms) {
		t.Errorf("states = %v, want one per monitor", states)
	}
}
