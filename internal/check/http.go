package check

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/back-to-code/sante/internal/config"
)

// UserAgent is sent with HTTP checks unless the monitor sets its own.
var UserAgent = "sante"

const maxBodyBytes = 1 << 20

type httpChecker struct {
	m      config.Monitor
	client *http.Client
}

func newHTTP(m config.Monitor) *httpChecker {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// A fresh connection per check, so a pooled connection never hides a
	// target that stopped accepting new ones.
	transport.DisableKeepAlives = true
	if m.TLSSkipVerify {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	client := &http.Client{Transport: transport}
	if !*m.FollowRedirects {
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	}
	return &httpChecker{m: m, client: client}
}

func (c *httpChecker) Check(ctx context.Context) (string, error) {
	var body io.Reader
	if c.m.Body != "" {
		body = strings.NewReader(c.m.Body)
	}
	req, err := http.NewRequestWithContext(ctx, c.m.Method, c.m.URL, body)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", UserAgent)
	for k, v := range c.m.Headers {
		req.Header.Set(k, v)
	}
	if host := req.Header.Get("Host"); host != "" {
		req.Host = host
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return "", cleanError(err)
	}
	defer resp.Body.Close()

	if !c.statusOK(resp.StatusCode) {
		return "", fmt.Errorf("unexpected status %s", resp.Status)
	}
	if c.m.ExpectBody != "" {
		b, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
		if err != nil {
			return "", fmt.Errorf("reading body: %w", cleanError(err))
		}
		if !strings.Contains(string(b), c.m.ExpectBody) {
			return "", fmt.Errorf("status %s but body does not contain %q", resp.Status, c.m.ExpectBody)
		}
	}
	return resp.Status, nil
}

func (c *httpChecker) statusOK(code int) bool {
	if len(c.m.ExpectStatus) == 0 {
		return code >= 200 && code < 400
	}
	return slices.Contains(c.m.ExpectStatus, code)
}
