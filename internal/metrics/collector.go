// Package metrics exposes collected switch data as Prometheus metrics.
package metrics

import (
	"fmt"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/krisiasty/arex/internal/collector"
	"github.com/krisiasty/arex/internal/eapi"
)

// Collector renders the store at scrape time.
//
// Reading at scrape time rather than caching means a scrape reflects whatever
// the last poll established, and the same snapshot backs /status, so the two
// cannot disagree.
//
// Deliberately absent is anything about this process's own memory, goroutines
// or CPU. The Go and process collectors registered alongside export all of
// that, and export it better.
type Collector struct {
	store          *collector.Store
	stalenessLimit time.Duration
	now            func() time.Time
}

// NewCollector returns a collector over store.
func NewCollector(store *collector.Store, stalenessLimit time.Duration) *Collector {
	return &Collector{store: store, stalenessLimit: stalenessLimit, now: time.Now}
}

func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range descs {
		ch <- d
	}
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	collectBuildInfo(ch)
	now := c.now()
	for _, sw := range c.store.All() {
		c.collectSwitch(ch, sw, now)
	}
}

// set emits one sample, using the value type declared in the catalogue so a
// family cannot be declared a counter and emitted as a gauge.
//
// A label mismatch becomes a scrape error rather than a panic: promhttp
// reports an invalid metric, which is visible and recoverable, whereas a
// panic in Collect would take down the handler.
func set(ch chan<- prometheus.Metric, name string, v float64, labelValues ...string) {
	d, ok := descs[name]
	if !ok {
		ch <- prometheus.NewInvalidMetric(
			prometheus.NewInvalidDesc(fmt.Errorf("metric %q is not in the catalogue", name)),
			fmt.Errorf("metric %q is not in the catalogue", name))
		return
	}
	m, err := prometheus.NewConstMetric(d, types[name], v, labelValues...)
	if err != nil {
		ch <- prometheus.NewInvalidMetric(d, err)
		return
	}
	ch <- m
}

func setBool(ch chan<- prometheus.Metric, name string, b bool, labelValues ...string) {
	v := 0.0
	if b {
		v = 1.0
	}
	set(ch, name, v, labelValues...)
}

// setTime emits an epoch, skipping the zero EOS uses for "never". A zero
// would read as 1970 and fire every age-based alert at once.
func setTime(ch chan<- prometheus.Metric, name string, epoch float64, labelValues ...string) {
	if epoch <= 0 {
		return
	}
	set(ch, name, epoch, labelValues...)
}

func collectBuildInfo(ch chan<- prometheus.Metric) {
	b := buildInfo()
	set(ch, "arex_build_info", 1, b.version, b.revision, b.goVersion, b.modified)
}

