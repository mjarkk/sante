package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"go.yaml.in/yaml/v3"
)

// Retention is how long check results are kept; the status page shows one bar per day of it.
const Retention = 90 * 24 * time.Hour

type Type string

const (
	HTTP     Type = "http"
	TCP      Type = "tcp"
	Redis    Type = "redis"
	MySQL    Type = "mysql"
	Postgres Type = "postgres"
)

func (t Type) Label() string {
	switch t {
	case HTTP:
		return "HTTP"
	case TCP:
		return "TCP"
	case Redis:
		return "Redis"
	case MySQL:
		return "MySQL"
	case Postgres:
		return "PostgreSQL"
	}
	return string(t)
}

type Config struct {
	Server        Server        `yaml:"server"`
	Storage       Storage       `yaml:"storage"`
	UI            UI            `yaml:"ui"`
	Defaults      Defaults      `yaml:"defaults"`
	Notifications Notifications `yaml:"notifications"`
	Monitors      []Monitor     `yaml:"monitors"`

	Path     string         `yaml:"-"`
	Location *time.Location `yaml:"-"`
}

type Server struct {
	Listen    string `yaml:"listen"`
	Timezone  string `yaml:"timezone"`
	LogLevel  string `yaml:"log_level"`
	AuthToken string `yaml:"auth_token"`
}

type Storage struct {
	Path string `yaml:"path"`
}

type UI struct {
	Title   string   `yaml:"title"`
	Refresh Duration `yaml:"refresh"`
}

type Defaults struct {
	Interval Duration `yaml:"interval"`
	Timeout  Duration `yaml:"timeout"`
	Retries  *int     `yaml:"retries"`
}

type Notifications struct {
	Webhooks []Webhook `yaml:"webhooks"`
}

type WebhookType string

const (
	Slack      WebhookType = "slack"
	Mattermost WebhookType = "mattermost"
)

func (t WebhookType) Label() string {
	switch t {
	case Slack:
		return "Slack"
	case Mattermost:
		return "Mattermost"
	}
	return string(t)
}

type Webhook struct {
	Type     WebhookType `yaml:"type"`
	URL      string      `yaml:"url"`
	Channel  string      `yaml:"channel"`
	Username string      `yaml:"username"`
}

