package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/krisiasty/arex/config"
	"github.com/krisiasty/arex/internal/collector"
	"github.com/krisiasty/arex/internal/eapi"
	"github.com/krisiasty/arex/internal/metrics"
)

func main() {
	// Cancelled on SIGINT or SIGTERM, which is how a container runtime or
	// systemd asks arex to stop.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfgPath := flag.String("config", "config.json", "path to config file")
	debug := flag.Bool("debug", false, "log every eAPI request: status, timing, sizes and commands")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	store, err := collector.NewStore(cfg.Switches, cfg.Collect)
	if err != nil {
		log.Fatalf("%v", err)
	}

	if *debug {
		log.Printf("debug logging enabled: one line per eAPI request")
	}

	// One poller goroutine per switch, started at staggered offsets so the
	// fleet is not polled simultaneously.
	for i, sw := range cfg.Switches {
		data := store.Get(sw.Label())

		opts := []eapi.Option{eapi.WithStats(&data.Stats)}
		if *debug {
			opts = append(opts, eapi.WithDebug(sw.Label()))
		}
		client, err := eapi.NewClient(
			sw.Host,
			sw.Username,
			sw.Password,
			cfg.ScrapeTimeout.Duration,
			sw.TLSOptions(cfg.TLSSkipVerify),
			opts...,
		)
		if err != nil {
			log.Fatalf("switch %s: %v", sw.Label(), err)
		}
		offset := collector.PollOffset(i, len(cfg.Switches), cfg.PollInterval.Duration)
		go collector.PollLoop(ctx, client, data, cfg.PollInterval.Duration, offset)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		metrics.Write(w, store, cfg.StalenessLimit.Duration)
	})
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}

	log.Printf("arex listening on %s", cfg.ListenAddress)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	<-ctx.Done()
	stop() // restore default handling, so a second signal kills immediately

	// Let a scrape already in progress finish. Without this a restart can cut
	// a /metrics response mid-write, which Prometheus records as a failed
	// scrape rather than a clean gap.
	log.Printf("shutting down, waiting up to %s for in-flight scrapes", shutdownGrace)
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown: %v", err)
	}
	log.Printf("arex stopped")
}

// shutdownGrace bounds how long a scrape in progress may take to finish. It
// is shorter than a typical Prometheus scrape timeout, so a slow client
// cannot hold up a restart.
const shutdownGrace = 5 * time.Second