// collectSwitch renders one switch.
//
// Scrape health is always emitted. Beyond that, last-known-good data is
// served even when the most recent poll failed: riding out a transient eAPI
// failure is the main advantage of polling out-of-band.
func (c *Collector) collectSwitch(ch chan<- prometheus.Metric, sw *collector.SwitchData, now time.Time) {
	sw.RLock()
	defer sw.RUnlock()

	label := sw.Label
	collected := !sw.LastSuccess.IsZero()
	setBool(ch, "arista_scrape_success", collected && sw.ScrapeErr == nil, label)

	// Per-command state is scrape metadata, not switch data, so it is emitted
	// even when nothing was ever collected: a missing series is not zero in
	// Prometheus, and omitting these would drop the most broken switch out of
	// any aggregate over arista_command_success.
	if sw.CommandErrors != nil {
		for _, name := range sw.Commands {
			_, failed := sw.CommandErrors[name]
			setBool(ch, "arista_command_success", !failed, label, name)
		}
	}

	c.collectEAPIStats(ch, label, sw.Stats.Snapshot())

	if !collected {
		set(ch, "arista_scrape_age_seconds", -1, label)
		return // never collected: there is no switch data to serve
	}

	age := now.Sub(sw.LastSuccess)
	set(ch, "arista_scrape_age_seconds", age.Seconds(), label)
	if age > c.stalenessLimit {
		return // too old to be worth publishing
	}

	// Each command's data is bounded separately. Data from a failed command is
	// retained so a transient rejection does not create a gap, but one command
	// that keeps working must not hold the whole scrape "fresh" while
	// everything else silently ages.
	fresh := func(cli string) bool {
		last, ok := sw.CommandLastSuccess[cli]
		return ok && now.Sub(last) <= c.stalenessLimit
	}

	if fresh(collector.CmdVersion) {
		collectVersion(ch, label, sw.Version)
	}
	if fresh(collector.CmdProcessesTop) {
		collectCPUMemory(ch, label, sw.ProcessTop)
	}
	if fresh(collector.CmdEnvTemp) {
		collectTemperature(ch, label, sw.EnvTemp)
	}
	if fresh(collector.CmdEnvPower) {
		collectPower(ch, label, sw.EnvPower)
	}
	if fresh(collector.CmdEnvCooling) {
		collectCooling(ch, label, sw.EnvCooling)
	}
	if fresh(collector.CmdInterfaces) {
		collectInterfaces(ch, label, sw.Interfaces)
	}
	if fresh(collector.CmdBGPSummary) {
		collectBGP(ch, label, sw.BGPSummary)
	}
	if fresh(collector.CmdTransceivers) {
		collectTransceivers(ch, label, sw.Optics)
	}
	if fresh(collector.CmdPhy) {
		collectPhy(ch, label, sw.Phy)
	}
}

// collectEAPIStats emits arex's own request counters for one switch.
//
// These describe the exporter rather than the switch, so they are emitted
// regardless of scrape health. The attempt label is what makes retry
// amplification visible.
func (c *Collector) collectEAPIStats(ch chan<- prometheus.Metric, label string, snap eapi.StatsSnapshot) {
	for k, v := range snap.Requests {
		set(ch, "arista_eapi_requests_total", float64(v), label, string(k.Outcome), string(k.Attempt))
	}
	set(ch, "arista_eapi_response_bytes_total", float64(snap.ResponseBytes), label)
	set(ch, "arista_eapi_request_duration_seconds_total", snap.DurationSeconds, label)
}

func collectVersion(ch chan<- prometheus.Metric, label string, v eapi.ShowVersion) {
	set(ch, "arista_info", 1, label, v.ModelName, v.SerialNumber, v.Version, v.SystemMacAddress, v.Architecture)
	setTime(ch, "arista_boot_timestamp_seconds", v.BootupTimestamp, label)

	// kilobytes -> bytes. show version's memFree tracks MemAvailable, not
	// MemFree: it agrees with top's free+buffcache to within 0.05% and sits
	// ~5.3 GiB above top's strict memFree.
	set(ch, "arista_memory_total_bytes", float64(v.MemTotal)*1024, label)
	set(ch, "arista_memory_available_bytes", float64(v.MemFree)*1024, label)
}

func collectCPUMemory(ch chan<- prometheus.Metric, label string, p eapi.ShowProcessesTop) {
	cpu := p.CpuInfo.Cpu
	set(ch, "arista_cpu_user_percent", cpu.User, label)
	set(ch, "arista_cpu_system_percent", cpu.System, label)
	set(ch, "arista_cpu_nice_percent", cpu.Nice, label)
	set(ch, "arista_cpu_idle_percent", cpu.Idle, label)
	set(ch, "arista_cpu_iowait_percent", cpu.IoWait, label)
	set(ch, "arista_cpu_steal_percent", cpu.Stolen, label)

	if len(p.TimeInfo.LoadAvg) == 3 {
		set(ch, "arista_load_avg_1m", p.TimeInfo.LoadAvg[0], label)
		set(ch, "arista_load_avg_5m", p.TimeInfo.LoadAvg[1], label)
		set(ch, "arista_load_avg_15m", p.TimeInfo.LoadAvg[2], label)
	}

	// Strict free, plus used and cache, from the same sample so they are
	// mutually consistent. Alert on arista_memory_available_bytes instead.
	if m := p.MemInfo.Physical; m.Total > 0 {
		set(ch, "arista_memory_free_bytes", float64(m.Free)*1024, label)
		set(ch, "arista_memory_used_bytes", float64(m.Used)*1024, label)
		set(ch, "arista_memory_buffer_bytes", float64(m.Buffer)*1024, label)
	}
}

