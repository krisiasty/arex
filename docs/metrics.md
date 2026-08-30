# Metrics reference

Every series arex exposes, what it means, and which ones are worth alerting on. All switch metrics carry a `switch`
label.

All metrics carry a `switch` label. Descriptions and other mutable text live on `_info` metrics rather than on the
counters, so editing a port description does not change a counter's series identity and break `rate()`.

## Scrape health

| Metric | Type | Description |
| --- | --- | --- |
| `arista_scrape_success` | gauge | 1 if the last poll succeeded |
| `arista_scrape_age_seconds` | gauge | Seconds since the last successful poll, -1 if never |
| `arista_command_success` | gauge | 1 if this eAPI command succeeded, labelled by `command` |

Last-known-good data keeps being served while `arista_scrape_age_seconds` is below `stalenessLimit`, so a transient
eAPI failure does not create gaps in every series. `arista_command_success` is per-command: one command rejected by
a platform costs only its own metrics.

## Exporter self-metrics

These describe arex rather than the switch, so they are reported even when a switch is unreachable — which is when
request counts matter most.

| Metric | Type | Description |
| --- | --- | --- |
| `arex_build_info` | gauge | Version, VCS revision and Go version of the running binary. Always 1 |
| `go_*`, `process_*` | — | Go runtime and process metrics: goroutines, heap, GC, memory, open files |
| `arista_eapi_requests_total` | counter | eAPI requests made, by `outcome` and `attempt` |
| `arista_credential_reloads_total` | counter | Password file re-reads after a rejection, by `outcome`: `rotated`, `unchanged`, `failed` |
| `arista_eapi_response_bytes_total` | counter | Total response bytes received from this switch |
| `arista_eapi_request_duration_seconds_total` | counter | Total time spent on eAPI requests to this switch |

The `go_*` and `process_*` families come from the standard Go and process collectors rather than being computed
here: they are read at scrape time and are more accurate than anything this exporter could assemble.
`process_resident_memory_bytes` is the one to watch when scaling out.

`arex_build_info` is the only arex metric with no `switch` label, and that is why it has its own prefix — it describes
the process, not a device. The revision comes from the VCS information the Go toolchain embeds automatically, so a
plain `go build` produces a usable answer; a release can override the version with
`-ldflags "-X github.com/krisiasty/arex/internal/buildinfo.Version=v1.2.3"`, which is what the release does.
The same value answers `arex -version`, so the metric and the binary cannot disagree about what is deployed.

`outcome` is `success`, `eapi_error` (the switch answered and rejected a command), `http_error` (a status such as
401, which applies to every command) or `transport_error`. `attempt` is `batch` for the normal single request
carrying every command, or `retry` for the per-command fallback.

The `attempt` label is what makes retry amplification visible. A switch failing authentication should show exactly
one request per poll interval:

```promql
# requests per poll — should be ~1, not one per command
rate(arista_eapi_requests_total[5m]) * 30

# bandwidth per switch, the number that decides whether to filter interfaces
rate(arista_eapi_response_bytes_total[5m])

# mean request latency
rate(arista_eapi_request_duration_seconds_total[5m])
  / rate(arista_eapi_requests_total[5m])
```

`stalenessLimit` is applied **per command**, not just to the scrape as a whole. Data from a command that fails is
retained so a single rejection does not create a gap, but it is dropped once that command has not succeeded within
the limit. Without this, one command that kept working would hold `arista_scrape_age_seconds` near zero while every
other metric aged indefinitely — arex would report a healthy, current scrape over hours-old readings. So a switch
where some commands work and others do not will show `arista_scrape_success 1`, a small age, `arista_command_success
0` for the failing commands, and no series at all from those commands.

## System

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
| `arista_cpu_irq_percent` | gauge | CPU servicing hardware interrupts |
| `arista_cpu_softirq_percent` | gauge | CPU servicing software interrupts |
| `arista_cpu_steal_percent` | gauge | CPU stolen by the hypervisor, for vEOS |
| `arista_load_avg_1m` / `_5m` / `_15m` | gauge | Load averages |

`arista_memory_free_bytes` and `arista_memory_available_bytes` measure different things and differ substantially —
by about 5.3 GiB on a 16 GB switch. Linux spends idle RAM on page cache, so low *free* memory is the normal healthy
state. Alert on *available*.

