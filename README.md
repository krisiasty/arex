# arex

Prometheus exporter for Arista switches via eAPI.

## Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `arista_scrape_success` | gauge | 1 if last scrape succeeded |
| `arista_scrape_age_seconds` | gauge | Seconds since last successful scrape |
| `arista_info` | gauge | Switch identity labels (model, serial, version) |
| `arista_boot_timestamp_seconds` | gauge | Unix timestamp of last boot |
| `arista_memory_total_bytes` | gauge | Total physical memory |
| `arista_memory_free_bytes` | gauge | Free physical memory |
| `arista_cpu_user_percent` | gauge | CPU user time % |
| `arista_cpu_system_percent` | gauge | CPU system time % |
| `arista_cpu_idle_percent` | gauge | CPU idle % |
| `arista_cpu_iowait_percent` | gauge | CPU iowait % |
| `arista_load_avg_1m` | gauge | 1-minute load average |
| `arista_load_avg_5m` | gauge | 5-minute load average |
| `arista_load_avg_15m` | gauge | 15-minute load average |
| `arista_temperature_system_ok` | gauge | 1 if overall temperature ok |
| `arista_temperature_celsius` | gauge | Per-sensor temperature |
| `arista_temperature_max_celsius` | gauge | Per-sensor historical max |
| `arista_temperature_overheat_threshold_celsius` | gauge | Overheat threshold |
| `arista_temperature_critical_threshold_celsius` | gauge | Critical threshold |
| `arista_temperature_alert` | gauge | 1 if sensor in alert state |
| `arista_temperature_sensor_ok` | gauge | 1 if sensor hardware ok |
| `arista_psu_ok` | gauge | 1 if PSU state ok |
| `arista_psu_capacity_watts` | gauge | PSU rated capacity |
| `arista_psu_output_power_watts` | gauge | PSU current output power |
| `arista_psu_input_voltage_volts` | gauge | PSU input voltage |
| `arista_psu_output_voltage_volts` | gauge | PSU output voltage |
| `arista_psu_input_current_amps` | gauge | PSU input current |
| `arista_psu_output_current_amps` | gauge | PSU output current |
| `arista_psu_temperature_celsius` | gauge | PSU sensor temperature |
| `arista_cooling_ok` | gauge | 1 if overall cooling ok |
| `arista_cooling_ambient_temperature_celsius` | gauge | Ambient temperature |
| `arista_cooling_info` | gauge | Cooling config info labels |
| `arista_fan_ok` | gauge | 1 if fan status ok |
| `arista_fan_speed_configured_percent` | gauge | Configured fan speed % |
| `arista_fan_speed_actual_percent` | gauge | Actual fan speed % |
| `arista_fan_speed_max_rpm` | gauge | Fan maximum speed RPM |
| `arista_fan_speed_stable` | gauge | 1 if fan speed stable |
| `arista_interface_link_up` | gauge | 1 if interface line protocol up |
| `arista_interface_in_octets_total` | counter | Inbound octets |
| `arista_interface_out_octets_total` | counter | Outbound octets |
| `arista_interface_in_errors_total` | counter | Inbound errors |
| `arista_interface_out_errors_total` | counter | Outbound errors |
| `arista_interface_in_discards_total` | counter | Inbound discards |
| `arista_interface_out_discards_total` | counter | Outbound discards |
| `arista_bgp_peer_up` | gauge | 1 if BGP peer Established |
| `arista_bgp_peer_prefixes_received` | gauge | Prefixes received from peer |
| `arista_bgp_peer_uptime_seconds` | gauge | BGP session uptime |

## Switch configuration

Enable eAPI on each switch:

```
management api http-commands
   no shutdown
   protocol https
   no protocol http
```

Create a read-only user:

```
role prometheus-ro
   10 permit command show.*

username prometheus privilege 15 role prometheus-ro secret SHA512 <hash>
```

## Running

```bash
# Build
go build -o arex .

# Run
./arex -config config.json
```

## Docker

```bash
docker build -t arex .
docker run -d \
  -p 9100:9100 \
  -v /etc/arex/config.json:/etc/arex/config.json:ro \
  arex
```

## Configuration

See `config.example.json`. All durations are Go duration strings (`30s`, `1m`, etc.).

| Field | Default | Description |
|-------|---------|-------------|
| `listenAddress` | `:9100` | Address to serve `/metrics` on |
| `pollInterval` | `30s` | How often to poll each switch |
| `scrapeTimeout` | `10s` | eAPI request timeout |
| `tlsSkipVerify` | `true` | Skip TLS cert verification |
| `stalenessLimit` | `90s` | Stop emitting metrics if data is older than this |
| `switches` | required | List of switch connection configs |

## Useful PromQL

```promql
# Uptime
time() - arista_boot_timestamp_seconds

# Interface bandwidth utilisation (bps)
rate(arista_interface_in_octets_total[5m]) * 8

# PSU load %
arista_psu_output_power_watts / arista_psu_capacity_watts * 100

# Fan speed deviation (failing fan indicator)
arista_fan_speed_actual_percent - arista_fan_speed_configured_percent

# Attach model/version to any metric
rate(arista_interface_in_octets_total[5m])
  * on(switch) group_left(model, version)
arista_info
```

## Alerting examples

```yaml
groups:
  - name: arista
    rules:
      - alert: SwitchScrapeDown
        expr: arista_scrape_success == 0
        for: 2m

      - alert: SwitchMetricsStale
        expr: arista_scrape_age_seconds > 90
        for: 5m

      - alert: SwitchTemperatureAlert
        expr: arista_temperature_alert == 1
        for: 1m

      - alert: SwitchFanDown
        expr: arista_fan_ok == 0
        for: 1m

      - alert: SwitchPSUDown
        expr: arista_psu_ok == 0
        for: 1m

      - alert: SwitchPSUHighLoad
        expr: arista_psu_output_power_watts / arista_psu_capacity_watts > 0.8
        for: 5m

      - alert: SwitchBGPPeerDown
        expr: arista_bgp_peer_up == 0
        for: 2m

      - alert: SwitchInterfaceDown
        expr: arista_interface_link_up == 0
        for: 2m
```
