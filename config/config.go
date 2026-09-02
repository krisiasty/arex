// Package config loads and validates arex's configuration file.
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/krisiasty/arex/internal/eapi"
)

// MaxSwitches is the hard number of switches one arex instance accepts.
//
// It is a defensive ceiling, not a tested capacity: large fleets should be
// split across separate instances.
const MaxSwitches = 1000

// Config is the top-level configuration for arex.
type Config struct {
	ListenAddress  string   `json:"listenAddress"`  // default ":9100"
	PollInterval   duration `json:"pollInterval"`   // default 30s
	ScrapeTimeout  duration `json:"scrapeTimeout"`  // default 10s
	StalenessLimit duration `json:"stalenessLimit"` // default 3x pollInterval

	// TLSSkipVerify is not a setting. It was global once and is per-switch
	// now, and it is accepted here only so that a config still setting it can
	// be told where it went: the decoder rejects unknown fields, so removing
	// it outright would produce `unknown field "tlsSkipVerify"` -- true, and
	// no help to whoever has to fix the file. A pointer so that setting it to
	// false is caught too, which is the case most worth catching: it would
	// otherwise read as "verification is on" while doing nothing at all.
	TLSSkipVerify *bool `json:"tlsSkipVerify"`

	// Debug logs one record per eAPI request. Configurable as well as a flag
	// so a deployment can be verbose without changing how it is invoked --
	// under systemd or a container runtime, the config file is usually easier
	// to edit than the command line. The -debug flag wins when given.
	Debug    bool           `json:"debug"` // default false
	Switches []SwitchConfig `json:"switches"`

	// LogLevel is the minimum level that reaches the log: debug, info, warn
	// (or warning) or error. Empty means info.
	//
	// Only the per-request "eapi request successful" record sits at info;
	// everything else arex has to say is warn or above. So warn is the level
	// that drops the one line a healthy fleet repeats on every poll and keeps
	// the rest. Debug wins over this, from either the flag or the config:
	// asking for debug and getting warn would be a trap.
	LogLevel string `json:"logLevel"` // default info

	// ProbeAddress serves /livez and /readyz on a second listener, in plain
	// HTTP, when set.
	//
	// It exists for mutual TLS. RequireAndVerifyClientCert applies to a
	// listener rather than a path, and a kubelet probe presents no client
	// certificate, so without a second port the probes would have to drop to
	// checking that the port is open -- losing the readiness gate that waits
	// for every switch to be polled once. These two endpoints report only
	// whether arex is up, so a plain port exposes nothing.
	ProbeAddress string `json:"probeAddress"`

	// ListenTLS serves /metrics over HTTPS, and optionally requires a client
	// certificate. Absent means plain HTTP.
	ListenTLS ListenTLS `json:"listenTLS"`

	// ListenAuth requires callers to authenticate. Absent means no
	// authentication; /livez and /readyz are never covered.
	ListenAuth ListenAuth `json:"listenAuth"`

	// PasswordFile is the credential file for every switch that does not name
	// its own. A fleet normally shares one monitoring account, and repeating
	// the path per switch is how configs drift.
	PasswordFile string `json:"passwordFile"`

	// Warnings are problems worth reporting that do not prevent startup.
	// Collected rather than logged because Load runs before the logger
	// exists -- the config decides the log level.
	Warnings []string `json:"-"`

	// Collect enables optional command groups for every switch. Collection is
	// opt-in: anything not listed here or in a switch override is not collected.
	// A nil map means the block was absent, which is an error -- defaulting it
	// either way would silently change what a deployment gathers.
	Collect CollectSet `json:"collect"`
}