// collectTemperature emits system and PSU sensors.
//
// PSU sensors come from this command rather than from environment power: only
// here do they carry thresholds, historical maximum, alert state and a
// human-readable description.
func collectTemperature(ch chan<- prometheus.Metric, label string, env eapi.ShowEnvironmentTemp) {
	setBool(ch, "arista_temperature_system_ok", env.SystemStatus == "temperatureOk", label)
	setBool(ch, "arista_temperature_shutdown_on_overheat", env.ShutdownOnOverheat, label)

	emit := func(prefix string, s eapi.TempSensor, lv ...string) {
		lv = append(lv, s.Name, s.Description, s.Position)
		set(ch, prefix+"_celsius", s.CurrentTemperature, lv...)
		set(ch, prefix+"_max_celsius", s.MaxTemperature, lv...)
		set(ch, prefix+"_overheat_threshold_celsius", s.OverheatThreshold, lv...)
		set(ch, prefix+"_critical_threshold_celsius", s.CriticalThreshold, lv...)
		setBool(ch, prefix+"_alert", s.InAlertState, lv...)
		setBool(ch, prefix+"_sensor_ok", s.HwStatus == "ok", lv...)
	}

	for _, s := range env.TempSensors {
		emit("arista_temperature", s, label, "system")
	}
	for _, slot := range env.PowerSupplySlots {
		for _, s := range slot.TempSensors {
			emit("arista_psu_temperature", s, label, slot.RelPos)
		}
	}
}

func collectPower(ch chan<- prometheus.Metric, label string, env eapi.ShowEnvironmentPower) {
	for slot, psu := range env.PowerSupplies {
		set(ch, "arista_psu_info", 1, label, slot, psu.ModelName)
		setBool(ch, "arista_psu_ok", psu.State == "ok", label, slot)
		set(ch, "arista_psu_capacity_watts", psu.Capacity, label, slot)
		set(ch, "arista_psu_output_power_watts", psu.OutputPower, label, slot)
		set(ch, "arista_psu_input_voltage_volts", psu.InputVoltage, label, slot)
		set(ch, "arista_psu_output_voltage_volts", psu.OutputVoltage, label, slot)
		set(ch, "arista_psu_input_current_amps", psu.InputCurrent, label, slot)
		set(ch, "arista_psu_output_current_amps", psu.OutputCurrent, label, slot)
		setTime(ch, "arista_psu_boot_timestamp_seconds", psu.Uptime, label, slot)
	}
}

func collectCooling(ch chan<- prometheus.Metric, label string, env eapi.ShowEnvironmentCooling) {
	setBool(ch, "arista_cooling_ok", env.SystemStatus == "coolingOk", label)
	setBool(ch, "arista_cooling_fans_ok", env.FansStatus == "fanAlarmOk", label)
	setBool(ch, "arista_cooling_shutdown_on_insufficient_fans", env.ShutdownOnInsufficientFans, label)
	set(ch, "arista_cooling_ambient_temperature_celsius", env.AmbientTemperature, label)
	set(ch, "arista_cooling_info", 1, label, env.AirflowDirection, env.CoolingMode)

	emitFan := func(f eapi.Fan, tray, location string) {
		set(ch, "arista_fan_info", 1, label, tray, f.Label, location, f.VendorModel)
		setBool(ch, "arista_fan_ok", f.Status == "ok", label, tray, f.Label, location)
		set(ch, "arista_fan_speed_configured_percent", float64(f.ConfiguredSpeed), label, tray, f.Label, location)
		set(ch, "arista_fan_speed_actual_percent", float64(f.ActualSpeed), label, tray, f.Label, location)
		set(ch, "arista_fan_speed_max_rpm", float64(f.MaxSpeed), label, tray, f.Label, location)
		setBool(ch, "arista_fan_speed_stable", f.SpeedStable, label, tray, f.Label, location)
		setTime(ch, "arista_fan_boot_timestamp_seconds", f.Uptime, label, tray, f.Label, location)
	}
	for _, tray := range env.FanTraySlots {
		for _, f := range tray.Fans {
			emitFan(f, tray.Label, "fantray")
		}
	}
	for _, psu := range env.PowerSupplySlots {
		for _, f := range psu.Fans {
			emitFan(f, psu.Label, "psu")
		}
	}
}

