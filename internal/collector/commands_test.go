package collector

import (
	"strings"
	"testing"

	"github.com/krisiasty/arex/config"
)

func allEnabled() map[string]config.ModuleConfig {
	m := make(map[string]config.ModuleConfig, len(config.CollectKeys))
	for _, k := range config.CollectKeys {
		m[k] = config.ModuleConfig{Enabled: true, Interval: directInterval}
	}
	return m
}

// Every config key must map to a command and vice versa, or a key could be
// accepted by validation and then quietly do nothing.
func TestEveryCollectKeyMapsToACommand(t *testing.T) {
	for _, k := range config.CollectKeys {
		if _, ok := optionalCommands[k]; !ok {
			t.Errorf("config key %q has no command", k)
		}
	}
	for k := range optionalCommands {
		found := false
		for _, ck := range config.CollectKeys {
			if ck == k {
				found = true
			}
		}
		if !found {
			t.Errorf("command key %q is not a valid config key", k)
		}
	}
}

func TestVersionIsAlwaysCollected(t *testing.T) {
	specs := commandsFor(map[string]config.ModuleConfig{}, "", directInterval)
	if len(specs) != 1 || specs[0].name != CmdVersion {
		t.Fatalf("with nothing enabled, want only show version, got %d", len(specs))
	}
}

func TestAllEnabledGivesEveryCommand(t *testing.T) {
	if n := len(commandsFor(allEnabled(), "", directInterval)); n != everyCommand() {
		t.Errorf("commands = %d, want %d", n, everyCommand())
	}
}

func TestVXLANUsesCanonicalEAPICommand(t *testing.T) {
	specs := commandsFor(map[string]config.ModuleConfig{
		"vxlan": {Enabled: true},
	}, "", directInterval)

	for _, spec := range specs {
		if spec.name == CmdVXLANInterface {
			if spec.cli != "show interfaces vxlan 1" {
				t.Errorf("VXLAN interface command = %q, want canonical eAPI spelling", spec.cli)
			}
			if spec.name != "show interface vxlan 1" {
				t.Errorf("VXLAN metric command label = %q, want stable spelling", spec.name)
			}
			return
		}
	}
	t.Fatal("VXLAN interface command missing")
}

// everyCommand is show version plus every command the collect keys name.
// Deliberately not len(CollectKeys)+1: a key may name several commands, as the
// overlay keys do.
func everyCommand() int {
	n := 1
	for _, key := range config.CollectKeys {
		n += len(optionalCommands[key])
	}
	return n
}

func TestDisabledCommandsAreNotIssued(t *testing.T) {
	set := allEnabled()
	delete(set, "phy")
	delete(set, "bgp")

	var cli []string
	for _, s := range commandsFor(set, "", directInterval) {
		cli = append(cli, s.cli)
	}
	joined := strings.Join(cli, "\n")
	for _, absent := range []string{"phy detail", "bgp summary"} {
		if strings.Contains(joined, absent) {
			t.Errorf("disabled command still issued: %s", absent)
		}
	}
	if !strings.Contains(joined, "transceiver detail") {
		t.Error("enabled command missing")
	}
}

func TestScopeIsSplicedIntoInterfaceCommandsOnly(t *testing.T) {
	const scope = "Ethernet1/1-4,Ethernet29/1-4"
	specs := commandsFor(allEnabled(), scope, directInterval)

	want := map[string]string{
		CmdInterfaces:   "show interfaces " + scope,
		CmdTransceivers: "show interfaces " + scope + " transceiver detail",
		CmdPhy:          "show interfaces " + scope + " phy detail",
	}
	for _, s := range specs {
		if expect, ok := want[s.name]; ok {
			if s.cli != expect {
				t.Errorf("%s -> %q, want %q", s.name, s.cli, expect)
			}
			delete(want, s.name)
			continue
		}
		if strings.Contains(s.cli, scope) {
			t.Errorf("scope leaked into %q", s.cli)
		}
	}
	for name := range want {
		t.Errorf("scoped command missing: %s", name)
	}
}

// The metric label must stay stable, or switches with different scopes would
// each get their own arista_command_success series for the same command.
func TestMetricNameIsIndependentOfScope(t *testing.T) {
	for _, s := range commandsFor(allEnabled(), "Ethernet1/1", directInterval) {
		if strings.Contains(s.name, "Ethernet") {
			t.Errorf("scope leaked into the metric label: %q", s.name)
		}
	}
}

func TestStoreUsesPerSwitchCommands(t *testing.T) {
	full := allEnabled()
	lean := map[string]config.ModuleConfig{
		"phy": {Enabled: false},
		"bgp": {Enabled: false},
	}

	store, err := NewStore([]config.SwitchConfig{
		{Host: "https://192.0.2.1", Username: "u", Password: "p", Name: "full", Collect: full},
		{Host: "https://192.0.2.2", Username: "u", Password: "p", Name: "lean", Collect: lean},
	}, full, directInterval)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(store.Get("full").Commands); n != everyCommand() {
		t.Errorf("full switch has %d commands", n)
	}
	if n := len(store.Get("lean").Commands); n != everyCommand()-2 {
		t.Errorf("lean switch has %d commands, want all except phy and bgp", n)
	}
}
