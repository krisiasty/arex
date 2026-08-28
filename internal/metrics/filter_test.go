package metrics

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

func filterHandler(t *testing.T) http.Handler {
	t.Helper()
	store, index := twoSwitchStore(t)
	return NewHandler(store, 90*time.Second, index)
}

// families returns the metric names present in a response.
func families(body string) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(body, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name := line
		if i := strings.IndexAny(line, "{ "); i > 0 {
			name = line[:i]
		}
		out[name] = true
	}
	return out
}

func TestModuleSelectsOneGroup(t *testing.T) {
	h := filterHandler(t)
	code, body := get(t, h, "/metrics?target=leaf-1&module=power")
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, body)
	}
	fams := families(body)
	if !fams["arista_psu_ok"] {
		t.Error("power module should include arista_psu_ok")
	}
	for _, unwanted := range []string{
		"arista_interface_link_up", "arista_phy_link_up", "arista_cooling_ok", "arista_bgp_peer_up",
	} {
		if fams[unwanted] {
			t.Errorf("power module should not include %s", unwanted)
		}
	}
}

// Scrape health is not part of any module: it describes the poll, not the data,
// and suppressing it would make a filtered view look like a dead switch.
func TestModuleKeepsScrapeHealth(t *testing.T) {
	h := filterHandler(t)
	_, body := get(t, h, "/metrics?target=leaf-1&module=power")
	fams := families(body)
	for _, want := range []string{"arista_scrape_success", "arista_scrape_age_seconds"} {
		if !fams[want] {
			t.Errorf("a filtered view should still report %s", want)
		}
	}
}

func TestModuleVersionSelectsIdentity(t *testing.T) {
	h := filterHandler(t)
	_, body := get(t, h, "/metrics?target=leaf-1&module=version")
	fams := families(body)
	if !fams["arista_info"] || !fams["arista_memory_total_bytes"] {
		t.Errorf("version module should carry identity and memory: %v", fams)
	}
	if fams["arista_cpu_idle_percent"] {
		t.Error("cpu comes from the processes module, not version")
	}
}

func TestUnknownModuleIsAnError(t *testing.T) {
	h := filterHandler(t)
	code, body := get(t, h, "/metrics?target=leaf-1&module=nonsense")
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
	if !strings.Contains(body, "nonsense") {
		t.Errorf("error should name the module: %s", body)
	}
}

// Asking about an interface should not return power supplies: the question
// narrows to the families that describe an interface.
func TestInterfaceNarrowsToInterfaceFamilies(t *testing.T) {
	h := filterHandler(t)
	code, body := get(t, h, "/metrics?target=leaf-1&interface=Ethernet1/1")
	if code != http.StatusOK {
		t.Fatalf("status = %d: %s", code, body)
	}
	fams := families(body)
	for _, want := range []string{"arista_interface_link_up", "arista_transceiver_rx_power_dbm", "arista_phy_link_up"} {
		if !fams[want] {
			t.Errorf("missing %s", want)
		}
	}
	for _, unwanted := range []string{"arista_psu_ok", "arista_cooling_ok", "arista_bgp_peer_up", "arista_cpu_idle_percent"} {
		if fams[unwanted] {
			t.Errorf("an interface query should not return %s", unwanted)
		}
	}
	// And only the requested interface.
	for _, line := range strings.Split(body, "\n") {
		if strings.Contains(line, `interface="`) && !strings.Contains(line, `interface="Ethernet1/1"`) {
			t.Errorf("other interfaces leaked: %s", line)
		}
	}
}

func TestInterfaceAndModuleCombine(t *testing.T) {
	h := filterHandler(t)
	_, body := get(t, h, "/metrics?target=leaf-1&interface=Ethernet1/1&module=phy")
	fams := families(body)
	if !fams["arista_phy_link_up"] {
		t.Error("missing phy metrics")
	}
	if fams["arista_interface_link_up"] || fams["arista_transceiver_rx_power_dbm"] {
		t.Errorf("module=phy should exclude other interface families: %v", fams)
	}
}

// A typo must say so rather than return an empty body a human would read as
// "this interface has no data".
func TestUnknownInterfaceIsAnError(t *testing.T) {
	h := filterHandler(t)
	code, body := get(t, h, "/metrics?target=leaf-1&interface=Ethernet99/9")
	if code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", code)
	}
	if !strings.Contains(body, "Ethernet99/9") {
		t.Errorf("error should name the interface: %s", body)
	}
}

func TestFiltersRejectedForInternalTarget(t *testing.T) {
	h := filterHandler(t)
	for _, q := range []string{"?target=internal&module=phy", "?target=internal&interface=Ethernet1/1"} {
		code, body := get(t, h, "/metrics"+q)
		if code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400 (%s)", q, code, strings.TrimSpace(body))
		}
	}
}

// Without a target, a filter applies across every switch: "show me this
// interface wherever it exists" is a reasonable question.
func TestFilterWithoutTargetSpansSwitches(t *testing.T) {
	h := filterHandler(t)
	code, body := get(t, h, "/metrics?module=power")
	if code != http.StatusOK {
		t.Fatalf("status = %d", code)
	}
	for _, want := range []string{`switch="leaf-1"`, `switch="leaf-2"`} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %s", want)
		}
	}
	if families(body)["arista_interface_link_up"] {
		t.Error("module filter should still exclude other groups")
	}
}
