package main

import (
	"os"
	"slices"
	"syscall"
	"testing"
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