The eight CPU modes are all of EOS's, so they sum to 100 and utilisation can be had either as `100 -
arista_cpu_idle_percent` or by summing the busy modes. Treat any single sample as noisy: `show processes top once`
reports a short window, and consecutive samples on an idle switch have been observed 12 points apart. Use
`avg_over_time()` or `arista_load_avg_1m`, which averages over a minute, for anything you alert on.

## Environment

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

## Clock

From `show ntp associations`, polled every minute by default. `show ntp status` is not issued: everything it
reports beyond a bare `synchronised`/`unsynchronised` string disappears from the JSON the moment the switch is
unsynchronised, and the same state is derivable here from the selected peer.

This matters more than it looks. Every timestamp arex exports — boot time, last counter clear, last link
transition, DOM refresh, PSU and fan boot times — is dated by the switch's own clock. If that clock drifts, those
timestamps quietly become wrong, and no other metric reports it.

| Metric | Type | Description |
| --- | --- | --- |
| `arista_ntp_synchronised` | gauge | 1 if the switch has selected an NTP source |
| `arista_ntp_offset_seconds` | gauge | Offset from the selected source — **alert on this** |
| `arista_ntp_peer_info` | gauge | `refid` and `peer_type` labels |
| `arista_ntp_peer_selected` | gauge | 1 if the clock is being steered to this peer |
| `arista_ntp_peer_offset_seconds` | gauge | Offset this peer reports |
| `arista_ntp_peer_delay_seconds` | gauge | Round-trip delay to the peer |
| `arista_ntp_peer_jitter_seconds` | gauge | Dispersion of recent offset measurements |
| `arista_ntp_peer_stratum` | gauge | Stratum the peer reports; 16 means the peer is itself unsynchronised |
| `arista_ntp_peer_poll_interval_seconds` | gauge | How often the switch polls this peer |
| `arista_ntp_peer_reachable_samples` | gauge | Successful polls in the last 8 attempts, 0–8 |
| `arista_ntp_peer_last_received_timestamp_seconds` | gauge | Last reply from this peer |

Per-peer series carry a `peer` label. EOS reports delay, offset and jitter in milliseconds; arex converts them to
seconds.

### Why the offset series has no peer label

`arista_ntp_offset_seconds` is the offset of the selected peer only, and it is absent entirely while the switch is
unsynchronised. That is deliberate, and it is the one thing to understand about this module.

A server that has never answered reports `offset: 0.0`, `delay: 0.0` and a stratum of 16 — its offset is not a
measurement, it is a placeholder. So a rule of the form `abs(arista_ntp_peer_offset_seconds) > 0.1` reads a
completely dead NTP server as the healthiest clock in the fleet. Alert on `arista_ntp_offset_seconds`, which only
exists when there is something real to measure, and use `arista_ntp_synchronised == 0` to catch its absence.

The per-peer offsets are still exported, because watching a peer's offset settle is how you tell a recovering
server from a dead one. Gate any rule on them with `arista_ntp_peer_reachable_samples` or
`arista_ntp_peer_selected`.

### Peers come and go

`peers` reflects ntpd's associations, not the running configuration. A configured server is not guaranteed to have
an entry, so the disappearance of a peer's series is not by itself evidence of anything — and "a configured server
is missing" is not a question these metrics can answer. `arista_ntp_synchronised` is the reliable signal.

`arista_ntp_peer_last_received_timestamp_seconds` is omitted for a peer that has never answered: EOS reports
`-2208988800` there, the NTP epoch of 1900-01-01 in Unix seconds, which is a sentinel rather than a time.

## Hardware tables

From `show hardware capacity`, polled every five minutes by default. This is the blind spot with no substitute:
when a forwarding table fills, entries are dropped rather than queued, and nothing else arex collects would say
why traffic stopped landing.

| Metric | Type | Description |
| --- | --- | --- |
| `arista_hardware_capacity_used` | gauge | Entries used |
| `arista_hardware_capacity_free` | gauge | Entries free in the shared pool — see below |
| `arista_hardware_capacity_limit` | gauge | Entries the table can hold |
| `arista_hardware_capacity_high_watermark` | gauge | Peak used since boot |
| `arista_hardware_capacity_info` | gauge | `shared_features`: what is consuming this row |

Series carry `table`, `feature` and `chip`. All three are needed: a table appears once per feature, and
`MMU_MCAST` appears both against a chip and with none.

Utilisation is `used / limit`. EOS also reports a `usedPercent`, which arex does not export — it is truncated to a
whole number, so a table at 0.978% reports `0` and everything below one percent looks idle.

### Alert on the row, not the table

The tightest row is what fails first. On a real leaf `IFP` sits at 7% overall while two of its twelve TCAM slices
are at 37%, and an ACL that does not fit in a slice is rejected however much room the aggregate has. So the
shipped rule evaluates every row independently rather than aggregating by `table`.

### A row with an empty feature is not reliably a total

For most tables it is: `EFP`, `IFP`, `VFP`, `MAC`, `Host`, `LPM` and `MPLS` all report a `feature=""` row equal to
the sum of their feature rows. Three do not — `NextHop` reports 280 where its features sum to 281, and
`OverlayEcmp` and `UnderlayEcmp` use the empty-feature row for a *different resource* with its own limit, members
against groups. Nothing here is derived from anything else for that reason; every row is exported as EOS reports
it.

### `free` is the pool's, not the row's

`used + free` does not equal `limit` on 15 of 75 rows, because features of one table share a pool. `Host/V6Hosts`
reports `used: 0` against a limit of 147455 with only 147208 free — the difference went to `V4Hosts`. So `free` is
the honest headroom for a shared pool and `limit - used` overstates it.

### Row order means nothing

EOS returns the rows in no stable order; captures from three switches each began with a different one. Nothing in
arex depends on the ordering, and a test asserts that reversing the array produces identical output.

## Overlay: VXLAN and EVPN

Three collect keys, because the parts fail independently and cost wildly different amounts.

| Key | Commands | Default interval | Wire cost |
| --- | --- | --- | --- |
| `vxlan` | `show vxlan vtep`, `show interface vxlan 1`, `show vxlan address-table count` | `pollInterval` | 7.5 kB |
| `evpn` | `show bgp evpn summary`, `show bgp evpn route-type count` | `pollInterval` | 0.7 kB |
| `esi` | `show bgp evpn instance` | `15m` | **19 kB** |

`show interface vxlan 1` fails outright on a switch with no VXLAN interface, so a spine leaves
`vxlan` off rather than absorbing a failed command every poll. `evpn` and `esi` are fine on a
route-reflector spine, which simply reports no instances.

### The data plane

| Metric | Type | Description |
| --- | --- | --- |
| `arista_vxlan_interface_up` | gauge | 1 if the VXLAN interface line protocol is up |
| `arista_vxlan_interface_info` | gauge | `source_interface`, `source_address`, `replication_mode`, `encapsulation` |
| `arista_vxlan_remote_vtep_count` | gauge | Remote VTEPs known — **alert on this** |
| `arista_vxlan_remote_vtep_info` | gauge | `vtep`, `tunnel_types` (comma-joined: `unicast`, `flood`) |
| `arista_vxlan_remote_mac_count` | gauge | Overlay MACs learned from each remote VTEP |
| `arista_vxlan_vni_info` | gauge | VLAN-to-VNI binding |
| `arista_vxlan_vrf_vni_info` | gauge | VRF-to-VNI binding for a routed overlay |
| `arista_vxlan_vlan_flood_vteps` | gauge | Remote VTEPs in a VLAN's flood list |

Alert on the **count**, not the per-VTEP series. EOS reports only VTEPs it knows, so one leaving
the fabric removes its series rather than setting it to zero, and absence is not something a
threshold can be written against.

### The control plane

`arista_bgp_evpn_peer_up`, `_prefixes_received` / `_accepted` / `_advertised`,
`_state_change_timestamp_seconds` and `_info`, all labelled `vrf`, `peer`, `asn` — the same shape
as the IPv4 series, deliberately.

They are **separate series, not extra labels on `arista_bgp_peer_*`**. The same neighbour normally
carries both an IPv4 unicast and an EVPN session, and either can fail while the other stays up. A
healthy underlay session is exactly what makes a dead overlay one hard to notice.

`arista_bgp_evpn_routes{route_type}` counts path entries by type — `auto_discovery`, `mac_ip`,
`imet`, `ethernet_segment`, `ip_prefix_ipv4`, `ip_prefix_ipv6`. Paths, not unique routes: a route
learned from two peers counts twice, so these are not comparable with a filtered per-type count.

### Ethernet segments

| Metric | Type | Description |
| --- | --- | --- |
| `arista_evpn_esi_up` | gauge | 1 if **this switch's own** link into the segment is up |
| `arista_evpn_esi_designated_forwarder` | gauge | 1 if this switch is the elected forwarder |
| `arista_evpn_esi_designated_forwarder_elected` | gauge | 1 if *anyone* is elected |
| `arista_evpn_esi_forwarding_peers` | gauge | Peers forwarding — **alert on this** |
| `arista_evpn_esi_info` | gauge | `interface`, `redundancy_mode` |

All carry `instance` and `esi`. **Both are needed.** DF election runs per VLAN-aware bundle, so one
ESI legitimately has a different forwarder in each bundle it belongs to — the modulus algorithm
spreading the role, not a fault. A metric keyed by ESI alone would report one bundle's answer as
the whole truth.

#### Why three forwarder metrics

`designated_forwarder` alone is ambiguous. A 0 means either "the other leaf is forwarding, all is
well" or "nobody is forwarding, broadcast traffic is going nowhere" — opposite conclusions from the
same value. `_elected` separates them:

| `_designated_forwarder` | `_elected` | Meaning |
| --- | --- | --- |
| 1 | 1 | This switch forwards for the segment |
| 0 | 1 | Its peer forwards. Normal |
| 0 | 0 | **No forwarder at all** |

The third row is only representable because EOS reports an empty `dFPeer.ip` rather than omitting
the field, and swaps `dFElectionAlgorithm` for `dFElectionState: "pending"` while an election has
not settled.

#### Why `up` is not enough

When one leaf of a multihomed pair loses its link, **the survivor still reports the segment up** —
correctly, its own link is fine. Only `forwarding_peers` dropping from 2 to 1 shows, from either
side, that the segment is running on one leg. A rule watching `up` sees nothing wrong from the
switch that still works.

## Interfaces

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

## BGP

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

## Transceivers

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

## PHY

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
| `arista_phy_fec_info` | gauge | `encoding` and `codeword_size` labels — only on links with FEC |
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

## FEC counters are diagnostic, not an alerting source

Field data changed what these are good for, so read this before building rules on them.

On a switch up for 123 days, most FEC counters last changed **16 days after boot and had been frozen for 107 days
since** — including a port sitting at 409,686 corrected and 469 uncorrected codewords. EOS is not sampling these
registers continuously: a value of 409,686 recorded across two observed changes cannot be a per-increment count.
The only ports whose FEC counters had moved recently were the two with by far the highest flap counts, which
suggests the registers are read around link events rather than on a timer.

So neither field is a live error rate:

- `*_changes_total` rises only when EOS re-reads the register, not when errors occur. A `rate()` over it is zero
  almost always, including on a port with hundreds of uncorrected codewords.
- the gauge is a cumulative count that persists after a repair and can be cleared from the CLI, so it goes down as
  well as up.

**Alert on the interface error counters instead.** `arista_interface_in_errors_detail_total{cause="fcsErrors"}` and
`{cause="symbolErrors"}` come from `show interfaces`, which is refreshed on every poll, and
`arista_phy_pcs_high_ber` and `arista_phy_mac_local_fault` are live status bits. Use FEC to *diagnose* a link those
have already flagged: which lane, corrected versus uncorrected, and when it last moved.

If you do want a rule on FEC, gate it on recency so a long-repaired fault does not fire for ever:

```promql
delta(arista_phy_fec_uncorrected_codewords[1h]) > 0

time() - arista_phy_fec_uncorrected_codewords_last_change_timestamp_seconds < 86400
```

Series carry a `phy` label because an interface can have more than one PHY, line side and system side.

Every `*_timestamp_seconds` metric is an epoch reported by the switch, so they are only meaningful if the switch
clock is correct. Keep NTP configured.

---

Back to the [README](../README.md). See also [configuration](configuration.md) for choosing what is collected, and
[PromQL](promql.md) for queries built on these series.
