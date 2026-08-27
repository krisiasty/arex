package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

const minimal = `{"switches":[{"host":"https://192.0.2.1","username":"u","password":"p"}]}`

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

// An omitted tlsSkipVerify yields false, so certificate verification is on.
// A stock switch serves a self-signed certificate, so this is documented as
// something most deployments must set explicitly.
func TestTLSSkipVerifyDefaultsToFalse(t *testing.T) {
	cfg, err := Load(write(t, minimal))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.TLSSkipVerify {
		t.Error("TLSSkipVerify defaulted to true; README documents false")
	}
}

func TestStalenessLimitTracksPollInterval(t *testing.T) {
	cfg, err := Load(write(t, `{"pollInterval":"1m","switches":[
		{"host":"https://192.0.2.1","username":"u","password":"p"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StalenessLimit.Duration != 3*time.Minute {
		t.Errorf("StalenessLimit = %v, want 3m", cfg.StalenessLimit.Duration)
	}
}

func TestDurationStringsParse(t *testing.T) {
	cfg, err := Load(write(t, `{"pollInterval":"45s","scrapeTimeout":"2500ms","switches":[
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
	_, err := Load(write(t, `{"pollInterval":"30","switches":[
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
		{"missing host", `{"switches":[{"username":"u","password":"p"}]}`, "missing host"},
		{"missing username", `{"switches":[{"host":"h","password":"p"}]}`, "missing username"},
		{"missing password", `{"switches":[{"host":"h","username":"u"}]}`, "missing password"},
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
