package web

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/back-to-code/sante/internal/check"
	"github.com/back-to-code/sante/internal/config"
	"github.com/back-to-code/sante/internal/scheduler"
	"github.com/back-to-code/sante/internal/store"
)

func newTestServer(t *testing.T) (*httptest.Server, *scheduler.Scheduler) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })

	cfg, _, err := config.Parse([]byte(`
server:
  auth_token: s3cret
ui:
  title: Acme Status
notifications:
  webhooks:
    - type: slack
      url: https://hooks.slack.com/services/T0/B0/hookSecret
      channel: "#ops"
monitors:
  - name: Queue
    group: Infra
    type: tcp
    address: ` + ln.Addr().String() + `
    interval: 1s
  - name: Orders DB
    type: postgres
    dsn: postgres://app:hunter2@127.0.0.1:1/orders
`))
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(t.TempDir(), "sante.db"), cfg.Location)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })

	var jobs []scheduler.Job
	for _, m := range cfg.Monitors {
		c, err := check.New(m)
		if err != nil {
			t.Fatal(err)
		}
		jobs = append(jobs, scheduler.Job{Monitor: m, Checker: c})
	}
	log := slog.New(slog.DiscardHandler)
	sched := scheduler.New(st, jobs, nil, log)
	srv, err := New(cfg, st, sched, "test", log)
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, sched
}

const bearer = "Bearer s3cret"

func get(t *testing.T, url string, header ...string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	return roundTrip(t, req, header...)
}

func post(t *testing.T, url string, form url.Values, header ...string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("POST", url, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return roundTrip(t, req, header...)
}

func roundTrip(t *testing.T, req *http.Request, header ...string) (*http.Response, string) {
	t.Helper()
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestPages(t *testing.T) {
	ts, sched := newTestServer(t)

	resp, body := get(t, ts.URL+"/health?format=json", "Authorization", bearer)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("health before the scheduler runs = %d, want 503", resp.StatusCode)
	}

	go sched.Run(t.Context())
	deadline := time.Now().Add(10 * time.Second)
	for sched.LastCheck().IsZero() && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}

	resp, body = get(t, ts.URL+"/")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Queue") || !strings.Contains(body, `data-level="ok"`) {
		t.Errorf("status page = %d, missing the up monitor:\n%.500s", resp.StatusCode, body)
	}
	if !strings.Contains(body, "<title>Acme Status</title>") || !strings.Contains(body, `<span class="brand-name">Acme Status</span>`) {
		t.Errorf("status page does not use ui.title:\n%.800s", body)
	}
	if strings.Count(body, `class="bar"`) != 2*days {
		t.Errorf("status page has %d bars, want %d", strings.Count(body, `class="bar"`), 2*days)
	}

	resp, body = get(t, ts.URL+"/monitors/queue", "Authorization", bearer)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Check log") {
		t.Errorf("monitor page = %d", resp.StatusCode)
	}
	if resp, _ = get(t, ts.URL+"/monitors/nope", "Authorization", bearer); resp.StatusCode != http.StatusNotFound {
		t.Errorf("unknown monitor = %d, want 404", resp.StatusCode)
	}

	resp, body = get(t, ts.URL+"/health", "Accept", "application/json", "Authorization", bearer)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("health = %d, want 200: %s", resp.StatusCode, body)
	}
	var report struct {
		Status   string
		Settings struct{ Monitors []struct{ ID string } }
	}
	if err := json.Unmarshal([]byte(body), &report); err != nil {
		t.Fatal(err)
	}
	if report.Status != "ok" || len(report.Settings.Monitors) != 2 {
		t.Errorf("report = %+v", report)
	}
	if strings.Contains(body, "hunter2") || strings.Contains(body, "hookSecret") {
		t.Error("health report leaks the database password or the webhook URL")
	}
	if !strings.Contains(body, `"url": "https://hooks.slack.com/xxxxx"`) {
		t.Errorf("health report does not list the webhook: %s", body)
	}

	resp, body = get(t, ts.URL+"/health", "Authorization", bearer)
	if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") || !strings.Contains(body, "server.listen") {
		t.Errorf("health page is not HTML with settings: %s", resp.Header.Get("Content-Type"))
	}
	if !strings.Contains(body, "slack · https://hooks.slack.com/xxxxx · #ops") || strings.Contains(body, "hookSecret") {
		t.Errorf("health page does not list the redacted webhook:\n%s", body)
	}

	resp, body = get(t, ts.URL+"/api/status")
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `"id": "orders-db"`) {
		t.Errorf("api status = %d: %.300s", resp.StatusCode, body)
	}
}

