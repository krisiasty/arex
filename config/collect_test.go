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
	_, err := Load(writeRaw(t, `{
		"switches":[{"tlsSkipVerify":true,"host":"https://192.0.2.1","username":"u","password":"p"}]}`))
	if err == nil {
		t.Fatal("a config with no collect block must be rejected")
	}
	if !strings.Contains(err.Error(), "collect") {
		t.Errorf("error should name the missing block: %v", err)
	}
}

// A typo must not silently disable collection.
func TestUnknownCollectKeyIsRejected(t *testing.T) {
	_, err := Load(write(t, `{
		"collect":{"interfaces":{"enabled": true},"phy":{"enabled": true},"transciever":{"enabled": true}},
		"switches":[{"tlsSkipVerify":true,"host":"https://192.0.2.1","username":"u","password":"p"}]}`))
	if err == nil {
		t.Fatal("an unknown collect key must be rejected")
	}
	if !strings.Contains(err.Error(), "transciever") {
		t.Errorf("error should name the offending key: %v", err)
	}
}

// A per-switch block inherits the global modules, can add role-specific ones,
// and can explicitly disable a global module.
func TestPerSwitchCollectMergesWithDefault(t *testing.T) {
	cfg, err := Load(write(t, `{
		"collect":{"processes":{"enabled": true},"temperature":{"enabled": true},"power":{"enabled": true},"cooling":{"enabled": true},
		           "interfaces":{"enabled": true},"bgp":{"enabled": true},"transceiver":{"enabled": true},"phy":{"enabled": true}},
		"switches":[
			{"tlsSkipVerify":true,"host":"https://192.0.2.1","username":"u","password":"p","name":"full"},
			{"tlsSkipVerify":true,"host":"https://192.0.2.2","username":"u","password":"p","name":"leaf",
			 "collect":{"bgp":{"enabled": false},"ntp":{"enabled": true}}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if n := len(cfg.Switches[0].EffectiveCollect(cfg.Collect, cfg.PollInterval.Duration)); n != 8 {
		t.Errorf("inheriting switch enables %d, want 8", n)
	}
	leaf := cfg.Switches[1].EffectiveCollect(cfg.Collect, cfg.PollInterval.Duration)
	if len(leaf) != 8 || !leaf["processes"].Enabled || !leaf["ntp"].Enabled {
		t.Errorf("merged switch = %v, want inherited globals plus ntp", leaf)
	}
	if _, ok := leaf["bgp"]; ok {
		t.Errorf("merged switch = %v, want per-switch bgp disable to win", leaf)
	}
}

// A scope containing a newline would be a second command as far as the CLI
// is concerned; reject rather than pass it through.
func TestInterfaceScopeRejectsControlCharacters(t *testing.T) {
	_, err := Load(write(t, `{
		"collect":{"interfaces":{"enabled": true},"transceiver":{"enabled": true},"phy":{"enabled": true},"bgp":{"enabled": true},
		           "processes":{"enabled": true},"temperature":{"enabled": true},"power":{"enabled": true},"cooling":{"enabled": true}},
		"switches":[{"tlsSkipVerify":true,"host":"https://192.0.2.1","username":"u","password":"p",
		             "interfaceScope":"Ethernet1/1\nshow running-config"}]}`))
	if err == nil {
		t.Fatal("a scope containing a newline must be rejected")
	}
}

// "internal" is reserved: /metrics?target=internal selects arex's own
// metrics, so a switch by that name would make the query ambiguous.
func TestReservedSwitchNameIsRejected(t *testing.T) {
	_, err := Load(write(t, `{
		"switches":[{"tlsSkipVerify":true,"host":"https://192.0.2.1","username":"u","password":"p","name":"internal"}]}`))
	if err == nil {
		t.Fatal("a switch named \"internal\" must be rejected")
	}
	if !strings.Contains(err.Error(), "reserved") {
		t.Errorf("error should say the name is reserved: %v", err)
	}
}
