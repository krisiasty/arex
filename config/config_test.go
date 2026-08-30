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
const collectAll = `"collect":{"processes":{"enabled": true},"temperature":{"enabled": true},"power":{"enabled": true},` +
	`"cooling":{"enabled": true},"interfaces":{"enabled": true},"bgp":{"enabled": true},"transceiver":{"enabled": true},"phy":{"enabled": true}},`

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
const minimal = `{"switches":[
	{"tlsSkipVerify":true,"host":"https://192.0.2.1","username":"u","password":"p"}]}`

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

// tlsSkipVerify was global once. A config that still sets it there must say
// where it went: DisallowUnknownFields would otherwise report an unknown
// field, which is true and useless.
func TestGlobalTLSSkipVerifyIsRejected(t *testing.T) {
	for name, body := range map[string]string{
		"true": `{"tlsSkipVerify":true,"switches":[
			{"host":"https://192.0.2.1","username":"u","password":"p","tlsSkipVerify":true}]}`,
		// Rejected even when false: the key no longer does anything, and
		// ignoring it would leave someone believing verification was on.
		"false": `{"tlsSkipVerify":false,"switches":[
			{"host":"https://192.0.2.1","username":"u","password":"p","tlsSkipVerify":true}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, body))
			if err == nil {
				t.Fatal("a global tlsSkipVerify must be rejected")
			}
			for _, want := range []string{"tlsSkipVerify", "per-switch"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error should mention %q: %v", want, err)
				}
			}
			if strings.Contains(err.Error(), "unknown field") {
				t.Errorf("error should explain the move, not report an unknown field: %v", err)
			}
		})
	}
}

// Skipping verification for one switch says nothing about the others.
func TestPerSwitchTLSSkipVerifyIsIndependent(t *testing.T) {
	cfg, err := Load(write(t, `{"switches":[
		{"host":"https://192.0.2.1","username":"u","password":"p","tlsSkipVerify":true},
		{"host":"https://192.0.2.2","username":"u","password":"p","caFile":"/etc/arex/ca.pem"}]}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := cfg.Switches[0].TLSOptions(); !got.SkipVerify {
		t.Errorf("switch[0] should skip verification: %+v", got)
	}
	if got := cfg.Switches[1].TLSOptions(); got.SkipVerify {
		t.Errorf("switch[1] must not inherit switch[0]'s tlsSkipVerify: %+v", got)
	}
}

// Skipping and verifying are contradictory instructions for one switch. While
// tlsSkipVerify was global this combination was meaningful -- the per-switch
// method overrode the fleet default -- but per-switch it can only be a
// mistake, and silently preferring one would hide it.
func TestTLSSkipVerifyWithAVerificationMethodIsRejected(t *testing.T) {
	for name, body := range map[string]string{
		"caFile": `{"switches":[{"host":"https://192.0.2.1","username":"u","password":"p",
			"tlsSkipVerify":true,"caFile":"/etc/arex/ca.pem"}]}`,
		"pin": `{"switches":[{"host":"https://192.0.2.1","username":"u","password":"p",
			"tlsSkipVerify":true,"pinnedCertSha256":"AB:CD"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Load(write(t, body))
			if err == nil {
				t.Fatalf("tlsSkipVerify with %s must be rejected", name)
			}
			if !strings.Contains(err.Error(), "tlsSkipVerify") {
				t.Errorf("error should name tlsSkipVerify: %v", err)
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

// TLSOptions carries each switch's own choice, and nothing else: there is no
// fleet-wide setting left for it to fall back to.
func TestTLSOptionsReflectOneSwitchOnly(t *testing.T) {
	pinned := SwitchConfig{Host: "h", PinnedCertSHA256: "abcd"}.TLSOptions()
	if pinned.PinnedCertSHA256 != "abcd" {
		t.Errorf("pin not carried through: %+v", pinned)
	}
	if pinned.SkipVerify {
		t.Errorf("a pinned switch must not skip verification: %+v", pinned)
	}
	skipped := SwitchConfig{Host: "h", TLSSkipVerify: true}.TLSOptions()
	if !skipped.SkipVerify {
		t.Errorf("tlsSkipVerify not carried through: %+v", skipped)
	}
}

func TestStalenessLimitTracksPollInterval(t *testing.T) {
	cfg, err := Load(write(t, `{"pollInterval":"1m","switches":[
		{"tlsSkipVerify":true,"host":"https://192.0.2.1","username":"u","password":"p"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StalenessLimit.Duration != 3*time.Minute {
		t.Errorf("StalenessLimit = %v, want 3m", cfg.StalenessLimit.Duration)
	}
}

func TestDurationStringsParse(t *testing.T) {
	cfg, err := Load(write(t, `{"pollInterval":"45s","scrapeTimeout":"2500ms","switches":[
		{"tlsSkipVerify":true,"host":"https://192.0.2.1","username":"u","password":"p"}]}`))
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
	_, err := Load(write(t, `{"pollInterval":"30","switches":[
		{"tlsSkipVerify":true,"host":"https://192.0.2.1","username":"u","password":"p"}]}`))
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
		{"missing host", `{"switches":[{"username":"u","password":"p"}]}`, "missing host"},
		{"missing username", `{"switches":[{"tlsSkipVerify":true,"host":"h","password":"p"}]}`, "missing username"},
		{"no credential", `{"switches":[{"tlsSkipVerify":true,"host":"h","username":"u"}]}`, "has no credential"},
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

func TestSwitchLimit(t *testing.T) {
	switches := make([]SwitchConfig, MaxSwitches)
	for i := range switches {
		switches[i] = SwitchConfig{
			Host:          "https://192.0.2.1",
			Username:      "u",
			Password:      "p",
			TLSSkipVerify: true,
		}
	}
	cfg := Config{Switches: switches, Collect: CollectSet{}}
	if err := cfg.validate(); err != nil {
		t.Fatalf("%d switches rejected: %v", MaxSwitches, err)
	}

	cfg.Switches = append(cfg.Switches, switches[0])
	err := cfg.validate()
	if err == nil {
		t.Fatalf("%d switches accepted", MaxSwitches+1)
	}
	want := "1001 switches configured; maximum is 1000 per instance"
	if !strings.Contains(err.Error(), want) {
		t.Errorf("error = %v, want mention of %q", err, want)
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

func TestFabricParsesPerSwitch(t *testing.T) {
	cfg, err := Load(write(t, `{"switches":[
		{"tlsSkipVerify":true,"host":"https://192.0.2.1","username":"u","password":"p",
		 "fabric":"fabric-a"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Switches[0].Fabric; got != "fabric-a" {
		t.Errorf("Fabric = %q, want fabric-a", got)
	}
}

// debug is off unless asked for, and readable from the config so a deployment
// can turn it on without changing how it is invoked.
func TestDebugDefaultsOffAndParses(t *testing.T) {
	base := `{%s"collect":{"interfaces":{"enabled":true}},
		"switches":[{"tlsSkipVerify":true,"host":"https://192.0.2.1","username":"u","password":"p"}]}`

	off, err := Load(writeRaw(t, strings.Replace(base, "%s", "", 1)))
	if err != nil {
		t.Fatal(err)
	}
	if off.Debug {
		t.Error("debug must be off unless the config asks for it")
	}

	on, err := Load(writeRaw(t, strings.Replace(base, "%s", `"debug":true,`, 1)))
	if err != nil {
		t.Fatal(err)
	}
	if !on.Debug {
		t.Error(`"debug":true was not read`)
	}
}