func TestAuth(t *testing.T) {
	ts, _ := newTestServer(t)

	resp, body := get(t, ts.URL+"/")
	if strings.Contains(body, `href="/monitors/`) || !strings.Contains(body, `popovertarget="auth"`) || strings.Contains(body, `href="/health"`) {
		t.Errorf("signed-out status page links to private pages or lacks the Authenticate button:\n%.500s", body)
	}
	if resp, body = get(t, ts.URL+"/api/status"); strings.Contains(body, `"message"`) {
		t.Errorf("signed-out api status includes check messages: %.300s", body)
	}

	for _, path := range []string{"/health", "/monitors/queue", "/monitors/nope"} {
		resp, _ = get(t, ts.URL+path)
		if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login?next="+url.QueryEscape(path) {
			t.Errorf("signed-out %s = %d to %q, want a redirect to the login page", path, resp.StatusCode, resp.Header.Get("Location"))
		}
	}
	for _, header := range []string{"", "Bearer nope", "Basic czNjcmV0"} {
		resp, _ = get(t, ts.URL+"/health?format=json", "Authorization", header)
		if resp.StatusCode != http.StatusUnauthorized || !strings.HasPrefix(resp.Header.Get("WWW-Authenticate"), "Bearer ") {
			t.Errorf("health with Authorization %q = %d, want 401", header, resp.StatusCode)
		}
	}

	resp, body = post(t, ts.URL+"/login", url.Values{"token": {"nope"}, "next": {"/health"}})
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "not valid") || len(resp.Cookies()) != 0 {
		t.Errorf("login with a wrong token = %d, cookies %v", resp.StatusCode, resp.Cookies())
	}
	if resp, _ = post(t, ts.URL+"/login", url.Values{"token": {"s3cret"}}, "Sec-Fetch-Site", "cross-site"); resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-site login = %d, want 403", resp.StatusCode)
	}
	if resp, _ = post(t, ts.URL+"/login", url.Values{"token": {"s3cret"}, "next": {"//evil.example"}}); resp.Header.Get("Location") != "/" {
		t.Errorf("login redirected off-site to %q", resp.Header.Get("Location"))
	}

	resp, _ = post(t, ts.URL+"/login", url.Values{"token": {"s3cret"}, "next": {"/health"}})
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/health" {
		t.Fatalf("login = %d to %q, want a redirect to /health", resp.StatusCode, resp.Header.Get("Location"))
	}
	cookies := resp.Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || strings.Contains(cookies[0].Value, "s3cret") {
		t.Fatalf("session cookie = %+v", cookies)
	}
	session := cookies[0].Name + "=" + cookies[0].Value

	if resp, body = get(t, ts.URL+"/", "Cookie", session); !strings.Contains(body, `href="/monitors/queue"`) || !strings.Contains(body, `href="/health"`) {
		t.Errorf("signed-in status page lacks links to private pages:\n%.500s", body)
	}
	for _, path := range []string{"/health", "/monitors/queue"} {
		if resp, _ = get(t, ts.URL+path, "Cookie", session); resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("signed-in %s = %d", path, resp.StatusCode)
		}
	}

	resp, _ = post(t, ts.URL+"/logout", nil, "Cookie", session)
	if c := resp.Cookies(); resp.StatusCode != http.StatusSeeOther || len(c) != 1 || c[0].MaxAge >= 0 {
		t.Errorf("logout = %d, cookies %+v, want the session cleared", resp.StatusCode, c)
	}
}

func TestAssets(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, body := get(t, ts.URL+"/")
	start := strings.Index(body, "/static/app.css?v=")
	if start < 0 {
		t.Fatal("page does not reference app.css")
	}
	href := body[start : start+strings.IndexByte(body[start:], '"')]

	resp, _ = get(t, ts.URL+href, "Accept-Encoding", "gzip")
	if resp.Header.Get("Content-Encoding") != "gzip" || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Errorf("app.css headers = %v", resp.Header)
	}
	_, css := get(t, ts.URL+href)
	swatches := regexp.MustCompile(`data-palette="(\w+)"`).FindAllStringSubmatch(body, -1)
	if len(swatches) == 0 {
		t.Error("page has no theme color swatches")
	}
	for _, m := range swatches {
		if !strings.Contains(css, `[data-palette="`+m[1]+`"]`) {
			t.Errorf("app.css does not define the %s palette its swatch shows", m[1])
		}
	}
	if resp, _ = get(t, ts.URL+"/static/missing.js"); resp.StatusCode != http.StatusNotFound {
		t.Errorf("missing asset = %d", resp.StatusCode)
	}
}
