package eapi

import "testing"

// These lock the five schemas that were verified correct against EOS
// 4.35.4M. They are regression guards, not TDD cycles: the point is that an
// EOS upgrade renaming a field fails the build's tests instead of silently
// zeroing a metric.

func TestVersionSchema(t *testing.T) {
	var v ShowVersion
	load(t, "show_version.json", &v)

	if v.ModelName != "DCS-7050CX3-32C-R" || v.Version != "4.35.4M" {
		t.Errorf("model/version = %q/%q", v.ModelName, v.Version)
	}
	if v.BootupTimestamp != 1777294086.4660642 {
		t.Errorf("BootupTimestamp = %v (must be a Unix epoch, not a duration)", v.BootupTimestamp)
	}
	// memTotal is kilobytes: 16280716 kB is a 16 GB box.
	if v.MemTotal != 16280716 {
		t.Errorf("MemTotal = %d kB", v.MemTotal)
	}
}

func TestProcessesTopSchema(t *testing.T) {
	var p ShowProcessesTop
	load(t, "show_processes_top_once.json", &p)

	// The CPU block hangs off a key literally spelled "%Cpu(s)".
	if p.CpuInfo.Cpu.Idle != 82.7 || p.CpuInfo.Cpu.User != 13.5 {
		t.Errorf(`cpuInfo["%%Cpu(s)"] did not resolve: idle=%v user=%v`,
			p.CpuInfo.Cpu.Idle, p.CpuInfo.Cpu.User)
	}
	if len(p.TimeInfo.LoadAvg) != 3 {
		t.Fatalf("LoadAvg = %v, want 3 elements", p.TimeInfo.LoadAvg)
	}
	m := p.MemInfo.Physical
	if m.Free != 7159603 || m.Used != 3599667 || m.Buffer != 6654156 {
		t.Errorf("physicalMem free/used/buffer = %d/%d/%d", m.Free, m.Used, m.Buffer)
	}
	// This strict-free figure is ~5.3 GiB below show version's memFree,
	// which tracks MemAvailable. Both are exposed, under separate names.
	if m.Free >= 12674140 {
		t.Errorf("top memFree (%d) should be well below show version memFree (12674140)", m.Free)
	}
}

func TestTemperatureSchema(t *testing.T) {
	var e ShowEnvironmentTemp
	load(t, "show_system_environment_temperature.json", &e)

	if e.SystemStatus != "temperatureOk" || !e.ShutdownOnOverheat {
		t.Errorf("systemStatus=%q shutdownOnOverheat=%v", e.SystemStatus, e.ShutdownOnOverheat)
	}
	if len(e.TempSensors) != 7 {
		t.Errorf("system sensors = %d, want 7", len(e.TempSensors))
	}
	// PSU sensors come from this command, not from environment power: only
	// here do they carry thresholds, historical max and a description.
	psu := 0
	for _, slot := range e.PowerSupplySlots {
		psu += len(slot.TempSensors)
	}
	if psu != 6 {
		t.Errorf("PSU sensors = %d, want 6", psu)
	}
	s := e.TempSensors[0]
	if s.Name != "TempSensor1" || s.Description != "Cpu temp sensor" ||
		s.OverheatThreshold != 95.0 || s.CriticalThreshold != 115.0 {
		t.Errorf("first sensor = %+v", s)
	}
}

func TestPowerSchema(t *testing.T) {
	var e ShowEnvironmentPower
	load(t, "show_system_environment_power.json", &e)

	if len(e.PowerSupplies) != 2 {
		t.Fatalf("PSUs = %d, want 2", len(e.PowerSupplies))
	}
	p := e.PowerSupplies["1"]
	if p.State != "ok" || p.Capacity != 500.0 || p.OutputPower != 80.0 {
		t.Errorf("psu 1 = %+v", p)
	}
	// uptime is a Unix epoch, which is why the metric is named
	// arista_psu_boot_timestamp_seconds rather than an uptime duration.
	if p.Uptime != 1777294197.5810893 {
		t.Errorf("Uptime = %v", p.Uptime)
	}
	if len(p.TempSensors) != 3 || len(p.Fans) != 1 {
		t.Errorf("psu 1 sensors=%d fans=%d", len(p.TempSensors), len(p.Fans))
	}
}

func TestCoolingSchema(t *testing.T) {
	var e ShowEnvironmentCooling
	load(t, "show_system_environment_cooling.json", &e)

	if e.SystemStatus != "coolingOk" || e.FansStatus != "fanAlarmOk" {
		t.Errorf("systemStatus=%q fansStatus=%q", e.SystemStatus, e.FansStatus)
	}
	if !e.ShutdownOnInsufficientFans || e.AmbientTemperature != 21.5 {
		t.Errorf("shutdownOnInsufficientFans=%v ambient=%v",
			e.ShutdownOnInsufficientFans, e.AmbientTemperature)
	}
	fans := 0
	for _, tr := range e.FanTraySlots {
		fans += len(tr.Fans)
	}
	for _, ps := range e.PowerSupplySlots {
		fans += len(ps.Fans)
	}
	if fans != 6 {
		t.Errorf("fans = %d, want 6 (4 tray + 2 PSU)", fans)
	}
	f := e.FanTraySlots[0].Fans[0]
	// maxSpeed is RPM; configured and actual are percentages.
	if f.MaxSpeed != 25000 || f.ConfiguredSpeed != 33 || f.ActualSpeed != 34 {
		t.Errorf("fan = %+v", f)
	}
	if f.Uptime != 1777294189.1848712 {
		t.Errorf("fan Uptime = %v (epoch, not duration)", f.Uptime)
	}
}
