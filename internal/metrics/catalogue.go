package metrics

import (
	"strings"

	"github.com/prometheus/client_golang/prometheus"
)

// Label sets, named so the catalogue below stays readable and so a family's
// labels are declared once rather than at every emission point.
var (
	lSwitch      = []string{"switch"}
	lCommand     = []string{"switch", "command"}
	lEAPI        = []string{"switch", "outcome", "attempt"}
	lInfo        = []string{"switch", "model", "serial", "version", "mac", "arch"}
	lSensor      = []string{"switch", "location", "sensor", "description", "position"}
	lPSUSensor   = []string{"switch", "psu", "sensor", "description", "position"}
	lPSU         = []string{"switch", "psu"}
	lPSUInfo     = []string{"switch", "psu", "model"}
	lCoolingInfo = []string{"switch", "airflow", "mode"}
	lFan         = []string{"switch", "tray", "fan", "location"}
	lFanInfo     = []string{"switch", "tray", "fan", "location", "vendor_model"}
	lIface       = []string{"switch", "interface"}
	lIfaceInfo   = []string{"switch", "interface", "description", "membership", "mtu"}
	lIfaceCast   = []string{"switch", "interface", "cast"}
	lIfaceCause  = []string{"switch", "interface", "cause"}
	lPeer        = []string{"switch", "vrf", "peer", "asn"}
	lPeerInfo    = []string{"switch", "vrf", "peer", "asn", "description"}
	lXcvrInfo    = []string{"switch", "interface", "slot", "channel", "media_type", "vendor_sn"}
	lXcvrThresh  = []string{"switch", "interface", "level"}
	lPhy         = []string{"switch", "interface", "phy"}
	lPhyInfo     = []string{"switch", "interface", "phy", "chip", "firmware", "oper_speed"}
	lFECInfo     = []string{"switch", "interface", "phy", "encoding", "codeword_size"}
	lFECLane     = []string{"switch", "interface", "phy", "lane"}
	lBuild       = []string{"version", "revision", "go_version", "modified"}
)

// metricDef is one metric family: its name, whether it counts or measures,
// what it means, and the labels it varies by.
//
// Holding the value type here rather than at the emission point means a
// family cannot be declared a counter and then emitted as a gauge.
type metricDef struct {
	name   string
	typ    string
	help   string
	labels []string
}

func (d metricDef) valueType() prometheus.ValueType {
	if d.typ == "counter" {
		return prometheus.CounterValue
	}
	return prometheus.GaugeValue
}

