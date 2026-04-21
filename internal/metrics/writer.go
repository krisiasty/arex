package metrics

import (
	"fmt"
	"io"
	"time"

	"github.com/krisiasty/arex/internal/collector"
	"github.com/krisiasty/arex/internal/eapi"
)

// Write renders all metrics for all switches in Prometheus text exposition
// format (https://prometheus.io/docs/instrumenting/exposition_formats/).
func Write(w io.Writer, store *collector.Store, stalenessLimit time.Duration) {
	writeHelp(w)
	now := time.Now()
	for _, sw := range store.All() {
		writeSwitch(w, sw, now, stalenessLimit)
	}
}

// writeSwitch renders metrics for a single switch.
func writeSwitch(w io.Writer, sw *collector.SwitchData, now time.Time, stalenessLimit time.Duration) {
	sw.RLock()
	defer sw.RUnlock()

	host := sw.Label
	scrapeErr := sw.ScrapeErr
	lastSuccess := sw.LastSuccess

	upVal := 1.0
	if scrapeErr != nil {
		upVal = 0.0
	}

	age := now.Sub(lastSuccess).Seconds()
	if lastSuccess.IsZero() {
		age = -1 // not yet collected
	}

	_, _ = fmt.Fprintf(w, "arista_scrape_success{switch=%q} %g\n", host, upVal)
	_, _ = fmt.Fprintf(w, "arista_scrape_age_seconds{switch=%q} %g\n", host, age)

	// Do not emit stale metrics — Prometheus will handle disappearing series.
	if !lastSuccess.IsZero() && now.Sub(lastSuccess) > stalenessLimit {
		return
	}
	if scrapeErr != nil {
		return
	}

	writeVersion(w, host, sw.Version)
	writeCPUMemory(w, host, sw.ProcessTop)
	writeTemperature(w, host, sw.EnvTemp)
	writePower(w, host, sw.EnvPower)
	writeCooling(w, host, sw.EnvCooling)
	writeInterfaces(w, host, sw.Interfaces)
	writeBGP(w, host, sw.BGPSummary)
}

// writeVersion emits switch identity and resource metrics from show version.
func writeVersion(w io.Writer, host string, v eapi.ShowVersion) {
	// Static identity as an info metric — use in PromQL joins.
	_, _ = fmt.Fprintf(w, "arista_info{switch=%q,model=%q,serial=%q,version=%q,mac=%q,arch=%q} 1\n",
		host, v.ModelName, v.SerialNumber, v.Version, v.SystemMacAddress, v.Architecture)

	// Bootup timestamp — derive uptime in PromQL as: time() - arista_boot_timestamp_seconds
	fmt.Fprintf(w, "arista_boot_timestamp_seconds{switch=%q} %g\n", host, v.BootupTimestamp)

	// Memory from show version (kilobytes → bytes)
	fmt.Fprintf(w, "arista_memory_total_bytes{switch=%q} %d\n", host, v.MemTotal*1024)
	fmt.Fprintf(w, "arista_memory_free_bytes{switch=%q} %d\n", host, v.MemFree*1024)
}

// writeCPUMemory emits CPU and load average metrics from show processes top once.
func writeCPUMemory(w io.Writer, host string, p eapi.ShowProcessesTop) {
	cpu := p.CpuInfo.Cpu
	fmt.Fprintf(w, "arista_cpu_user_percent{switch=%q} %g\n", host, cpu.User)
	fmt.Fprintf(w, "arista_cpu_system_percent{switch=%q} %g\n", host, cpu.System)
	fmt.Fprintf(w, "arista_cpu_idle_percent{switch=%q} %g\n", host, cpu.Idle)
	fmt.Fprintf(w, "arista_cpu_iowait_percent{switch=%q} %g\n", host, cpu.IoWait)

	if len(p.TimeInfo.LoadAvg) == 3 {
		fmt.Fprintf(w, "arista_load_avg_1m{switch=%q} %g\n", host, p.TimeInfo.LoadAvg[0])
		fmt.Fprintf(w, "arista_load_avg_5m{switch=%q} %g\n", host, p.TimeInfo.LoadAvg[1])
		fmt.Fprintf(w, "arista_load_avg_15m{switch=%q} %g\n", host, p.TimeInfo.LoadAvg[2])
	}
}

