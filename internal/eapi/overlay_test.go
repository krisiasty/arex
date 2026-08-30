package eapi

import "testing"

func TestVXLANVTEPFixture(t *testing.T) {
	var got ShowVXLANVTEP
	load(t, "show_vxlan_vtep_fabric_a_leaf_1.json", &got)

	if n := len(got.Interfaces["Vxlan1"].VTEPs); n != 1 {
		t.Fatalf("Vxlan1 VTEPs = %d, want 1", n)
	}
	types := got.VTEPTunnelTypes["192.0.2.102"].TunnelTypes
	if len(types) != 2 || types[0].TunnelType != "unicast" || types[1].TunnelType != "flood" {
		t.Errorf("tunnel types = %#v, want unicast and flood", types)
	}

	var spine ShowVXLANVTEP
	load(t, "show_vxlan_vtep_fabric_a_spine_1.json", &spine)
	if len(spine.Interfaces) != 0 || len(spine.VTEPTunnelTypes) != 0 {
		t.Error("spine should have no terminated VTEPs")
	}
}

func TestVXLANInterfaceFixture(t *testing.T) {
	var got ShowInterfaceVXLAN
	load(t, "show_interface_vxlan_1_fabric_a_leaf_1.json", &got)

	vx := got.Interfaces["Vxlan1"]
	if vx.LineProtocolStatus != "up" || vx.InterfaceStatus != "connected" {
		t.Errorf("Vxlan1 status = %q/%q", vx.LineProtocolStatus, vx.InterfaceStatus)
	}
	if vx.SourceInterface != "Loopback0" || vx.SourceAddress != "192.0.2.101" {
		t.Errorf("Vxlan1 source = %q/%q", vx.SourceInterface, vx.SourceAddress)
	}
	if len(vx.VLANToVNIMap) != 43 || len(vx.VLANToVTEPMap) != 36 || len(vx.VRFToVNIMap) != 6 {
		t.Errorf("mapping counts = VLAN/VNI %d, flood %d, VRF/VNI %d",
			len(vx.VLANToVNIMap), len(vx.VLANToVTEPMap), len(vx.VRFToVNIMap))
	}
}

func TestBGPEVPNSummaryFixture(t *testing.T) {
	var got ShowBGPEVPNSummary
	load(t, "show_bgp_evpn_summary_fabric_a_leaf_1.json", &got)

	vrf := got.Vrfs["default"]
	if vrf.Asn != "4200000101" || len(vrf.Peers) != 2 {
		t.Errorf("EVPN summary ASN/peers = %q/%d", vrf.Asn, len(vrf.Peers))
	}
	peer := vrf.Peers["198.51.100.101"]
	if peer.PeerState != "Established" || peer.PrefixAccepted == 0 {
		t.Errorf("EVPN peer = %#v", peer)
	}
}

func TestVXLANAndEVPNCounts(t *testing.T) {
	var macs ShowVXLANAddressTableCount
	load(t, "show_vxlan_address_table_count_fabric_a_leaf_1.json", &macs)
	if got := macs.VTEPCounts["192.0.2.102"]; got != 4 {
		t.Errorf("remote MAC count = %d, want 4", got)
	}

	var routes ShowBGPEVPNRouteTypeCount
	load(t, "show_bgp_evpn_route_type_count_fabric_a_leaf_1.json", &routes)
	if routes.AutoDiscovery != 720 || routes.MACIP != 840 || routes.IPPrefixIPv6 != 0 {
		t.Errorf("route path counts = %#v", routes)
	}
}

func TestBGPEVPNInstanceFixture(t *testing.T) {
	var got ShowBGPEVPNInstance
	load(t, "show_bgp_evpn_instance_fabric_a_leaf_1.json", &got)

	if len(got.Instances) != 4 {
		t.Fatalf("instances = %d, want 4", len(got.Instances))
	}
	instance := got.Instances["VLAN-aware bundle TENANT_PROD_PRIVATE"]
	if instance.LocalVTEP != "192.0.2.101" || len(instance.EthernetSegments) != 37 {
		t.Errorf("instance local VTEP/segments = %q/%d", instance.LocalVTEP, len(instance.EthernetSegments))
	}
	segment := instance.EthernetSegments["0000:0000:0000:0000:1001"]
	if segment.State != "up" || segment.DFPeer.IP != "192.0.2.101" || len(segment.ForwardingPeers) != 2 {
		t.Errorf("segment = %#v", segment)
	}

	var spine ShowBGPEVPNInstance
	load(t, "show_bgp_evpn_instance_fabric_a_spine_1.json", &spine)
	if len(spine.Instances) != 0 {
		t.Errorf("spine instances = %d, want 0", len(spine.Instances))
	}
}

