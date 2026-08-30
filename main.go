// Command arex polls Arista EOS switches over eAPI and exposes the results as
// Prometheus metrics.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/krisiasty/arex/config"
	"github.com/krisiasty/arex/internal/buildinfo"
	"github.com/krisiasty/arex/internal/collector"
	"github.com/krisiasty/arex/internal/health"
	"github.com/krisiasty/arex/internal/legal"
	"github.com/krisiasty/arex/internal/listen"
	"github.com/krisiasty/arex/internal/metrics"
	"github.com/krisiasty/arex/internal/secret"
)

// shutdownGrace bounds how long a scrape in progress may take to finish. It is
// shorter than a typical Prometheus scrape timeout, so a slow client cannot
// hold up a restart.
const shutdownGrace = 5 * time.Second

func main() {
	cfgPath := flag.String("config", "config.yaml", "path to config file")
	debug := flag.Bool("debug", false,
		"log every eAPI request: status, timing, sizes and commands; overrides the config")
	version := flag.Bool("version", false, "print version and build information, then exit")
	licenses := flag.Bool("licenses", false, "print third-party licenses and notices, then exit")
	check := flag.Bool("check", false, "validate the config file and exit")
	flag.Parse()

	// Both answered before anything else, for the same reason: they have to be
	// readable from a container that has no shell and no copy of the source
	// tree, and without a config file existing.
	if *version {
		fmt.Println(buildinfo.String())
		return
	}
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

// startProbes serves /livez and /readyz on their own plain-HTTP listener.
//
// Returns nil when probeAddress is unset, which is the default: one listener
// serves everything, and the probes are simply exempt from authentication.
func startProbes(ctx context.Context, cfg *config.Config, checker *health.Checker,
	logger *slog.Logger) (*http.Server, error) {
	if cfg.ProbeAddress == "" {
		return nil, nil //nolint:nilnil // no probe listener is a valid outcome
	}

	mux := http.NewServeMux()
	// Only the two liveness endpoints. /status stays on the main listener: it
	// names the switches, and this one has neither TLS nor authentication.
	checker.RegisterProbes(mux)

	srv := &http.Server{
		Addr:              cfg.ProbeAddress,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	// Bound here rather than inside the goroutine so an address already in use
	// is an error from run, not a log line after arex claims to be serving.
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", cfg.ProbeAddress)
	if err != nil {
		return nil, fmt.Errorf("probe listener: %w", err)
	}

	logger.Info("serving probes", "address", cfg.ProbeAddress,
		"endpoints", []string{"/livez", "/readyz"}, "tls", false, "auth", false)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("probe listener stopped", "error", err)
		}
	}()
	return srv, nil
}

// protect wraps the mux with whatever authentication is configured.
//
// The probes are exempt: a Kubernetes liveness probe sends no credentials, so
// requiring them on /livez would turn a health check into a restart loop, and
// those endpoints report only whether arex is up. /status is not exempt --
// it names the switches.
func protect(mux http.Handler, cfg *config.Config, logger *slog.Logger) (http.Handler, error) {
	b := cfg.ListenAuth.Basic
	if b == nil {
		return mux, nil
	}
	cred, err := secret.NewFileCredential(b.PasswordFile)
	if err != nil {
		return nil, fmt.Errorf("listenAuth.basic: %w", err)
	}
	logger.Info("requiring basic authentication",
		"user", b.Username, "exempt", []string{"/livez", "/readyz"})
	return listen.BasicAuth(mux, b.Username, cred, "/livez", "/readyz"), nil
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

// shutdownSignals stop arex. These are what a container runtime and systemd
// send to ask a service to exit.
var shutdownSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM}

// nonFatalSignals are caught so they do not kill the process.
//
// SIGHUP is here because Go's default disposition for it is to terminate, and
// conventionally it asks a daemon to reload. arex reads its config once, at
// startup, so there is nothing to reload -- but dying is the wrong answer to
// being asked, and it is exactly what a "systemctl reload" used to do.
var nonFatalSignals = []os.Signal{syscall.SIGHUP}

func run(cfgPath string, debugFlag, debugSet bool) error {
	// Cancelled on SIGINT or SIGTERM, which is how a container runtime or
	// systemd asks arex to stop.
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals...)
	defer stop()

	// Caught and reported rather than left to the default disposition. The
	// channel is buffered so a signal arriving before the reader is ready is
	// not lost, and never closed: the process is exiting either way.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, nonFatalSignals...)
	defer signal.Stop(hup)

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
		//nolint:govet // shadow: per-iteration, and assigning the outer err would leak between switches
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

	handler, err := protect(mux, cfg, logger)
	if err != nil {
		return err
	}

	tlsCfg, err := listen.TLSConfig(listen.Options{
		CertFile:     cfg.ListenTLS.CertFile,
		KeyFile:      cfg.ListenTLS.KeyFile,
		ClientCAFile: cfg.ListenTLS.ClientCAFile,
	})
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           handler,
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	logger.Info("listening", "address", cfg.ListenAddress,
		"endpoints", []string{"/metrics", "/livez", "/readyz", "/status"},
		"tls", cfg.ListenTLS.Enabled(),
		"client_certificate", cfg.ListenTLS.RequiresClientCert(),
		"auth", cfg.ListenAuth.Enabled())

	// The probe listener, when configured: plain HTTP, and only the two
	// endpoints that report whether arex is up. Started before the main one so
	// a failure to bind is reported before anything is serving.
	probeSrv, err := startProbes(ctx, cfg, checker, logger)
	if err != nil {
		return err
	}
	if probeSrv != nil {
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
			defer cancel()
			_ = probeSrv.Shutdown(shutdownCtx)
		}()
	}

	serveErr := make(chan error, 1)
	go func() {
		// Empty strings: the certificate comes from TLSConfig, which reloads
		// it when it changes on disk.
		serve := srv.ListenAndServe
		if tlsCfg != nil {
			serve = func() error { return srv.ListenAndServeTLS("", "") }
		}
		if err := serve(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
		}
	}()

	for done := false; !done; {
		select {
		case err := <-serveErr:
			return err
		case s := <-hup:
			// Someone asked for a reload and is waiting for something to
			// happen. Saying plainly that nothing will is better than both
			// dying and silently ignoring it.
			logger.Warn("signal ignored: arex reads its config only at startup",
				"signal", s.String(), "action", "restart to apply configuration changes")
		case <-ctx.Done():
			done = true
		}
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
