package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
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

	// Collect enables optional command groups for every switch that does not
	// override it. Collection is opt-in: anything not listed here is not
	// collected. A nil map means the block was absent, which is an error --
	// defaulting it either way would silently change what a deployment
	// gathers.
	Collect CollectSet `json:"collect"`
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

	// Collect overrides the top-level set for this switch, wholesale.
	Collect CollectSet `json:"collect"`

	// InterfaceScope is passed to the switch verbatim as the interface
	// argument of the interface-related commands, e.g.
	// "Ethernet1/1-4,Ethernet29/1-4". Empty means every interface.
	//
	// Verbatim because EOS accepts forms that are not worth modelling: a
	// per-cage subinterface range silently returns only the interfaces that
	// exist, so it survives breakout changes, whereas a range before the
	// slash is rejected outright and a non-existent cage fails the whole
	// command.
	InterfaceScope string `json:"interfaceScope"`
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
	if c.Collect == nil {
		return fmt.Errorf("config: no collect block; collection is opt-in, so an absent "+
			"block would gather only \"show version\". List the groups to enable: %s",
			strings.Join(CollectKeys, ", "))
	}
	if err := validateCollect(c.Collect, "collect", c.PollInterval.Duration); err != nil {
		return err
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
		if sw.Label() == ReservedTarget {
			return fmt.Errorf("config: switch[%d] is named %q, which is reserved: "+
				"/metrics?target=%s selects arex's own metrics", i, ReservedTarget, ReservedTarget)
		}
		if err := validateCollect(sw.Collect, fmt.Sprintf("switch[%d] (%s)", i, sw.Label()), c.PollInterval.Duration); err != nil {
			return err
		}
		if err := validateScope(sw.InterfaceScope, fmt.Sprintf("switch[%d] (%s)", i, sw.Label())); err != nil {
			return err
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

// ReservedTarget is the switch name that cannot be used, because
// /metrics?target=internal selects arex's own metrics.
//
// Declared here rather than imported from the metrics package: config must not
// depend on it, and a name collision has to be rejected at load time rather
// than becoming an ambiguous query later.
const ReservedTarget = "internal"

// CollectKeys names every optional command group. show version has no key:
// it is always collected, being the identity metric everything else joins
// against.
var CollectKeys = []string{
	"processes", "temperature", "power", "cooling",
	"interfaces", "bgp", "transceiver", "phy",
}

// EffectiveCollect resolves which optional groups this switch collects.
//
// A per-switch block replaces the default wholesale rather than merging, so
// there is no partial inheritance to reason about: what you see in a
// switch's block is exactly what it collects.
func (s SwitchConfig) EffectiveCollect(defaults map[string]ModuleConfig,
	pollInterval time.Duration) map[string]ModuleConfig {
	src := defaults
	if s.Collect != nil {
		src = s.Collect
	}
	out := make(map[string]ModuleConfig, len(src))
	for k, v := range src {
		if !v.Enabled {
			continue
		}
		out[k] = ModuleConfig{
			Enabled:  true,
			Interval: resolveInterval(k, v.Interval, pollInterval),
		}
	}
	return out
}

// validateCollect rejects unknown keys, so a typo cannot silently disable
// collection.
func validateCollect(set CollectSet, where string, pollInterval time.Duration) error {
	known := make(map[string]bool, len(CollectKeys))
	for _, k := range CollectKeys {
		known[k] = true
	}
	for k, m := range set {
		if !known[k] {
			return fmt.Errorf("config: %s: unknown key %q; valid keys are %s",
				where, k, strings.Join(CollectKeys, ", "))
		}
		// Rejected rather than clamped: the loop cannot tick faster than
		// pollInterval, so an interval below it is a mistake worth reporting.
		if m.Interval > 0 && m.Interval < pollInterval {
			return fmt.Errorf("config: %s: %q interval %s is shorter than pollInterval %s",
				where, k, m.Interval, pollInterval)
		}
	}
	return nil
}

// validateScope rejects anything that would not survive being spliced into a
// CLI command. The scope is passed to the switch verbatim, so a control
// character in it is a malformed command rather than a filter.
func validateScope(scope, where string) error {
	for _, r := range scope {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("config: %s: interfaceScope contains a control character", where)
		}
	}
	return nil
}
