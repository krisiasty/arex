// Command arex polls Arista EOS switches over eAPI and exposes the results as
// Prometheus metrics.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
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
	"github.com/krisiasty/arex/internal/legal"
	"github.com/krisiasty/arex/internal/metrics"
)

// shutdownGrace bounds how long a scrape in progress may take to finish. It is
// shorter than a typical Prometheus scrape timeout, so a slow client cannot
// hold up a restart.
const shutdownGrace = 5 * time.Second

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config file")
	debug := flag.Bool("debug", false,
		"log every eAPI request: status, timing, sizes and commands; overrides the config")
	licenses := flag.Bool("licenses", false, "print third-party licenses and notices, then exit")
	check := flag.Bool("check", false, "validate the config file and exit")
	flag.Parse()

	// Answered before anything else: the notices have to be readable from a
	// container that has no shell and no copy of the source tree.
	if *licenses {
		fmt.Print(legal.ThirdPartyNotices())
		return
	}

	// Whether the flag was given at all, as opposed to what it defaulted to:
	// a bool flag cannot otherwise be distinguished from an absent one, and
	// -debug=false has to be able to override a config that enables it.
	debugSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "debug" {
			debugSet = true
		}
	})

	if *check {
		if err := checkConfig(*cfgPath); err != nil {
			fmt.Fprintf(os.Stderr, "arex: %v\n", err)
			os.Exit(1)
		}
		return
	}

	if err := run(*cfgPath, *debug, debugSet); err != nil {
		// Plain text rather than a JSON log line. Nothing reaches this that is
		// not a startup failure, so the only reader is the person who just ran
		// arex -- and a JSON string escapes every quote in a message whose
		// purpose is to show them the shape a config field wants.
		fmt.Fprintf(os.Stderr, "arex: %v\n", err)
		os.Exit(1)
	}
}

// checkConfig validates a config file without starting anything.
//
// Useful as a systemd ExecStartPre, where a typo should stop the restart rather
// than take the service down, and in CI, where the example manifests would
// otherwise rot unnoticed.
func checkConfig(path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	for _, w := range cfg.Warnings {
		fmt.Fprintf(os.Stderr, "arex: warning: %s\n", w)
	}

	// Building each client exercises everything that can fail before the first
	// request: an unreadable CA bundle, a malformed certificate pin, a
	// credential file that has stopped being readable. Nothing connects.
	for _, sw := range cfg.Switches {
		if _, err := newClient(sw, cfg); err != nil {
			return fmt.Errorf("switch %s: %w", sw.Label(), err)
		}
	}

	fmt.Printf("%s: %d switch(es), poll interval %s\n",
		path, len(cfg.Switches), cfg.PollInterval)
	return nil
}

// resolveDebug picks between the config's setting and the flag. The flag wins
// when it was given, so a deployment can be started verbosely without editing
// its config, or quietly with -debug=false when its config enables debug.
func resolveDebug(fromConfig, fromFlag, flagSet bool) bool {
	if flagSet {
		return fromFlag
	}
	return fromConfig
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

func run(cfgPath string, debugFlag, debugSet bool) error {
	// Cancelled on SIGINT or SIGTERM, which is how a container runtime or
	// systemd asks arex to stop.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	// The logger is built after the config is read, since the config can set
	// the level. Nothing logs before this point: a config error is printed by
	// main, not logged.
	debug := resolveDebug(cfg.Debug, debugFlag, debugSet)
	logger := newLogger(debug)
	slog.SetDefault(logger)

	// Collected during Load, which runs before the logger exists.
	for _, w := range cfg.Warnings {
		logger.Warn("configuration warning", "detail", w)
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
