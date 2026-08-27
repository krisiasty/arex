package eapi

import "testing"

func TestTransceiverReadingsAndThresholds(t *testing.T) {
	var got ShowTransceiverDetail
	load(t, "show_interfaces_transceiver_detail.json", &got)

	x := got.Interfaces["Ethernet1/1"]
	if x.Slot != "Ethernet1" || x.Channel != "1" {
		t.Errorf("slot/channel = %q/%q, want Ethernet1/1", x.Slot, x.Channel)
	}
	if x.MediaType != "40GBASE-SR4" {
		t.Errorf("MediaType = %q", x.MediaType)
	}
	if x.RxPower != -1.4333151638846502 {
		t.Errorf("RxPower = %v", x.RxPower)
	}
	if x.Temperature != 35.09765625 {
		t.Errorf("Temperature = %v", x.Temperature)
	}
}

// EOS pads vendorSn in some commands and not others; the struct must not
// carry that difference into label values.
func TestTransceiverVendorSerialIsTrimmed(t *testing.T) {
	var got ShowTransceiverDetail
	load(t, "show_interfaces_transceiver_detail.json", &got)

	if sn := got.Interfaces["Ethernet1/1"].VendorSn; sn != "XXX000ab0002" {
		t.Errorf("VendorSn = %q, want %q", sn, "XXX000ab0002")
	}
}

// totalRxPower reports the *Overridden flags but no limits at all. A missing
// limit must stay distinguishable from a legitimate 0.0 (txBias low alarm).
func TestTransceiverAbsentThresholdIsNotZero(t *testing.T) {
	var got ShowTransceiverDetail
	load(t, "show_interfaces_transceiver_detail.json", &got)

	d := got.Interfaces["Ethernet1/1"].Details

	if d["totalRxPower"].HighAlarm != nil {
		t.Errorf("totalRxPower HighAlarm = %v, want nil (absent)", *d["totalRxPower"].HighAlarm)
	}
	bias := d["txBias"]
	if bias.LowAlarm == nil {
		t.Fatal("txBias LowAlarm is nil, want a real 0.0")
	}
	if *bias.LowAlarm != 0.0 {
		t.Errorf("txBias LowAlarm = %v, want 0.0", *bias.LowAlarm)
	}
	if rx := d["rxPower"]; rx.LowWarn == nil || *rx.LowWarn != -15.003129173815964 {
		t.Errorf("rxPower LowWarn = %v", rx.LowWarn)
	}
}

func TestTransceiverThresholdsVaryByMediaType(t *testing.T) {
	var got ShowTransceiverDetail
	load(t, "show_interfaces_transceiver_detail.json", &got)

	sr4 := got.Interfaces["Ethernet7/1"].Details["txBias"]   // 100GBASE-SR4
	lrl4 := got.Interfaces["Ethernet31/1"].Details["txBias"] // 40GBASE-LRL4
	if *sr4.HighAlarm != 11.0 {
		t.Errorf("SR4 txBias HighAlarm = %v, want 11", *sr4.HighAlarm)
	}
	if *lrl4.HighAlarm != 80.0 {
		t.Errorf("LRL4 txBias HighAlarm = %v, want 80", *lrl4.HighAlarm)
	}
}

func TestPhyFECIsOptionalBySpeed(t *testing.T) {
	var got ShowPhyDetail
	load(t, "show_interfaces_phy_detail.json", &got)

	tenG := got.Interfaces["Ethernet1/1"].PhyStatuses[0]
	if tenG.FEC != nil {
		t.Error("10G interface should have no fec block")
	}
	if tenG.PCS.BlockLock == nil {
		t.Error("10G interface should have pcs.blockLock")
	}

	twentyFiveG := got.Interfaces["Ethernet4/1"].PhyStatuses[0]
	if twentyFiveG.FEC == nil {
		t.Fatal("25G interface should have a fec block")
	}
	if twentyFiveG.PCS.BlockLock != nil {
		t.Error("25G interface should not have pcs.blockLock (FEC replaces it)")
	}
}

func TestPhyFECCounterValueAndChanges(t *testing.T) {
	var got ShowPhyDetail
	load(t, "show_interfaces_phy_detail.json", &got)

	fec := got.Interfaces["Ethernet4/1"].PhyStatuses[0].FEC
	if fec.Encoding != "reedSolomon" || fec.CodewordSize != "528" {
		t.Errorf("encoding/codewordSize = %q/%q", fec.Encoding, fec.CodewordSize)
	}
	// value and changes disagree (3 vs 13); both are exposed, typed differently.
	if fec.UncorrectedCodewords.Value != 3 {
		t.Errorf("UncorrectedCodewords.Value = %v, want 3", fec.UncorrectedCodewords.Value)
	}
	if fec.UncorrectedCodewords.Changes != 13 {
		t.Errorf("UncorrectedCodewords.Changes = %d, want 13", fec.UncorrectedCodewords.Changes)
	}
	if fec.CorrectedCodewords.LastChange != 1787066000.4227245 {
		t.Errorf("CorrectedCodewords.LastChange = %v", fec.CorrectedCodewords.LastChange)
	}
}

// laneMap is map[string]int while its sibling correctedSymbols is
// map[string]{value,changes,lastChange} — same object, different shapes.
func TestPhyPerLaneSymbolsOnlyOnNativeSpeed(t *testing.T) {
	var got ShowPhyDetail
	load(t, "show_interfaces_phy_detail.json", &got)

	if n := len(got.Interfaces["Ethernet4/1"].PhyStatuses[0].FEC.CorrectedSymbols); n != 0 {
		t.Errorf("25G correctedSymbols = %d lanes, want 0", n)
	}
	hundred := got.Interfaces["Ethernet29/1"].PhyStatuses[0].FEC
	if n := len(hundred.CorrectedSymbols); n != 4 {
		t.Errorf("100G correctedSymbols = %d lanes, want 4", n)
	}
	if hundred.LaneMap["3"] != 3 {
		t.Errorf("laneMap[3] = %d, want 3", hundred.LaneMap["3"])
	}
}

func TestPhyMacFaultsAndSpeed(t *testing.T) {
	var got ShowPhyDetail
	load(t, "show_interfaces_phy_detail.json", &got)

	i := got.Interfaces["Ethernet4/1"]
	p := i.PhyStatuses[0]
	if p.OperSpeed != "25Gbps" {
		t.Errorf("OperSpeed = %q", p.OperSpeed)
	}
	if i.MacFaults.LocalFault.Changes != 62 {
		t.Errorf("LocalFault.Changes = %d, want 62", i.MacFaults.LocalFault.Changes)
	}
	if i.MacFaults.LocalFault.Value {
		t.Error("LocalFault.Value = true, want false")
	}
	if sn := i.Transceiver.VendorSn; sn != "XXX000a`0004" {
		t.Errorf("phy VendorSn = %q, want trailing whitespace trimmed", sn)
	}
}
