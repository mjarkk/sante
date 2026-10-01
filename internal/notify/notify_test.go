package notify

import (
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/back-to-code/sante/internal/config"
	"github.com/back-to-code/sante/internal/store"
)

func testConfig(t *testing.T, webhooks string) *config.Config {
	t.Helper()
	cfg, _, err := config.Parse([]byte(`
server:
  auth_token: s3cret
ui:
  title: Acme <Status>
notifications:
  webhooks:
` + webhooks + `
monitors:
  - name: Payments [API]
    group: Platform & Co
    type: tcp
    address: payments:443
`))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

var downResult = store.Result{
	MonitorID: "payments-api",
	CheckedAt: time.Now(),
	Message:   "unexpected status 503 <!channel> & `x`\nmore",
}

func TestDownMessage(t *testing.T) {
	cfg := testConfig(t, `
    - type: slack
      url: https://hooks.slack.com/services/T0/B0/secret
      channel: "#ops"
    - type: mattermost
      url: https://chat.example.com/hooks/secret
      username: Sante`)
	m := cfg.Monitors[0]
	slack := downMessage(cfg.Notifications.Webhooks[0], cfg.UI.Title, m, downResult)
	mattermost := downMessage(cfg.Notifications.Webhooks[1], cfg.UI.Title, m, downResult)

	tests := []struct{ name, got, want string }{
		{"slack text", slack.Text, ":red_circle: *Payments [API]* is down"},
		{"slack reason", slack.Attachments[0].Text, "`unexpected status 503 &lt;!channel&gt; &amp; 'x' more`"},
		{"slack group", slack.Attachments[0].Fields[0].Value, "Platform &amp; Co"},
		{"slack footer", slack.Attachments[0].Footer, "Acme &lt;Status&gt;"},
		{"slack channel", slack.Channel, "#ops"},
		{"slack fallback", slack.Attachments[0].Fallback, "Payments [API] is down: " + downResult.Message},
		{"mattermost text", mattermost.Text, `:red_circle: **Payments \[API\]** is down` + "\n" +
			"`unexpected status 503 <!channel> & 'x' more`\n" +
			"**Group:** Platform & Co · **Type:** TCP\n" +
			`_Acme \<Status\>_`},
		{"mattermost username", mattermost.Username, "Sante"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

// TestMattermostPayload holds the payload to the rules Mattermost enforces
// beyond Slack's (see IncomingWebhookRequest in mattermost/server/public/model),
// and to text only, since its search skips attachments.
func TestMattermostPayload(t *testing.T) {
	cfg := testConfig(t, "    - type: mattermost\n      url: https://chat.example.com/hooks/secret")
	r := downResult
	r.Message = strings.Repeat("é", 2*maxMessageRunes)
	body, err := json.Marshal(downMessage(cfg.Notifications.Webhooks[0], cfg.UI.Title, cfg.Monitors[0], r))
	if err != nil {
		t.Fatal(err)
	}

	var req struct {
		Text        string  `json:"text"`
		Channel     *string `json:"channel"`
		Type        *string `json:"type"`
		Blocks      any     `json:"blocks"`
		Attachments any     `json:"attachments"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatal(err)
	}
	if req.Channel != nil || req.Type != nil || req.Blocks != nil || req.Attachments != nil {
		t.Errorf("payload sets channel, type, blocks or attachments: %s", body)
	}
	if got := strings.Count(req.Text, "é"); got != maxMessageRunes-1 {
		t.Errorf("text has %d runes of the reason, want it truncated to %d", got, maxMessageRunes-1)
	}
}

type receiver struct {
	mu       sync.Mutex
	bodies   []string
	statuses []int
	headers  []map[string]string
	replies  []string
}

func (rc *receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	body, _ := io.ReadAll(r.Body)
	if r.Header.Get("Content-Type") != "application/json" {
		http.Error(w, "bad content type", http.StatusUnsupportedMediaType)
		return
	}
	n := len(rc.bodies)
	rc.bodies = append(rc.bodies, string(body))
	status, reply := http.StatusOK, "ok"
	if n < len(rc.statuses) {
		status, reply = rc.statuses[n], rc.replies[n]
		for k, v := range rc.headers[n] {
			w.Header().Set(k, v)
		}
	}
	w.WriteHeader(status)
	io.WriteString(w, reply)
}

func (rc *receiver) count() int {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return len(rc.bodies)
}

type logBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *logBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

func TestDelivery(t *testing.T) {
	retryBackoff = time.Millisecond
	t.Cleanup(func() { retryBackoff = 5 * time.Second })

	// As a production Mattermost server answers for a channel that does not exist.
	const mattermostNotFound = `{"id":"web.incoming_webhook.general.app_error","message":"Failed to handle the payload of media type application/json for incoming webhook secret.","detailed_error":"","status_code":404}`
	const mattermostDetailed = `{"id":"web.incoming_webhook.general.app_error","message":"Failed to handle the payload.","detailed_error":"Couldn't find the channel.","status_code":404}`
	tests := []struct {
		name      string
		statuses  []int
		headers   []map[string]string
		replies   []string
		wantPosts int
		wantLog   string
		minTime   time.Duration
	}{
		{name: "delivered", wantPosts: 1, wantLog: "sent down notification"},
		{name: "rate limited", statuses: []int{429}, headers: []map[string]string{{"Retry-After": "1"}}, replies: []string{"rate_limited"},
			wantPosts: 2, wantLog: "sent down notification", minTime: time.Second},
		{name: "server errors", statuses: []int{502, 503, 500}, headers: make([]map[string]string, 3), replies: []string{"", "", "rollup_error"},
			wantPosts: 3, wantLog: `err="500 Internal Server Error: rollup_error (after 3 attempts)"`},
		{name: "rejected", statuses: []int{404}, headers: make([]map[string]string, 1), replies: []string{mattermostNotFound},
			wantPosts: 1, wantLog: `err="404 Not Found: Failed to handle the payload of media type application/json for incoming webhook xxxxx."`},
		{name: "rejected in developer mode", statuses: []int{404}, headers: make([]map[string]string, 1), replies: []string{mattermostDetailed},
			wantPosts: 1, wantLog: `err="404 Not Found: Failed to handle the payload. Couldn't find the channel."`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rc := &receiver{statuses: tt.statuses, headers: tt.headers, replies: tt.replies}
			srv := httptest.NewServer(rc)
			defer srv.Close()

			var logs logBuffer
			cfg := testConfig(t, "    - type: mattermost\n      url: "+srv.URL+"/hooks/secret")
			n := New(cfg, "sante/test", slog.New(slog.NewTextHandler(&logs, nil)))
			started := time.Now()
			n.Down(t.Context(), cfg.Monitors[0], downResult)

			if took := time.Since(started); took < tt.minTime {
				t.Errorf("delivered after %s, want at least %s", took, tt.minTime)
			}
			if got := rc.count(); got != tt.wantPosts {
				t.Errorf("posts = %d, want %d", got, tt.wantPosts)
			}
			if !strings.Contains(logs.String(), tt.wantLog) {
				t.Errorf("log does not contain %q:\n%s", tt.wantLog, logs.String())
			}
			if strings.Contains(logs.String(), "secret") {
				t.Errorf("log leaks the webhook secret:\n%s", logs.String())
			}
		})
	}
}

func TestTest(t *testing.T) {
	ok := &receiver{}
	okSrv := httptest.NewServer(ok)
	defer okSrv.Close()
	rejecting := &receiver{statuses: []int{404}, headers: make([]map[string]string, 1), replies: []string{"channel_not_found"}}
	rejectingSrv := httptest.NewServer(rejecting)
	defer rejectingSrv.Close()

	cfg := testConfig(t, "    - type: slack\n      url: "+rejectingSrv.URL+"/services/T0/B0/secret\n"+
		"    - type: mattermost\n      url: "+okSrv.URL+"/hooks/secret")
	errs := New(cfg, "sante/test", slog.New(slog.DiscardHandler)).Test(t.Context())

	if len(errs) != 2 || errs[0] == nil || errs[0].Error() != "404 Not Found: channel_not_found" || errs[1] != nil {
		t.Errorf("errs = %v, want the Slack rejection and nil", errs)
	}
	if !strings.Contains(ok.bodies[0], "notification test** is down") || !strings.Contains(ok.bodies[0], "nothing is down") {
		t.Errorf("test alert does not say it is a test: %s", ok.bodies[0])
	}
}

func TestDeliveryErrorHidesURL(t *testing.T) {
	retryBackoff = time.Millisecond
	t.Cleanup(func() { retryBackoff = 5 * time.Second })

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	var logs logBuffer
	cfg := testConfig(t, "    - type: slack\n      url: http://"+addr+"/services/T0/B0/secret")
	New(cfg, "sante/test", slog.New(slog.NewTextHandler(&logs, nil))).Down(t.Context(), cfg.Monitors[0], downResult)
	if !strings.Contains(logs.String(), "connection refused") || strings.Contains(logs.String(), "secret") {
		t.Errorf("log should give the reason without the URL path:\n%s", logs.String())
	}
}

func TestRedactURL(t *testing.T) {
	if got := RedactURL("https://hooks.slack.com/services/T0/B0/secret"); got != "https://hooks.slack.com/xxxxx" {
		t.Errorf("RedactURL = %q", got)
	}
}