type Monitor struct {
	ID          string   `yaml:"id"`
	Name        string   `yaml:"name"`
	Group       string   `yaml:"group"`
	Description string   `yaml:"description"`
	Type        Type     `yaml:"type"`
	Interval    Duration `yaml:"interval"`
	Timeout     Duration `yaml:"timeout"`
	Retries     *int     `yaml:"retries"`

	// http
	URL             string            `yaml:"url"`
	Method          string            `yaml:"method"`
	Headers         map[string]string `yaml:"headers"`
	Body            string            `yaml:"body"`
	ExpectStatus    []int             `yaml:"expect_status"`
	ExpectBody      string            `yaml:"expect_body"`
	FollowRedirects *bool             `yaml:"follow_redirects"`
	TLSSkipVerify   bool              `yaml:"tls_skip_verify"`

	// tcp, redis
	Address string `yaml:"address"`

	// redis
	Username string `yaml:"username"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
	TLS      bool   `yaml:"tls"`

	// mysql, postgres
	DSN   string `yaml:"dsn"`
	Query string `yaml:"query"`
}

// Duration accepts Go duration strings ("90s", "5m") and bare integers as seconds.
type Duration time.Duration

func (d Duration) Std() time.Duration { return time.Duration(d) }

func (d Duration) String() string { return time.Duration(d).String() }

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("line %d: expected a duration such as 30s or 5m", n.Line)
	}
	if secs, err := strconv.Atoi(n.Value); err == nil {
		*d = Duration(time.Duration(secs) * time.Second)
		return nil
	}
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return fmt.Errorf("line %d: invalid duration %q, use a value such as 30s or 5m", n.Line, n.Value)
	}
	*d = Duration(v)
	return nil
}

// Load reads the config file at path, expands environment variable references
// in its values, applies defaults and validates the result. The returned
// warnings name variables that were referenced without a default but are unset.
func Load(path string) (*Config, []string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	cfg, warnings, err := Parse(raw)
	if err != nil {
		return nil, warnings, fmt.Errorf("%s: %w", path, err)
	}
	cfg.Path = path
	return cfg, warnings, nil
}

func Parse(raw []byte) (*Config, []string, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(raw, &root); err != nil {
		return nil, nil, err
	}

	var warnings []string
	seen := map[string]bool{}
	missing := func(name string) {
		if !seen[name] {
			seen[name] = true
			warnings = append(warnings, fmt.Sprintf("environment variable %s is not set, using an empty value", name))
		}
	}
	if err := expandNode(&root, missing); err != nil {
		return nil, warnings, err
	}

	cfg := &Config{}
	if len(root.Content) == 0 {
		return nil, warnings, errors.New("config is empty")
	}
	if err := unknownFields(&root, reflect.TypeFor[*Config]()); err != nil {
		return nil, warnings, err
	}
	if err := root.Decode(cfg); err != nil {
		return nil, warnings, err
	}
	if err := cfg.normalize(); err != nil {
		return nil, warnings, err
	}
	return cfg, warnings, nil
}

func expandNode(n *yaml.Node, missing func(string)) error {
	if n.Kind == yaml.ScalarNode {
		v, err := expand(n.Value, os.LookupEnv, missing)
		if err != nil {
			return fmt.Errorf("line %d: %w", n.Line, err)
		}
		if v != n.Value {
			n.Value = v
			// The tag was resolved from the unexpanded text, so `port: ${PORT}`
			// would stay a string; clearing it lets the decoder resolve again.
			if n.Style&(yaml.SingleQuotedStyle|yaml.DoubleQuotedStyle|yaml.LiteralStyle|yaml.FoldedStyle) == 0 {
				n.Tag = ""
			}
		}
		return nil
	}
	for i, child := range n.Content {
		if n.Kind == yaml.MappingNode && i%2 == 0 {
			continue
		}
		if err := expandNode(child, missing); err != nil {
			return err
		}
	}
	return nil
}

// unknownFields rejects mapping keys that no struct field claims, so a typo
// such as "intervall" fails loudly instead of silently using the default.
func unknownFields(n *yaml.Node, t reflect.Type) error {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch n.Kind {
	case yaml.DocumentNode:
		return unknownFields(n.Content[0], t)
	case yaml.AliasNode:
		return unknownFields(n.Alias, t)
	case yaml.SequenceNode:
		if t.Kind() != reflect.Slice {
			return nil
		}
		for _, item := range n.Content {
			if err := unknownFields(item, t.Elem()); err != nil {
				return err
			}
		}
	case yaml.MappingNode:
		if t.Kind() != reflect.Struct {
			return nil
		}
		fields := map[string]reflect.Type{}
		var names []string
		for f := range t.Fields() {
			name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			if name == "" || name == "-" {
				continue
			}
			fields[name] = f.Type
			names = append(names, name)
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := n.Content[i]
			if key.Value == "<<" {
				continue
			}
			ft, ok := fields[key.Value]
			if !ok {
				return fmt.Errorf("line %d: unknown field %q (known fields: %s)", key.Line, key.Value, strings.Join(names, ", "))
			}
			if err := unknownFields(n.Content[i+1], ft); err != nil {
				return err
			}
		}
	}
	return nil
}

var idPattern = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}._-]*$`)

func (c *Config) normalize() error {
	if c.Server.Listen == "" {
		c.Server.Listen = ":8080"
	}
	if c.Server.Timezone == "" {
		c.Server.Timezone = "UTC"
	}
	loc, err := time.LoadLocation(c.Server.Timezone)
	if err != nil {
		return fmt.Errorf("server.timezone: %w", err)
	}
	c.Location = loc
	if c.Server.LogLevel == "" {
		c.Server.LogLevel = "info"
	}
	if !slices.Contains([]string{"debug", "info", "warn", "error"}, c.Server.LogLevel) {
		return fmt.Errorf("server.log_level: %q is not one of debug, info, warn, error", c.Server.LogLevel)
	}

	if c.Storage.Path == "" {
		c.Storage.Path = "data/sante.db"
	}

	c.UI.Title = strings.TrimSpace(c.UI.Title)
	if c.UI.Title == "" {
		c.UI.Title = "Sante"
	}
	if c.UI.Refresh == 0 {
		c.UI.Refresh = Duration(30 * time.Second)
	}
	if c.UI.Refresh.Std() < 5*time.Second {
		return errors.New("ui.refresh: must be at least 5s")
	}

	if c.Defaults.Interval == 0 {
		c.Defaults.Interval = Duration(time.Minute)
	}
	if c.Defaults.Timeout == 0 {
		c.Defaults.Timeout = Duration(10 * time.Second)
	}
	if c.Defaults.Retries == nil {
		c.Defaults.Retries = new(0)
	}
	if c.Defaults.Interval.Std() < time.Second {
		return errors.New("defaults.interval: must be at least 1s")
	}
	if c.Defaults.Timeout <= 0 {
		return errors.New("defaults.timeout: must be positive")
	}
	if *c.Defaults.Retries < 0 {
		return errors.New("defaults.retries: must not be negative")
	}

	for i := range c.Notifications.Webhooks {
		if err := c.Notifications.Webhooks[i].normalize(); err != nil {
			return fmt.Errorf("notifications.webhooks[%d]: %w", i, err)
		}
	}

	ids := map[string]int{}
	for i := range c.Monitors {
		m := &c.Monitors[i]
		if err := m.normalize(c.Defaults); err != nil {
			label := fmt.Sprintf("monitors[%d]", i)
			if m.Name != "" {
				label = fmt.Sprintf("monitors[%d] (%s)", i, m.Name)
			}
			return fmt.Errorf("%s: %w", label, err)
		}
		if prev, dup := ids[m.ID]; dup {
			return fmt.Errorf("monitors[%d] (%s): id %q is already used by monitors[%d], set a unique id", i, m.Name, m.ID, prev)
		}
		ids[m.ID] = i
	}

	if c.Server.AuthToken == "" {
		return errors.New("server.auth_token: required, it unlocks /health and the monitor pages")
	}
	return nil
}

