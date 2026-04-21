package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/yourusername/arex/config"
	"github.com/yourusername/arex/internal/collector"
	"github.com/yourusername/arex/internal/eapi"
	"github.com/yourusername/arex/internal/metrics"
)

func main() {
	cfgPath := flag.String("config", "config.json", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	store := collector.NewStore(cfg.Switches)

	// Start one poller goroutine per switch.
	for _, sw := range cfg.Switches {
		client := eapi.NewClient(
			sw.Host,
			sw.Username,
			sw.Password,
			cfg.ScrapeTimeout.Duration,
			cfg.TLSSkipVerify,
		)
		data := store.All()
		// Find the SwitchData for this switch by label.
		var sd *collector.SwitchData
		for _, d := range data {
			if d.Label == sw.Label() {
				sd = d
				break
			}
		}
		go collector.PollLoop(client, sd, cfg.PollInterval.Duration)
	}

	http.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		metrics.Write(w, store, cfg.StalenessLimit.Duration)
	})

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	log.Printf("arex listening on %s", cfg.ListenAddress)
	log.Fatal(http.ListenAndServe(cfg.ListenAddress, nil))
}
