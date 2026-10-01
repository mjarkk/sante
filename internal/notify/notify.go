package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/back-to-code/sante/internal/config"
	"github.com/back-to-code/sante/internal/store"
)

const (
	attempts       = 3
	attemptTimeout = 10 * time.Second
	maxRetryAfter  = time.Minute
)

var retryBackoff = 5 * time.Second

// Notifier is safe for concurrent use.
type Notifier struct {
	hooks     []*webhook
	title     string
	userAgent string
	client    *http.Client
	log       *slog.Logger
}

type webhook struct {
	config.Webhook
	// One send at a time: an outage that downs many monitors at once would
	// otherwise trip Slack's limit of about one message per second per webhook.
	mu sync.Mutex
}

func New(cfg *config.Config, userAgent string, log *slog.Logger) *Notifier {
	n := &Notifier{title: cfg.UI.Title, userAgent: userAgent, client: &http.Client{}, log: log}
	for _, w := range cfg.Notifications.Webhooks {
		n.hooks = append(n.hooks, &webhook{Webhook: w})
	}
	return n
}

// Down returns once every webhook has the message or has failed, and logs
// failures rather than returning them. Cancelling ctx stops retries but not
// an attempt in flight.
func (n *Notifier) Down(ctx context.Context, m config.Monitor, r store.Result) {
	var wg sync.WaitGroup
	for _, h := range n.hooks {
		wg.Go(func() {
			body, err := json.Marshal(downMessage(h.Webhook, n.title, m, r))
			if err == nil {
				err = n.deliver(ctx, h, body)
			}
			attrs := []any{"monitor", m.ID, "webhook", h.Type, "url", RedactURL(h.URL)}
			if err != nil {
				n.log.Error("sending down notification", append(attrs, "err", err)...)
			} else {
				n.log.Info("sent down notification", attrs...)
			}
		})
	}
	wg.Wait()
}

func (n *Notifier) deliver(ctx context.Context, h *webhook, body []byte) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	for attempt := 1; ; attempt++ {
		// Not cancelled on shutdown: the result is already stored as down, so
		// after a restart this alert would never be sent.
		retry, retryAfter, err := n.post(context.WithoutCancel(ctx), h.URL, body)
		if err == nil || !retry || attempt == attempts {
			if err != nil && attempt > 1 {
				err = fmt.Errorf("%w (after %d attempts)", err, attempt)
			}
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(max(retryAfter, time.Duration(attempt)*retryBackoff)):
		}
	}
}

// post reports whether a failure may be temporary, and how long the receiver
// asked to wait before trying again (zero when it did not say).
func (n *Notifier) post(ctx context.Context, url string, body []byte) (retry bool, retryAfter time.Duration, err error) {
	ctx, cancel := context.WithTimeout(ctx, attemptTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return false, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	// Mattermost translates its error messages into the server's default language otherwise.
	req.Header.Set("Accept-Language", "en")
	req.Header.Set("User-Agent", n.userAgent)
	resp, err := n.client.Do(req)
	if err != nil {
		return true, 0, cleanError(err)
	}
	defer resp.Body.Close()
	reply, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return false, 0, nil
	}
	err = errors.New(resp.Status)
	if msg := replyMessage(reply); msg != "" {
		// Mattermost's messages quote the hook id, which is the secret.
		if secret := path.Base(req.URL.Path); len(secret) > 1 {
			msg = strings.ReplaceAll(msg, secret, "xxxxx")
		}
		err = fmt.Errorf("%s: %s", resp.Status, msg)
	}
	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		secs, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		return true, min(time.Duration(secs)*time.Second, maxRetryAfter), err
	case resp.StatusCode >= 500:
		return true, 0, err
	}
	return false, 0, err
}

// replyMessage extracts the reason from an error reply: Slack answers in
// plain text ("channel_not_found"), Mattermost in JSON with a generic
// "message" and the actual cause in "detailed_error", which servers outside
// developer mode leave empty.
func replyMessage(reply []byte) string {
	var mattermost struct {
		Message       string `json:"message"`
		DetailedError string `json:"detailed_error"`
	}
	if json.Unmarshal(reply, &mattermost) == nil && mattermost.Message != "" {
		if mattermost.DetailedError != "" {
			return mattermost.Message + " " + mattermost.DetailedError
		}
		return mattermost.Message
	}
	return truncate(strings.Join(strings.Fields(string(reply)), " "), 200)
}

// cleanError drops the wrapping that net/http adds, which repeats the
// webhook URL and with it the secret in its path.
func cleanError(err error) error {
	if uerr, ok := errors.AsType[*url.Error](err); ok {
		err = uerr.Err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("timed out")
	}
	return err
}

// RedactURL keeps only the scheme and host of a webhook URL; Slack and
// Mattermost both put the secret in the path.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(unparsable URL)"
	}
	return u.Scheme + "://" + u.Host + "/xxxxx"
}
