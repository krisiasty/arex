package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSecret writes a password file with the given mode and returns its path.
func writeSecret(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "password")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// switchCfg renders a config with one switch carrying the given extra fields.
func switchCfg(fields string) string {
	return `{"collect":{"interfaces":{"enabled":true}},
		"switches":[{"tlsSkipVerify":true,"host":"https://192.0.2.1","username":"u",` + fields + `"name":"sw1"}]}`
}

func TestPasswordFileIsAccepted(t *testing.T) {
	p := writeSecret(t, "hunter2", 0o400)
	cfg, err := Load(writeRaw(t, switchCfg(`"passwordFile":"`+p+`",`)))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Switches[0].EffectivePasswordFile(cfg.PasswordFile); got != p {
		t.Errorf("effective password file = %q, want %q", got, p)
	}
}

// A fleet usually shares one monitoring account, so the file can be declared
// once instead of repeated for every switch.
func TestTopLevelPasswordFileAppliesToSwitches(t *testing.T) {
	p := writeSecret(t, "hunter2", 0o400)
	cfg, err := Load(writeRaw(t, `{"passwordFile":"`+p+`",
		"collect":{"interfaces":{"enabled":true}},
		"switches":[{"tlsSkipVerify":true,"host":"https://192.0.2.1","username":"u","name":"a"},
		            {"tlsSkipVerify":true,"host":"https://192.0.2.2","username":"u","name":"b"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, sw := range cfg.Switches {
		if got := sw.EffectivePasswordFile(cfg.PasswordFile); got != p {
			t.Errorf("%s: effective password file = %q, want the fleet default", sw.Label(), got)
		}
	}
}

// A switch may still carry its own credential, which wins over the default.
func TestPerSwitchOverridesTheFleetDefault(t *testing.T) {
	fleet := writeSecret(t, "fleet", 0o400)
	own := writeSecret(t, "own", 0o400)
	cfg, err := Load(writeRaw(t, `{"passwordFile":"`+fleet+`",
		"collect":{"interfaces":{"enabled":true}},
		"switches":[{"tlsSkipVerify":true,"host":"https://192.0.2.1","username":"u","name":"a"},
		            {"tlsSkipVerify":true,"host":"https://192.0.2.2","username":"u","name":"b",
		             "passwordFile":"`+own+`"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Switches[1].EffectivePasswordFile(cfg.PasswordFile); got != own {
		t.Errorf("per-switch file = %q, want %q", got, own)
	}
	// An inline password on one switch does not consult the file at all.
	if got := cfg.Switches[0].EffectivePasswordFile(cfg.PasswordFile); got != fleet {
		t.Errorf("switch a should fall back to the fleet file, got %q", got)
	}
}

func TestInlinePasswordWinsOverTheFleetFile(t *testing.T) {
	fleet := writeSecret(t, "fleet", 0o400)
	cfg, err := Load(writeRaw(t, `{"passwordFile":"`+fleet+`",
		"collect":{"interfaces":{"enabled":true}},
		"switches":[{"tlsSkipVerify":true,"host":"https://192.0.2.1","username":"u","name":"a","password":"inline"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Switches[0].EffectivePasswordFile(cfg.PasswordFile); got != "" {
		t.Errorf("an explicit inline password must not consult a file, got %q", got)
	}
}

// Both set on one switch is ambiguous, so it is rejected rather than resolved
// by a precedence rule nobody will remember.
func TestPasswordAndPasswordFileTogetherIsRejected(t *testing.T) {
	p := writeSecret(t, "hunter2", 0o400)
	_, err := Load(writeRaw(t, switchCfg(`"password":"x","passwordFile":"`+p+`",`)))
	if err == nil {
		t.Fatal("setting both password and passwordFile must be rejected")
	}
	for _, want := range []string{"sw1", "passwordFile"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q: %v", want, err)
		}
	}
}

func TestSomeCredentialIsRequired(t *testing.T) {
	_, err := Load(writeRaw(t, switchCfg("")))
	if err == nil {
		t.Fatal("a switch with no password and no passwordFile must be rejected")
	}
	if !strings.Contains(err.Error(), "passwordFile") {
		t.Errorf("error should name the alternative: %v", err)
	}
}

// Startup is where a bad path should surface, not the first poll.
func TestUnreadableOrEmptyPasswordFileIsRejected(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nope")
	if _, err := Load(writeRaw(t, switchCfg(`"passwordFile":"`+missing+`",`))); err == nil {
		t.Error("a missing password file must be rejected at load")
	}

	empty := writeSecret(t, "\n", 0o400)
	_, err := Load(writeRaw(t, switchCfg(`"passwordFile":"`+empty+`",`)))
	if err == nil {
		t.Error("a password file holding only a newline must be rejected")
	}
}

// ESO mounts secrets 0644 by default, so this is a warning rather than an
// error: refusing to start would be worse than saying so.
func TestLoosePermissionsWarnButDoNotFail(t *testing.T) {
	p := writeSecret(t, "hunter2", 0o644)
	cfg, err := Load(writeRaw(t, switchCfg(`"passwordFile":"`+p+`",`)))
	if err != nil {
		t.Fatalf("loose permissions must not prevent startup: %v", err)
	}
	if len(cfg.Warnings) == 0 {
		t.Fatal("a world-readable password file should produce a warning")
	}
	joined := strings.Join(cfg.Warnings, "\n")
	if !strings.Contains(joined, "sw1") || !strings.Contains(joined, "0644") {
		t.Errorf("warning should name the switch and the mode: %q", joined)
	}
}

func TestTightPermissionsWarnAboutNothing(t *testing.T) {
	p := writeSecret(t, "hunter2", 0o400)
	cfg, err := Load(writeRaw(t, switchCfg(`"passwordFile":"`+p+`",`)))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Warnings) != 0 {
		t.Errorf("no warnings expected, got %q", cfg.Warnings)
	}
}
