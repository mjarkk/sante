package main

import (
	"cmp"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/back-to-code/sante/internal/check"
	"github.com/back-to-code/sante/internal/config"
	"github.com/back-to-code/sante/internal/scheduler"
	"github.com/back-to-code/sante/internal/seed"
	"github.com/back-to-code/sante/internal/store"
	"github.com/back-to-code/sante/internal/web"
)

var version = "dev"

const usage = `Usage: sante [command] [-config path]

Commands:
  serve        run the monitor and web UI (default)
                 -seed   first fill an empty database with 90 days of demo history
  seed         fill the database with 90 days of synthetic history and exit
                 -reset  replace the existing history of the configured monitors
  validate     check the config file and exit
  healthcheck  exit 0 when the running instance reports healthy
  version      print the version

The config path defaults to $SANTE_CONFIG, then config.yaml.
`

func main() {
	if err := run(os.Args[1:]); err != nil {
		if !errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(os.Stderr, "sante:", err)
		}
		os.Exit(1)
	}
}

func run(args []string) error {
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	flags := flag.NewFlagSet("sante", flag.ContinueOnError)
	flags.Usage = func() { fmt.Fprint(os.Stderr, usage) }
	flags.SetOutput(io.Discard)
	configPath := flags.String("config", cmp.Or(os.Getenv("SANTE_CONFIG"), "config.yaml"), "path to the YAML config file")
	var seedEmpty, reset bool
	switch cmd {
	case "serve":
		flags.BoolVar(&seedEmpty, "seed", false, "fill an empty database with synthetic history first")
	case "seed":
		flags.BoolVar(&reset, "reset", false, "replace the existing history of the configured monitors")
	}
	if err := flags.Parse(args); err != nil {
		return err
	}

	switch cmd {
	case "serve":
		return serve(*configPath, seedEmpty)
	case "seed":
		return seedHistory(*configPath, reset)
	case "validate":
		return validate(*configPath)
	case "healthcheck":
		return healthcheck(*configPath)
	case "version":
		fmt.Println(version)
		return nil
	case "help":
		flags.Usage()
		return nil
	}
	flags.Usage()
	return fmt.Errorf("unknown command %q", cmd)
}

func load(path string, log *slog.Logger) (*config.Config, []scheduler.Job, error) {
	cfg, warnings, err := config.Load(path)
	for _, w := range warnings {
		log.Warn(w)
	}
	if err != nil {
		return nil, nil, err
	}
	jobs := make([]scheduler.Job, 0, len(cfg.Monitors))
	for _, m := range cfg.Monitors {
		c, err := check.New(m)
		if err != nil {
			return nil, nil, fmt.Errorf("monitor %s: %w", m.ID, err)
		}
		jobs = append(jobs, scheduler.Job{Monitor: m, Checker: c})
	}
	return cfg, jobs, nil
}

func serve(path string, seedEmpty bool) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, jobs, err := load(path, log)
	if err != nil {
		return err
	}
	var level slog.Level
	_ = level.UnmarshalText([]byte(cfg.Server.LogLevel))
	log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(log)
	check.UserAgent = "sante/" + version

	st, err := store.Open(cfg.Storage.Path, cfg.Location)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if seedEmpty {
		has, err := st.HasResults(ctx)
		if err != nil {
			return err
		}
		if has {
			log.Info("database already has results, not seeding")
		} else {
			n, err := seed.Run(ctx, st, cfg.Monitors, time.Now())
			if err != nil {
				return err
			}
			log.Info("seeded synthetic history", "results", n, "monitors", len(cfg.Monitors))
		}
	}

	sched := scheduler.New(st, jobs, log)
	srv, err := web.New(cfg, st, sched, version, log)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Addr:              cfg.Server.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	ln, err := net.Listen("tcp", cfg.Server.Listen)
	if err != nil {
		return err
	}

	schedDone := make(chan struct{})
	go func() {
		sched.Run(ctx)
		close(schedDone)
	}()
	serveErr := make(chan error, 1)
	go func() { serveErr <- httpServer.Serve(ln) }()

	log.Info("sante started", "version", version, "listen", ln.Addr().String(), "monitors", len(jobs),
		"database", cfg.Storage.Path, "timezone", cfg.Location.String())

	select {
	case err = <-serveErr:
		stop()
	case <-ctx.Done():
		log.Info("shutting down")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if serr := httpServer.Shutdown(shutdownCtx); serr != nil && err == nil {
		err = serr
	}
	<-schedDone
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}

func seedHistory(path string, reset bool) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, _, err := load(path, log)
	if err != nil {
		return err
	}
	st, err := store.Open(cfg.Storage.Path, cfg.Location)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer st.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if reset {
		ids := make([]string, len(cfg.Monitors))
		for i, m := range cfg.Monitors {
			ids[i] = m.ID
		}
		if err := st.Delete(ctx, ids...); err != nil {
			return err
		}
	} else if has, err := st.HasResults(ctx); err != nil {
		return err
	} else if has {
		return fmt.Errorf("%s already holds check results; pass -reset to replace the history of the configured monitors", cfg.Storage.Path)
	}

	started := time.Now()
	n, err := seed.Run(ctx, st, cfg.Monitors, started)
	if err != nil {
		return err
	}
	fmt.Printf("seeded %d results for %d monitors into %s in %s\n", n, len(cfg.Monitors), cfg.Storage.Path, time.Since(started).Round(time.Millisecond))
	return nil
}

func validate(path string) error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	cfg, _, err := load(path, log)
	if err != nil {
		return err
	}
	fmt.Printf("%s is valid: %d monitors, listening on %s, timezone %s\n", path, len(cfg.Monitors), cfg.Server.Listen, cfg.Location)
	for _, m := range cfg.Monitors {
		target, _ := check.Describe(m)
		fmt.Printf("  %-24s %-10s every %-6s %s\n", m.ID, m.Type.Label(), m.Interval, target)
	}
	return nil
}

func healthcheck(path string) error {
	cfg, _, err := config.Load(path)
	if err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(cfg.Server.Listen)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); host == "" || ip != nil && ip.IsUnspecified() {
		host = "127.0.0.1"
	}
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", "http://"+net.JoinHostPort(host, port)+"/health?format=json", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.Server.AuthToken)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", resp.Status)
	}
	return nil
}