func collectInterfaces(ch chan<- prometheus.Metric, label string, ifaces eapi.ShowInterfaces) {
	for name, iface := range ifaces.Interfaces {
		c := iface.InterfaceCounters

		// Description and membership live on an info metric: editing a port
		// description must not change the identity of its counter series.
		set(ch, "arista_interface_info", 1, label, name,
			iface.Description, iface.InterfaceMembership, strconv.Itoa(iface.MTU))

		setBool(ch, "arista_interface_link_up", iface.LineProtocolStatus == "up", label, name)
		if iface.Bandwidth > 0 {
			set(ch, "arista_interface_speed_bits_per_second", float64(iface.Bandwidth), label, name)
		}

		set(ch, "arista_interface_in_octets_total", float64(c.InOctets), label, name)
		set(ch, "arista_interface_out_octets_total", float64(c.OutOctets), label, name)
		set(ch, "arista_interface_in_errors_total", float64(c.InputErrors), label, name)
		set(ch, "arista_interface_out_errors_total", float64(c.OutputErrors), label, name)
		set(ch, "arista_interface_in_discards_total", float64(c.InDiscards), label, name)
		set(ch, "arista_interface_out_discards_total", float64(c.OutDiscards), label, name)
		set(ch, "arista_interface_link_status_changes_total", float64(c.LinkStatusChanges), label, name)

		for _, p := range []struct {
			cast    string
			in, out uint64
		}{
			{"broadcast", c.InBroadcastPkts, c.OutBroadcastPkts},
			{"multicast", c.InMulticastPkts, c.OutMulticastPkts},
			{"unicast", c.InUcastPkts, c.OutUcastPkts},
		} {
			set(ch, "arista_interface_in_packets_total", float64(p.in), label, name, p.cast)
			set(ch, "arista_interface_out_packets_total", float64(p.out), label, name, p.cast)
		}

		d := c.InputErrorsDetail
		for _, e := range []struct {
			cause string
			v     uint64
		}{
			{"alignmentErrors", d.AlignmentErrors}, {"fcsErrors", d.FcsErrors},
			{"giantFrames", d.GiantFrames}, {"runtFrames", d.RuntFrames},
			{"rxPause", d.RxPause}, {"symbolErrors", d.SymbolErrors},
		} {
			set(ch, "arista_interface_in_errors_detail_total", float64(e.v), label, name, e.cause)
		}
		o := c.OutputErrorsDetail
		for _, e := range []struct {
			cause string
			v     uint64
		}{
			{"collisions", o.Collisions}, {"deferredTransmissions", o.DeferredTransmissions},
			{"lateCollisions", o.LateCollisions}, {"txPause", o.TxPause},
		} {
			set(ch, "arista_interface_out_errors_detail_total", float64(e.v), label, name, e.cause)
		}

		setTime(ch, "arista_interface_last_counter_clear_timestamp_seconds", c.LastClear, label, name)
		setTime(ch, "arista_interface_counter_refresh_timestamp_seconds", c.CounterRefreshTime, label, name)
		setTime(ch, "arista_interface_last_status_change_timestamp_seconds", iface.LastStatusChangeTimestamp, label, name)
	}
}

