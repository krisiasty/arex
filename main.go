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
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	store, err := collector.NewStore(cfg.Switches)
	if err != nil {
		log.Fatalf("%v", err)
	}

	// One poller goroutine per switch.
	for _, sw := range cfg.Switches {
		client := eapi.NewClient(
			sw.Host,
			sw.Username,
			sw.Password,
			cfg.ScrapeTimeout.Duration,
			cfg.TLSSkipVerify,
		)
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
