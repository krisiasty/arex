package main

import "testing"

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
