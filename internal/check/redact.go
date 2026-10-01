package check

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/go-sql-driver/mysql"
)

const redacted = "xxxxx"

var sensitiveWords = []string{"pass", "secret", "token", "key", "auth", "cookie", "sig", "credential"}

func isSensitive(name string) bool {
	name = strings.ToLower(name)
	for _, w := range sensitiveWords {
		if strings.Contains(name, w) {
			return true
		}
	}
	return false
}

func redactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return "(unparsable URL)"
	}
	if u.RawQuery != "" {
		q := u.Query()
		for k := range q {
			if isSensitive(k) {
				q.Set(k, redacted)
			}
		}
		u.RawQuery = q.Encode()
	}
	return u.Redacted()
}

func redactMySQLDSN(dsn string) string {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return "(unparsable DSN)"
	}
	if cfg.Passwd != "" {
		cfg.Passwd = redacted
	}
	return cfg.FormatDSN()
}

var pgPassword = regexp.MustCompile(`(?i)(\bpassword\s*=\s*)('(?:[^'\\]|\\.)*'|\S+)`)

func redactPostgresDSN(dsn string) string {
	if strings.Contains(dsn, "://") {
		return redactURL(dsn)
	}
	return pgPassword.ReplaceAllString(dsn, "${1}"+redacted)
}
