package main

import (
	"flag"
	"log"
	"net/http"
	"time"

	"github.com/krisiasty/arex/config"
	"github.com/krisiasty/arex/internal/collector"
	"github.com/krisiasty/arex/internal/eapi"
	"github.com/krisiasty/arex/internal/metrics"
)

func main() {
	cfgPath := flag.String("config", "config.json", "path to config file")
	debug := flag.Bool("debug", false, "log every eAPI request: status, timing, sizes and commands")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	store, err := collector.NewStore(cfg.Switches)
	if err != nil {
		log.Fatalf("%v", err)
	}

	if *debug {
		log.Printf("debug logging enabled: one line per eAPI request")
	}

	// One poller goroutine per switch.
	for _, sw := range cfg.Switches {
		var opts []eapi.Option
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
		go collector.PollLoop(client, store.Get(sw.Label()), cfg.PollInterval.Duration)
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

	log.Printf("arex listening on %s", cfg.ListenAddress)
	srv := &http.Server{
		Addr:              cfg.ListenAddress,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
	}
	log.Fatal(srv.ListenAndServe())
}