func collectBGP(ch chan<- prometheus.Metric, label string, bgp eapi.ShowBGPSummary) {
	for vrf, v := range bgp.Vrfs {
		for peer, p := range v.Peers {
			set(ch, "arista_bgp_peer_info", 1, label, vrf, peer, p.Asn, p.Description)
			setBool(ch, "arista_bgp_peer_up", p.PeerState == "Established", label, vrf, peer, p.Asn)
			setBool(ch, "arista_bgp_peer_under_maintenance", p.UnderMaintenance, label, vrf, peer, p.Asn)
			set(ch, "arista_bgp_peer_prefixes_received", float64(p.PrefixReceived), label, vrf, peer, p.Asn)
			set(ch, "arista_bgp_peer_prefixes_accepted", float64(p.PrefixAccepted), label, vrf, peer, p.Asn)
			set(ch, "arista_bgp_peer_prefixes_advertised", float64(p.PrefixAdvertised), label, vrf, peer, p.Asn)

			// upDownTime is the last transition, up or down -- a timestamp, not
			// a duration. Emitted for down peers too: knowing when a session
			// dropped is the more useful case.
			setTime(ch, "arista_bgp_peer_state_change_timestamp_seconds", p.UpDownTime, label, vrf, peer, p.Asn)
		}
	}
}

// domParams maps an EOS threshold key to its metric prefix and unit.
var domParams = []struct {
	key, prefix, unit string
	reading           func(eapi.Transceiver) float64
}{
	{"temperature", "arista_transceiver_temperature", "celsius",
		func(x eapi.Transceiver) float64 { return x.Temperature }},
	{"voltage", "arista_transceiver_voltage", "volts",
		func(x eapi.Transceiver) float64 { return x.Voltage }},
	{"txBias", "arista_transceiver_tx_bias", "milliamps",
		func(x eapi.Transceiver) float64 { return x.TxBias }},
	{"txPower", "arista_transceiver_tx_power", "dbm",
		func(x eapi.Transceiver) float64 { return x.TxPower }},
	{"rxPower", "arista_transceiver_rx_power", "dbm",
		func(x eapi.Transceiver) float64 { return x.RxPower }},
}

// collectTransceivers emits DOM readings and the optic's own limits.
//
// totalRxPower is deliberately not emitted: EOS reports it with the
// *Overridden flags but no limit values at all.
func collectTransceivers(ch chan<- prometheus.Metric, label string, t eapi.ShowTransceiverDetail) {
	for name, x := range t.Interfaces {
		set(ch, "arista_transceiver_info", 1, label, name,
			x.Slot, x.Channel, x.MediaType, string(x.VendorSn))
		setTime(ch, "arista_transceiver_update_timestamp_seconds", x.UpdateTime, label, name)

		for _, p := range domParams {
			set(ch, p.prefix+"_"+p.unit, p.reading(x), label, name)

			th, ok := x.Details[p.key]
			if !ok {
				continue
			}
			// A nil limit means EOS reported none. Emitting 0 instead would
			// hand alert rules a fabricated threshold.
			for _, t := range []struct {
				level string
				v     *float64
			}{
				{"high_alarm", th.HighAlarm}, {"high_warn", th.HighWarn},
				{"low_alarm", th.LowAlarm}, {"low_warn", th.LowWarn},
			} {
				if t.v == nil {
					continue
				}
				set(ch, p.prefix+"_threshold_"+p.unit, *t.v, label, name, t.level)
			}
		}
	}
}

