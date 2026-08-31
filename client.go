package main

import (
	"fmt"
	"github.com/krisiasty/arex/internal/secret"
	"log/slog"

	"github.com/krisiasty/arex/config"
	"github.com/krisiasty/arex/internal/eapi"
)

// eapiOption aliases the client option type, so main does not spread the
// package name across the wiring.
type eapiOption = eapi.Option

func withStats(s *eapi.Stats) eapiOption              { return eapi.WithStats(s) }
func withLogging(n string, l *slog.Logger) eapiOption { return eapi.WithLogging(n, l) }
func withDebug(n string, l *slog.Logger) eapiOption   { return eapi.WithDebug(n, l) }

// newClient builds the eAPI client for one switch.
func newClient(sw config.SwitchConfig, cfg *config.Config, opts ...eapiOption) (*eapi.Client, error) {
	cred, err := credentialFor(sw, cfg)
	if err != nil {
		return nil, err
	}
	// The password always arrives through the credential, so there is one path
	// for reading it whether it came from the config or a file.
	return eapi.NewClient(
		sw.Host,
		sw.Username,
		"",
		cfg.ScrapeTimeout.Duration,
		sw.TLSOptions(),
		append(opts, eapi.WithCredential(cred))...,
	)
}

// credentialFor resolves this switch's password source.
//
// A file is read here rather than at config load so the credential object that
// can re-read it outlives startup; the config has already checked the path is
// usable, and this names the switch if it stopped being usable since.
func credentialFor(sw config.SwitchConfig, cfg *config.Config) (*secret.Credential, error) {
	file := sw.EffectivePasswordFile(cfg.PasswordFile)
	if file == "" {
		return secret.NewStaticCredential(sw.Password), nil
	}
	cred, err := secret.NewFileCredential(file)
	if err != nil {
		return nil, fmt.Errorf("switch %s: %w", sw.Label(), err)
	}
	return cred, nil
}
