package web

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"runtime"
	"time"

	"github.com/back-to-code/sante/internal/check"
	"github.com/back-to-code/sante/internal/notify"
)

type healthReport struct {
	Status        string         `json:"status"`
	Version       string         `json:"version"`
	GoVersion     string         `json:"go_version"`
	StartedAt     time.Time      `json:"started_at"`
	UptimeSeconds int64          `json:"uptime_seconds"`
	Checks        healthChecks   `json:"checks"`
	Settings      healthSettings `json:"settings"`
}

type healthChecks struct {
	Database  databaseHealth  `json:"database"`
	Scheduler schedulerHealth `json:"scheduler"`
}

type databaseHealth struct {
	Status    string  `json:"status"`
	LatencyMS float64 `json:"latency_ms"`
	SizeBytes int64   `json:"size_bytes"`
	Error     string  `json:"error,omitempty"`
}

type schedulerHealth struct {
	Status      string    `json:"status"`
	Monitors    int       `json:"monitors"`
	LastCheckAt time.Time `json:"last_check_at,omitzero"`
}

type healthSettings struct {
	ConfigFile    string            `json:"config_file"`
	Listen        string            `json:"listen"`
	Timezone      string            `json:"timezone"`
	LogLevel      string            `json:"log_level"`
	Database      string            `json:"database"`
	RetentionDays int               `json:"retention_days"`
	UI            uiSettings        `json:"ui"`
	Defaults      defaultSettings   `json:"defaults"`
	Webhooks      []webhookSettings `json:"webhooks"`
	Monitors      []monitorSettings `json:"monitors"`
}

type uiSettings struct {
	Title          string `json:"title"`
	RefreshSeconds int    `json:"refresh_seconds"`
}

type defaultSettings struct {
	Interval string `json:"interval"`
	Timeout  string `json:"timeout"`
	Retries  int    `json:"retries"`
}

type webhookSettings struct {
	Type     string `json:"type"`
	URL      string `json:"url"`
	Channel  string `json:"channel,omitempty"`
	Username string `json:"username,omitempty"`
}

type monitorSettings struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Group    string  `json:"group,omitempty"`
	Type     string  `json:"type"`
	Target   string  `json:"target"`
	Interval string  `json:"interval"`
	Timeout  string  `json:"timeout"`
	Retries  int     `json:"retries"`
	Options  options `json:"options"`
}

// options keeps its display order in HTML and encodes as a JSON object.
type options [][2]string

func (o options) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, kv := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		k, _ := json.Marshal(kv[0])
		v, _ := json.Marshal(kv[1])
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

func (s *Server) healthReport(ctx context.Context) healthReport {
	cfg := s.cfg
	h := healthReport{
		Status:        "ok",
		Version:       s.version,
		GoVersion:     runtime.Version(),
		StartedAt:     s.started.UTC(),
		UptimeSeconds: int64(time.Since(s.started).Seconds()),
		Settings: healthSettings{
			ConfigFile:    cfg.Path,
			Listen:        cfg.Server.Listen,
			Timezone:      cfg.Location.String(),
			LogLevel:      cfg.Server.LogLevel,
			Database:      cfg.Storage.Path,
			RetentionDays: days,
			UI:            uiSettings{cfg.UI.Title, int(cfg.UI.Refresh.Std().Seconds())},
			Defaults:      defaultSettings{cfg.Defaults.Interval.String(), cfg.Defaults.Timeout.String(), *cfg.Defaults.Retries},
			Webhooks:      []webhookSettings{},
			Monitors:      []monitorSettings{},
		},
	}
	for _, w := range cfg.Notifications.Webhooks {
		h.Settings.Webhooks = append(h.Settings.Webhooks, webhookSettings{
			Type: string(w.Type), URL: notify.RedactURL(w.URL), Channel: w.Channel, Username: w.Username,
		})
	}

	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	started := time.Now()
	err := s.store.Ping(pingCtx)
	h.Checks.Database = databaseHealth{
		Status:    "ok",
		LatencyMS: float64(time.Since(started).Microseconds()) / 1000,
		SizeBytes: s.store.Size(),
	}
	if err != nil {
		h.Status, h.Checks.Database.Status, h.Checks.Database.Error = "unhealthy", "unhealthy", err.Error()
	}

	h.Checks.Scheduler = schedulerHealth{Status: "ok", Monitors: len(cfg.Monitors), LastCheckAt: s.scheduler.LastCheck().UTC()}
	if !s.scheduler.Running() {
		h.Status, h.Checks.Scheduler.Status = "unhealthy", "stopped"
	}

	for _, m := range cfg.Monitors {
		target, opts := check.Describe(m)
		h.Settings.Monitors = append(h.Settings.Monitors, monitorSettings{
			ID: m.ID, Name: m.Name, Group: m.Group, Type: string(m.Type), Target: target,
			Interval: m.Interval.String(), Timeout: m.Timeout.String(), Retries: *m.Retries, Options: opts,
		})
	}
	return h
}

type healthPage struct {
	layout
	Report         healthReport
	Level          level
	DatabaseLevel  level
	SchedulerLevel level
	Uptime         string
	Size           string
	Types          map[string]string
	JSON           string
}

func okLevel(status string) level {
	if status == "ok" {
		return levelOK
	}
	return levelMajor
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	report := s.healthReport(r.Context())
	status := http.StatusOK
	if report.Status != "ok" {
		status = http.StatusServiceUnavailable
	}
	if wantsJSON(r) {
		s.json(w, r, status, report)
		return
	}
	types := map[string]string{}
	for _, m := range s.cfg.Monitors {
		types[m.ID] = m.Type.Label()
	}
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, r, status, "health", healthPage{
		layout:         s.layout(r, "Health", "health", false),
		Report:         report,
		Level:          okLevel(report.Status),
		DatabaseLevel:  okLevel(report.Checks.Database.Status),
		SchedulerLevel: okLevel(report.Checks.Scheduler.Status),
		Uptime:         formatDuration(time.Since(s.started)),
		Size:           formatBytes(report.Checks.Database.SizeBytes),
		Types:          types,
		JSON:           string(body),
	})
}

func formatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return formatCount(int(n)) + " B"
	}
	f, suffix := float64(n), ""
	for _, suffix = range []string{"KB", "MB", "GB", "TB"} {
		f /= unit
		if f < unit {
			break
		}
	}
	return num(f) + " " + suffix
}
