package config

import (
	"strings"
	"testing"
	"time"
)

func TestExpand(t *testing.T) {
	env := map[string]string{"HOST": "db.local", "EMPTY": "", "PORT": "5432"}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	tests := []struct {
		in, want string
		missing  []string
	}{
		{in: "plain", want: "plain"},
		{in: "$HOST:$PORT", want: "db.local:5432"},
		{in: "${HOST}", want: "db.local"},
		{in: "${UNSET}", want: "", missing: []string{"UNSET"}},
		{in: "${UNSET:-fallback}", want: "fallback"},
		{in: "${EMPTY:-fallback}", want: "fallback"},
		{in: "${EMPTY-fallback}", want: ""},
		{in: "${UNSET-fallback}", want: "fallback"},
		{in: "${UNSET:-${HOST}}", want: "db.local"},
		{in: "${UNSET:-$OTHER}", want: "", missing: []string{"OTHER"}},
		{in: "pa$$word", want: "pa$word"},
		{in: "costs 5$", want: "costs 5$"},
		{in: "$1 and $-", want: "$1 and $-"},
		{in: "${HOST}_x", want: "db.local_x"},
		{in: "$HOST_x", want: "", missing: []string{"HOST_x"}},
	}
	for _, tt := range tests {
		var missing []string
		got, err := expand(tt.in, lookup, func(n string) { missing = append(missing, n) })
		if err != nil {
			t.Errorf("expand(%q): %v", tt.in, err)
			continue
		}
		if got != tt.want {
			t.Errorf("expand(%q) = %q, want %q", tt.in, got, tt.want)
		}
		if strings.Join(missing, ",") != strings.Join(tt.missing, ",") {
			t.Errorf("expand(%q) missing = %v, want %v", tt.in, missing, tt.missing)
		}
	}
}

func TestExpandErrors(t *testing.T) {
	lookup := func(k string) (string, bool) { return "", k == "EMPTY" }
	tests := map[string]string{
		"${UNSET:?must be set}": "environment variable UNSET must be set",
		"${EMPTY:?}":            "environment variable EMPTY is required",
		"${UNSET?}":             "environment variable UNSET is required",
		"${HOST":                "unterminated",
		"${}":                   "invalid variable reference",
		"${A B}":                "invalid variable reference",
	}
	for in, want := range tests {
		_, err := expand(in, lookup, func(string) {})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("expand(%q) error = %v, want it to contain %q", in, err, want)
		}
	}
	if _, err := expand("${EMPTY?}", lookup, func(string) {}); err != nil {
		t.Errorf("${EMPTY?} should accept a set but empty variable: %v", err)
	}
}

func TestParse(t *testing.T) {
	t.Setenv("SANTE_TEST_PORT", "9090")
	t.Setenv("SANTE_TEST_DB", "3")
	t.Setenv("SANTE_TEST_PASSWORD", "s3cr#t: yes")

	cfg, warnings, err := Parse([]byte(`
server:
  listen: :${SANTE_TEST_PORT}
  timezone: Europe/Amsterdam
  auth_token: ${SANTE_TEST_PASSWORD}
defaults:
  interval: 30s
  timeout: 45s
monitors:
  - name: Public API
    type: http
    url: https://api.example.com/health
  - name: Cache
    type: redis
    address: cache:6379
    password: ${SANTE_TEST_PASSWORD}
    db: ${SANTE_TEST_DB}
    interval: 10
  - name: Orders
    id: orders-db
    type: PostgreSQL
    dsn: postgres://app:${SANTE_TEST_MISSING}@db/orders
    timeout: 5s
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "SANTE_TEST_MISSING") {
		t.Errorf("warnings = %v", warnings)
	}
	if cfg.Server.Listen != ":9090" {
		t.Errorf("listen = %q", cfg.Server.Listen)
	}
	if cfg.Server.AuthToken != "s3cr#t: yes" {
		t.Errorf("auth_token = %q", cfg.Server.AuthToken)
	}
	if cfg.Location.String() != "Europe/Amsterdam" {
		t.Errorf("location = %v", cfg.Location)
	}
	if cfg.UI.Title != "Sante" {
		t.Errorf("ui.title = %q, want the default", cfg.UI.Title)
	}

	api, cache, orders := cfg.Monitors[0], cfg.Monitors[1], cfg.Monitors[2]
	if api.ID != "public-api" || api.Method != "GET" || !*api.FollowRedirects {
		t.Errorf("http defaults not applied: %+v", api)
	}
	if api.Interval.Std() != 30*time.Second || api.Timeout.Std() != 30*time.Second {
		t.Errorf("api interval/timeout = %s/%s, want the timeout capped at the interval", api.Interval, api.Timeout)
	}
	if cache.Password != "s3cr#t: yes" || cache.DB != 3 {
		t.Errorf("cache password/db = %q/%d", cache.Password, cache.DB)
	}
	if cache.Interval.Std() != 10*time.Second || cache.Timeout.Std() != 10*time.Second {
		t.Errorf("cache interval/timeout = %s/%s", cache.Interval, cache.Timeout)
	}
	if orders.Type != Postgres || orders.ID != "orders-db" || orders.DSN != "postgres://app:@db/orders" {
		t.Errorf("orders = %+v", orders)
	}
	if *orders.Retries != 0 {
		t.Errorf("retries = %d", *orders.Retries)
	}
}

func TestParseErrors(t *testing.T) {
	tests := map[string]string{
		"monitors:\n  - name: a\n    type: tcp\n    address: x:1\n    intervall: 5s\n":                            `line 5: unknown field "intervall"`,
		"monitors:\n  - name: a\n    type: ftp\n":                                                                 `unknown type "ftp"`,
		"monitors:\n  - name: a\n    type: tcp\n    address: nope\n":                                              `address must be host:port`,
		"monitors:\n  - name: a\n    type: http\n    url: example.com\n":                                          `absolute http`,
		"monitors:\n  - name: a\n    type: tcp\n    address: x:1\n  - name: A\n    type: tcp\n    address: x:2\n": `id "a" is already used`,
		"monitors:\n  - name: a\n    type: tcp\n    address: x:1\n    interval: 5s\n    timeout: 10s\n":           `timeout 10s is longer than interval 5s`,
		"server:\n  listen: ${PORT:?is needed}\n":                                                                 `line 2: environment variable PORT is needed`,
		"defaults:\n  interval: soon\n":                                                                           `invalid duration "soon"`,
		"monitors: []\n":                                                                                          `server.auth_token: required`,
	}
	for in, want := range tests {
		_, _, err := Parse([]byte(in))
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Parse(%q) error = %v, want it to contain %q", in, err, want)
		}
	}
}
