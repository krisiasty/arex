package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/krisiasty/arex/internal/eapi"
)

// Config is the top-level configuration for arex.
type Config struct {
	ListenAddress  string         `json:"listenAddress"`  // default ":9100"
	PollInterval   duration       `json:"pollInterval"`   // default 30s
	ScrapeTimeout  duration       `json:"scrapeTimeout"`  // default 10s
	TLSSkipVerify  bool           `json:"tlsSkipVerify"`  // default false (Go zero value; no default applied)
	StalenessLimit duration       `json:"stalenessLimit"` // default 3x pollInterval
	Switches       []SwitchConfig `json:"switches"`
}

// SwitchConfig holds connection details for a single switch.
type SwitchConfig struct {
	Host     string `json:"host"` // e.g. "https://192.168.1.1"
	Username string `json:"username"`
	Password string `json:"password"`
	// Optional human-readable name used as the "switch" label.
	// Falls back to Host if empty.
	Name string `json:"name"`

	// CAFile is a PEM bundle to verify this switch's certificate against.
	// Use it once the switch serves a certificate with correct subject
	// alternative names.
	CAFile string `json:"caFile"`

	// PinnedCertSHA256 pins this switch's leaf certificate by SHA-256, as
	// printed by "openssl x509 -fingerprint -sha256". Colons and case are
	// ignored. A stock EOS switch cannot be verified any other way: its
	// default certificate carries no subject alternative names, so no
	// hostname or address can match it.
	PinnedCertSHA256 string `json:"pinnedCertSha256"`
}

// TLSOptions returns how this switch's certificate should be verified.
// Per-switch settings take precedence over the global tlsSkipVerify.
func (s SwitchConfig) TLSOptions(globalSkipVerify bool) eapi.TLSOptions {
	return eapi.TLSOptions{
		SkipVerify:       globalSkipVerify,
		CAFile:           s.CAFile,
		PinnedCertSHA256: s.PinnedCertSHA256,
	}
}

// Label returns the label to use for this switch in Prometheus metrics.
func (s SwitchConfig) Label() string {
	if s.Name != "" {
		return s.Name
	}
	return s.Host
}

// Load reads and parses a JSON config file from path.
func Load(path string) (*Config, error) {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer func() { _ = f.Close() }()

	var cfg Config
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.applyDefaults()

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) applyDefaults() {
	if c.ListenAddress == "" {
		c.ListenAddress = ":9100"
	}
	if c.PollInterval.Duration == 0 {
		c.PollInterval.Duration = 30 * time.Second
	}
	if c.ScrapeTimeout.Duration == 0 {
		c.ScrapeTimeout.Duration = 10 * time.Second
	}
	if c.StalenessLimit.Duration == 0 {
		c.StalenessLimit.Duration = 3 * c.PollInterval.Duration
	}
}

func (c *Config) validate() error {
	if len(c.Switches) == 0 {
		return fmt.Errorf("config: no switches defined")
	}
	for i, sw := range c.Switches {
		if sw.Host == "" {
			return fmt.Errorf("config: switch[%d] missing host", i)
		}
		if sw.Username == "" {
			return fmt.Errorf("config: switch[%d] missing username", i)
		}
		if sw.Password == "" {
			return fmt.Errorf("config: switch[%d] missing password", i)
		}
		if sw.CAFile != "" && sw.PinnedCertSHA256 != "" {
			return fmt.Errorf("config: switch[%d] (%s) sets both caFile and pinnedCertSha256; pick one",
				i, sw.Label())
		}
		if sw.CAFile != "" || sw.PinnedCertSHA256 != "" {
			continue // an explicit verification method was chosen
		}
		if !c.TLSSkipVerify {
			return fmt.Errorf("config: switch[%d] (%s) has no way to verify TLS: set caFile, "+
				"pinnedCertSha256, or tlsSkipVerify. A stock EOS switch serves a certificate with "+
				"no subject alternative names, which cannot be verified by hostname; see README", i, sw.Label())
		}
	}
	return nil
}

// duration is a time.Duration that unmarshals from a JSON string like "30s".
type duration struct {
	time.Duration
}

func (d *duration) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", s, err)
	}
	d.Duration = v
	return nil
}