// collectPhy emits the PHY subset: MAC faults, PCS, FEC and PMA.
//
// Serdes is excluded: half the payload, eye values are meaningless on a down
// link, and every field drifts between polls.
func collectPhy(ch chan<- prometheus.Metric, label string, phy eapi.ShowPhyDetail) {
	for name, iface := range phy.Interfaces {
		setBool(ch, "arista_phy_interface_up", iface.InterfaceState.Current == "up", label, name)
		set(ch, "arista_phy_interface_changes_total", float64(iface.InterfaceState.Changes), label, name)

		boolAttr(ch, "arista_phy_mac_local_fault", iface.MacFaults.LocalFault, label, name)
		boolAttr(ch, "arista_phy_mac_remote_fault", iface.MacFaults.RemoteFault, label, name)

		for _, p := range iface.PhyStatuses {
			// Multiple PHYs per interface (line side, system side).
			phyName := p.Description.Location

			set(ch, "arista_phy_info", 1, label, name, phyName,
				p.Chip.ModelName, p.Chip.FirmwareRev, p.OperSpeed)
			set(ch, "arista_phy_interrupt_count", float64(p.InterruptCount), label, name, phyName)

			setBool(ch, "arista_phy_link_up", p.PhyState.Value == "linkUp", label, name, phyName)
			set(ch, "arista_phy_link_changes_total", float64(p.PhyState.Changes), label, name, phyName)

			setBool(ch, "arista_phy_pcs_link_up", p.PCS.LinkStatus.Value == "up", label, name, phyName)
			set(ch, "arista_phy_pcs_link_changes_total", float64(p.PCS.LinkStatus.Changes), label, name, phyName)
			boolAttr(ch, "arista_phy_pcs_high_ber", p.PCS.HighBer, label, name, phyName)
			countAttr(ch, "arista_phy_pcs_last_high_ber_count", p.PCS.LastHighBerCount, label, name, phyName)
			countAttr(ch, "arista_phy_pcs_last_errored_block_count", p.PCS.LastErroredBlockCount, label, name, phyName)

			// Present only on links without FEC; alignment lock replaces it.
			if p.PCS.BlockLock != nil {
				boolAttr(ch, "arista_phy_pcs_block_lock", *p.PCS.BlockLock, label, name, phyName)
			}

			setBool(ch, "arista_phy_pma_link_up", p.PMA.LinkStatus.Value == "up", label, name, phyName)
			set(ch, "arista_phy_pma_link_changes_total", float64(p.PMA.LinkStatus.Changes), label, name, phyName)
			boolAttr(ch, "arista_phy_pma_signal_detect", p.PMA.SignalDetect, label, name, phyName)

			if p.FEC == nil {
				continue
			}
			// FEC encoding and codeword size are configuration, so they go on
			// an info metric: on the value series they would change series
			// identity whenever a link's FEC config changes.
			set(ch, "arista_phy_fec_info", 1, label, name, phyName,
				p.FEC.Encoding, p.FEC.CodewordSize)

			boolAttr(ch, "arista_phy_fec_alignment_lock", p.FEC.AlignmentLock, label, name, phyName)
			countAttr(ch, "arista_phy_fec_corrected_codewords", p.FEC.CorrectedCodewords, label, name, phyName)
			countAttr(ch, "arista_phy_fec_uncorrected_codewords", p.FEC.UncorrectedCodewords, label, name, phyName)

			// Per-lane, and only reported at native speeds.
			for lane, sym := range p.FEC.CorrectedSymbols {
				countAttr(ch, "arista_phy_fec_corrected_symbols", sym, label, name, phyName, lane)
			}
		}
	}
}

// boolAttr emits a PHY boolean plus its transition counter.
func boolAttr(ch chan<- prometheus.Metric, name string, a eapi.PhyBoolAttr, lv ...string) {
	setBool(ch, name, a.Value, lv...)
	set(ch, name+"_changes_total", float64(a.Changes), lv...)
}

// countAttr emits a PHY numeric attribute as three series.
//
// Value is a gauge: its semantics are ambiguous (uncorrectedCodewords read 3
// with 13 changes, and 409686 with 2) and the underlying counters are
// clearable, so rate() over it could be nonsense. Changes only rises, so it
// is the rate signal, and LastChange distinguishes "broken now" from "was
// broken, since repaired".
func countAttr(ch chan<- prometheus.Metric, name string, a eapi.PhyCounterAttr, lv ...string) {
	set(ch, name, a.Value, lv...)
	set(ch, name+"_changes_total", float64(a.Changes), lv...)
	setTime(ch, name+"_last_change_timestamp_seconds", a.LastChange, lv...)
}