// writeTemperature emits metrics from show system environment temperature.
func writeTemperature(w io.Writer, host string, env eapi.ShowEnvironmentTemp) {
	systemOk := boolToFloat(env.SystemStatus == "temperatureOk")
	fmt.Fprintf(w, "arista_temperature_system_ok{switch=%q} %g\n", host, systemOk)

	emitSensor := func(s eapi.TempSensor, location string) {
		l := fmt.Sprintf("switch=%q,sensor=%q,location=%q,description=%q",
			host, s.Name, location, s.Description)
		fmt.Fprintf(w, "arista_temperature_celsius{%s} %g\n", l, s.CurrentTemperature)
		fmt.Fprintf(w, "arista_temperature_max_celsius{%s} %g\n", l, s.MaxTemperature)
		fmt.Fprintf(w, "arista_temperature_overheat_threshold_celsius{%s} %g\n", l, s.OverheatThreshold)
		fmt.Fprintf(w, "arista_temperature_critical_threshold_celsius{%s} %g\n", l, s.CriticalThreshold)
		fmt.Fprintf(w, "arista_temperature_alert{%s} %g\n", l, boolToFloat(s.InAlertState))
		fmt.Fprintf(w, "arista_temperature_sensor_ok{%s} %g\n", l, boolToFloat(s.HwStatus == "ok"))
	}

	for _, s := range env.TempSensors {
		emitSensor(s, "system")
	}
	// PSU temp sensors are emitted from writePower to avoid duplicates.
}

// writePower emits metrics from show system environment power.
func writePower(w io.Writer, host string, env eapi.ShowEnvironmentPower) {
	for slot, psu := range env.PowerSupplies {
		pl := fmt.Sprintf("switch=%q,psu=%q,model=%q", host, slot, psu.ModelName)

		fmt.Fprintf(w, "arista_psu_ok{%s} %g\n", pl, boolToFloat(psu.State == "ok"))
		fmt.Fprintf(w, "arista_psu_capacity_watts{%s} %g\n", pl, psu.Capacity)
		fmt.Fprintf(w, "arista_psu_output_power_watts{%s} %g\n", pl, psu.OutputPower)
		fmt.Fprintf(w, "arista_psu_input_voltage_volts{%s} %g\n", pl, psu.InputVoltage)
		fmt.Fprintf(w, "arista_psu_output_voltage_volts{%s} %g\n", pl, psu.OutputVoltage)
		fmt.Fprintf(w, "arista_psu_input_current_amps{%s} %g\n", pl, psu.InputCurrent)
		fmt.Fprintf(w, "arista_psu_output_current_amps{%s} %g\n", pl, psu.OutputCurrent)
		fmt.Fprintf(w, "arista_psu_boot_timestamp_seconds{%s} %g\n", pl, psu.Uptime)

		// PSU temp sensors
		for sensorName, sensor := range psu.TempSensors {
			sl := fmt.Sprintf("switch=%q,psu=%q,sensor=%q", host, slot, sensorName)
			fmt.Fprintf(w, "arista_psu_temperature_celsius{%s} %g\n", sl, sensor.Temperature)
			fmt.Fprintf(w, "arista_psu_temperature_sensor_ok{%s} %g\n", sl,
				boolToFloat(sensor.Status == "ok"))
		}
	}
}