// A segment whose local link is down, captured from both sides of a real
// multihomed pair while one leg was shut.
//
// The two views disagree, and that disagreement is the point: the leaf that
// lost the link reports the segment down with no forwarder, while its peer
// reports it up because its own link is fine. Only ForwardingPeers shows,
// from either side, that the segment is running on one leg.
func TestEthernetSegmentDownOnOneLeg(t *testing.T) {
	const (
		esi   = "0000:0000:0000:0000:1002"
		bundl = "VLAN-aware bundle TENANT_PROD_PRIVATE"
	)

	var down ShowBGPEVPNInstance
	load(t, "show_bgp_evpn_instance_fabric_a_leaf_1.json", &down)
	lost := down.Instances[bundl].EthernetSegments[esi]

	if lost.Up() {
		t.Errorf("state = %q, want down on the leaf that lost the link", lost.State)
	}
	// EOS swaps dFElectionAlgorithm for dFElectionState here. Decoding the
	// wrong one silently drops the only field that says why there is no DF.
	if lost.DFElectionState != "pending" || lost.DFElectionAlgorithm != "" {
		t.Errorf("election algorithm/state = %q/%q, want \"\"/pending",
			lost.DFElectionAlgorithm, lost.DFElectionState)
	}
	if lost.HasDesignatedForwarder() {
		t.Errorf("dFPeer = %q, want no forwarder elected", lost.DFPeer.IP)
	}
	if len(lost.ForwardingPeers) != 0 || len(lost.NonDFPeers) != 0 {
		t.Errorf("peers = %d forwarding / %d non-DF, want none", len(lost.ForwardingPeers), len(lost.NonDFPeers))
	}

	var survivor ShowBGPEVPNInstance
	load(t, "show_bgp_evpn_instance_fabric_a_leaf_2.json", &survivor)
	kept := survivor.Instances[bundl].EthernetSegments[esi]

	if !kept.Up() {
		t.Errorf("survivor state = %q, want up: its own link is fine", kept.State)
	}
	if !kept.DesignatedForwarder("192.0.2.102") {
		t.Errorf("survivor dFPeer = %q, want itself after re-election", kept.DFPeer.IP)
	}
	if len(kept.ForwardingPeers) != 1 {
		t.Errorf("survivor forwarding peers = %d, want 1: the redundancy is gone",
			len(kept.ForwardingPeers))
	}

	// The healthy segments in the same capture, for contrast.
	healthy := down.Instances[bundl].EthernetSegments["0000:0000:0000:0000:1001"]
	if !healthy.Up() || healthy.DFElectionAlgorithm != "modulus" ||
		!healthy.DesignatedForwarder("192.0.2.101") || len(healthy.ForwardingPeers) != 2 {
		t.Errorf("healthy segment = %#v", healthy)
	}
}

// DF election runs per VLAN-aware bundle, so one ESI can have different
// forwarders in different bundles. Keying a metric by ESI alone would report
// one bundle's answer as the whole truth.
func TestDesignatedForwarderVariesByBundle(t *testing.T) {
	var got ShowBGPEVPNInstance
	load(t, "show_bgp_evpn_instance_fabric_a_leaf_1.json", &got)

	seen := map[string]map[string]bool{}
	for _, inst := range got.Instances {
		for esi, seg := range inst.EthernetSegments {
			if seen[esi] == nil {
				seen[esi] = map[string]bool{}
			}
			seen[esi][seg.DFPeer.IP] = true
		}
	}
	split := 0
	for _, peers := range seen {
		if len(peers) > 1 {
			split++
		}
	}
	if split == 0 {
		t.Error("no ESI has a different DF in different bundles; the fixture no longer " +
			"covers per-bundle election, and a per-ESI metric key would look correct")
	}
}
