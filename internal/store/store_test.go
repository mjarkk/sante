package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStore(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Amsterdam")
	s, err := Open(filepath.Join(t.TempDir(), "nested", "sante.db"), loc)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := t.Context()

	// 23:30 UTC is already the next day in Amsterdam.
	base := time.Date(2026, 9, 30, 23, 30, 0, 0, time.UTC)
	record := func(at time.Time, up bool, latency time.Duration) {
		t.Helper()
		if err := s.Record(ctx, Result{MonitorID: "api", CheckedAt: at, Up: up, Latency: latency, Message: "m"}, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	record(base.Add(-48*time.Hour), true, 10*time.Millisecond)
	record(base, true, 20*time.Millisecond)
	record(base.Add(time.Minute), false, 5*time.Second)
	record(base.Add(2*time.Minute), false, 5*time.Second)
	record(base.Add(3*time.Minute), true, 40*time.Millisecond)

	days, err := s.Days(ctx, "2026-09-01")
	if err != nil {
		t.Fatal(err)
	}
	got := days["api"]
	if len(got) != 2 || got[0].Day != "2026-09-29" || got[1].Day != "2026-10-01" {
		t.Fatalf("days = %+v", got)
	}
	today := got[1]
	if today.Checks != 4 || today.Failures != 2 || today.Downtime != 2*time.Minute || today.AvgLatency != 30*time.Millisecond {
		t.Errorf("today = %+v", today)
	}

	states, err := s.States(ctx)
	if err != nil {
		t.Fatal(err)
	}
	st := states["api"]
	if !st.Up || !st.ChangedAt.Equal(base.Add(3*time.Minute)) {
		t.Errorf("state = %+v, want up since the last check", st)
	}

	ups, err := s.Uptimes(ctx, "api", base.Add(-time.Hour), base.Add(-72*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if ups[0].Checks != 4 || ups[0].Failures != 2 || ups[1].Checks != 5 {
		t.Errorf("uptimes = %+v", ups)
	}

	failures, err := s.Results(ctx, "api", base.Add(time.Hour), true, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(failures) != 2 || !failures[0].CheckedAt.After(failures[1].CheckedAt) {
		t.Errorf("failures = %+v", failures)
	}

	pruned, err := s.Prune(ctx, base.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Errorf("pruned %d results, want 1", pruned)
	}
	days, _ = s.Days(ctx, "2000-01-01")
	if len(days["api"]) != 1 {
		t.Errorf("days after prune = %+v", days["api"])
	}
}