// writeCooling emits metrics from show system environment cooling.
func writeCooling(w io.Writer, host string, env eapi.ShowEnvironmentCooling) {
	fmt.Fprintf(w, "arista_cooling_ok{switch=%q} %g\n", host,
		boolToFloat(env.SystemStatus == "coolingOk"))
	fmt.Fprintf(w, "arista_cooling_ambient_temperature_celsius{switch=%q} %g\n",
		host, env.AmbientTemperature)
	fmt.Fprintf(w, "arista_cooling_info{switch=%q,airflow=%q,mode=%q} 1\n",
		host, env.AirflowDirection, env.CoolingMode)

	emitFan := func(f eapi.Fan, tray, location string) {
		l := fmt.Sprintf("switch=%q,tray=%q,fan=%q,location=%q",
			host, tray, f.Label, location)
		fmt.Fprintf(w, "arista_fan_ok{%s} %g\n", l, boolToFloat(f.Status == "ok"))
		fmt.Fprintf(w, "arista_fan_speed_configured_percent{%s} %d\n", l, f.ConfiguredSpeed)
		fmt.Fprintf(w, "arista_fan_speed_actual_percent{%s} %d\n", l, f.ActualSpeed)
		fmt.Fprintf(w, "arista_fan_speed_max_rpm{%s} %d\n", l, f.MaxSpeed)
		fmt.Fprintf(w, "arista_fan_speed_stable{%s} %g\n", l, boolToFloat(f.SpeedStable))
		fmt.Fprintf(w, "arista_fan_boot_timestamp_seconds{%s} %g\n", l, f.Uptime)
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

// writeInterfaces emits metrics from show interfaces.
func writeInterfaces(w io.Writer, host string, ifaces eapi.ShowInterfaces) {
	for name, iface := range ifaces.Interfaces {
		l := fmt.Sprintf("switch=%q,interface=%q", host, name)
		linkUp := boolToFloat(iface.LineProtocolStatus == "up")

		fmt.Fprintf(w, "arista_interface_link_up{%s} %g\n", l, linkUp)
		fmt.Fprintf(w, "arista_interface_in_octets_total{%s} %d\n", l, iface.InterfaceCounters.InOctets)
		fmt.Fprintf(w, "arista_interface_out_octets_total{%s} %d\n", l, iface.InterfaceCounters.OutOctets)
		fmt.Fprintf(w, "arista_interface_in_errors_total{%s} %d\n", l, iface.InterfaceCounters.InputErrors)
		fmt.Fprintf(w, "arista_interface_out_errors_total{%s} %d\n", l, iface.InterfaceCounters.OutputErrors)
		fmt.Fprintf(w, "arista_interface_in_discards_total{%s} %d\n", l, iface.InterfaceCounters.InDiscards)
		fmt.Fprintf(w, "arista_interface_out_discards_total{%s} %d\n", l, iface.InterfaceCounters.TotalOutDrops)
	}
}

// writeBGP emits metrics from show ip bgp summary.
func writeBGP(w io.Writer, host string, bgp eapi.ShowBGPSummary) {
	for vrf, v := range bgp.Vrfs {
		for peer, p := range v.Peers {
			l := fmt.Sprintf("switch=%q,vrf=%q,peer=%q,asn=%d", host, vrf, peer, p.Asn)
			peerUp := boolToFloat(p.PeerState == "Established")
			fmt.Fprintf(w, "arista_bgp_peer_up{%s} %g\n", l, peerUp)
			fmt.Fprintf(w, "arista_bgp_peer_prefixes_received{%s} %d\n", l, p.PrefixReceived)
			// uptime is 0 when peer is down — only emit when meaningful
			if p.PeerState == "Established" {
				fmt.Fprintf(w, "arista_bgp_peer_uptime_seconds{%s} %g\n", l, p.Uptime)
			}
		}
	}
}

// writeHelp emits HELP and TYPE lines for all metrics.
// Placed once at the top of the output.
func writeHelp(w io.Writer) {
	lines := []string{
		"# HELP arista_scrape_success 1 if last scrape succeeded, 0 otherwise",
		"# TYPE arista_scrape_success gauge",
		"# HELP arista_scrape_age_seconds Seconds since last successful scrape (-1 if never)",
		"# TYPE arista_scrape_age_seconds gauge",
		"# HELP arista_info Switch identity (model, serial, version). Always 1.",
		"# TYPE arista_info gauge",
		"# HELP arista_boot_timestamp_seconds Unix timestamp of last boot",
		"# TYPE arista_boot_timestamp_seconds gauge",
		"# HELP arista_memory_total_bytes Total physical memory in bytes",
		"# TYPE arista_memory_total_bytes gauge",
		"# HELP arista_memory_free_bytes Free physical memory in bytes",
		"# TYPE arista_memory_free_bytes gauge",
		"# HELP arista_cpu_user_percent CPU time spent in user space",
		"# TYPE arista_cpu_user_percent gauge",
		"# HELP arista_cpu_system_percent CPU time spent in kernel space",
		"# TYPE arista_cpu_system_percent gauge",
		"# HELP arista_cpu_idle_percent CPU idle percentage",
		"# TYPE arista_cpu_idle_percent gauge",
		"# HELP arista_cpu_iowait_percent CPU time waiting on I/O",
		"# TYPE arista_cpu_iowait_percent gauge",
		"# HELP arista_load_avg_1m 1-minute load average",
		"# TYPE arista_load_avg_1m gauge",
		"# HELP arista_load_avg_5m 5-minute load average",
		"# TYPE arista_load_avg_5m gauge",
		"# HELP arista_load_avg_15m 15-minute load average",
		"# TYPE arista_load_avg_15m gauge",
		"# HELP arista_temperature_system_ok 1 if overall temperature status is ok",
		"# TYPE arista_temperature_system_ok gauge",
		"# HELP arista_temperature_celsius Current sensor temperature in Celsius",
		"# TYPE arista_temperature_celsius gauge",
		"# HELP arista_temperature_max_celsius Historical maximum sensor temperature",
		"# TYPE arista_temperature_max_celsius gauge",
		"# HELP arista_temperature_overheat_threshold_celsius Overheat threshold in Celsius",
		"# TYPE arista_temperature_overheat_threshold_celsius gauge",
		"# HELP arista_temperature_critical_threshold_celsius Critical threshold in Celsius",
		"# TYPE arista_temperature_critical_threshold_celsius gauge",
		"# HELP arista_temperature_alert 1 if sensor is in alert state",
		"# TYPE arista_temperature_alert gauge",
		"# HELP arista_temperature_sensor_ok 1 if sensor hardware status is ok",
		"# TYPE arista_temperature_sensor_ok gauge",
		"# HELP arista_psu_ok 1 if PSU state is ok",
		"# TYPE arista_psu_ok gauge",
		"# HELP arista_psu_capacity_watts PSU rated capacity in watts",
		"# TYPE arista_psu_capacity_watts gauge",
		"# HELP arista_psu_output_power_watts PSU current output power in watts",
		"# TYPE arista_psu_output_power_watts gauge",
		"# HELP arista_psu_input_voltage_volts PSU input voltage",
		"# TYPE arista_psu_input_voltage_volts gauge",
		"# HELP arista_psu_output_voltage_volts PSU output voltage",
		"# TYPE arista_psu_output_voltage_volts gauge",
		"# HELP arista_psu_input_current_amps PSU input current in amps",
		"# TYPE arista_psu_input_current_amps gauge",
		"# HELP arista_psu_output_current_amps PSU output current in amps",
		"# TYPE arista_psu_output_current_amps gauge",
		"# HELP arista_psu_boot_timestamp_seconds Unix timestamp when PSU came online",
		"# TYPE arista_psu_boot_timestamp_seconds gauge",
		"# HELP arista_psu_temperature_celsius PSU sensor temperature in Celsius",
		"# TYPE arista_psu_temperature_celsius gauge",
		"# HELP arista_psu_temperature_sensor_ok 1 if PSU temperature sensor is ok",
		"# TYPE arista_psu_temperature_sensor_ok gauge",
		"# HELP arista_cooling_ok 1 if overall cooling status is ok",
		"# TYPE arista_cooling_ok gauge",
		"# HELP arista_cooling_ambient_temperature_celsius Ambient temperature in Celsius",
		"# TYPE arista_cooling_ambient_temperature_celsius gauge",
		"# HELP arista_cooling_info Cooling configuration info. Always 1.",
		"# TYPE arista_cooling_info gauge",
		"# HELP arista_fan_ok 1 if fan status is ok",
		"# TYPE arista_fan_ok gauge",
		"# HELP arista_fan_speed_configured_percent Configured fan speed as percentage",
		"# TYPE arista_fan_speed_configured_percent gauge",
		"# HELP arista_fan_speed_actual_percent Actual fan speed as percentage",
		"# TYPE arista_fan_speed_actual_percent gauge",
		"# HELP arista_fan_speed_max_rpm Fan maximum speed in RPM",
		"# TYPE arista_fan_speed_max_rpm gauge",
		"# HELP arista_fan_speed_stable 1 if fan speed has stabilised",
		"# TYPE arista_fan_speed_stable gauge",
		"# HELP arista_fan_boot_timestamp_seconds Unix timestamp when fan came online",
		"# TYPE arista_fan_boot_timestamp_seconds gauge",
		"# HELP arista_interface_link_up 1 if interface line protocol is up",
		"# TYPE arista_interface_link_up gauge",
		"# HELP arista_interface_in_octets_total Total inbound octets",
		"# TYPE arista_interface_in_octets_total counter",
		"# HELP arista_interface_out_octets_total Total outbound octets",
		"# TYPE arista_interface_out_octets_total counter",
		"# HELP arista_interface_in_errors_total Total inbound errors",
		"# TYPE arista_interface_in_errors_total counter",
		"# HELP arista_interface_out_errors_total Total outbound errors",
		"# TYPE arista_interface_out_errors_total counter",
		"# HELP arista_interface_in_discards_total Total inbound discards",
		"# TYPE arista_interface_in_discards_total counter",
		"# HELP arista_interface_out_discards_total Total outbound discards",
		"# TYPE arista_interface_out_discards_total counter",
		"# HELP arista_bgp_peer_up 1 if BGP peer is in Established state",
		"# TYPE arista_bgp_peer_up gauge",
		"# HELP arista_bgp_peer_prefixes_received Number of prefixes received from peer",
		"# TYPE arista_bgp_peer_prefixes_received gauge",
		"# HELP arista_bgp_peer_uptime_seconds Seconds since BGP session was established",
		"# TYPE arista_bgp_peer_uptime_seconds gauge",
	}
	for _, l := range lines {
		_, _ = fmt.Fprintln(w, l)
	}
}

func boolToFloat(b bool) float64 {
	if b {
		return 1.0
	}
	return 0.0
}
