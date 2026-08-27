package metrics

import (
	"fmt"
	"io"
)

type metricDef struct {
	name, typ, help string
}

// boolAttr expands a PHY boolean attribute into its two series.
func boolAttr(name, help string) []metricDef {
	return []metricDef{
		{name, "gauge", help},
		{name + "_changes_total", "counter", "Transitions of " + name + " since boot"},
	}
}

// counterAttr expands a PHY numeric attribute into its three series.
func counterAttr(name, help string) []metricDef {
	return []metricDef{
		{name, "gauge", help + " (gauge: EOS semantics are ambiguous and the counter is clearable)"},
		{name + "_changes_total", "counter", "Times " + name + " changed since boot; the reliable rate signal"},
		{name + "_last_change_timestamp_seconds", "gauge", "Unix timestamp " + name + " last changed"},
	}
}

// sensorSet expands a temperature sensor family.
func sensorSet(prefix, subject string) []metricDef {
	return []metricDef{
		{prefix + "_celsius", "gauge", "Current " + subject + " temperature in Celsius"},
		{prefix + "_max_celsius", "gauge", "Historical maximum " + subject + " temperature"},
		{prefix + "_overheat_threshold_celsius", "gauge", "Overheat threshold reported by the " + subject + " sensor"},
		{prefix + "_critical_threshold_celsius", "gauge", "Critical threshold reported by the " + subject + " sensor"},
		{prefix + "_alert", "gauge", "1 if the " + subject + " sensor is in alert state"},
		{prefix + "_sensor_ok", "gauge", "1 if the " + subject + " sensor hardware is ok"},
	}
}

// domSet expands a transceiver parameter into a reading plus its thresholds.
func domSet(prefix, unit, subject string) []metricDef {
	return []metricDef{
		{prefix + "_" + unit, "gauge", "Transceiver " + subject},
		{prefix + "_threshold_" + unit, "gauge",
			"Threshold for transceiver " + subject + " as reported by the optic; level is high_alarm, high_warn, low_alarm or low_warn"},
	}
}

func flatten(groups ...[]metricDef) []metricDef {
	var out []metricDef
	for _, g := range groups {
		out = append(out, g...)
	}
	return out
}

