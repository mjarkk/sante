package check

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/back-to-code/sante/internal/config"
)

func monitor(t *testing.T, yaml string) config.Monitor {
	t.Helper()
	cfg, _, err := config.Parse([]byte("server:\n  auth_token: s3cret\nmonitors:\n" + yaml))
	if err != nil {
		t.Fatal(err)
	}
	return cfg.Monitors[0]
}

func TestHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.Write([]byte(`{"status":"ok"}`))
		case "/redirect":
			http.Redirect(w, r, "/ok", http.StatusFound)
		case "/slow":
			time.Sleep(200 * time.Millisecond)
		default:
			http.Error(w, "nope", http.StatusServiceUnavailable)
		}
	}))
	defer srv.Close()

	tests := []struct {
		name, extra, path string
		wantErr           string
	}{
		{name: "ok", path: "/ok"},
		{name: "body match", path: "/ok", extra: `expect_body: '"status":"ok"'`},
		{name: "body mismatch", path: "/ok", extra: "expect_body: healthy", wantErr: `body does not contain "healthy"`},
		{name: "server error", path: "/down", wantErr: "unexpected status 503"},
		{name: "expected error status", path: "/down", extra: "expect_status: [503]"},
		{name: "redirect followed", path: "/redirect", extra: "expect_status: [200]"},
		{name: "redirect not followed", path: "/redirect", extra: "follow_redirects: false\n    expect_status: [200]", wantErr: "unexpected status 302"},
		{name: "timeout", path: "/slow", extra: "timeout: 50ms", wantErr: "timed out"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := monitor(t, "  - name: x\n    type: http\n    url: "+srv.URL+tt.path+"?token=secret\n    "+tt.extra+"\n")
			c, err := New(m)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), m.Timeout.Std())
			defer cancel()
			_, err = c.Check(ctx)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Errorf("error = %v, want %q", err, tt.wantErr)
			case err != nil && strings.Contains(err.Error(), "secret"):
				t.Errorf("error leaks the query string: %v", err)
			}
		})
	}
}

func TestTCP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	c, _ := New(monitor(t, "  - name: x\n    type: tcp\n    address: "+addr+"\n"))
	if _, err := c.Check(t.Context()); err != nil {
		t.Errorf("open port: %v", err)
	}
	ln.Close()
	if _, err := c.Check(t.Context()); err == nil {
		t.Error("closed port reported healthy")
	}
}

func TestDescribeRedacts(t *testing.T) {
	tests := []struct{ yaml, want string }{
		{"type: http\n    url: https://user:pw@example.com/h?api_key=abc&page=2", "https://user:xxxxx@example.com/h?api_key=xxxxx&page=2"},
		{"type: mysql\n    dsn: app:pw@tcp(db:3306)/shop", "app:xxxxx@tcp(db:3306)/shop"},
		{"type: postgres\n    dsn: postgres://app:pw@db:5432/shop?sslmode=disable", "postgres://app:xxxxx@db:5432/shop?sslmode=disable"},
		{"type: postgres\n    dsn: host=db user=app password='p w' dbname=shop", "host=db user=app password=xxxxx dbname=shop"},
	}
	for _, tt := range tests {
		target, _ := Describe(monitor(t, "  - name: x\n    "+tt.yaml+"\n"))
		if target != tt.want {
			t.Errorf("target = %q, want %q", target, tt.want)
		}
	}

	_, opts := Describe(monitor(t, "  - name: x\n    type: redis\n    address: r:6379\n    password: pw\n"))
	for _, o := range opts {
		if o[1] == "pw" {
			t.Errorf("redis password not redacted: %v", opts)
		}
	}
}
