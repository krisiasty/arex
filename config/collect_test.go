package config

import (
	"strings"
	"testing"
)

// show version is not optional: it is the identity metric every other series
// is joined against, and there is no useful scrape without it.
// Collection is opt-in, so anything not enabled is not collected.
// Omitting collect entirely would silently reduce a working deployment to a
// single command, so it is rejected rather than defaulted.
func TestMissingCollectIsRejected(t *testing.T) {
	_, err := Load(writeRaw(t, `{"tlsSkipVerify":true,
		"switches":[{"host":"https://192.0.2.1","username":"u","password":"p"}]}`))
	if err == nil {
		t.Fatal("a config with no collect block must be rejected")
	}
	if !strings.Contains(err.Error(), "collect") {
		t.Errorf("error should name the missing block: %v", err)
	}
}

// A typo must not silently disable collection.
func TestUnknownCollectKeyIsRejected(t *testing.T) {
	_, err := Load(write(t, `{"tlsSkipVerify":true,
		"collect":{"interfaces":true,"phy":true,"transciever":true},
		"switches":[{"host":"https://192.0.2.1","username":"u","password":"p"}]}`))
	if err == nil {
		t.Fatal("an unknown collect key must be rejected")
	}
	if !strings.Contains(err.Error(), "transciever") {
		t.Errorf("error should name the offending key: %v", err)
	}
}

// A per-switch block replaces the default wholesale, so there is no
// partial-inheritance puzzle to reason about.
func TestPerSwitchCollectReplacesDefault(t *testing.T) {
	cfg, err := Load(write(t, `{"tlsSkipVerify":true,
		"collect":{"processes":true,"temperature":true,"power":true,"cooling":true,
		           "interfaces":true,"bgp":true,"transceiver":true,"phy":true},
		"switches":[
			{"host":"https://192.0.2.1","username":"u","password":"p","name":"full"},
			{"host":"https://192.0.2.2","username":"u","password":"p","name":"lean",
			 "collect":{"interfaces":true}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(cfg.Switches[0].EffectiveCollect(cfg.Collect)); n != 8 {
		t.Errorf("inheriting switch enables %d, want 8", n)
	}
	lean := cfg.Switches[1].EffectiveCollect(cfg.Collect)
	if len(lean) != 1 || !lean["interfaces"] {
		t.Errorf("overriding switch = %v, want interfaces only", lean)
	}
}

// A scope containing a newline would be a second command as far as the CLI
// is concerned; reject rather than pass it through.
func TestInterfaceScopeRejectsControlCharacters(t *testing.T) {
	_, err := Load(write(t, `{"tlsSkipVerify":true,
		"collect":{"interfaces":true,"transceiver":true,"phy":true,"bgp":true,
		           "processes":true,"temperature":true,"power":true,"cooling":true},
		"switches":[{"host":"https://192.0.2.1","username":"u","password":"p",
		             "interfaceScope":"Ethernet1/1\nshow running-config"}]}`))
	if err == nil {
		t.Fatal("a scope containing a newline must be rejected")
	}
}