// metricDefs is every metric arex can produce.
var metricDefs = []metricDef{
	{"arex_build_info", "gauge", "Version, VCS revision and Go version of the running arex. Always 1", lBuild},
	{"arista_bgp_peer_info", "gauge", "BGP peer description labels. Always 1", lPeerInfo},
	{"arista_bgp_peer_prefixes_accepted", "gauge", "Prefixes accepted after policy; below received means prefixes were rejected", lPeer},
	{"arista_bgp_peer_prefixes_advertised", "gauge", "Prefixes advertised to the peer", lPeer},
	{"arista_bgp_peer_prefixes_received", "gauge", "Prefixes received from the peer", lPeer},
	{"arista_bgp_peer_state_change_timestamp_seconds", "gauge", "Unix timestamp the session last changed state, up or down", lPeer},
	{"arista_bgp_peer_under_maintenance", "gauge", "1 if the BGP peer is under maintenance", lPeer},
	{"arista_bgp_peer_up", "gauge", "1 if the BGP peer is Established", lPeer},
	{"arista_boot_timestamp_seconds", "gauge", "Unix timestamp of last boot", lSwitch},
	{"arista_command_success", "gauge", "1 if this eAPI command succeeded in the last poll", lCommand},
	{"arista_cooling_ambient_temperature_celsius", "gauge", "Ambient temperature", lSwitch},
	{"arista_cooling_fans_ok", "gauge", "1 if the fan alarm status is ok", lSwitch},
	{"arista_cooling_info", "gauge", "Cooling configuration labels. Always 1", lCoolingInfo},
	{"arista_cooling_ok", "gauge", "1 if overall cooling status is ok", lSwitch},
	{"arista_cooling_shutdown_on_insufficient_fans", "gauge", "1 if the switch is configured to shut down on insufficient fans", lSwitch},
	{"arista_cpu_idle_percent", "gauge", "CPU idle percentage", lSwitch},
	{"arista_cpu_iowait_percent", "gauge", "CPU time waiting on I/O", lSwitch},
	{"arista_cpu_nice_percent", "gauge", "CPU time on niced processes", lSwitch},
	{"arista_cpu_steal_percent", "gauge", "CPU time stolen by the hypervisor (vEOS)", lSwitch},
	{"arista_cpu_system_percent", "gauge", "CPU time in kernel space", lSwitch},
	{"arista_cpu_user_percent", "gauge", "CPU time in user space", lSwitch},
	{"arista_eapi_request_duration_seconds_total", "counter", "Total time spent on eAPI requests to this switch", lSwitch},
	{"arista_eapi_requests_total", "counter", "eAPI requests arex has made, by outcome and by whether it was the normal batch or a per-command retry", lEAPI},
	{"arista_eapi_response_bytes_total", "counter", "Total eAPI response bytes received from this switch", lSwitch},
	{"arista_fan_boot_timestamp_seconds", "gauge", "Unix timestamp when the fan came online", lFan},
	{"arista_fan_info", "gauge", "Fan vendor model labels. Always 1", lFanInfo},
	{"arista_fan_ok", "gauge", "1 if fan status is ok", lFan},
	{"arista_fan_speed_actual_percent", "gauge", "Actual fan speed as a percentage", lFan},
	{"arista_fan_speed_configured_percent", "gauge", "Configured fan speed as a percentage", lFan},
	{"arista_fan_speed_max_rpm", "gauge", "Fan maximum speed in RPM", lFan},
	{"arista_fan_speed_stable", "gauge", "1 if fan speed has stabilised", lFan},
	{"arista_info", "gauge", "Switch identity labels. Always 1", lInfo},
	{"arista_interface_counter_refresh_timestamp_seconds", "gauge", "Unix timestamp counters were last refreshed by EOS", lIface},
	{"arista_interface_in_discards_total", "counter", "Inbound discards", lIface},
	{"arista_interface_in_errors_detail_total", "counter", "Inbound errors broken down by cause", lIfaceCause},
	{"arista_interface_in_errors_total", "counter", "Inbound errors", lIface},
	{"arista_interface_in_octets_total", "counter", "Inbound octets", lIface},
	{"arista_interface_in_packets_total", "counter", "Inbound packets by cast", lIfaceCast},
	{"arista_interface_info", "gauge", "Interface description, port-channel membership and MTU. Always 1", lIfaceInfo},
	{"arista_interface_last_counter_clear_timestamp_seconds", "gauge", "Unix timestamp counters were last cleared", lIface},
	{"arista_interface_last_status_change_timestamp_seconds", "gauge", "Unix timestamp the interface last changed state", lIface},
	{"arista_interface_link_status_changes_total", "counter", "Link state transitions since counters were cleared", lIface},
	{"arista_interface_link_up", "gauge", "1 if the interface line protocol is up", lIface},
	{"arista_interface_out_discards_total", "counter", "Outbound discards", lIface},
	{"arista_interface_out_errors_detail_total", "counter", "Outbound errors broken down by cause", lIfaceCause},
	{"arista_interface_out_errors_total", "counter", "Outbound errors", lIface},
	{"arista_interface_out_octets_total", "counter", "Outbound octets", lIface},
	{"arista_interface_out_packets_total", "counter", "Outbound packets by cast", lIfaceCast},
	{"arista_interface_speed_bits_per_second", "gauge", "Negotiated interface speed", lIface},
	{"arista_load_avg_15m", "gauge", "15-minute load average", lSwitch},
	{"arista_load_avg_1m", "gauge", "1-minute load average", lSwitch},
	{"arista_load_avg_5m", "gauge", "5-minute load average", lSwitch},
	{"arista_memory_available_bytes", "gauge", "Memory available for allocation, including reclaimable cache. Alert on this, not on free", lSwitch},
	{"arista_memory_buffer_bytes", "gauge", "Buffer and cache memory as reported by EOS; does not reconcile exactly with total/used/free", lSwitch},
	{"arista_memory_free_bytes", "gauge", "Strictly unused memory. Normally low: Linux spends idle RAM on cache", lSwitch},
	{"arista_memory_total_bytes", "gauge", "Total physical memory", lSwitch},
	{"arista_memory_used_bytes", "gauge", "Memory in use", lSwitch},
	{"arista_phy_fec_alignment_lock", "gauge", "1 if FEC alignment lock is achieved; absent on links without FEC", lPhy},
	{"arista_phy_fec_alignment_lock_changes_total", "counter", "Transitions of arista_phy_fec_alignment_lock since boot", lPhy},
	{"arista_phy_fec_corrected_codewords", "gauge", "FEC codewords corrected (gauge: EOS semantics are ambiguous and the counter is clearable)", lPhy},
	{"arista_phy_fec_corrected_codewords_changes_total", "counter", "Times arista_phy_fec_corrected_codewords changed since boot; the reliable rate signal", lPhy},
	{"arista_phy_fec_corrected_codewords_last_change_timestamp_seconds", "gauge", "Unix timestamp arista_phy_fec_corrected_codewords last changed", lPhy},
	{"arista_phy_fec_corrected_symbols", "gauge", "FEC symbols corrected on this lane (gauge: EOS semantics are ambiguous and the counter is clearable)", lFECLane},
	{"arista_phy_fec_corrected_symbols_changes_total", "counter", "Times arista_phy_fec_corrected_symbols changed since boot; the reliable rate signal", lFECLane},
	{"arista_phy_fec_corrected_symbols_last_change_timestamp_seconds", "gauge", "Unix timestamp arista_phy_fec_corrected_symbols last changed", lFECLane},
	{"arista_phy_fec_info", "gauge", "FEC encoding and codeword size. Always 1", lFECInfo},
	{"arista_phy_fec_uncorrected_codewords", "gauge", "FEC codewords that could not be corrected; frames were lost (gauge: EOS semantics are ambiguous and the counter is clearable)", lPhy},
	{"arista_phy_fec_uncorrected_codewords_changes_total", "counter", "Times arista_phy_fec_uncorrected_codewords changed since boot; the reliable rate signal", lPhy},
	{"arista_phy_fec_uncorrected_codewords_last_change_timestamp_seconds", "gauge", "Unix timestamp arista_phy_fec_uncorrected_codewords last changed", lPhy},
	{"arista_phy_info", "gauge", "PHY chip, firmware and operating speed. Always 1", lPhyInfo},
	{"arista_phy_interface_changes_total", "counter", "Interface state transitions seen by the PHY", lIface},
	{"arista_phy_interface_up", "gauge", "1 if the PHY reports the interface up", lIface},
	{"arista_phy_interrupt_count", "gauge", "PHY interrupt count", lPhy},
	{"arista_phy_link_changes_total", "counter", "PHY state transitions since boot", lPhy},
	{"arista_phy_link_up", "gauge", "1 if the PHY state is linkUp", lPhy},
	{"arista_phy_mac_local_fault", "gauge", "1 if the MAC reports a local fault", lIface},
	{"arista_phy_mac_local_fault_changes_total", "counter", "Transitions of arista_phy_mac_local_fault since boot", lIface},
	{"arista_phy_mac_remote_fault", "gauge", "1 if the MAC reports a remote fault", lIface},
	{"arista_phy_mac_remote_fault_changes_total", "counter", "Transitions of arista_phy_mac_remote_fault since boot", lIface},
	{"arista_phy_pcs_block_lock", "gauge", "1 if PCS block lock is achieved; absent on links running FEC", lPhy},
	{"arista_phy_pcs_block_lock_changes_total", "counter", "Transitions of arista_phy_pcs_block_lock since boot", lPhy},
	{"arista_phy_pcs_high_ber", "gauge", "1 if the PCS reports a high bit error rate", lPhy},
	{"arista_phy_pcs_high_ber_changes_total", "counter", "Transitions of arista_phy_pcs_high_ber since boot", lPhy},
	{"arista_phy_pcs_last_errored_block_count", "gauge", "PCS errored block count as last observed (gauge: EOS semantics are ambiguous and the counter is clearable)", lPhy},
	{"arista_phy_pcs_last_errored_block_count_changes_total", "counter", "Times arista_phy_pcs_last_errored_block_count changed since boot; the reliable rate signal", lPhy},
	{"arista_phy_pcs_last_errored_block_count_last_change_timestamp_seconds", "gauge", "Unix timestamp arista_phy_pcs_last_errored_block_count last changed", lPhy},
	{"arista_phy_pcs_last_high_ber_count", "gauge", "PCS high-BER count as last observed (gauge: EOS semantics are ambiguous and the counter is clearable)", lPhy},
	{"arista_phy_pcs_last_high_ber_count_changes_total", "counter", "Times arista_phy_pcs_last_high_ber_count changed since boot; the reliable rate signal", lPhy},
	{"arista_phy_pcs_last_high_ber_count_last_change_timestamp_seconds", "gauge", "Unix timestamp arista_phy_pcs_last_high_ber_count last changed", lPhy},
	{"arista_phy_pcs_link_changes_total", "counter", "PCS link transitions since boot", lPhy},
	{"arista_phy_pcs_link_up", "gauge", "1 if the PCS link is up", lPhy},
	{"arista_phy_pma_link_changes_total", "counter", "PMA link transitions since boot", lPhy},
	{"arista_phy_pma_link_up", "gauge", "1 if the PMA link is up", lPhy},
	{"arista_phy_pma_signal_detect", "gauge", "1 if the PMA detects a signal", lPhy},
	{"arista_phy_pma_signal_detect_changes_total", "counter", "Transitions of arista_phy_pma_signal_detect since boot", lPhy},
	{"arista_psu_boot_timestamp_seconds", "gauge", "Unix timestamp when the PSU came online", lPSU},
	{"arista_psu_capacity_watts", "gauge", "PSU rated capacity", lPSU},
	{"arista_psu_info", "gauge", "PSU model labels. Always 1", lPSUInfo},
	{"arista_psu_input_current_amps", "gauge", "PSU input current", lPSU},
	{"arista_psu_input_voltage_volts", "gauge", "PSU input voltage", lPSU},
	{"arista_psu_ok", "gauge", "1 if PSU state is ok", lPSU},
	{"arista_psu_output_current_amps", "gauge", "PSU output current", lPSU},
	{"arista_psu_output_power_watts", "gauge", "PSU current output power", lPSU},
	{"arista_psu_output_voltage_volts", "gauge", "PSU output voltage", lPSU},
	{"arista_psu_temperature_alert", "gauge", "1 if the PSU sensor is in alert state", lPSUSensor},
	{"arista_psu_temperature_celsius", "gauge", "Current PSU temperature in Celsius", lPSUSensor},
	{"arista_psu_temperature_critical_threshold_celsius", "gauge", "Critical threshold reported by the PSU sensor", lPSUSensor},
	{"arista_psu_temperature_max_celsius", "gauge", "Historical maximum PSU temperature", lPSUSensor},
	{"arista_psu_temperature_overheat_threshold_celsius", "gauge", "Overheat threshold reported by the PSU sensor", lPSUSensor},
	{"arista_psu_temperature_sensor_ok", "gauge", "1 if the PSU sensor hardware is ok", lPSUSensor},
	{"arista_scrape_age_seconds", "gauge", "Seconds since the last successful poll (-1 if never)", lSwitch},
	{"arista_scrape_success", "gauge", "1 if the last poll of this switch succeeded", lSwitch},
	{"arista_temperature_alert", "gauge", "1 if the system sensor is in alert state", lSensor},
	{"arista_temperature_celsius", "gauge", "Current system temperature in Celsius", lSensor},
	{"arista_temperature_critical_threshold_celsius", "gauge", "Critical threshold reported by the system sensor", lSensor},
	{"arista_temperature_max_celsius", "gauge", "Historical maximum system temperature", lSensor},
	{"arista_temperature_overheat_threshold_celsius", "gauge", "Overheat threshold reported by the system sensor", lSensor},
	{"arista_temperature_sensor_ok", "gauge", "1 if the system sensor hardware is ok", lSensor},
	{"arista_temperature_shutdown_on_overheat", "gauge", "1 if the switch is configured to shut down on overheat", lSwitch},
	{"arista_temperature_system_ok", "gauge", "1 if overall temperature status is ok", lSwitch},
	{"arista_transceiver_info", "gauge", "Transceiver slot, lane, media type and serial. Always 1", lXcvrInfo},
	{"arista_transceiver_rx_power_dbm", "gauge", "Transceiver receive optical power for this lane", lIface},
	{"arista_transceiver_rx_power_threshold_dbm", "gauge", "Threshold for transceiver receive optical power for this lane as reported by the optic; level is high_alarm, high_warn, low_alarm or low_warn", lXcvrThresh},
	{"arista_transceiver_temperature_celsius", "gauge", "Transceiver module temperature", lIface},
	{"arista_transceiver_temperature_threshold_celsius", "gauge", "Threshold for transceiver module temperature as reported by the optic; level is high_alarm, high_warn, low_alarm or low_warn", lXcvrThresh},
	{"arista_transceiver_tx_bias_milliamps", "gauge", "Transceiver laser bias current for this lane", lIface},
	{"arista_transceiver_tx_bias_threshold_milliamps", "gauge", "Threshold for transceiver laser bias current for this lane as reported by the optic; level is high_alarm, high_warn, low_alarm or low_warn", lXcvrThresh},
	{"arista_transceiver_tx_power_dbm", "gauge", "Transceiver transmit optical power for this lane", lIface},
	{"arista_transceiver_tx_power_threshold_dbm", "gauge", "Threshold for transceiver transmit optical power for this lane as reported by the optic; level is high_alarm, high_warn, low_alarm or low_warn", lXcvrThresh},
	{"arista_transceiver_update_timestamp_seconds", "gauge", "Unix timestamp EOS last refreshed this optic's DOM data", lIface},
	{"arista_transceiver_voltage_threshold_volts", "gauge", "Threshold for transceiver supply voltage as reported by the optic; level is high_alarm, high_warn, low_alarm or low_warn", lXcvrThresh},
	{"arista_transceiver_voltage_volts", "gauge", "Transceiver supply voltage", lIface},
}

// descs is the catalogue compiled into Prometheus descriptors, keyed by name.
var descs = func() map[string]*prometheus.Desc {
	out := make(map[string]*prometheus.Desc, len(metricDefs))
	for _, d := range metricDefs {
		out[d.name] = prometheus.NewDesc(d.name, d.help, d.labels, nil)
	}
	return out
}()

// switchDescs and internalDescs partition the catalogue by prefix.
//
// arista_ metrics describe a switch and carry a switch label; arex_ metrics
// describe this process and carry none. That naming rule is what lets a
// filtered scrape select one or the other, and it is why the two collectors
// can be registered together without advertising the same descriptor twice.
var switchDescs, internalDescs = func() (sw, in []*prometheus.Desc) {
	for _, d := range metricDefs {
		if strings.HasPrefix(d.name, "arex_") {
			in = append(in, descs[d.name])
			continue
		}
		sw = append(sw, descs[d.name])
	}
	return sw, in
}()

// types mirrors descs, so an emission uses the declared value type.
var types = func() map[string]prometheus.ValueType {
	out := make(map[string]prometheus.ValueType, len(metricDefs))
	for _, d := range metricDefs {
		out[d.name] = d.valueType()
	}
	return out
}()
