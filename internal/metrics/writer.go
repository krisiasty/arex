// Package metrics renders collected switch data in the Prometheus text
// exposition format (https://prometheus.io/docs/instrumenting/exposition_formats/).
package metrics

import (
	"fmt"
	"io"
	"maps"
	"slices"
	"time"

	"github.com/krisiasty/arex/internal/collector"
	"github.com/krisiasty/arex/internal/eapi"
)

// Write renders metrics for every switch in the store.
func Write(w io.Writer, store *collector.Store, stalenessLimit time.Duration) {
	writeHelp(w)
	now := time.Now()
	for _, sw := range store.All() {
		writeSwitch(w, sw, now, stalenessLimit)
	}
}

// gauge emits one sample.
func gauge(w io.Writer, name, lbls string, v float64) {
	_, _ = fmt.Fprintf(w, "%s{%s} %g\n", name, lbls, v)
}

// counter emits one integer sample.
func counter(w io.Writer, name, lbls string, v uint64) {
	_, _ = fmt.Fprintf(w, "%s{%s} %d\n", name, lbls, v)
}

// boolGauge emits 1 or 0.
func boolGauge(w io.Writer, name, lbls string, b bool) {
	gauge(w, name, lbls, boolToFloat(b))
}

// timestamp emits an epoch, skipping the zero value EOS uses for "never".
func timestamp(w io.Writer, name, lbls string, epoch float64) {
	if epoch <= 0 {
		return
	}
	gauge(w, name, lbls, epoch)
}

// writeSwitch renders metrics for a single switch.
//
// Scrape health is always emitted. Beyond that, last-known-good data is
// served even when the most recent poll failed: riding out a transient
// eAPI failure is the main advantage of polling out-of-band, and dropping
// every series on one refused connection throws it away.
func writeSwitch(w io.Writer, sw *collector.SwitchData, now time.Time, stalenessLimit time.Duration) {
	sw.RLock()
	defer sw.RUnlock()

	sl := labels("switch", sw.Label)

	// Never collected is not a success, even before the first error is
	// recorded: on startup a poll can be in flight for a full timeout.
	collected := !sw.LastSuccess.IsZero()
	boolGauge(w, "arista_scrape_success", sl, collected && sw.ScrapeErr == nil)

	// Per-command state is scrape metadata, not switch data, so it is
	// emitted even when nothing was ever collected. A missing series is not
	// zero in Prometheus: omitting these would drop the most broken switch
	// out of any aggregate over arista_command_success.
	if sw.CommandErrors != nil {
		for _, cli := range collector.Commands() {
			_, failed := sw.CommandErrors[cli]
			boolGauge(w, "arista_command_success", join(sl, labels("command", cli)), !failed)
		}
	}

	if !collected {
		gauge(w, "arista_scrape_age_seconds", sl, -1)
		return // never collected: there is no switch data to serve
	}

	age := now.Sub(sw.LastSuccess)
	gauge(w, "arista_scrape_age_seconds", sl, age.Seconds())
	if age > stalenessLimit {
		return // too old to be worth publishing
	}

	writeVersion(w, sl, sw.Version)
	writeCPUMemory(w, sl, sw.ProcessTop)
	writeTemperature(w, sl, sw.EnvTemp)
	writePower(w, sl, sw.EnvPower)
	writeCooling(w, sl, sw.EnvCooling)
	writeInterfaces(w, sl, sw.Interfaces)
	writeBGP(w, sl, sw.BGPSummary)
	writeTransceivers(w, sl, sw.Optics)
	writePhy(w, sl, sw.Phy)
}

// writeVersion emits identity, boot time and available memory.
func writeVersion(w io.Writer, sl string, v eapi.ShowVersion) {
	gauge(w, "arista_info", join(sl, labels(
		"model", v.ModelName,
		"serial", v.SerialNumber,
		"version", v.Version,
		"mac", v.SystemMacAddress,
		"arch", v.Architecture,
	)), 1)

	// Uptime is derivable in PromQL: time() - arista_boot_timestamp_seconds
	timestamp(w, "arista_boot_timestamp_seconds", sl, v.BootupTimestamp)

	// kilobytes -> bytes. show version's memFree tracks MemAvailable, not
	// MemFree: it agrees with top's free+buffcache to within 0.05% and sits
	// ~5.3 GiB above top's strict memFree. Named for what it measures.
	gauge(w, "arista_memory_total_bytes", sl, float64(v.MemTotal)*1024)
	gauge(w, "arista_memory_available_bytes", sl, float64(v.MemFree)*1024)
}

