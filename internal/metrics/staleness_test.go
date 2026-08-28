package metrics

import (
	"testing"
	"time"

	"github.com/krisiasty/arex/config"
	"github.com/krisiasty/arex/internal/collector"
	"github.com/krisiasty/arex/internal/eapi"
)

// A module polled less often than stalenessLimit must be judged against its
// own interval. Otherwise enabling a 15-minute module would make its metrics
// vanish permanently: every scrape would find them older than the 90-second
// default and suppress them.
func TestSlowModuleIsNotStaleWithinItsInterval(t *testing.T) {
	store, err := collector.NewStore([]config.SwitchConfig{
		{Host: "h", Username: "u", Password: "p", Name: "sw1"},
	}, map[string]config.ModuleConfig{
		"interfaces": {Enabled: true},
		"phy":        {Enabled: true, Interval: 15 * time.Minute},
	}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	sd := store.All()[0]
	collector.Collect(fixtureRunner{t}, sd)

	// Ten minutes on: well past the 90s limit, but inside phy's interval.
	sd.CommandLastSuccess[collector.CmdPhy] = time.Now().Add(-10 * time.Minute)

	out := gather(t, store, 90*time.Second)
	if sample(out, "arista_phy_fec_uncorrected_codewords", "") == "" {
		t.Error("a 15m module 10m after its last poll must still be served")
	}
}

// The bound is still finite: a slow module that has stopped being collected
// must eventually be suppressed, or a dead module would serve one value for
// ever.
func TestSlowModuleGoesStaleAfterSeveralIntervals(t *testing.T) {
	store, err := collector.NewStore([]config.SwitchConfig{
		{Host: "h", Username: "u", Password: "p", Name: "sw1"},
	}, map[string]config.ModuleConfig{
		"interfaces": {Enabled: true},
		"phy":        {Enabled: true, Interval: 15 * time.Minute},
	}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	sd := store.All()[0]
	collector.Collect(fixtureRunner{t}, sd)

	sd.CommandLastSuccess[collector.CmdPhy] = time.Now().Add(-90 * time.Minute)

	out := gather(t, store, 90*time.Second)
	if sample(out, "arista_phy_fec_uncorrected_codewords", "") != "" {
		t.Error("a module six intervals behind must be suppressed")
	}
	// The fast modules are unaffected.
	if sample(out, "arista_interface_link_up", `interface="Ethernet1/1"`) == "" {
		t.Error("fast modules must be unaffected by a slow module going stale")
	}
}

// A configured stalenessLimit longer than the module interval still applies:
// the per-module bound raises the floor, it does not lower a deliberate
// choice.
func TestConfiguredLimitWinsWhenLonger(t *testing.T) {
	store, err := collector.NewStore([]config.SwitchConfig{
		{Host: "h", Username: "u", Password: "p", Name: "sw1"},
	}, map[string]config.ModuleConfig{"interfaces": {Enabled: true}}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	sd := store.All()[0]
	collector.Collect(fixtureRunner{t}, sd)

	sd.CommandLastSuccess[collector.CmdInterfaces] = time.Now().Add(-30 * time.Minute)

	out := gather(t, store, time.Hour)
	if sample(out, "arista_interface_link_up", `interface="Ethernet1/1"`) == "" {
		t.Error("an explicit one-hour stalenessLimit must be honoured")
	}
}

// A rotation is worth seeing from Prometheus, not just from the logs: if a
// mounted secret stops being readable, the counter says so while the scrape
// still shows the switch failing.
func TestCredentialReloadsAreExposed(t *testing.T) {
	store, err := collector.NewStore([]config.SwitchConfig{
		{Host: "h", Username: "u", Password: "p", Name: "sw1"},
	}, config.CollectSet{"interfaces": {Enabled: true}}, 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	sd := store.All()[0]
	sd.Stats.RecordReload(eapi.ReloadRotated)
	sd.Stats.RecordReload(eapi.ReloadUnchanged)
	sd.Stats.RecordReload(eapi.ReloadUnchanged)
	collector.Collect(fixtureRunner{t}, sd)

	out := gather(t, store, 90*time.Second)
	for outcome, want := range map[string]string{"rotated": "1", "unchanged": "2"} {
		got := sample(out, "arista_credential_reloads_total", `outcome="`+outcome+`"`)
		if got != want {
			t.Errorf("reloads{outcome=%q} = %q, want %q", outcome, got, want)
		}
	}
}