// SwitchConfig holds connection details for a single switch.
type SwitchConfig struct {
	Host     string `json:"host"` // e.g. "https://192.168.1.1"
	Username string `json:"username"`

	// Password is the credential inline. Convenient for a quick test; use
	// PasswordFile for anything else, since a file can be given restrictive
	// permissions, delivered by systemd credentials or a Kubernetes secret,
	// and re-read after a rotation without restarting arex.
	Password string `json:"password"`

	// PasswordFile holds the credential instead. Trailing newlines are
	// stripped, since writing a secret with a shell redirect appends one.
	PasswordFile string `json:"passwordFile"`
	// Optional human-readable name used as the "switch" label.
	// Falls back to Host if empty.
	Name string `json:"name"`

	// Fabric identifies the EVPN fabric this switch belongs to. It is emitted
	// on ESI metrics so fabric-wide elections are not aggregated across
	// independent fabrics. Empty is suitable when arex monitors one fabric.
	Fabric string `json:"fabric"`

	// TLSSkipVerify disables verification of this switch's certificate.
	// Per-switch rather than global: skipping verification is a decision
	// about one switch's certificate, and a fleet-wide default made it easy
	// to add a switch that quietly inherited it.
	TLSSkipVerify bool `json:"tlsSkipVerify"`

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

	// Collect overrides top-level modules by key and can add role-specific ones.
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
func (s SwitchConfig) TLSOptions() eapi.TLSOptions {
	return eapi.TLSOptions{
		SkipVerify:       s.TLSSkipVerify,
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

// EffectivePasswordFile returns the credential file this switch should read,
// or "" when it carries its password inline.
//
// An inline password is an explicit per-switch choice, so it wins over the
// fleet default rather than being silently overridden by it.
func (s SwitchConfig) EffectivePasswordFile(fleetDefault string) string {
	if s.Password != "" {
		return ""
	}
	if s.PasswordFile != "" {
		return s.PasswordFile
	}
	return fleetDefault
}

// checkPasswordFile confirms the credential is usable at startup rather than
// on the first poll, and reports a mode that lets others read it.
//
// A loose mode is a warning, not an error: the External Secrets Operator
// mounts secrets 0644 by default, and refusing to start would be worse than
// saying so. See secretModeWarning for which modes count as loose.
func checkPasswordFile(path, where string) (warning string, err error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("config: %s: passwordFile: %w", where, err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("config: %s: passwordFile %s is a directory", where, path)
	}

	body, err := os.ReadFile(path) //nolint:gosec // the path is operator-supplied config
	if err != nil {
		return "", fmt.Errorf("config: %s: passwordFile: %w", where, err)
	}
	if TrimSecret(body) == "" {
		return "", fmt.Errorf("config: %s: passwordFile %s is empty", where, path)
	}

	return secretModeWarning(fmt.Sprintf("config: %s: passwordFile", where), path, info), nil
}

// TrimSecret strips the line ending a secret file usually carries.
//
// Only the line ending: "echo secret > file" appends a newline that EOS would
// otherwise reject as part of the password, while a space could conceivably be
// part of a real one.
func TrimSecret(b []byte) string {
	return strings.TrimRight(string(b), "\r\n")
}

// Load reads and parses a config file from path.
//
// The file is YAML, which also accepts JSON: JSON is valid YAML, so both forms
// load through one path with no extension rule and no second parser. The YAML
// is converted to JSON rather than decoded directly, which keeps every json
// tag and every custom UnmarshalJSON in this package doing the work -- the
// durations, the collect set and the module objects all validate the same way
// whichever form the file was written in.
func Load(path string) (*Config, error) {
	body, err := os.ReadFile(path) //nolint:gosec // the path is an operator-supplied flag, not user input
	if err != nil {
		return nil, fmt.Errorf("open config: %w", err)
	}

	var tree any
	if uerr := yaml.Unmarshal(body, &tree); uerr != nil {
		return nil, fmt.Errorf("parse config: %w", uerr)
	}
	asJSON, err := json.Marshal(tree)
	if err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	var cfg Config
	dec := json.NewDecoder(bytes.NewReader(asJSON))
	// A misspelled key would otherwise be ignored, leaving a setting that
	// silently does nothing -- the same failure the collect block rejects.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
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
	// Checked before anything else so a config written against the old shape
	// is told what changed, rather than failing later on a switch that looks
	// like it has no verification method when the file plainly sets one.
	if c.TLSSkipVerify != nil {
		return errors.New("config: tlsSkipVerify is per-switch now, not global: " +
			"set it on each switch entry that needs it. It was a fleet-wide default, which made " +
			"it easy to add a switch that inherited it without anyone deciding to")
	}
	if len(c.Switches) == 0 {
		return errors.New("config: no switches defined")
	}
	if len(c.Switches) > MaxSwitches {
		return fmt.Errorf("config: %d switches configured; maximum is %d per instance: "+
			"split large fleets across separate arex instances", len(c.Switches), MaxSwitches)
	}
	if c.Collect == nil {
		return fmt.Errorf("config: no collect block; collection is opt-in, so an absent "+
			"block would gather only \"show version\". List the groups to enable: %s",
			strings.Join(CollectKeys, ", "))
	}
	if err := validateCollect(c.Collect, "collect", c.PollInterval.Duration); err != nil {
		return err
	}
	if c.LogLevel != "" {
		if _, err := ParseLogLevel(c.LogLevel); err != nil {
			return fmt.Errorf("config: logLevel: %w", err)
		}
	}
	listenWarnings, err := c.validateListen()
	if err != nil {
		return err
	}
	c.Warnings = append(c.Warnings, listenWarnings...)
	for i, sw := range c.Switches {
		if sw.Host == "" {
			return fmt.Errorf("config: switch[%d] missing host", i)
		}
		if sw.Username == "" {
			return fmt.Errorf("config: switch[%d] missing username", i)
		}
		where := fmt.Sprintf("switch[%d] (%s)", i, sw.Label())
		if sw.Password != "" && sw.PasswordFile != "" {
			return fmt.Errorf("config: %s sets both password and passwordFile; pick one", where)
		}
		file := sw.EffectivePasswordFile(c.PasswordFile)
		if sw.Password == "" && file == "" {
			return fmt.Errorf("config: %s has no credential: set password or passwordFile, "+
				"or a top-level passwordFile for the whole fleet", where)
		}
		if file != "" {
			warning, ferr := checkPasswordFile(file, where)
			if ferr != nil {
				return ferr
			}
			if warning != "" {
				c.Warnings = append(c.Warnings, warning)
			}
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
		if sw.TLSSkipVerify && (sw.CAFile != "" || sw.PinnedCertSHA256 != "") {
			return fmt.Errorf("config: switch[%d] (%s) sets tlsSkipVerify alongside a way to verify "+
				"its certificate; pick one", i, sw.Label())
		}
		if sw.CAFile != "" || sw.PinnedCertSHA256 != "" || sw.TLSSkipVerify {
			continue // an explicit choice was made
		}
		return fmt.Errorf("config: switch[%d] (%s) has no way to verify TLS: set caFile, "+
			"pinnedCertSha256, or tlsSkipVerify on it. A stock EOS switch serves a certificate with "+
			"no subject alternative names, which cannot be verified by hostname; see README", i, sw.Label())
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
	"processes", "temperature", "power", "cooling", "ntp", "capacity",
	"interfaces", "bgp", "vxlan", "evpn", "esi", "transceiver", "phy",
}

// EffectiveCollect resolves which optional groups this switch collects.
//
// A per-switch block merges with the defaults by module key. An entry replaces
// the corresponding default, including its interval; enabled:false explicitly
// disables a globally enabled module for that switch.
func (s SwitchConfig) EffectiveCollect(defaults map[string]ModuleConfig,
	pollInterval time.Duration) map[string]ModuleConfig {
	merged := make(map[string]ModuleConfig, len(defaults))
	for k, v := range defaults {
		merged[k] = v
	}
	for k, v := range s.Collect {
		merged[k] = v
	}

	out := make(map[string]ModuleConfig, len(merged))
	for k, v := range merged {
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
