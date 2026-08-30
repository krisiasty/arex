package metrics

import (
	"strings"
	"testing"
)

const (
	privBundle = `instance="VLAN-aware bundle TENANT_PROD_PRIVATE"`
	pubBundle  = `instance="VLAN-aware bundle TENANT_PROD_PUBLIC"`
	downESI    = `esi="0000:0000:0000:0000:1002"`
	healthyESI = `esi="0000:0000:0000:0000:1001"`
	splitESI   = `esi="0000:0000:0000:0000:1008"`
)

// The three states a segment can be in are distinguishable, which is the whole
// reason designated_forwarder_elected exists alongside designated_forwarder.
// Without it, "the other leaf is forwarding" and "nobody is forwarding" both
// read as 0 and an alert cannot tell a working fabric from a broken one.
func TestESIDistinguishesNoForwarderFromSomeoneElsesForwarder(t *testing.T) {
	out := render(t, nil)

	for _, c := range []struct {
		what              string
		labels            []string
		df, elected, want string
	}{
		{"this switch forwards", []string{privBundle, healthyESI}, "1", "1", "2"},
		{"the peer forwards", []string{pubBundle, splitESI}, "0", "1", "2"},
		{"nobody forwards", []string{privBundle, downESI}, "0", "0", "0"},
	} {
		df := sample(out, "arista_evpn_esi_designated_forwarder", c.labels...)
		el := sample(out, "arista_evpn_esi_designated_forwarder_elected", c.labels...)
		fp := sample(out, "arista_evpn_esi_forwarding_peers", c.labels...)
		if df != c.df || el != c.elected || fp != c.want {
			t.Errorf("%s: forwarder=%q elected=%q peers=%q, want %s/%s/%s",
				c.what, df, el, fp, c.df, c.elected, c.want)
		}
	}
}

// The same ESI in two bundles, with a different forwarder in each. Keyed by
// segment alone these would collide and one would silently win.
func TestESIIsKeyedByBundleAndSegment(t *testing.T) {
	out := render(t, nil)

	priv := sample(out, "arista_evpn_esi_designated_forwarder", privBundle, splitESI)
	pub := sample(out, "arista_evpn_esi_designated_forwarder", pubBundle, splitESI)
	if priv == "" || pub == "" {
		t.Fatalf("one series per bundle expected, got priv=%q pub=%q", priv, pub)
	}
	if priv == pub {
		t.Errorf("both bundles report forwarder=%q; the fixture no longer covers per-bundle election", priv)
	}
}

// A segment down on this switch still reports up on the switch that kept its
// link. forwarding_peers is what shows it from either side, so it has to be
// emitted for every segment rather than only when something looks wrong.
func TestESIDownSegmentReportsZeroForwardingPeers(t *testing.T) {
	out := render(t, nil)

	if got := sample(out, "arista_evpn_esi_up", privBundle, downESI); got != "0" {
		t.Errorf("down segment up = %q, want 0", got)
	}
	if got := sample(out, "arista_evpn_esi_info", privBundle, downESI,
		`interface="Port-Channel112"`, `redundancy_mode="allActive"`); got != "1" {
		t.Errorf("down segment info = %q, want 1: labels survive the segment going down", got)
	}
}

// EVPN sessions are their own series, not labels on the IPv4 ones. The same
// neighbour carries both, and either can fail while the other stays up.
func TestEVPNPeersAreSeparateFromIPv4Peers(t *testing.T) {
	out := render(t, nil)

	if got := sample(out, "arista_bgp_evpn_peer_up", `peer="198.51.100.101"`); got != "1" {
		t.Errorf("evpn peer up = %q, want 1", got)
	}
	// The IPv4 module's own peers are a different set entirely.
	if strings.Contains(out, `arista_bgp_peer_up{asn="4200000100"`) {
		t.Error("an EVPN peer leaked into the IPv4 peer series")
	}
	if got := sample(out, "arista_bgp_evpn_peer_prefixes_received", `peer="198.51.100.101"`); got != "653" {
		t.Errorf("evpn prefixes received = %q, want 653", got)
	}
}

// All six route types, including the zero one: a missing series and a zero
// count mean different things, and type-5 IPv6 is legitimately zero here.
func TestEVPNRouteTypesIncludeTheZeroOne(t *testing.T) {
	out := render(t, nil)

	for _, c := range [][2]string{
		{"auto_discovery", "720"}, {"mac_ip", "840"}, {"imet", "105"},
		{"ethernet_segment", "110"}, {"ip_prefix_ipv4", "225"}, {"ip_prefix_ipv6", "0"},
	} {
		if got := sample(out, "arista_bgp_evpn_routes", `route_type="`+c[0]+`"`); got != c[1] {
			t.Errorf("route type %s = %q, want %s", c[0], got, c[1])
		}
	}
}

// The VTEP count is a stable series to alert on. The per-VTEP series cannot be:
// EOS reports only VTEPs it knows, so one leaving removes its series rather
// than zeroing it.
func TestVXLANReportsVTEPCountAndDetail(t *testing.T) {
	out := render(t, nil)

	if got := sample(out, "arista_vxlan_remote_vtep_count", `interface="Vxlan1"`); got != "1" {
		t.Errorf("vtep count = %q, want 1", got)
	}
	if got := sample(out, "arista_vxlan_remote_vtep_info", `vtep="192.0.2.102"`,
		`tunnel_types="unicast,flood"`); got != "1" {
		t.Errorf("vtep info = %q, want 1 with both tunnel types joined", got)
	}
	if got := sample(out, "arista_vxlan_remote_mac_count", `vtep="192.0.2.102"`); got != "4" {
		t.Errorf("remote mac count = %q, want 4", got)
	}
}

// The interface's own state, and the bindings that say what it carries.
func TestVXLANInterfaceStateAndBindings(t *testing.T) {
	out := render(t, nil)

	if got := sample(out, "arista_vxlan_interface_up", `interface="Vxlan1"`); got != "1" {
		t.Errorf("vxlan interface up = %q, want 1", got)
	}
	if got := sample(out, "arista_vxlan_interface_info", `source_interface="Loopback0"`,
		`source_address="192.0.2.101"`, `replication_mode="headendVcs"`); got != "1" {
		t.Errorf("vxlan interface info = %q, want 1", got)
	}
	if got := sample(out, "arista_vxlan_vni_info", `vlan="201"`, `vni="20201"`); got != "1" {
		t.Errorf("vni binding = %q, want 1", got)
	}
	if got := sample(out, "arista_vxlan_vrf_vni_info", `vrf="TENANT_PROD_PRIVATE"`, `vni="6000002"`); got != "1" {
		t.Errorf("vrf vni binding = %q, want 1", got)
	}
	if got := sample(out, "arista_vxlan_vlan_flood_vteps", `vlan="202"`); got != "1" {
		t.Errorf("flood list size = %q, want 1", got)
	}
}
