package config

import (
	"log/slog"
	"strings"
	"testing"
)

// warning is accepted alongside warn because syslog, Python and most people
// say warning, while slog's own vocabulary is warn. Refusing one of them would
// be a needless startup failure.
func TestLogLevelSpellings(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"warning", slog.LevelWarn},
		{"WARNING", slog.LevelWarn},
		{"Warn", slog.LevelWarn},
		{" warn ", slog.LevelWarn},
		{"error", slog.LevelError},
	} {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseLogLevel(tc.in)
			if err != nil {
				t.Fatalf("ParseLogLevel(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseLogLevel(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// slog.Level.UnmarshalText would accept "Error-8" and quietly mean INFO. An
// explicit table does not, so a config names the level it appears to name.
func TestLogLevelRejectsAnythingElse(t *testing.T) {
	for _, in := range []string{"", "warnn", "trace", "fatal", "Error-8", "info+4", "5"} {
		if _, err := ParseLogLevel(in); err == nil {
			t.Errorf("ParseLogLevel(%q) should have failed", in)
		}
	}
}

// A typo has to fail at load, so -check catches it before a deploy rather than
// a quiet fleet turning out to be a verbose one.
func TestBadLogLevelFailsLoad(t *testing.T) {
	_, err := Load(writeRaw(t, `{"logLevel":"quiet",
		"collect":{"interfaces":{"enabled":true}},
		"switches":[{"tlsSkipVerify":true,"host":"https://192.0.2.1","username":"u","password":"p","name":"sw1"}]}`))
	if err == nil {
		t.Fatal("an unknown logLevel must be rejected at load")
	}
	if !strings.Contains(err.Error(), "logLevel") || !strings.Contains(err.Error(), "warning") {
		t.Errorf("error should name the field and the accepted values: %v", err)
	}
}

func TestLogLevelIsOptional(t *testing.T) {
	cfg, err := Load(writeRaw(t, `{"collect":{"interfaces":{"enabled":true}},
		"switches":[{"tlsSkipVerify":true,"host":"https://192.0.2.1","username":"u","password":"p","name":"sw1"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LogLevel != "" {
		t.Errorf("logLevel should default to empty, got %q", cfg.LogLevel)
	}
}
