package main

import (
	"context"
	"errors"
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/krisiasty/arex/config"
	"github.com/krisiasty/arex/internal/collector"
	"github.com/krisiasty/arex/internal/health"
	"github.com/krisiasty/arex/internal/metrics"
)

// shutdownGrace bounds how long a scrape in progress may take to finish. It is
// shorter than a typical Prometheus scrape timeout, so a slow client cannot
// hold up a restart.
const shutdownGrace = 5 * time.Second

func main() {
	cfgPath := flag.String("config", "config.json", "path to config file")
	debug := flag.Bool("debug", false, "log every eAPI request: status, timing, sizes and commands")
	flag.Parse()

	logger := newLogger(*debug)
	slog.SetDefault(logger)

	if err := run(logger, *cfgPath, *debug); err != nil {
		logger.Error("startup failed", "error", err)
		os.Exit(1)
	}
}

// newLogger returns a JSON logger on stdout.
//
// JSON unconditionally rather than switching format with -debug: a log format
// that changes with verbosity cannot be parsed by anything downstream, and the
// per-request debug output is only useful if it can be queried. Timestamps are
// UTC with milliseconds, so lines from different hosts sort together.
func newLogger(debug bool) *slog.Logger {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: level,
		ReplaceAttr: func(_ []string, attr slog.Attr) slog.Attr {
			if attr.Key == slog.TimeKey && attr.Value.Kind() == slog.KindTime {
				return slog.String(attr.Key, attr.Value.Time().UTC().Format("2006-01-02T15:04:05.000Z07:00"))
			}
			return attr
		},
	}))
}

func run(logger *slog.Logger, cfgPath string, debug bool) error {
	// Cancelled on SIGINT or SIGTERM, which is how a container runtime or
	// systemd asks arex to stop.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	store, err := collector.NewStore(cfg.Switches, cfg.Collect, cfg.PollInterval.Duration)
	if err != nil {
		return err
	}

	build := metrics.BuildLabels()
	logger.Info("arex starting",
		"version", build.Version,
		"revision", build.Revision,
		"go_version", build.GoVersion,
		"modified", build.Modified,
		"switches", len(cfg.Switches),
		"poll_interval", cfg.PollInterval.String(),
		"staleness_limit", cfg.StalenessLimit.String(),
		"debug", debug,
	)

	// One poller goroutine per switch, started at staggered offsets so the
	// fleet is not polled simultaneously.
	for i, sw := range cfg.Switches {
		data := store.Get(sw.Label())

		// Logged per switch: collection is configured individually, so the
		// only way to confirm what a deployment actually polls -- and how
		// often -- is to see the resolved schedule.
		logger.Info("switch schedule",
			"switch", sw.Label(),
			"modules", describeSchedule(data.Schedule()),
		)

		opts := []eapiOption{withStats(&data.Stats)}
		if debug {
			opts = append(opts, withDebug(sw.Label(), logger))
		}
		client, err := newClient(sw, cfg, opts...)
		if err != nil {
			return err
		}

		offset := collector.PollOffset(i, len(cfg.Switches), cfg.PollInterval.Duration)
		go collector.PollLoop(ctx, client, data, cfg.PollInterval.Duration, offset)
	}

	checker := health.New(store, cfg.PollInterval.Duration)

	mux := http.NewServeMux()
	mux.Handle("/metrics", metrics.NewHandler(store, cfg.StalenessLimit.Duration,
		metrics.TargetIndex(cfg.Switches)))
	checker.Register(mux, logger, cfg.StalenessLimit.Duration)

	srv := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	logger.Info("listening", "address", cfg.ListenAddress,
		"endpoints", []string{"/metrics", "/livez", "/readyz", "/status"})

	serveErr := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	select {
	case err := <-serveErr:
		return err
	case <-ctx.Done():
	}
	stop() // restore default handling, so a second signal kills immediately

	// Let a scrape already in progress finish. Without this a restart can cut a
	// /metrics response mid-write, which Prometheus records as a failed scrape
	// rather than a clean gap.
	logger.Info("shutting down", "grace", shutdownGrace.String())
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Warn("shutdown incomplete", "error", err)
	}
	logger.Info("arex stopped")
	return nil
}

// describeSchedule renders a switch's command schedule as "command=interval"
// pairs, so one log line shows the whole resolved plan.
func describeSchedule(sched []collector.ModuleSchedule) string {
	parts := make([]string, 0, len(sched))
	for _, m := range sched {
		parts = append(parts, m.Command+"="+m.Interval.String())
	}
	return strings.Join(parts, " ")
}
