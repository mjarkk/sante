package check

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/back-to-code/sante/internal/config"
)

type Checker interface {
	// Check returns a short description of the healthy response, or an error
	// describing why the target is unhealthy. ctx carries the check timeout.
	Check(ctx context.Context) (string, error)
}

// New fails when m's type-specific settings, such as a DSN, do not parse.
func New(m config.Monitor) (Checker, error) {
	switch m.Type {
	case config.HTTP:
		return newHTTP(m), nil
	case config.TCP:
		return tcpChecker{address: m.Address}, nil
	case config.Redis:
		return newRedis(m), nil
	case config.MySQL:
		return newMySQL(m)
	case config.Postgres:
		return newPostgres(m)
	}
	return nil, fmt.Errorf("unsupported type %q", m.Type)
}

// Describe returns the target and settings of m for display, with secrets redacted.
func Describe(m config.Monitor) (target string, options [][2]string) {
	opt := func(k, v string) { options = append(options, [2]string{k, v}) }
	switch m.Type {
	case config.HTTP:
		target = redactURL(m.URL)
		opt("method", m.Method)
		if len(m.ExpectStatus) > 0 {
			opt("expect_status", fmt.Sprint(m.ExpectStatus))
		} else {
			opt("expect_status", "200-399")
		}
		if m.ExpectBody != "" {
			opt("expect_body", m.ExpectBody)
		}
		opt("follow_redirects", fmt.Sprint(*m.FollowRedirects))
		if m.TLSSkipVerify {
			opt("tls_skip_verify", "true")
		}
		for k, v := range m.Headers {
			if isSensitive(k) {
				v = redacted
			}
			opt("header "+k, v)
		}
		if m.Body != "" {
			opt("body", fmt.Sprintf("%d bytes", len(m.Body)))
		}
	case config.TCP:
		target = m.Address
	case config.Redis:
		target = m.Address
		if m.Username != "" {
			opt("username", m.Username)
		}
		if m.Password != "" {
			opt("password", redacted)
		}
		opt("db", fmt.Sprint(m.DB))
		opt("tls", fmt.Sprint(m.TLS))
	case config.MySQL:
		target = redactMySQLDSN(m.DSN)
		opt("query", queryOrPing(m.Query))
	case config.Postgres:
		target = redactPostgresDSN(m.DSN)
		opt("query", queryOrPing(m.Query))
	}
	return target, options
}

func queryOrPing(q string) string {
	if q == "" {
		return "(ping)"
	}
	return q
}

// cleanError drops the wrapping that net/http adds, which repeats the
// request URL including any credentials in its query string.
func cleanError(err error) error {
	if uerr, ok := errors.AsType[*url.Error](err); ok {
		err = uerr.Err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return errors.New("timed out")
	}
	return err
}
