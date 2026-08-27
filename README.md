# arex

Prometheus exporter for Arista switches via eAPI.

Arista EOS ships no Prometheus exporter. Custom binaries can be installed on EOS, but the supported methods for
installing and managing them assume the switch can reach the outside world from the default VRF — and management
interfaces normally sit in the `management` VRF instead, which makes that approach impractical.

arex sidesteps the problem by running off-box. It connects out to each switch over eAPI, polls a set of `show`
commands on an interval, caches the results, and serves them as Prometheus metrics. Nothing is installed on the
switch, so the VRF the management interface lives in only has to be reachable from wherever arex runs — see
[switch configuration](#switch-configuration) for the one VRF-specific step this requires on the switch itself.

## Metrics

All metrics carry a `switch` label. Descriptions and other mutable text live on `_info` metrics rather than on the
counters, so editing a port description does not change a counter's series identity and break `rate()`.

### Scrape health

| Metric | Type | Description |
| --- | --- | --- |
| `arista_scrape_success` | gauge | 1 if the last poll succeeded |
| `arista_scrape_age_seconds` | gauge | Seconds since the last successful poll, -1 if never |
| `arista_command_success` | gauge | 1 if this eAPI command succeeded, labelled by `command` |

Last-known-good data keeps being served while `arista_scrape_age_seconds` is below `stalenessLimit`, so a transient
eAPI failure does not create gaps in every series. `arista_command_success` is per-command: one command rejected by
a platform costs only its own metrics.

### System

| Metric | Type | Description |
| --- | --- | --- |
| `arista_info` | gauge | Identity labels: `model`, `serial`, `version`, `mac`, `arch` |
| `arista_boot_timestamp_seconds` | gauge | Unix timestamp of last boot |
| `arista_memory_total_bytes` | gauge | Total physical memory |
| `arista_memory_available_bytes` | gauge | Allocatable memory including reclaimable cache — **alert on this** |
| `arista_memory_free_bytes` | gauge | Strictly unused memory; normally low, see below |
| `arista_memory_used_bytes` | gauge | Memory in use |
| `arista_memory_buffer_bytes` | gauge | Buffer and cache as reported by EOS |
| `arista_cpu_user_percent` | gauge | CPU time in user space |
| `arista_cpu_system_percent` | gauge | CPU time in kernel space |
| `arista_cpu_nice_percent` | gauge | CPU time on niced processes |
| `arista_cpu_idle_percent` | gauge | CPU idle |
| `arista_cpu_iowait_percent` | gauge | CPU waiting on I/O |
| `arista_cpu_steal_percent` | gauge | CPU stolen by the hypervisor, for vEOS |
| `arista_load_avg_1m` / `_5m` / `_15m` | gauge | Load averages |

`arista_memory_free_bytes` and `arista_memory_available_bytes` measure different things and differ substantially —
by about 5.3 GiB on a 16 GB switch. Linux spends idle RAM on page cache, so low *free* memory is the normal healthy
state. Alert on *available*.

### Environment

| Metric | Type | Description |
| --- | --- | --- |
| `arista_temperature_system_ok` | gauge | 1 if overall temperature status is ok |
| `arista_temperature_shutdown_on_overheat` | gauge | 1 if configured to shut down on overheat |
| `arista_temperature_celsius` | gauge | Per-sensor temperature |
| `arista_temperature_max_celsius` | gauge | Per-sensor historical maximum |
| `arista_temperature_overheat_threshold_celsius` | gauge | Sensor's own overheat threshold |
| `arista_temperature_critical_threshold_celsius` | gauge | Sensor's own critical threshold |
| `arista_temperature_alert` | gauge | 1 if the sensor is in alert state |
| `arista_temperature_sensor_ok` | gauge | 1 if sensor hardware is ok |
| `arista_psu_temperature_*` | gauge | The same six series for PSU sensors, labelled by `psu` |
| `arista_psu_info` | gauge | PSU `model` label |
| `arista_psu_ok` | gauge | 1 if PSU state is ok |
| `arista_psu_capacity_watts` | gauge | Rated capacity |
| `arista_psu_output_power_watts` | gauge | Current output power |
| `arista_psu_input_voltage_volts` / `_output_voltage_volts` | gauge | PSU voltages |
| `arista_psu_input_current_amps` / `_output_current_amps` | gauge | PSU currents |
| `arista_psu_boot_timestamp_seconds` | gauge | When the PSU came online |
| `arista_cooling_ok` | gauge | 1 if overall cooling status is ok |
| `arista_cooling_fans_ok` | gauge | 1 if the fan alarm status is ok |
| `arista_cooling_shutdown_on_insufficient_fans` | gauge | 1 if configured to shut down on insufficient fans |
| `arista_cooling_ambient_temperature_celsius` | gauge | Ambient temperature |
| `arista_cooling_info` | gauge | `airflow` and `mode` labels |
| `arista_fan_info` | gauge | Fan `vendor_model` label |
| `arista_fan_ok` | gauge | 1 if fan status is ok |
| `arista_fan_speed_configured_percent` / `_actual_percent` | gauge | Fan speed as a percentage |
| `arista_fan_speed_max_rpm` | gauge | Fan maximum speed in RPM |
| `arista_fan_speed_stable` | gauge | 1 if fan speed has stabilised |
| `arista_fan_boot_timestamp_seconds` | gauge | When the fan came online |

Sensor series carry `sensor`, `description` and `position` labels; `position` is `inlet`, `outlet` or `other`.
Thresholds come from the sensor itself, so alerts need no hardcoded limits.

### Interfaces

| Metric | Type | Description |
| --- | --- | --- |
| `arista_interface_info` | gauge | `description`, `membership`, `mtu` labels |
| `arista_interface_link_up` | gauge | 1 if the line protocol is up |
| `arista_interface_speed_bits_per_second` | gauge | Negotiated speed, for utilisation calculations |
| `arista_interface_in_octets_total` / `_out_octets_total` | counter | Octets |
| `arista_interface_in_errors_total` / `_out_errors_total` | counter | Errors |
| `arista_interface_in_discards_total` / `_out_discards_total` | counter | Discards |
| `arista_interface_in_packets_total` / `_out_packets_total` | counter | Packets, labelled by `cast` |
| `arista_interface_in_errors_detail_total` | counter | Inbound errors broken down by `cause` |
| `arista_interface_out_errors_detail_total` | counter | Outbound errors broken down by `cause` |
| `arista_interface_link_status_changes_total` | counter | Link transitions — the flap counter |
| `arista_interface_last_counter_clear_timestamp_seconds` | gauge | When counters were last cleared |
| `arista_interface_counter_refresh_timestamp_seconds` | gauge | When EOS last refreshed the counters |
| `arista_interface_last_status_change_timestamp_seconds` | gauge | When the interface last changed state |

The `cause` label on the error-detail counters is `fcsErrors`, `symbolErrors`, `runtFrames`, `giantFrames`,
`alignmentErrors` or `rxPause` inbound, and `collisions`, `lateCollisions`, `deferredTransmissions` or `txPause`
outbound. `cast` is `unicast`, `multicast` or `broadcast`.

### BGP

Collected with `vrf all`, so peers in every VRF appear, not just the default one.

| Metric | Type | Description |
| --- | --- | --- |
| `arista_bgp_peer_info` | gauge | Peer `description` label |
| `arista_bgp_peer_up` | gauge | 1 if the peer is Established |
| `arista_bgp_peer_under_maintenance` | gauge | 1 if the peer is under maintenance |
| `arista_bgp_peer_prefixes_received` | gauge | Prefixes received |
| `arista_bgp_peer_prefixes_accepted` | gauge | Prefixes accepted after policy |
| `arista_bgp_peer_prefixes_advertised` | gauge | Prefixes advertised |
| `arista_bgp_peer_state_change_timestamp_seconds` | gauge | When the session last changed state, up or down |

Series carry `vrf`, `peer` and `asn`. `asn` is a string label: EOS quotes it, and 4-byte private ASNs exceed a
32-bit integer. Accepted below received means prefixes were rejected by policy.

### Transceivers

From `show interfaces transceiver detail`. Interfaces with no optic installed produce no series.

| Metric | Type | Description |
| --- | --- | --- |
| `arista_transceiver_info` | gauge | `slot`, `channel`, `media_type`, `vendor_sn` labels |
| `arista_transceiver_temperature_celsius` | gauge | Module temperature |
| `arista_transceiver_voltage_volts` | gauge | Supply voltage |
| `arista_transceiver_tx_bias_milliamps` | gauge | Laser bias current, per lane |
| `arista_transceiver_tx_power_dbm` | gauge | Transmit optical power, per lane |
| `arista_transceiver_rx_power_dbm` | gauge | Receive optical power, per lane |
| `arista_transceiver_*_threshold_*` | gauge | The optic's own limits, labelled by `level` |
| `arista_transceiver_update_timestamp_seconds` | gauge | When EOS last refreshed this optic's DOM data |

`level` is `high_alarm`, `high_warn`, `low_alarm` or `low_warn`.

Thresholds are read from each optic, and they vary widely by media type — `txBias` high alarm is 11 mA on
100GBASE-SR4 but 80 mA on 40GBASE-LRL4 — so alerts should compare against them rather than against hardcoded
numbers. A threshold series is **omitted** when the optic reports no limit, so absent is distinguishable from a
legitimate limit of zero.

Temperature and voltage are module-level. On a broken-out cage all subinterfaces share one physical optic and
report identical values, so group by `slot` to count optics or to avoid alerting four times on one module.

### PHY

From `show interfaces phy detail`. The schema varies by speed, so some series exist only on some links. Serdes
data (eye margins, DFE and TX taps, VGA) is deliberately not collected: it is roughly half the payload, the eye
values are meaningless while a link is down, and every field drifts on each poll.

| Metric | Type | Description |
| --- | --- | --- |
| `arista_phy_info` | gauge | `chip`, `firmware`, `oper_speed` labels |
| `arista_phy_interface_up` | gauge | 1 if the PHY reports the interface up |
| `arista_phy_link_up` | gauge | 1 if PHY state is linkUp |
| `arista_phy_pcs_link_up` | gauge | 1 if the PCS link is up |
| `arista_phy_pcs_block_lock` | gauge | 1 if PCS block lock is achieved — **only on links without FEC** |
| `arista_phy_pcs_high_ber` | gauge | 1 if the PCS reports a high bit error rate |
| `arista_phy_pcs_last_high_ber_count` | gauge | High-BER count as last observed |
| `arista_phy_pcs_last_errored_block_count` | gauge | Errored block count as last observed |
| `arista_phy_pma_link_up` | gauge | 1 if the PMA link is up |
| `arista_phy_pma_signal_detect` | gauge | 1 if the PMA detects a signal |
| `arista_phy_fec_alignment_lock` | gauge | 1 if FEC alignment lock is achieved — **only on links with FEC** |
| `arista_phy_fec_corrected_codewords` | gauge | FEC codewords corrected |
| `arista_phy_fec_uncorrected_codewords` | gauge | FEC codewords that could not be corrected: frames were lost |
| `arista_phy_fec_corrected_symbols` | gauge | Corrected symbols per `lane`, at native speeds only |
| `arista_phy_mac_local_fault` / `_remote_fault` | gauge | MAC fault signalling |
| `arista_phy_interrupt_count` | gauge | PHY interrupt count |
| `*_changes_total` | counter | Transitions of the corresponding series since boot |
| `*_last_change_timestamp_seconds` | gauge | When the corresponding series last changed |

RS-FEC alignment lock replaces PCS block lock, so a link has exactly one of the two: `arista_phy_pcs_block_lock`
below 25G, `arista_phy_fec_alignment_lock` at 25G and above.

**Read the `_changes_total` counters, not the gauges, to detect activity.** EOS reports these attributes as a
value with a change count, and the value's semantics are ambiguous: on a link that had been repaired,
`uncorrected_codewords` read 3 with 13 changes, which fits neither a running total nor a per-interval count. The
counters are also clearable from the CLI, so the value can go down. `*_changes_total` only ever rises, and
`*_last_change_timestamp_seconds` distinguishes a link that is failing now from one that failed and was fixed.

Series carry a `phy` label because an interface can have more than one PHY, line side and system side.

Every `*_timestamp_seconds` metric is an epoch reported by the switch, so they are only meaningful if the switch
clock is correct. Keep NTP configured.

## Switch configuration

### Enable eAPI

eAPI must be enabled in the VRF that carries the management interface. On a switch managed through the `management`
VRF — the usual case — the `vrf management` stanza is required, and connections to the management address are
refused without it.

```text
management api http-commands
   no shutdown
   protocol https
   no protocol http
   !
   vrf management
      no shutdown
!
```

Both levels of `no shutdown` are required, and this is exactly where eAPI departs from `management ssh`. The SSH
idiom — shut the service at the top level, enable it per VRF — does not work here. With only the `vrf` stanza set,
the API stays off entirely:

```text
Enabled: No
HTTPS server: enabled, set to use port 443
VRFs: None
```

The `vrf management` block is present in the running config but inert, and `VRFs: None` is reported regardless. Note
the tell in the HTTPS server line: `enabled` means configured but not listening, whereas `running` means actually
serving. Only the top-level `no shutdown` promotes one to the other. Verified on EOS 4.35.4M.

`protocol https` and `no protocol http` are the defaults on current EOS and can be omitted; they are shown to be
explicit, and to reset a switch where plaintext HTTP was enabled earlier.

If the management interface is in the default VRF, drop the `vrf management` block. If it is in a VRF under another
name, substitute that name — arex itself needs no VRF setting either way, since it connects from off-box.

Confirm the HTTPS server is running and bound to the right VRF:

```text
show management api http-commands
```

Expect `Enabled: Yes`, `HTTPS server: running`, `VRFs: management`, and the management interface listed under
`URLs`. Note the reported `SSL Profile` — a stock switch uses `ARISTA_DEFAULT_SELF_SIGNED_PROFILE`, whose
certificate no client can verify, so set [`tlsSkipVerify`](#configuration) to `true` unless you have installed a
certificate signed by a CA that arex trusts.

Then verify eAPI answers from wherever arex will run. This is the same request arex issues:

```bash
curl -k -u prometheus:secret https://<mgmt-ip>/command-api \
  -d '{"jsonrpc":"2.0","method":"runCmds","params":{"version":1,"cmds":["show version"],"format":"json"},"id":1}'
```

### Create a read-only user

arex only ever issues `show` commands, so restrict it to those:

```text
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
| --- | --- | --- |
| `listenAddress` | `:9100` | Address to serve `/metrics` on |
| `pollInterval` | `30s` | How often to poll each switch |
| `scrapeTimeout` | `10s` | eAPI request timeout |
| `tlsSkipVerify` | `false` | Skip TLS verification for switches with no per-switch method set. See [TLS](#tls) |
| `stalenessLimit` | `90s` | Stop emitting metrics if data is older than this |
| `switches` | required | List of switch connection configs |

Per-switch fields:

| Field | Description |
| --- | --- |
| `host` | Scheme and address of the switch, e.g. `https://10.10.0.11` |
| `username` | eAPI username |
| `password` | eAPI password |
| `name` | Value for the `switch` label on every metric. Optional, falls back to `host` |
| `caFile` | PEM bundle to verify this switch's certificate against. See [TLS](#tls) |
| `pinnedCertSha256` | SHA-256 of this switch's leaf certificate. See [TLS](#tls) |

Point `host` at the switch's management address — on a typical switch that address lives in the `management` VRF.
Keep `name` unique across switches: two entries sharing one label collapse into a single metric series.

There is no VRF setting in arex's own config. It runs off-box, so the VRF is purely a switch-side concern, handled
by the `vrf management` stanza in [switch configuration](#switch-configuration).

## TLS

Every switch needs exactly one of `caFile`, `pinnedCertSha256`, or the global `tlsSkipVerify`. arex refuses to
start otherwise, rather than defaulting to something wrong in either direction: verifying fails against every
stock switch, and silently skipping hides that nothing is being checked.

### Why the stock certificate cannot be verified

A factory switch serves `ARISTA_DEFAULT_SELF_SIGNED_PROFILE`. Inspect it with:

```text
show management security ssl certificate | json
show management security ssl profile detail | json
```

On EOS 4.35.4M that certificate has:

- a common name of `ARISTA_DEFAULT_SELF_SIGNED_PROFILE` — the profile name, not a hostname
- **no `subjectAltName` extension at all**, its only extension being `subjectKeyIdentifier`
- validity from 1970 to 9999, so it never expires and never rotates on its own

EOS diagnoses this itself: the profile reports `errorType: "hostnameMismatch"` while still calling itself `valid`,
so the switch knowingly serves a certificate that does not identify it.

Go has matched hostnames against SANs only, with no common-name fallback, since 1.15. With no SANs there is no
hostname and no IP address that can ever match, so **adding this certificate to a trust store does not help** —
the failure is naming, not trust:

```text
x509: cannot validate certificate for 192.0.2.33 because it doesn't contain any IP SANs
```

That is why arex offers pinning. It is not a workaround for self-signed certificates in general; it is the only
verification possible against a switch whose certificate names nothing.

### Option 1: replace the certificate (recommended)

Issue certificates from your own CA, or self-signed with correct SANs covering the address arex connects to, bind
them to an SSL profile, and point eAPI at that profile. Then give arex the CA bundle:

```json
{ "host": "https://10.10.0.11", "caFile": "/etc/arex/switch-ca.pem" }
```

This is the only option that survives certificate rotation without reconfiguring arex, and the only one where a
compromised switch key does not go unnoticed indefinitely.

### Option 2: pin the certificate

Works against a stock switch with no changes on the switch at all. Read the fingerprint from the host arex will
run on, which also proves the network path works:

```bash
openssl s_client -connect 10.10.0.11:443 </dev/null 2>/dev/null \
  | openssl x509 -noout -fingerprint -sha256
```

```json
{ "host": "https://10.10.0.11", "pinnedCertSha256": "A1:B2:...:90" }
```

Colons and case are ignored, so the `openssl` output can be pasted verbatim. arex pins the leaf certificate's DER
digest and enforces it on resumed TLS sessions as well as fresh ones.

Read the fingerprint over a path you trust — reading it over the network you are trying to protect only tells you
what the network says today. Ideally take it from the switch console. A pin here is unusually stable because the
default certificate never expires, so it changes only if someone regenerates it; when that happens arex logs both
the presented and the expected digest so the new value can be verified and configured.

### Option 3: skip verification

```json
{ "tlsSkipVerify": true }
```

Applies to every switch without a per-switch method, so it can be combined with pinning some switches and
skipping others. Defensible on an isolated out-of-band management network, but anything able to intercept that
network can feed arex whatever it likes — including metrics showing every optic healthy.

## Useful PromQL

```promql
# Uptime
time() - arista_boot_timestamp_seconds

# Memory utilisation — available, not free
(1 - arista_memory_available_bytes / arista_memory_total_bytes) * 100

# Interface utilisation as a percentage of link speed
rate(arista_interface_in_octets_total[5m]) * 8
  / on(switch, interface) arista_interface_speed_bits_per_second * 100

# PSU load %
arista_psu_output_power_watts / arista_psu_capacity_watts * 100

# Fan speed deviation (failing fan indicator)
arista_fan_speed_actual_percent - arista_fan_speed_configured_percent

# BGP session uptime
time() - arista_bgp_peer_state_change_timestamp_seconds

# Prefixes rejected by inbound policy
arista_bgp_peer_prefixes_received - arista_bgp_peer_prefixes_accepted

# Optical receive margin above the optic's own low warning, in dB
arista_transceiver_rx_power_dbm
  - on(switch, interface) group_left
    arista_transceiver_rx_power_threshold_dbm{level="low_warn"}

# Attach model/version to any metric
rate(arista_interface_in_octets_total[5m])
  * on(switch) group_left(model, version) arista_info
```

## Alerting examples

```yaml
groups:
  - name: arista
    rules:
      - alert: SwitchScrapeDown
        expr: arista_scrape_success == 0
        for: 2m

      - alert: SwitchCommandFailing
        expr: arista_command_success == 0
        for: 15m
        annotations:
          summary: "{{ $labels.switch }}: eAPI command {{ $labels.command }} is failing"

      - alert: SwitchMemoryLow
        expr: arista_memory_available_bytes / arista_memory_total_bytes < 0.1
        for: 15m

      - alert: SwitchTemperatureAlert
        expr: arista_temperature_alert == 1
        for: 1m

      - alert: SwitchTemperatureNearOverheat
        expr: |
          arista_temperature_celsius
            > on(switch, sensor) arista_temperature_overheat_threshold_celsius * 0.9
        for: 5m

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
        expr: arista_bgp_peer_up == 0 and arista_bgp_peer_under_maintenance == 0
        for: 2m

      - alert: SwitchInterfaceDown
        expr: arista_interface_link_up == 0
        for: 2m

      - alert: SwitchInterfaceFlapping
        expr: rate(arista_interface_link_status_changes_total[15m]) * 900 > 3
        for: 5m

      - alert: SwitchInterfaceErrors
        expr: rate(arista_interface_in_errors_total[15m]) > 0
        for: 10m

      # Gated on link_up: a populated but dark cage sits at the -30 dBm floor
      # for ever, well below its own low alarm, and would alert permanently.
      - alert: OpticRxPowerLow
        expr: |
          arista_transceiver_rx_power_dbm
            < on(switch, interface) group_left
              arista_transceiver_rx_power_threshold_dbm{level="low_warn"}
          and on(switch, interface) arista_interface_link_up == 1
        for: 10m

      - alert: OpticTemperatureHigh
        expr: |
          arista_transceiver_temperature_celsius
            > on(switch, interface) group_left
              arista_transceiver_temperature_threshold_celsius{level="high_warn"}
        for: 5m

      # Rate of change, never "> 0" on the gauge: the value persists after a
      # repair, so a value-based rule never clears.
      - alert: OpticFECUncorrectedErrors
        expr: rate(arista_phy_fec_uncorrected_codewords_changes_total[15m]) > 0
        for: 5m

      - alert: LinkMacFault
        expr: arista_phy_mac_local_fault == 1 or arista_phy_mac_remote_fault == 1
        for: 5m

      - alert: LinkHighBER
        expr: arista_phy_pcs_high_ber == 1
        for: 5m
```

Two rules above deserve the comments they carry. Both are cases where the obvious version of the rule fires
permanently and gets deleted in its first week:

- **Dark optics.** A cage with an optic installed but nothing plugged in reports `rxPower` at the EOS floor of
  -30 dBm, while transmitting normally. That is below the optic's own low alarm, so an ungated rule alerts for
  ever. Gating on `arista_interface_link_up == 1` keeps the alert on links you actually care about.
- **Repaired fibre.** FEC error values persist after the fault is fixed and the counters are cleared, so
  `arista_phy_fec_uncorrected_codewords > 0` never clears. Alert on the rate of `*_changes_total` instead.
