package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// collectAll enables every optional group. The block is mandatory, and most
// tests here are not about collection, so write injects it when absent.
const collectAll = `"collect":{"processes":true,"temperature":true,"power":true,` +
	`"cooling":true,"interfaces":true,"bgp":true,"transceiver":true,"phy":true},`

// write stores a config for Load, adding a full collect block if the body
// does not mention one.
func write(t *testing.T, body string) string {
	t.Helper()
	if !strings.Contains(body, `"collect"`) {
		body = strings.Replace(body, `"switches"`, collectAll+`"switches"`, 1)
	}
	return writeRaw(t, body)
}

// writeRaw stores a config verbatim, for tests about the collect block itself.
func writeRaw(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// A TLS verification choice is mandatory, so the smallest valid config
// includes one.
const minimal = `{"tlsSkipVerify":true,"switches":[
	{"host":"https://192.0.2.1","username":"u","password":"p"}]}`

func TestDefaultsApplied(t *testing.T) {
	cfg, err := Load(write(t, minimal))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ListenAddress != ":9100" {
		t.Errorf("ListenAddress = %q", cfg.ListenAddress)
	}
	if cfg.PollInterval.Duration != 30*time.Second {
		t.Errorf("PollInterval = %v", cfg.PollInterval.Duration)
	}
	if cfg.ScrapeTimeout.Duration != 10*time.Second {
		t.Errorf("ScrapeTimeout = %v", cfg.ScrapeTimeout.Duration)
	}
	if cfg.StalenessLimit.Duration != 90*time.Second {
		t.Errorf("StalenessLimit = %v, want 3x pollInterval", cfg.StalenessLimit.Duration)
	}
}

// Verification must be an explicit decision. A stock EOS switch serves a
// certificate with no subject alternative names, so silently defaulting
// either way is wrong: verifying fails for everyone, and skipping hides that
// nothing is being checked.
func TestTLSVerificationChoiceIsMandatory(t *testing.T) {
	_, err := Load(write(t, `{"switches":[
		{"host":"https://192.0.2.1","username":"u","password":"p"}]}`))
	if err == nil {
		t.Fatal("a switch with no TLS verification method must be rejected")
	}
	for _, want := range []string{"caFile", "pinnedCertSha256", "tlsSkipVerify"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should name %s as an option: %v", want, err)
		}
	}
}

func TestPerSwitchTLSOptionsSatisfyValidation(t *testing.T) {
	for name, body := range map[string]string{
		"caFile": `{"switches":[{"host":"https://192.0.2.1","username":"u","password":"p",
			"caFile":"/etc/arex/ca.pem"}]}`,
		"pin": `{"switches":[{"host":"https://192.0.2.1","username":"u","password":"p",
			"pinnedCertSha256":"AB:CD"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(write(t, body)); err != nil {
				t.Errorf("%s should satisfy the TLS requirement without tlsSkipVerify: %v", name, err)
			}
		})
	}
}

func TestCAFileAndPinTogetherIsRejected(t *testing.T) {
	_, err := Load(write(t, `{"switches":[{"host":"https://192.0.2.1","username":"u","password":"p",
		"caFile":"/etc/arex/ca.pem","pinnedCertSha256":"abcd"}]}`))
	if err == nil {
		t.Fatal("caFile and pinnedCertSha256 together must be rejected")
	}
	if !strings.Contains(err.Error(), "pick one") {
		t.Errorf("error = %v", err)
	}
}

// Per-switch settings win: one switch can be pinned while another is skipped.
func TestTLSOptionsPreferPerSwitchSettings(t *testing.T) {
	sw := SwitchConfig{Host: "h", PinnedCertSHA256: "abcd"}
	opts := sw.TLSOptions(true)
	if opts.PinnedCertSHA256 != "abcd" {
		t.Errorf("pin not carried through: %+v", opts)
	}
	plain := SwitchConfig{Host: "h"}.TLSOptions(true)
	if !plain.SkipVerify {
		t.Error("global skipVerify should apply when no per-switch option is set")
	}
}

func TestStalenessLimitTracksPollInterval(t *testing.T) {
	cfg, err := Load(write(t, `{"tlsSkipVerify":true,"pollInterval":"1m","switches":[
		{"host":"https://192.0.2.1","username":"u","password":"p"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StalenessLimit.Duration != 3*time.Minute {
		t.Errorf("StalenessLimit = %v, want 3m", cfg.StalenessLimit.Duration)
	}
}

func TestDurationStringsParse(t *testing.T) {
	cfg, err := Load(write(t, `{"tlsSkipVerify":true,"pollInterval":"45s","scrapeTimeout":"2500ms","switches":[
		{"host":"https://192.0.2.1","username":"u","password":"p"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.PollInterval.Duration != 45*time.Second {
		t.Errorf("PollInterval = %v", cfg.PollInterval.Duration)
	}
	if cfg.ScrapeTimeout.Duration != 2500*time.Millisecond {
		t.Errorf("ScrapeTimeout = %v", cfg.ScrapeTimeout.Duration)
	}
}

func TestInvalidDurationIsRejected(t *testing.T) {
	_, err := Load(write(t, `{"tlsSkipVerify":true,"pollInterval":"30","switches":[
		{"host":"https://192.0.2.1","username":"u","password":"p"}]}`))
	if err == nil {
		t.Fatal("a bare number is not a Go duration; want an error")
	}
	if !strings.Contains(err.Error(), "invalid duration") {
		t.Errorf("error = %v", err)
	}
}

func TestValidationRejectsIncompleteSwitches(t *testing.T) {
	for _, tc := range []struct{ name, body, want string }{
		{"no switches", `{"switches":[]}`, "no switches"},
		{"missing host", `{"tlsSkipVerify":true,"switches":[{"username":"u","password":"p"}]}`, "missing host"},
		{"missing username", `{"tlsSkipVerify":true,"switches":[{"host":"h","password":"p"}]}`, "missing username"},
		{"missing password", `{"tlsSkipVerify":true,"switches":[{"host":"h","username":"u"}]}`, "missing password"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(write(t, tc.body))
			if err == nil {
				t.Fatalf("want an error mentioning %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %v, want mention of %q", err, tc.want)
			}
		})
	}
}

func TestLabelFallsBackToHost(t *testing.T) {
	named := SwitchConfig{Host: "https://192.0.2.1", Name: "spine1"}
	unnamed := SwitchConfig{Host: "https://192.0.2.2"}
	if named.Label() != "spine1" {
		t.Errorf("Label = %q, want spine1", named.Label())
	}
	if unnamed.Label() != "https://192.0.2.2" {
		t.Errorf("Label = %q, want the host", unnamed.Label())
	}
}