func (w *Webhook) normalize() error {
	w.Type = WebhookType(strings.ToLower(string(w.Type)))
	switch w.Type {
	case Slack, Mattermost:
	case "":
		return errors.New("type is required (slack or mattermost)")
	default:
		return fmt.Errorf("unknown type %q (want slack or mattermost)", w.Type)
	}
	if u, err := url.Parse(w.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("url must be an absolute http:// or https:// URL")
	}
	w.Channel = strings.TrimSpace(w.Channel)
	w.Username = strings.TrimSpace(w.Username)
	return nil
}

func (m *Monitor) normalize(d Defaults) error {
	m.Name = strings.TrimSpace(m.Name)
	if m.Name == "" {
		return errors.New("name is required")
	}
	if m.ID == "" {
		m.ID = slugify(m.Name)
		if m.ID == "" {
			return errors.New("cannot derive an id from the name, set id explicitly")
		}
	} else if !idPattern.MatchString(m.ID) {
		return fmt.Errorf("id %q may only contain letters, digits, '.', '_' and '-'", m.ID)
	}

	if m.Interval == 0 {
		m.Interval = d.Interval
	}
	if m.Interval.Std() < time.Second {
		return errors.New("interval must be at least 1s")
	}
	switch {
	case m.Timeout == 0:
		m.Timeout = min(d.Timeout, m.Interval)
	case m.Timeout < 0:
		return errors.New("timeout must be positive")
	case m.Timeout > m.Interval:
		return fmt.Errorf("timeout %s is longer than interval %s", m.Timeout, m.Interval)
	}
	if m.Retries == nil {
		m.Retries = new(*d.Retries)
	}
	if *m.Retries < 0 {
		return errors.New("retries must not be negative")
	}

	m.Type = Type(strings.ToLower(string(m.Type)))
	if m.Type == "postgresql" {
		m.Type = Postgres
	}
	switch m.Type {
	case HTTP:
		u, err := url.Parse(m.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return errors.New("url must be an absolute http:// or https:// URL")
		}
		m.Method = strings.ToUpper(m.Method)
		if m.Method == "" {
			m.Method = "GET"
		}
		for _, code := range m.ExpectStatus {
			if code < 100 || code > 599 {
				return fmt.Errorf("expect_status %d is not an HTTP status code", code)
			}
		}
		if m.FollowRedirects == nil {
			m.FollowRedirects = new(true)
		}
	case TCP, Redis:
		if _, _, err := net.SplitHostPort(m.Address); err != nil {
			return fmt.Errorf("address must be host:port: %w", err)
		}
		if m.DB < 0 {
			return errors.New("db must not be negative")
		}
	case MySQL, Postgres:
		if m.DSN == "" {
			return errors.New("dsn is required")
		}
	case "":
		return errors.New("type is required (http, tcp, redis, mysql or postgres)")
	default:
		return fmt.Errorf("unknown type %q (want http, tcp, redis, mysql or postgres)", m.Type)
	}
	return nil
}

func slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			b.WriteRune(r)
			dash = false
		} else {
			dash = true
		}
	}
	return b.String()
}