// writeCPUMemory emits CPU, load and the strict memory breakdown.
func writeCPUMemory(w io.Writer, sl string, p eapi.ShowProcessesTop) {
	cpu := p.CpuInfo.Cpu
	gauge(w, "arista_cpu_user_percent", sl, cpu.User)
	gauge(w, "arista_cpu_system_percent", sl, cpu.System)
	gauge(w, "arista_cpu_nice_percent", sl, cpu.Nice)
	gauge(w, "arista_cpu_idle_percent", sl, cpu.Idle)
	gauge(w, "arista_cpu_iowait_percent", sl, cpu.IoWait)
	gauge(w, "arista_cpu_steal_percent", sl, cpu.Stolen)

	if len(p.TimeInfo.LoadAvg) == 3 {
		gauge(w, "arista_load_avg_1m", sl, p.TimeInfo.LoadAvg[0])
		gauge(w, "arista_load_avg_5m", sl, p.TimeInfo.LoadAvg[1])
		gauge(w, "arista_load_avg_15m", sl, p.TimeInfo.LoadAvg[2])
	}

	// Strict free, plus used and cache, from the same sample so they are
	// mutually consistent. Alert on arista_memory_available_bytes instead:
	// Linux spends idle RAM on cache, so low free memory is normal.
	m := p.MemInfo.Physical
	if m.Total > 0 {
		gauge(w, "arista_memory_free_bytes", sl, float64(m.Free)*1024)
		gauge(w, "arista_memory_used_bytes", sl, float64(m.Used)*1024)
		gauge(w, "arista_memory_buffer_bytes", sl, float64(m.Buffer)*1024)
	}
}

// writeTemperature emits system and PSU sensors.
//
// PSU sensors come from this command rather than from environment power:
// only here do they carry thresholds, historical maximum, alert state and a
// human-readable description.
func writeTemperature(w io.Writer, sl string, env eapi.ShowEnvironmentTemp) {
	boolGauge(w, "arista_temperature_system_ok", sl, env.SystemStatus == "temperatureOk")
	boolGauge(w, "arista_temperature_shutdown_on_overheat", sl, env.ShutdownOnOverheat)

	emit := func(prefix, extra string, s eapi.TempSensor) {
		l := join(sl, extra, labels(
			"sensor", s.Name,
			"description", s.Description,
			"position", s.Position,
		))
		gauge(w, prefix+"_celsius", l, s.CurrentTemperature)
		gauge(w, prefix+"_max_celsius", l, s.MaxTemperature)
		gauge(w, prefix+"_overheat_threshold_celsius", l, s.OverheatThreshold)
		gauge(w, prefix+"_critical_threshold_celsius", l, s.CriticalThreshold)
		boolGauge(w, prefix+"_alert", l, s.InAlertState)
		boolGauge(w, prefix+"_sensor_ok", l, s.HwStatus == "ok")
	}

	for _, s := range env.TempSensors {
		emit("arista_temperature", labels("location", "system"), s)
	}
	for _, slot := range env.PowerSupplySlots {
		for _, s := range slot.TempSensors {
			emit("arista_psu_temperature", labels("psu", slot.RelPos), s)
		}
	}
}