var defs = flatten(
	[]metricDef{
		{"arista_scrape_success", "gauge", "1 if the last poll of this switch succeeded"},
		{"arista_scrape_age_seconds", "gauge", "Seconds since the last successful poll (-1 if never)"},
		{"arista_command_success", "gauge", "1 if this eAPI command succeeded in the last poll"},
		{"arista_eapi_requests_total", "counter", "eAPI requests arex has made, by outcome and by whether it was the normal batch or a per-command retry"},
		{"arista_eapi_response_bytes_total", "counter", "Total eAPI response bytes received from this switch"},
		{"arista_eapi_request_duration_seconds_total", "counter", "Total time spent on eAPI requests to this switch"},

		{"arista_info", "gauge", "Switch identity labels. Always 1"},
		{"arista_boot_timestamp_seconds", "gauge", "Unix timestamp of last boot"},

		{"arista_memory_total_bytes", "gauge", "Total physical memory"},
		{"arista_memory_available_bytes", "gauge", "Memory available for allocation, including reclaimable cache. Alert on this, not on free"},
		{"arista_memory_free_bytes", "gauge", "Strictly unused memory. Normally low: Linux spends idle RAM on cache"},
		{"arista_memory_used_bytes", "gauge", "Memory in use"},
		{"arista_memory_buffer_bytes", "gauge", "Buffer and cache memory as reported by EOS; does not reconcile exactly with total/used/free"},

		{"arista_cpu_user_percent", "gauge", "CPU time in user space"},
		{"arista_cpu_system_percent", "gauge", "CPU time in kernel space"},
		{"arista_cpu_nice_percent", "gauge", "CPU time on niced processes"},
		{"arista_cpu_idle_percent", "gauge", "CPU idle percentage"},
		{"arista_cpu_iowait_percent", "gauge", "CPU time waiting on I/O"},
		{"arista_cpu_steal_percent", "gauge", "CPU time stolen by the hypervisor (vEOS)"},
		{"arista_load_avg_1m", "gauge", "1-minute load average"},
		{"arista_load_avg_5m", "gauge", "5-minute load average"},
		{"arista_load_avg_15m", "gauge", "15-minute load average"},

		{"arista_temperature_system_ok", "gauge", "1 if overall temperature status is ok"},
		{"arista_temperature_shutdown_on_overheat", "gauge", "1 if the switch is configured to shut down on overheat"},

		{"arista_psu_info", "gauge", "PSU model labels. Always 1"},
		{"arista_psu_ok", "gauge", "1 if PSU state is ok"},
		{"arista_psu_capacity_watts", "gauge", "PSU rated capacity"},
		{"arista_psu_output_power_watts", "gauge", "PSU current output power"},
		{"arista_psu_input_voltage_volts", "gauge", "PSU input voltage"},
		{"arista_psu_output_voltage_volts", "gauge", "PSU output voltage"},
		{"arista_psu_input_current_amps", "gauge", "PSU input current"},
		{"arista_psu_output_current_amps", "gauge", "PSU output current"},
		{"arista_psu_boot_timestamp_seconds", "gauge", "Unix timestamp when the PSU came online"},

		{"arista_cooling_ok", "gauge", "1 if overall cooling status is ok"},
		{"arista_cooling_fans_ok", "gauge", "1 if the fan alarm status is ok"},
		{"arista_cooling_shutdown_on_insufficient_fans", "gauge", "1 if the switch is configured to shut down on insufficient fans"},
		{"arista_cooling_ambient_temperature_celsius", "gauge", "Ambient temperature"},
		{"arista_cooling_info", "gauge", "Cooling configuration labels. Always 1"},
		{"arista_fan_info", "gauge", "Fan vendor model labels. Always 1"},
		{"arista_fan_ok", "gauge", "1 if fan status is ok"},
		{"arista_fan_speed_configured_percent", "gauge", "Configured fan speed as a percentage"},
		{"arista_fan_speed_actual_percent", "gauge", "Actual fan speed as a percentage"},
		{"arista_fan_speed_max_rpm", "gauge", "Fan maximum speed in RPM"},
		{"arista_fan_speed_stable", "gauge", "1 if fan speed has stabilised"},
		{"arista_fan_boot_timestamp_seconds", "gauge", "Unix timestamp when the fan came online"},

		{"arista_interface_info", "gauge", "Interface description, port-channel membership and MTU. Always 1"},
		{"arista_interface_link_up", "gauge", "1 if the interface line protocol is up"},
		{"arista_interface_speed_bits_per_second", "gauge", "Negotiated interface speed"},
		{"arista_interface_in_octets_total", "counter", "Inbound octets"},
		{"arista_interface_out_octets_total", "counter", "Outbound octets"},
		{"arista_interface_in_errors_total", "counter", "Inbound errors"},
		{"arista_interface_out_errors_total", "counter", "Outbound errors"},
		{"arista_interface_in_discards_total", "counter", "Inbound discards"},
		{"arista_interface_out_discards_total", "counter", "Outbound discards"},
		{"arista_interface_link_status_changes_total", "counter", "Link state transitions since counters were cleared"},
		{"arista_interface_in_packets_total", "counter", "Inbound packets by cast"},
		{"arista_interface_out_packets_total", "counter", "Outbound packets by cast"},
		{"arista_interface_in_errors_detail_total", "counter", "Inbound errors broken down by cause"},
		{"arista_interface_out_errors_detail_total", "counter", "Outbound errors broken down by cause"},
		{"arista_interface_last_counter_clear_timestamp_seconds", "gauge", "Unix timestamp counters were last cleared"},
		{"arista_interface_counter_refresh_timestamp_seconds", "gauge", "Unix timestamp counters were last refreshed by EOS"},
		{"arista_interface_last_status_change_timestamp_seconds", "gauge", "Unix timestamp the interface last changed state"},

		{"arista_bgp_peer_info", "gauge", "BGP peer description labels. Always 1"},
		{"arista_bgp_peer_up", "gauge", "1 if the BGP peer is Established"},
		{"arista_bgp_peer_under_maintenance", "gauge", "1 if the BGP peer is under maintenance"},
		{"arista_bgp_peer_prefixes_received", "gauge", "Prefixes received from the peer"},
		{"arista_bgp_peer_prefixes_accepted", "gauge", "Prefixes accepted after policy; below received means prefixes were rejected"},
		{"arista_bgp_peer_prefixes_advertised", "gauge", "Prefixes advertised to the peer"},
		{"arista_bgp_peer_state_change_timestamp_seconds", "gauge", "Unix timestamp the session last changed state, up or down"},

		{"arista_transceiver_info", "gauge", "Transceiver slot, lane, media type and serial. Always 1"},
		{"arista_transceiver_update_timestamp_seconds", "gauge", "Unix timestamp EOS last refreshed this optic's DOM data"},

		{"arista_phy_interface_up", "gauge", "1 if the PHY reports the interface up"},
		{"arista_phy_interface_changes_total", "counter", "Interface state transitions seen by the PHY"},
		{"arista_phy_info", "gauge", "PHY chip, firmware and operating speed. Always 1"},
		{"arista_phy_fec_info", "gauge", "FEC encoding and codeword size. Always 1"},
		{"arista_phy_interrupt_count", "gauge", "PHY interrupt count"},
		{"arista_phy_link_up", "gauge", "1 if the PHY state is linkUp"},
		{"arista_phy_link_changes_total", "counter", "PHY state transitions since boot"},
		{"arista_phy_pcs_link_up", "gauge", "1 if the PCS link is up"},
		{"arista_phy_pcs_link_changes_total", "counter", "PCS link transitions since boot"},
		{"arista_phy_pma_link_up", "gauge", "1 if the PMA link is up"},
		{"arista_phy_pma_link_changes_total", "counter", "PMA link transitions since boot"},
	},

	sensorSet("arista_temperature", "system"),
	sensorSet("arista_psu_temperature", "PSU"),

	domSet("arista_transceiver_temperature", "celsius", "module temperature"),
	domSet("arista_transceiver_voltage", "volts", "supply voltage"),
	domSet("arista_transceiver_tx_bias", "milliamps", "laser bias current for this lane"),
	domSet("arista_transceiver_tx_power", "dbm", "transmit optical power for this lane"),
	domSet("arista_transceiver_rx_power", "dbm", "receive optical power for this lane"),

	boolAttr("arista_phy_mac_local_fault", "1 if the MAC reports a local fault"),
	boolAttr("arista_phy_mac_remote_fault", "1 if the MAC reports a remote fault"),
	boolAttr("arista_phy_pcs_high_ber", "1 if the PCS reports a high bit error rate"),
	boolAttr("arista_phy_pcs_block_lock", "1 if PCS block lock is achieved; absent on links running FEC"),
	boolAttr("arista_phy_pma_signal_detect", "1 if the PMA detects a signal"),
	boolAttr("arista_phy_fec_alignment_lock", "1 if FEC alignment lock is achieved; absent on links without FEC"),

	counterAttr("arista_phy_pcs_last_high_ber_count", "PCS high-BER count as last observed"),
	counterAttr("arista_phy_pcs_last_errored_block_count", "PCS errored block count as last observed"),
	counterAttr("arista_phy_fec_corrected_codewords", "FEC codewords corrected"),
	counterAttr("arista_phy_fec_uncorrected_codewords", "FEC codewords that could not be corrected; frames were lost"),
	counterAttr("arista_phy_fec_corrected_symbols", "FEC symbols corrected on this lane"),
)

// writeHelp emits HELP and TYPE for every metric arex can produce.
func writeHelp(w io.Writer) {
	for _, d := range defs {
		_, _ = fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", d.name, d.help, d.name, d.typ)
	}
}
