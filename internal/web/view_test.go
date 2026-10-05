package web

import "testing"

func TestSummarizeUptime(t *testing.T) {
	for _, tc := range []struct {
		name string
		rows []monitorRow
		want string
	}{
		{"no monitors", nil, ""},
		{"never checked", []monitorRow{{}}, ""},
		{"all up", []monitorRow{{checks: 10}, {checks: 3}}, "100%"},
		{"per monitor, not per check", []monitorRow{{checks: 1000}, {checks: 10, failures: 10}}, "50.00%"},
		{"unchecked monitors ignored", []monitorRow{{checks: 4, failures: 1}, {}}, "75.00%"},
		{"floored", []monitorRow{{checks: 100000, failures: 1}}, "99.99%"},
	} {
		if got := summarize(tc.rows).Uptime; got != tc.want {
			t.Errorf("%s: uptime = %q, want %q", tc.name, got, tc.want)
		}
	}
}
