package config

import (
	"strings"
	"testing"
	"time"
)

const modBase = `{"pollInterval":"30s","collect":%s,
	"switches":[{"tlsSkipVerify":true,"host":"https://192.0.2.1","username":"u","password":"p","name":"sw1"}]}`

func loadCollect(t *testing.T, collect string) *Config {
	t.Helper()
	cfg, err := Load(writeRaw(t, strings.Replace(modBase, "%s", collect, 1)))
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// loadCollectErr asserts that a collect block is rejected, and that the error
// mentions want (skipped when want is empty).
func loadCollectErr(t *testing.T, collect, want string) {
	t.Helper()
	_, err := Load(writeRaw(t, strings.Replace(modBase, "%s", collect, 1)))
	if err == nil {
		t.Fatalf("collect %s must be rejected", collect)
	}
	if want != "" && !strings.Contains(err.Error(), want) {
		t.Errorf("error should mention %q: %v", want, err)
	}
}

// A group is an object and nothing else, and the error says so, since a
// rejected config is only useful if it tells you the shape it wanted.
func TestNonObjectEntryIsRejected(t *testing.T) {
	for _, entry := range []string{
		`{"interfaces":true}`,
		`{"interfaces":{"enabled":true},"phy":false}`,
		`{"interfaces":"yes"}`,
		`{"interfaces":["enabled"]}`,
	} {
		loadCollectErr(t, entry, "enabled")
	}
}

// "enabled" is required rather than defaulted, for the same reason the collect
// block itself is: an absent value would have to mean something, and either
// meaning is a guess about intent.
func TestEnabledIsRequired(t *testing.T) {
	loadCollectErr(t, `{"interfaces":{"interval":"5m"}}`, "enabled")
}

func TestObjectFormSetsInterval(t *testing.T) {
	cfg := loadCollect(t, `{"interfaces":{"enabled":true},"phy":{"enabled":true,"interval":"1h"}}`)
	mods := cfg.Switches[0].EffectiveCollect(cfg.Collect, cfg.PollInterval.Duration)

	if got := mods["phy"].Interval; got != time.Hour {
		t.Errorf("phy interval = %v, want 1h", got)
	}
}

// interval stays optional: omitting it is unambiguous, since the module's
// default is documented and logged at startup.
func TestOmittedIntervalUsesTheDefault(t *testing.T) {
	cfg := loadCollect(t, `{"interfaces":{"enabled":true},"transceiver":{"enabled":true},"phy":{"enabled":true}}`)
	mods := cfg.Switches[0].EffectiveCollect(cfg.Collect, cfg.PollInterval.Duration)

	for module, want := range map[string]time.Duration{
		"interfaces":  30 * time.Second,
		"transceiver": 5 * time.Minute,
		"phy":         15 * time.Minute,
	} {
		if got := mods[module].Interval; got != want {
			t.Errorf("%s interval = %v, want %v", module, got, want)
		}
	}
}

func TestDisabledModuleIsNotCollected(t *testing.T) {
	cfg := loadCollect(t, `{"interfaces":{"enabled":true},"phy":{"enabled":false,"interval":"1h"}}`)
	mods := cfg.Switches[0].EffectiveCollect(cfg.Collect, cfg.PollInterval.Duration)
	if _, ok := mods["phy"]; ok {
		t.Error("enabled:false must not be collected whatever the interval")
	}
}

// pollInterval is the floor: a module cannot be polled more often than the
// loop ticks, and silently raising it would hide the mistake.
func TestModuleIntervalBelowPollIntervalIsRejected(t *testing.T) {
	loadCollectErr(t, `{"interfaces":{"enabled":true,"interval":"5s"}}`, "pollInterval")
}

// A default slower than pollInterval is fine; a default faster than it is
// raised, since the loop cannot tick more often than its interval.
func TestDefaultsNeverBeatThePollInterval(t *testing.T) {
	cfg, err := Load(writeRaw(t, `{"pollInterval":"30m",
		"collect":{"transceiver":{"enabled":true},"phy":{"enabled":true}},
		"switches":[{"tlsSkipVerify":true,"host":"https://192.0.2.1","username":"u","password":"p"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	mods := cfg.Switches[0].EffectiveCollect(cfg.Collect, cfg.PollInterval.Duration)
	for _, module := range []string{"transceiver", "phy"} {
		if got := mods[module].Interval; got != 30*time.Minute {
			t.Errorf("%s interval = %v, want the 30m poll interval", module, got)
		}
	}
}

func TestUnknownFieldInModuleObjectIsRejected(t *testing.T) {
	loadCollectErr(t, `{"interfaces":{"enabled":true,"intervall":"5m"}}`, "intervall")
}

func TestInvalidIntervalIsRejected(t *testing.T) {
	loadCollectErr(t, `{"interfaces":{"enabled":true,"interval":"soon"}}`, "")
	loadCollectErr(t, `{"interfaces":{"enabled":true,"interval":"0s"}}`, "")
}

// A per-switch block still replaces the default wholesale.
func TestPerSwitchOverrideCarriesIntervals(t *testing.T) {
	cfg, err := Load(writeRaw(t, `{"pollInterval":"30s",
		"collect":{"interfaces":{"enabled":true},"phy":{"enabled":true}},
		"switches":[
		  {"tlsSkipVerify":true,"host":"https://192.0.2.1","username":"u","password":"p","name":"a"},
		  {"tlsSkipVerify":true,"host":"https://192.0.2.2","username":"u","password":"p","name":"b",
		   "collect":{"phy":{"enabled":true,"interval":"1h"}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	a := cfg.Switches[0].EffectiveCollect(cfg.Collect, cfg.PollInterval.Duration)
	b := cfg.Switches[1].EffectiveCollect(cfg.Collect, cfg.PollInterval.Duration)

	if a["phy"].Interval != 15*time.Minute {
		t.Errorf("switch a phy = %v, want the 15m default", a["phy"].Interval)
	}
	if b["phy"].Interval != time.Hour {
		t.Errorf("switch b phy = %v, want 1h", b["phy"].Interval)
	}
	if _, ok := b["interfaces"]; ok {
		t.Error("a per-switch block replaces the default wholesale")
	}
}

// The error names the offending key. A config listing nine groups is not
// helped by being told that "true" is wrong somewhere in it.
func TestErrorNamesTheOffendingKey(t *testing.T) {
	for _, entry := range []string{
		`{"interfaces":{"enabled":true},"phy":true}`,
		`{"interfaces":{"enabled":true},"phy":{"interval":"5m"}}`,
		`{"interfaces":{"enabled":true},"phy":{"enabled":true,"intervall":"5m"}}`,
		`{"interfaces":{"enabled":true},"phy":{"enabled":true,"interval":"soon"}}`,
	} {
		loadCollectErr(t, entry, "phy")
	}
}

// ntpd polls its own upstream every 64 seconds at the fastest, so reading the
// associations on a 30-second loop returns the same numbers twice. A minute is
// the floor at which the values can have moved.
func TestNTPDefaultsToAMinute(t *testing.T) {
	cfg := loadCollect(t, `{"ntp":{"enabled":true}}`)
	mods := cfg.Switches[0].EffectiveCollect(cfg.Collect, cfg.PollInterval.Duration)

	if got := mods["ntp"].Interval; got != time.Minute {
		t.Errorf("ntp interval = %v, want 1m", got)
	}
}
