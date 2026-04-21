package config

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// Config is the top-level configuration for arex.
type Config struct {
	ListenAddress  string         `json:"listenAddress"`  // default ":9100"
	PollInterval   duration       `json:"pollInterval"`   // default 30s
	ScrapeTimeout  duration       `json:"scrapeTimeout"`  // default 10s
	TLSSkipVerify  bool           `json:"tlsSkipVerify"`  // default true
	StalenessLimit duration       `json:"stalenessLimit"` // default 3x pollInterval
	Switches       []SwitchConfig `json:"switches"`
}

// SwitchConfig holds connection details for a single switch.
type SwitchConfig struct {
	Host     string `json:"host"`     // e.g. "https://192.168.1.1"
	Username string `json:"username"`
	Password string `json:"password"`
	// Optional human-readable name used as the "switch" label.
	// Falls back to Host if empty.
	Name string `json:"name"`
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
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}
	defer f.Close()

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
