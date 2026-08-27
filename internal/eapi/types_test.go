package eapi

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// load unmarshals a repo-root testdata fixture into dst.
func load(t *testing.T, name string, dst interface{}) {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", name)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	if err := json.Unmarshal(raw, dst); err != nil {
		t.Fatalf("unmarshal %s: %v", name, err)
	}
}

func TestBGPPeerASNIsAString(t *testing.T) {
	var got ShowBGPSummary
	load(t, "show_ip_bgp_summary_vrf_all.json", &got)

	peer := got.Vrfs["default"].Peers["198.51.100.10"]
	if peer.Asn != "4200000000" {
		t.Errorf("Asn = %q, want %q", peer.Asn, "4200000000")
	}
}

func TestBGPPeerStateChangeReadsUpDownTime(t *testing.T) {
	var got ShowBGPSummary
	load(t, "show_ip_bgp_summary_vrf_all.json", &got)

	peer := got.Vrfs["default"].Peers["198.51.100.10"]
	if peer.UpDownTime != 1777294253.151276 {
		t.Errorf("UpDownTime = %v, want 1777294253.151276", peer.UpDownTime)
	}
}

func TestBGPPeerCarriesDescriptionAndPrefixCounts(t *testing.T) {
	var got ShowBGPSummary
	load(t, "show_ip_bgp_summary_vrf_all.json", &got)

	peer := got.Vrfs["INTERNET"].Peers["203.0.113.164"]
	if peer.Description != "transit-rtr-01" {
		t.Errorf("Description = %q, want %q", peer.Description, "transit-rtr-01")
	}
	if peer.PrefixAccepted != 48 {
		t.Errorf("PrefixAccepted = %d, want 48", peer.PrefixAccepted)
	}
	if peer.PrefixAdvertised != 19 {
		t.Errorf("PrefixAdvertised = %d, want 19", peer.PrefixAdvertised)
	}
}

func TestBGPCoversNonDefaultVRFs(t *testing.T) {
	var got ShowBGPSummary
	load(t, "show_ip_bgp_summary_vrf_all.json", &got)

	if _, ok := got.Vrfs["INTERNET"]; !ok {
		t.Fatal("INTERNET vrf missing")
	}
	if n := len(got.Vrfs["INTERNET"].Peers); n != 2 {
		t.Errorf("INTERNET peers = %d, want 2", n)
	}
	if n := len(got.Vrfs["TENANT_PRIV"].Peers); n != 0 {
		t.Errorf("TENANT_PRIV peers = %d, want 0", n)
	}
}

func TestInterfaceErrorCountersReadRealEOSKeys(t *testing.T) {
	var got ShowInterfaces
	load(t, "show_interfaces.json", &got)

	c := got.Interfaces["Ethernet2/1"].InterfaceCounters
	// EOS sends totalInErrors / totalOutErrors / outDiscards, not
	// inputErrors / outputErrors / totalOutDrops.
	if c.InputErrors != 4211 {
		t.Errorf("InputErrors = %d, want 4211 (json:\"totalInErrors\")", c.InputErrors)
	}
	if c.OutputErrors != 19 {
		t.Errorf("OutputErrors = %d, want 19 (json:\"totalOutErrors\")", c.OutputErrors)
	}
	if c.OutDiscards != 23 {
		t.Errorf("OutDiscards = %d, want 23 (json:\"outDiscards\")", c.OutDiscards)
	}
}

func TestInterfaceCarriesFlapsAndErrorDetail(t *testing.T) {
	var got ShowInterfaces
	load(t, "show_interfaces.json", &got)

	c := got.Interfaces["Ethernet2/1"].InterfaceCounters
	if c.LinkStatusChanges != 431 {
		t.Errorf("LinkStatusChanges = %d, want 431", c.LinkStatusChanges)
	}
	if c.InputErrorsDetail.FcsErrors != 4190 {
		t.Errorf("FcsErrors = %d, want 4190", c.InputErrorsDetail.FcsErrors)
	}
	if c.OutputErrorsDetail.TxPause != 13 {
		t.Errorf("TxPause = %d, want 13", c.OutputErrorsDetail.TxPause)
	}
}

func TestInterfaceCarriesBandwidthAndDescription(t *testing.T) {
	var got ShowInterfaces
	load(t, "show_interfaces.json", &got)

	i := got.Interfaces["Ethernet1/1"]
	if i.Bandwidth != 10000000000 {
		t.Errorf("Bandwidth = %d, want 10000000000", i.Bandwidth)
	}
	if i.Description != "host-a01 (planned) bond10" {
		t.Errorf("Description = %q", i.Description)
	}
	if i.InterfaceMembership != "Member of Port-Channel7" {
		t.Errorf("InterfaceMembership = %q", i.InterfaceMembership)
	}
}

func TestOctetCountersExceedUint32(t *testing.T) {
	var got ShowInterfaces
	load(t, "show_interfaces.json", &got)

	if c := got.Interfaces["Ethernet1/1"].InterfaceCounters; c.InOctets != 40456835181534 {
		t.Errorf("InOctets = %d, want 40456835181534", c.InOctets)
	}
}
