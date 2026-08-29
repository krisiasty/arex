package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeFile(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// The point of the change: a config a Kubernetes ConfigMap can hold natively,
// without a JSON blob embedded in YAML.
func TestYAMLConfigLoads(t *testing.T) {
	cfg, err := Load(writeFile(t, "config.yaml", `
# arex, in YAML
listenAddress: ":9100"
pollInterval: 30s

collect:
  interfaces:
    enabled: true
  transceiver:
    enabled: true
    interval: 5m
  phy:
    enabled: false

switches:
  - host: https://10.10.0.11
    username: prometheus
    password: secret
    tlsSkipVerify: true
    name: leaf1
    interfaceScope: Ethernet1/1-4,Ethernet29/1-4
`))
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ListenAddress != ":9100" {
		t.Errorf("listenAddress = %q", cfg.ListenAddress)
	}
	if cfg.PollInterval.Duration != 30*time.Second {
		t.Errorf("pollInterval = %v", cfg.PollInterval.Duration)
	}
	if len(cfg.Switches) != 1 || cfg.Switches[0].Name != "leaf1" {
		t.Fatalf("switches = %+v", cfg.Switches)
	}
	if got := cfg.Switches[0].Host; got != "https://10.10.0.11" {
		t.Errorf("host = %q: an unquoted URL must survive YAML", got)
	}
	if got := cfg.Switches[0].InterfaceScope; got != "Ethernet1/1-4,Ethernet29/1-4" {
		t.Errorf("interfaceScope = %q", got)
	}

	mods := cfg.Switches[0].EffectiveCollect(cfg.Collect, cfg.PollInterval.Duration)
	if m, ok := mods["transceiver"]; !ok || m.Interval != 5*time.Minute {
		t.Errorf("transceiver = %+v, want enabled at 5m", m)
	}
	if _, ok := mods["phy"]; ok {
		t.Error("phy was disabled")
	}
}

// JSON is valid YAML, so the old form keeps loading through the same path
// rather than needing a second parser or an extension rule.
func TestJSONConfigStillLoads(t *testing.T) {
	cfg, err := Load(writeFile(t, "config.json", `{
	"pollInterval": "45s",
	"collect": {"interfaces": {"enabled": true}},
	"switches": [
		{"host": "https://10.10.0.11", "username": "u", "password": "p", "name": "a",
		 "tlsSkipVerify": true}
	]
}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollInterval.Duration != 45*time.Second {
		t.Errorf("pollInterval = %v", cfg.PollInterval.Duration)
	}
	if len(cfg.Switches) != 1 {
		t.Fatalf("switches = %+v", cfg.Switches)
	}
}

// A typo in a top-level key used to be ignored, which is the same failure the
// collect block already rejects: the setting silently does nothing.
func TestUnknownTopLevelKeyIsRejected(t *testing.T) {
	_, err := Load(writeFile(t, "config.yaml", `
pollIntervall: 30s
collect:
  interfaces: {enabled: true}
switches:
  - host: h
    username: u
    password: p
`))
	if err == nil {
		t.Fatal("a misspelled top-level key must be rejected")
	}
	if !strings.Contains(err.Error(), "pollIntervall") {
		t.Errorf("error should name the key: %v", err)
	}
}

// YAML's own errors have to reach the operator, since a badly indented block
// is the mistake this format invites.
func TestMalformedYAMLIsReported(t *testing.T) {
	_, err := Load(writeFile(t, "config.yaml", "collect:\n  interfaces:\n   enabled: true\n  bad indent\n"))
	if err == nil {
		t.Fatal("malformed YAML must be rejected")
	}
	if !strings.Contains(err.Error(), "config") {
		t.Errorf("error should be attributed to the config: %v", err)
	}
}

// A name that YAML reads as a number cannot become a metric label. The error
// has to say so rather than reporting a type mismatch and leaving the operator
// to work out which field.
func TestNumericSwitchNameIsRejectedClearly(t *testing.T) {
	_, err := Load(writeFile(t, "config.yaml", `
collect:
  interfaces: {enabled: true}
switches:
  - host: h
    username: u
    password: p
    tlsSkipVerify: true
    name: 10
`))
	if err == nil {
		t.Fatal("a numeric name must be rejected")
	}
	if !strings.Contains(err.Error(), "name") {
		t.Errorf("error should name the field: %v", err)
	}
}
