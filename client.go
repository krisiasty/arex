package main

import (
	"log/slog"

	"github.com/krisiasty/arex/config"
	"github.com/krisiasty/arex/internal/eapi"
)

// eapiOption aliases the client option type, so main does not spread the
// package name across the wiring.
type eapiOption = eapi.Option

func withStats(s *eapi.Stats) eapiOption            { return eapi.WithStats(s) }
func withDebug(n string, l *slog.Logger) eapiOption { return eapi.WithDebug(n, l) }

// newClient builds the eAPI client for one switch.
func newClient(sw config.SwitchConfig, cfg *config.Config, opts ...eapiOption) (*eapi.Client, error) {
	return eapi.NewClient(
		sw.Host,
		sw.Username,
		sw.Password,
		cfg.ScrapeTimeout.Duration,
		sw.TLSOptions(cfg.TLSSkipVerify),
		opts...,
	)
}