// writePower emits PSU electrical metrics.
func writePower(w io.Writer, sl string, env eapi.ShowEnvironmentPower) {
	for _, slot := range slices.Sorted(maps.Keys(env.PowerSupplies)) {
		psu := env.PowerSupplies[slot]
		l := join(sl, labels("psu", slot))
		gauge(w, "arista_psu_info", join(l, labels("model", psu.ModelName)), 1)

		boolGauge(w, "arista_psu_ok", l, psu.State == "ok")
		gauge(w, "arista_psu_capacity_watts", l, psu.Capacity)
		gauge(w, "arista_psu_output_power_watts", l, psu.OutputPower)
		gauge(w, "arista_psu_input_voltage_volts", l, psu.InputVoltage)
		gauge(w, "arista_psu_output_voltage_volts", l, psu.OutputVoltage)
		gauge(w, "arista_psu_input_current_amps", l, psu.InputCurrent)
		gauge(w, "arista_psu_output_current_amps", l, psu.OutputCurrent)
		timestamp(w, "arista_psu_boot_timestamp_seconds", l, psu.Uptime)
	}
}

// writeCooling emits cooling status and per-fan metrics.
func writeCooling(w io.Writer, sl string, env eapi.ShowEnvironmentCooling) {
	boolGauge(w, "arista_cooling_ok", sl, env.SystemStatus == "coolingOk")
	boolGauge(w, "arista_cooling_fans_ok", sl, env.FansStatus == "fanAlarmOk")
	boolGauge(w, "arista_cooling_shutdown_on_insufficient_fans", sl, env.ShutdownOnInsufficientFans)
	gauge(w, "arista_cooling_ambient_temperature_celsius", sl, env.AmbientTemperature)
	gauge(w, "arista_cooling_info", join(sl, labels(
		"airflow", env.AirflowDirection,
		"mode", env.CoolingMode,
	)), 1)

	emitFan := func(f eapi.Fan, tray, location string) {
		l := join(sl, labels("tray", tray, "fan", f.Label, "location", location))
		gauge(w, "arista_fan_info", join(l, labels("vendor_model", f.VendorModel)), 1)
		boolGauge(w, "arista_fan_ok", l, f.Status == "ok")
		gauge(w, "arista_fan_speed_configured_percent", l, float64(f.ConfiguredSpeed))
		gauge(w, "arista_fan_speed_actual_percent", l, float64(f.ActualSpeed))
		gauge(w, "arista_fan_speed_max_rpm", l, float64(f.MaxSpeed))
		boolGauge(w, "arista_fan_speed_stable", l, f.SpeedStable)
		timestamp(w, "arista_fan_boot_timestamp_seconds", l, f.Uptime)
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

// writeInterfaces emits per-interface state and counters.
func writeInterfaces(w io.Writer, sl string, ifaces eapi.ShowInterfaces) {
	for _, name := range slices.Sorted(maps.Keys(ifaces.Interfaces)) {
		iface := ifaces.Interfaces[name]
		l := join(sl, labels("interface", name))
		c := iface.InterfaceCounters

		// Description and membership live on an info metric: editing a port
		// description must not change the identity of its counter series.
		gauge(w, "arista_interface_info", join(l, labels(
			"description", iface.Description,
			"membership", iface.InterfaceMembership,
			"mtu", itoa(iface.MTU),
		)), 1)

		boolGauge(w, "arista_interface_link_up", l, iface.LineProtocolStatus == "up")
		if iface.Bandwidth > 0 {
			gauge(w, "arista_interface_speed_bits_per_second", l, float64(iface.Bandwidth))
		}

		counter(w, "arista_interface_in_octets_total", l, c.InOctets)
		counter(w, "arista_interface_out_octets_total", l, c.OutOctets)
		counter(w, "arista_interface_in_errors_total", l, c.InputErrors)
		counter(w, "arista_interface_out_errors_total", l, c.OutputErrors)
		counter(w, "arista_interface_in_discards_total", l, c.InDiscards)
		counter(w, "arista_interface_out_discards_total", l, c.OutDiscards)
		counter(w, "arista_interface_link_status_changes_total", l, c.LinkStatusChanges)

		for _, p := range []struct {
			cast    string
			in, out uint64
		}{
			{"broadcast", c.InBroadcastPkts, c.OutBroadcastPkts},
			{"multicast", c.InMulticastPkts, c.OutMulticastPkts},
			{"unicast", c.InUcastPkts, c.OutUcastPkts},
		} {
			cl := join(l, labels("cast", p.cast))
			counter(w, "arista_interface_in_packets_total", cl, p.in)
			counter(w, "arista_interface_out_packets_total", cl, p.out)
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
			counter(w, "arista_interface_in_errors_detail_total", join(l, labels("cause", e.cause)), e.v)
		}
		o := c.OutputErrorsDetail
		for _, e := range []struct {
			cause string
			v     uint64
		}{
			{"collisions", o.Collisions}, {"deferredTransmissions", o.DeferredTransmissions},
			{"lateCollisions", o.LateCollisions}, {"txPause", o.TxPause},
		} {
			counter(w, "arista_interface_out_errors_detail_total", join(l, labels("cause", e.cause)), e.v)
		}

		timestamp(w, "arista_interface_last_counter_clear_timestamp_seconds", l, c.LastClear)
		timestamp(w, "arista_interface_counter_refresh_timestamp_seconds", l, c.CounterRefreshTime)
		timestamp(w, "arista_interface_last_status_change_timestamp_seconds", l, iface.LastStatusChangeTimestamp)
	}
}

// writeBGP emits per-peer state across every VRF.
func writeBGP(w io.Writer, sl string, bgp eapi.ShowBGPSummary) {
	for _, vrf := range slices.Sorted(maps.Keys(bgp.Vrfs)) {
		v := bgp.Vrfs[vrf]
		for _, peer := range slices.Sorted(maps.Keys(v.Peers)) {
			p := v.Peers[peer]
			l := join(sl, labels("vrf", vrf, "peer", peer, "asn", p.Asn))

			gauge(w, "arista_bgp_peer_info", join(l, labels("description", p.Description)), 1)
			boolGauge(w, "arista_bgp_peer_up", l, p.PeerState == "Established")
			boolGauge(w, "arista_bgp_peer_under_maintenance", l, p.UnderMaintenance)
			gauge(w, "arista_bgp_peer_prefixes_received", l, float64(p.PrefixReceived))
			gauge(w, "arista_bgp_peer_prefixes_accepted", l, float64(p.PrefixAccepted))
			gauge(w, "arista_bgp_peer_prefixes_advertised", l, float64(p.PrefixAdvertised))

			// upDownTime is the last transition, up or down -- a timestamp,
			// not a duration. Emitted for down peers too: knowing when a
			// session dropped is the more useful case.
			timestamp(w, "arista_bgp_peer_state_change_timestamp_seconds", l, p.UpDownTime)
		}
	}
}

// domParam maps an EOS threshold key to its metric prefix and unit suffix.
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

// writeTransceivers emits DOM readings and the optic's own limits.
//
// totalRxPower is deliberately not emitted: EOS reports it with the
// *Overridden flags but no limit values at all.
func writeTransceivers(w io.Writer, sl string, t eapi.ShowTransceiverDetail) {
	for _, name := range slices.Sorted(maps.Keys(t.Interfaces)) {
		x := t.Interfaces[name]
		l := join(sl, labels("interface", name))

		gauge(w, "arista_transceiver_info", join(l, labels(
			"slot", x.Slot,
			"channel", x.Channel,
			"media_type", x.MediaType,
			"vendor_sn", string(x.VendorSn),
		)), 1)
		timestamp(w, "arista_transceiver_update_timestamp_seconds", l, x.UpdateTime)

		for _, p := range domParams {
			gauge(w, p.prefix+"_"+p.unit, l, p.reading(x))

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
				gauge(w, p.prefix+"_threshold_"+p.unit, join(l, labels("level", t.level)), *t.v)
			}
		}
	}
}

// writePhy emits the PHY subset: MAC faults, PCS, FEC and PMA.
//
// Serdes is excluded: half the payload, eye values are meaningless on a
// down link, and every field drifts between polls.
func writePhy(w io.Writer, sl string, phy eapi.ShowPhyDetail) {
	for _, name := range slices.Sorted(maps.Keys(phy.Interfaces)) {
		iface := phy.Interfaces[name]
		il := join(sl, labels("interface", name))

		boolGauge(w, "arista_phy_interface_up", il, iface.InterfaceState.Current == "up")
		counter(w, "arista_phy_interface_changes_total", il, iface.InterfaceState.Changes)

		writeBoolAttr(w, "arista_phy_mac_local_fault", il, iface.MacFaults.LocalFault)
		writeBoolAttr(w, "arista_phy_mac_remote_fault", il, iface.MacFaults.RemoteFault)

		for _, p := range iface.PhyStatuses {
			// Multiple PHYs per interface (line side, system side).
			l := join(il, labels("phy", p.Description.Location))

			gauge(w, "arista_phy_info", join(l, labels(
				"chip", p.Chip.ModelName,
				"firmware", p.Chip.FirmwareRev,
				"oper_speed", p.OperSpeed,
			)), 1)
			gauge(w, "arista_phy_interrupt_count", l, float64(p.InterruptCount))

			boolGauge(w, "arista_phy_link_up", l, p.PhyState.Value == "linkUp")
			counter(w, "arista_phy_link_changes_total", l, p.PhyState.Changes)

			boolGauge(w, "arista_phy_pcs_link_up", l, p.PCS.LinkStatus.Value == "up")
			counter(w, "arista_phy_pcs_link_changes_total", l, p.PCS.LinkStatus.Changes)
			writeBoolAttr(w, "arista_phy_pcs_high_ber", l, p.PCS.HighBer)
			writeCounterAttr(w, "arista_phy_pcs_last_high_ber_count", l, p.PCS.LastHighBerCount)
			writeCounterAttr(w, "arista_phy_pcs_last_errored_block_count", l, p.PCS.LastErroredBlockCount)

			// Present only on links without FEC; alignment lock replaces it.
			if p.PCS.BlockLock != nil {
				writeBoolAttr(w, "arista_phy_pcs_block_lock", l, *p.PCS.BlockLock)
			}

			boolGauge(w, "arista_phy_pma_link_up", l, p.PMA.LinkStatus.Value == "up")
			counter(w, "arista_phy_pma_link_changes_total", l, p.PMA.LinkStatus.Changes)
			writeBoolAttr(w, "arista_phy_pma_signal_detect", l, p.PMA.SignalDetect)

			if p.FEC == nil {
				continue
			}
			// FEC encoding and codeword size are configuration, so they go
			// on an info metric. On the value series they would change
			// series identity whenever a link's FEC config changes -- a
			// speed change moving codeword size 528 to 544 -- breaking
			// rate() continuity, and they are redundant on nine series.
			gauge(w, "arista_phy_fec_info", join(l, labels(
				"encoding", p.FEC.Encoding,
				"codeword_size", p.FEC.CodewordSize,
			)), 1)

			writeBoolAttr(w, "arista_phy_fec_alignment_lock", l, p.FEC.AlignmentLock)
			writeCounterAttr(w, "arista_phy_fec_corrected_codewords", l, p.FEC.CorrectedCodewords)
			writeCounterAttr(w, "arista_phy_fec_uncorrected_codewords", l, p.FEC.UncorrectedCodewords)

			// Per-lane, and only reported at native speeds.
			for _, lane := range slices.Sorted(maps.Keys(p.FEC.CorrectedSymbols)) {
				sym := p.FEC.CorrectedSymbols[lane]
				writeCounterAttr(w, "arista_phy_fec_corrected_symbols",
					join(l, labels("lane", lane)), sym)
			}
		}
	}
}

// writeBoolAttr emits a PHY boolean plus its transition counter.
func writeBoolAttr(w io.Writer, name, lbls string, a eapi.PhyBoolAttr) {
	boolGauge(w, name, lbls, a.Value)
	counter(w, name+"_changes_total", lbls, a.Changes)
}

// writeCounterAttr emits a PHY numeric attribute as three series.
//
// Value is a gauge: its semantics are ambiguous (uncorrectedCodewords read 3
// with 13 changes) and the underlying counters are clearable, so rate() over
// it could be nonsense. Changes only rises, so it is the rate signal, and
// LastChange distinguishes "broken now" from "was broken, since repaired".
func writeCounterAttr(w io.Writer, name, lbls string, a eapi.PhyCounterAttr) {
	gauge(w, name, lbls, a.Value)
	counter(w, name+"_changes_total", lbls, a.Changes)
	timestamp(w, name+"_last_change_timestamp_seconds", lbls, a.LastChange)
}

func boolToFloat(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}
