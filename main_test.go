package main

import (
	"log/slog"
	"os"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/krisiasty/arex/internal/collector"
)

// hasSignal reports whether the list contains s.
func hasSignal(list []os.Signal, s os.Signal) bool {
	return slices.Contains(list, s)
}

// The flag wins over the config when it is given, so a running deployment can
// be started verbosely without editing its config file -- and can be started
// quietly with -debug=false when its config leaves debug on.
func TestDebugResolution(t *testing.T) {
	for _, tc := range []struct {
		name              string
		config, flag, set bool
		want              bool
	}{
		{name: "neither", want: false},
		{name: "config only", config: true, want: true},
		{name: "flag only", flag: true, set: true, want: true},
		{name: "flag agrees with config", config: true, flag: true, set: true, want: true},
		{name: "flag overrides config off", config: true, flag: false, set: true, want: false},
		{name: "unset flag does not override config", config: true, flag: false, set: false, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveDebug(tc.config, tc.flag, tc.set); got != tc.want {
				t.Errorf("resolveDebug(%v, %v, %v) = %v, want %v",
					tc.config, tc.flag, tc.set, got, tc.want)
			}
		})
	}
}

// Debug wins over any level, from either source, because a deployment asking
// for debug and getting warn would be a trap. Below that the flag beats the
// config, exactly as -debug does.
func TestLevelResolution(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		config, flag         string
		flagSet, debug, dOff bool
		want                 slog.Level
	}{
		{name: "nothing set", want: slog.LevelInfo},
		{name: "config warn", config: "warn", want: slog.LevelWarn},
		{name: "config warning spelling", config: "warning", want: slog.LevelWarn},
		{name: "config mixed case", config: "WaRn", want: slog.LevelWarn},
		{name: "flag warn", flag: "warn", flagSet: true, want: slog.LevelWarn},
		{name: "flag overrides config", config: "error", flag: "info", flagSet: true, want: slog.LevelInfo},
		{name: "unset flag does not override", config: "error", flag: "", want: slog.LevelError},
		{name: "debug beats config", config: "warn", debug: true, want: slog.LevelDebug},
		{name: "debug beats flag", flag: "error", flagSet: true, debug: true, want: slog.LevelDebug},
		{name: "debug off clamps config debug", config: "debug", dOff: true, want: slog.LevelInfo},
		{name: "debug off leaves warn alone", config: "warn", dOff: true, want: slog.LevelWarn},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveLevel(tc.config, tc.flag, tc.flagSet, tc.debug, tc.dOff)
			if err != nil {
				t.Fatalf("resolveLevel: %v", err)
			}
			if got != tc.want {
				t.Errorf("resolveLevel(%q, %q, %v, %v, %v) = %v, want %v",
					tc.config, tc.flag, tc.flagSet, tc.debug, tc.dOff, got, tc.want)
			}
		})
	}
}

// A typo is a startup failure, not a silent fall back to info: a deployment
// that meant to be quiet should not discover it is verbose from its log bill.
func TestBadLevelIsRejected(t *testing.T) {
	if _, err := resolveLevel("", "warnn", true, false, false); err == nil {
		t.Error("an unknown -log-level must be rejected")
	}
	if _, err := resolveLevel("chatty", "", false, false, false); err == nil {
		t.Error("an unknown config logLevel must be rejected")
	}
}

func TestDescribeScheduleListsModulesClearly(t *testing.T) {
	sched := []collector.ModuleSchedule{
		{Module: "bgp", Interval: 30 * time.Second},
		{Module: "vxlan", Interval: 5 * time.Minute},
		{Module: "phy", Interval: 15 * time.Minute},
	}
	if got, want := describeSchedule(sched), "bgp:30s, vxlan:5m, phy:15m"; got != want {
		t.Errorf("describeSchedule() = %q, want %q", got, want)
	}
}

// SIGHUP must not be fatal. Go's default disposition for it is to terminate,
// and the systemd unit used to send one on "systemctl reload" -- so a reload
// killed the exporter instead of reloading it.
func TestSighupIsNotFatal(t *testing.T) {
	if !hasSignal(nonFatalSignals, syscall.SIGHUP) {
		t.Error("SIGHUP must be handled, or the process dies on systemctl reload")
	}
	for _, s := range shutdownSignals {
		if s == syscall.SIGHUP {
			t.Error("SIGHUP must not be a shutdown signal: it is not a stop request")
		}
	}
}

// The signals a container runtime and systemd use to stop a service do have to
// stop it.
func TestShutdownSignalsAreHandled(t *testing.T) {
	for _, want := range []os.Signal{syscall.SIGINT, syscall.SIGTERM} {
		if !hasSignal(shutdownSignals, want) {
			t.Errorf("%v must stop arex", want)
		}
	}
}
