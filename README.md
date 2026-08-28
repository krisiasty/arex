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

### Exporter self-metrics

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
`-ldflags "-X github.com/krisiasty/arex/internal/metrics.Version=v1.2.3"`.

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

### FEC counters are diagnostic, not an alerting source

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

arex needs **no elevated privilege**. Every command it issues works at the default privilege level, so do not
grant `privilege 15`:

```text
role prometheus-ro
   10 deny mode config-all command .*
   15 deny command show (running-config|startup-config|tech-support).*
   20 permit command show .*

username prometheus role prometheus-ro secret SHA512 <hash>
```

Rule 15 must precede rule 20: EOS evaluates in sequence and the first match wins, so a broad permit placed first
would swallow it. It covers the `show` commands that dump configuration, which `show .*` would otherwise allow —
`show tech-support` in particular embeds the running configuration. The privilege level already refuses all three,
so this is redundant today; it is here so the role remains safe on its own if the privilege level ever changes.

This role has been verified under enforcement. With command authorization enabled, all nine commands arex issues
are permitted, and everything else — including `enable` — is refused:

```text
sw1>enable
% Authorization denied for command 'enable'
```

That refusal is the important one, and it comes from EOS denying any command no rule matches. Rule 10 is therefore
belt-and-braces rather than load-bearing; it is kept because an explicit statement of intent survives someone
later adding a permit rule that turns out to be broader than they meant.

Rule 20 permits every `show` rather than listing the commands arex issues. An exact list would be tighter, but the
command set grows: two commands were added to arex in a single development cycle, and a list-based role would have
started refusing them. `show .*` still permits privileged reads such as `show running-config` — the default
privilege level is what refuses those, which is why the two settings below work together and neither is sufficient
alone.

**A stock switch restricts a monitoring account far less than it appears to**, and the single setting that fixes
it is command authorization:

| | with command authorization | without it (the default) |
| --- | --- | --- |
| role rules | enforced | **ignored entirely** |
| commands matching no rule | denied | permitted |
| `enable` | denied by the role | **elevates with no password** |

Without it, a monitoring account is unrestricted in practice regardless of how careful the role looks. The
privilege level appears to hold — a privilege-1 user is refused `show running-config` — right up until anyone
types `enable`, and eAPI accepts `enable` as a command like any other, so this is reachable in one request over
the network:

```text
sw1>show running-config
% Invalid input (privileged mode required)
sw1>enable
sw1#show running-config
! ... the entire configuration
```

Setting an `enable secret` closes that specific path and is worth doing as defence in depth, since it still holds
if command authorization is ever turned off. But it is not the fix — enabling authorization with a role that
permits only what is needed is.

**Verify.** With authorization enabled and the role above, each of these must be refused:

```bash
# 1. a privileged read, unelevated
curl -k -u prometheus:<password> https://<switch>/command-api \
  -d '{"jsonrpc":"2.0","method":"runCmds","params":{"version":1,"cmds":["show running-config"],"format":"json"},"id":1}'

# 2. the same read, elevating first -- catches a missing enable secret
curl -k -u prometheus:<password> https://<switch>/command-api -d '{"jsonrpc":"2.0","method":"runCmds",
  "params":{"version":1,"cmds":["enable","show running-config"],"format":"json"},"id":1}'

# 3. a command the role forbids -- catches authorization being disabled
curl -k -u prometheus:<password> https://<switch>/command-api \
  -d '{"jsonrpc":"2.0","method":"runCmds","params":{"version":1,"cmds":["configure"],"format":"json"},"id":1}'
```

A refusal looks like this, and the useful part is in `data`, not `message`:

```json
{"code":1002,"message":"CLI command 1 of 1 'show running-config' failed: invalid command",
 "data":[{"errors":["Invalid input (privileged mode required)"]}]}
```

If any of them returns configuration instead, treat the credentials in your arex config as switch administrator
credentials, because that is what they are. They sit in a plaintext file readable by whatever runs arex, and arex
never needs any of that access.

Command authorization has to be enabled globally before role rules are consulted at all; until it is, a role is
inert no matter what it says. Check
`show running-config section aaa`, `show aaa` and `show users accounts detail`. Enabling it starts enforcing roles
for **every** account on every access path at once, including the session you type it in, so do it with a second
privileged session open and a way to roll back.

### Reference configuration

Everything below has been verified on an EOS 4.35.4M switch: eAPI reachable in the management VRF, all nine of
arex's commands permitted, and everything else refused.

```text
management api http-commands
   no shutdown
   protocol https
   no protocol http
   !
   vrf management
      no shutdown
!
aaa authorization exec default local
aaa authorization commands all default local
!
role prometheus-ro
   10 deny mode config-all command .*
   15 deny command show (running-config|startup-config|tech-support).*
   20 permit command show .*
!
username prometheus role prometheus-ro secret sha512 <hash>
```

Apply the `aaa` lines last and with care: they subject **every** account to role checks the moment they commit,
including the session you are typing in. Have a second privileged session open, confirm your administrative
accounts resolve to a role that permits configuration, and keep a way to roll back.

Then confirm the result. As `prometheus`, all of these must be refused:

```text
sw1>enable
% Authorization denied for command 'enable'
sw1>show running-config
% Invalid input (privileged mode required)
sw1>show startup-config
% Invalid input (privileged mode required)
sw1>show tech-support
% Incomplete command (privileged mode required)
```

With rule 15 in place the three configuration dumps are refused by the role instead, so they report
`Authorization denied for command …`. That change in wording is itself the check that rule 15 is working: the
message identifies which control refused, which is worth knowing whenever something unexpected is denied:

| message | mechanism |
| --- | --- |
| `Authorization denied for command …` | command authorization, i.e. the role |
| `Invalid input (privileged mode required)` | privilege level |

`enable` is refused by the role, and the configuration dumps by the privilege level. The two are independent and
both are load-bearing: the role stops elevation, and the privilege level stops privileged reads that `show .*`
would otherwise permit.

Finally, confirm arex itself is unaffected — its role becomes live at the same moment:

```bash
curl -s localhost:9100/metrics | grep arista_command_success
```

All nine at 1. Any at 0 and `-debug` reports the refusal verbatim in its `cause=` field.

## Running

```bash
# Build
go build -o arex .

# Validate a config without starting: exits non-zero and says what is wrong
./arex -check -config config.json

# Run
./arex -config config.json

# Run with per-request eAPI logging
./arex -config config.json -debug

# Print the licences of everything linked into this binary
./arex -licenses
```

`-check` loads the config and builds each switch's client without connecting, so it catches an unreadable CA bundle,
a malformed certificate pin and a credential file that has stopped being readable — the failures that otherwise
surface as a service that starts and immediately exits. [deploy/arex.service](deploy/arex.service) runs it as
`ExecStartPre`, so a bad edit stops the restart instead of taking the exporter down.

### Endpoints

| Path | Purpose |
| --- | --- |
| `/metrics` | Prometheus exposition; `?target=`, `?module=` and `?interface=` narrow it |
| `/livez` | 200 while the poll loop is cycling, 503 if it has stalled |
| `/readyz` | 200 once every switch has been polled at least once |
| `/status` | JSON detail per switch |
| `/health` | alias for `/livez` |

**Liveness** fails only if a poller has completed no attempt for ten poll intervals. A switch that is unreachable
or refusing credentials is not a liveness failure: restarting arex would not fix it, and killing a working process
over one bad device trades a visible per-switch failure for a restart loop. A process younger than that window is
live even before its first poll, since pollers start at staggered offsets.

**Readiness** means `/metrics` covers the whole configured set, not that the switches are healthy. A switch with
bad credentials fails every poll indefinitely, and tying readiness to switch health would take a working exporter
out of service while hiding the metrics that identify the broken device. Per-switch health is
`arista_scrape_success`.

**`/status`** reports each switch's last success and attempt, data age, whether it is stale, the commands it
collects and any that failed, with the switch's own error text. It carries no credentials.

```json
{
  "live": true,
  "ready": true,
  "uptime": "4m12s",
  "switches": [
    {
      "switch": "leaf-1",
      "scrapeOk": true,
      "lastSuccessAt": "2026-08-28T11:19:18Z",
      "ageSeconds": 5.871,
      "commands": ["show version", "show interfaces", "..."]
    }
  ]
}
```

### Scraping one switch at a time

By default `/metrics` returns everything: every switch plus arex's own metrics. That is the simplest setup and
needs no relabeling.

A `target` parameter narrows the response, which is optional:

| Request | Returns |
| --- | --- |
| `/metrics` | every switch, plus arex's own metrics |
| `/metrics?target=leaf-1` | that switch only |
| `/metrics?target=internal` | arex's own metrics only: `arex_*`, `go_*`, `process_*` |

A switch is addressable by its `name`, its configured `host`, or that host without the scheme — so relabeling can
use whichever identifier a job already has:

```bash
curl 'http://arex:9100/metrics?target=leaf-1'
curl 'http://arex:9100/metrics?target=https://10.36.48.15'
curl 'http://arex:9100/metrics?target=10.36.48.15'
```

An unknown target returns **400**. An empty body would leave Prometheus reporting a successful scrape with no
series, so a typo in relabeling fails visibly instead. A host shared by two switches is ambiguous and is not
addressable; use the names, which are unique.

`internal` is reserved, and a switch by that name is rejected at startup rather than becoming an ambiguous query.

**Filtering changes only what a scrape renders.** Collection is unaffected: pollers run on their own schedule, so
one poll of a switch serves any number of scrapers however they are configured. That matters with an HA Prometheus
pair — collecting on demand per scrape would double the switch-side cost, which is 1.4 seconds of eAPI time per
poll on a 32-port leaf.

A per-target scrape config, if you want each switch to be its own Prometheus target:

```yaml
scrape_configs:
  - job_name: arex-switches
    metrics_path: /metrics
    static_configs:
      - targets: [leaf-1, leaf-2, leaf-3]
    relabel_configs:
      - source_labels: [__address__]
        target_label: __param_target
      - source_labels: [__param_target]
        target_label: switch_target
      - target_label: __address__
        replacement: arex:9100

  - job_name: arex-internal
    metrics_path: /metrics
    params:
      target: [internal]
    static_configs:
      - targets: [arex:9100]
```

### Ad-hoc filtering

`module` and `interface` narrow a response further. These are for investigating a switch by hand, not for scrape
configuration — they filter what is rendered, and collection is untouched. What arex asks the switch for is decided
by `collect` and `interfaceScope`, which are deployment settings; these are questions.

| Request | Returns |
| --- | --- |
| `?target=leaf-1&module=power` | PSU metrics for that switch |
| `?target=leaf-1&interface=Ethernet4/1` | interface, transceiver and PHY metrics for that port |
| `?target=leaf-1&interface=Ethernet4/1&module=phy` | just the PHY view of that port |
| `?module=power` | PSU metrics across every switch |

`module` is one of the `collect` keys, plus `version`.

`interface` narrows to the families that *carry* an interface label — interface counters, transceiver and PHY.
Asking about a port and being handed power supply readings would not be an answer, so the narrowing is implied
rather than layered on top of everything.

Scrape health survives every filter. `arista_scrape_success` and `arista_scrape_age_seconds` describe the poll
rather than the data, and suppressing them would make a filtered view of a healthy switch indistinguishable from a
dead one.

Both reject mistakes rather than returning an empty body:

```console
$ curl 'localhost:9100/metrics?target=leaf-1&module=nope'
unknown module "nope": expected one of bgp, cooling, interfaces, phy, power,
processes, temperature, transceiver, version

$ curl 'localhost:9100/metrics?target=leaf-1&interface=Ethernet99/9'
no interface "Ethernet99/9" in the last poll of switch "leaf-1"
```

Interface names are checked against the last poll rather than against configuration, because interfaces are
discovered rather than declared. `target=internal` accepts neither filter, since neither means anything for
process metrics.

The narrowing is substantial, which is the point when reading output by hand:

```text
no filter                                       2003 samples
target=leaf-1                                    490
target=leaf-1&module=phy                         106
target=leaf-1&interface=Ethernet4/1               45
target=leaf-1&module=power                        32
```

### Logging

Logs are JSON Lines on stdout, one object per event, with UTC millisecond timestamps so records from different
hosts sort together. Debug raises the level rather than changing the format: a format that varied with verbosity
could not be parsed by anything downstream, and the per-request output is only worth having if it can be queried.

```json
{"time":"2026-08-28T09:19:18.537Z","level":"INFO","msg":"arex starting",
 "version":"v0.0.0-20260828085310-7a01577d0334+dirty","revision":"7a01577d0334",
 "go_version":"go1.27.0","switches":3,"poll_interval":"30s","staleness_limit":"90s","debug":false}
```

### Shutdown

arex stops cleanly on `SIGINT` or `SIGTERM`: pollers stop, and the HTTP server is given up to five seconds to
finish a scrape already in progress. Without that grace period a restart can cut a `/metrics` response mid-write,
which Prometheus records as a *failed scrape* rather than a clean gap — a distinction that matters if you alert on
scrape failures.

A second signal exits immediately. A poll already in flight is not interrupted; the eAPI request has its own
`scrapeTimeout`, and the process exits once the server has drained.

### Poll staggering

Pollers are started at increasing offsets so a fleet is not polled all at once. The first switch polls
immediately, and each subsequent one starts about three seconds later:

```text
[sw-a] starting poller (interval: 30s)
[sw-b] starting poller (interval: 30s, first poll in 3.207s)
[sw-c] starting poller (interval: 30s, first poll in 6.294s)
[sw-d] starting poller (interval: 30s, first poll in 9.926s)
```

Three seconds is enough separation because a full nine-command poll of a 32-port leaf takes well under two
seconds. If the fleet is large enough that three-second spacing would push the last poller past one interval, the
spacing shrinks so every switch still reports within one interval of startup.

Offsets are assigned by position rather than drawn at random. Random offsets only spread pollers on average: in one
field run, three switches drew 22.9s, 20.4s and 21.2s from a 30-second interval and polled within 2.4 seconds of
each other — the exact outcome the staggering exists to prevent. A small variation remains so that two arex
instances polling the same switches do not align, bounded well inside the spacing so it cannot reorder pollers back
into a cluster.

### Debug logging

Either `"debug": true` in the config or `-debug` on the command line adds one record per eAPI request. The flag
wins when given, so a deployment can be started verbosely without editing its config — or quietly with
`-debug=false` when its config leaves debug on. An absent flag does not override the config.

```json
{"time":"2026-08-28T09:19:18.545Z","level":"DEBUG","msg":"eapi request","switch":"leaf-1",
 "method":"POST","path":"/command-api","duration_ms":1435,"cmds":9,"req_bytes":350,
 "status":200,"resp_bytes":655360,"proto":"HTTP/1.1","conn":"reused","tls":"1.3"}
```

| Attribute | Meaning |
| --- | --- |
| `duration_ms` | Round trip including reading the response body |
| `cmds` | Number of commands; a `commands` array is added for batches of three or fewer |
| `req_bytes` / `resp_bytes` | Payload sizes; `show interfaces phy detail` alone runs to hundreds of kilobytes |
| `proto` | Negotiated HTTP version |
| `conn` | `new` or `reused` — see the note below |
| `tls` | Negotiated TLS version, on new connections |
| `eapi_error` / `eapi_message` / `eapi_cause` | JSON-RPC code, summary, and the cause from `error.data` |
| `error` | Transport failure, when the request never completed |

The `commands` array appears only for small batches because that is when a per-command retry is isolating a
failure; on a full batch it would be the same list on every record, thousands of times a day.

A 200 can still carry an eAPI rejection, so `eapi_error` is separate from `status`.

A **reused** connection can outlive a switch-side configuration change, so if a credential or role change appears
not to take effect, that attribute is the first thing to check.

Credentials never appear at any verbosity: the Authorization header is not logged, and neither is the password.

## Docker

```bash
docker build -t arex .
docker run -d \
  -p 9100:9100 \
  -v /etc/arex/config.json:/etc/arex/config.json:ro \
  -v /etc/arex/switch-password:/etc/arex/secret/password:ro \
  arex
```

The image is `distroless/static-debian13`, pinned by digest: no shell, no package manager, nothing writable, and it runs
as UID 65532. That satisfies `readOnlyRootFilesystem` and `runAsNonRoot`, and means mounted files must be readable
by that UID. It also means `docker exec` gets you nothing — use `-debug`, `/status` and the metrics instead.

`Dockerfile` builds from source for local use. Releases are built from `Dockerfile.goreleaser`, which copies an
already-built binary; both pin the same base digest, so a local image and a released one are built on the same
thing.

## Deployment

[deploy/arex.service](deploy/arex.service) is a hardened systemd unit — `DynamicUser`, `ProtectSystem=strict`, no
capabilities, and the password delivered through `LoadCredential=`.
[deploy/kubernetes.yaml](deploy/kubernetes.yaml) is a single-replica Deployment with a ConfigMap, a Secret volume,
probes wired to `/livez` and `/readyz`, and a ServiceMonitor.

Both are commented with the reasoning; see [Credentials](#credentials) for why one replica and why no `subPath`.

## Install

Released binaries and container images are published for `linux/amd64` and `linux/arm64` on every `v*` tag.

```bash
VERSION=v0.1.0

# Container: v-prefixed like the tag. vX.Y follows the latest patch, and
# latest follows the newest release.
docker pull ghcr.io/krisiasty/arex:${VERSION}

# Binary
curl -fsSL "https://github.com/krisiasty/arex/releases/download/${VERSION}/arex_${VERSION}_linux_amd64.tar.gz" \
  | tar xz
```

The tarball carries the binary, `LICENSE`, `NOTICE`, `THIRD_PARTY_NOTICES`, the README, and the `deploy/`
examples, and `checksums.txt` on the release page covers all of them. Darwin builds exist too, for running it
against a lab switch by hand.

Each tag is a multi-platform manifest, so Docker picks the architecture. Three tags per release: `vX.Y.Z` for the
exact version, `vX.Y` following its latest patch, and `latest` following the newest release.

`arex_build_info` reports the released version, because the release sets it at link time; a plain `go build`
reports `(devel)` and the VCS revision the toolchain embeds.

## Licensing

arex is Apache-2.0. `LICENSE` and `NOTICE` apply to arex itself.

`internal/legal/THIRD_PARTY_NOTICES` reproduces the licence texts, copyright notices and upstream `NOTICE`
contents of everything linked into the binary. It is embedded, so a container with no shell can still answer for
itself:

```bash
arex -licenses
```

It is generated, not maintained by hand:

```bash
./hack/gen-notices.sh
```

The script checks and collects licences for each released target separately — build constraints mean a dependency
can enter the graph on one platform and not another, so a single `GOOS` would under-report. It fails closed if a
dependency's licence is forbidden, restricted or unknown, and also if one carries source-redistribution
obligations, which a text notice cannot satisfy. CI regenerates and diffs the file, so a dependency added without
regenerating fails the build rather than shipping an image whose notices are incomplete.

## Configuration

See `config.example.json`. All durations are Go duration strings (`30s`, `1m`, etc.).

The example points `passwordFile` at a systemd credential path, so trying it locally means either creating that
file or replacing it with an inline `password`.

| Field | Default | Description |
| --- | --- | --- |
| `listenAddress` | `:9100` | Address to serve `/metrics` on |
| `pollInterval` | `30s` | How often to poll each switch |
| `scrapeTimeout` | `10s` | eAPI request timeout |
| `tlsSkipVerify` | `false` | Skip TLS verification for switches with no per-switch method set. See [TLS](#tls) |
| `stalenessLimit` | `90s` | Stop emitting metrics if data is older than this |
| `debug` | `false` | Log one record per eAPI request. See [Debug logging](#debug-logging) |
| `passwordFile` | — | Credential file for every switch that does not name its own. See [Credentials](#credentials) |
| `collect` | required | Optional command groups to collect. See [below](#choosing-what-to-collect) |
| `switches` | required | List of switch connection configs |

### Choosing what to collect

Collection is **opt-in**. `show version` is always issued — it provides `arista_info`, which everything else joins
against — and every other command group must be enabled explicitly:

```json
"collect": {
  "processes":   { "enabled": true },
  "temperature": { "enabled": true },
  "power":       { "enabled": true },
  "cooling":     { "enabled": true },
  "interfaces":  { "enabled": true },
  "bgp":         { "enabled": true },
  "transceiver": { "enabled": true, "interval": "5m" },
  "phy":         { "enabled": true, "interval": "15m" }
}
```

Every group is an object with a required `enabled` and an optional `interval`. `enabled` is required for the same
reason the `collect` block itself is: an absent value would have to mean something, and either meaning is a guess
about intent. See [How often to poll each module](#how-often-to-poll-each-module) for what `interval` is for and
why the defaults differ.

An absent `collect` block is a configuration error rather than a default, because defaulting it either way would
silently change what an existing deployment gathers. An unknown key is also an error, so a typo cannot quietly
disable a group, and so is an unknown field inside a group's object.

A switch may carry its own `collect`, which **replaces** the top-level set rather than merging with it — what you
see in a switch's block is exactly what it collects, including its intervals.

This is the main lever on cost. Measured on a 32-port leaf, one poll of everything is roughly 654 kB and occupies
eAPI for 1.4 seconds; three commands account for 88% of it, at about 30% each:

| command | share of a poll |
| --- | --- |
| `show interfaces phy detail` | 30% |
| `show interfaces` | 30% |
| `show interfaces transceiver detail` | 28% |
| the other six combined | 12% |

So disabling `phy` removes roughly a third of both the bytes and the switch-side work, and disabling `phy` and
`transceiver` together removes about 58%. Lengthening their intervals achieves most of the same saving while
keeping the metrics.

### How often to poll each module

Not every metric is worth reading at the same rate. Interface counters and link state change in seconds; optical
power drifts over days; the PHY's FEC registers, as measured on real hardware, are not refreshed on a timer at
all. Polling everything at `pollInterval` therefore spends most of the switch's eAPI budget re-reading values that
cannot have changed.

Each group has a default interval, which an explicit `interval` overrides:

| group | default | why |
| --- | --- | --- |
| `interfaces` | `pollInterval` | Error counters and link state are the fastest-moving signals arex collects, and the primary detection source. |
| `bgp` | `pollInterval` | Session state changes in seconds. |
| `processes`, `temperature`, `power`, `cooling` | `pollInterval` | Cheap — 12% of a poll for all six together, including `show version`. |
| `transceiver` | `5m` | Optical values are slowly-varying analogue readings; see below. |
| `phy` | `15m` | Its unique signal is FEC, which EOS does not refresh on a timer; see below. |

`pollInterval` is a floor: the poll loop cannot tick faster than it, so an `interval` shorter than `pollInterval`
is rejected at startup rather than silently clamped. Intervals need not be multiples of `pollInterval` — a group
is issued on the first tick at or after its interval elapses, so `"interval": "45s"` with a 30-second poll runs
every 60 seconds in practice.

Batching is preserved. Each tick sends the groups that are due as a single `runCmds` request, so a slow group
costs *fewer* requests, not more: with the defaults above and a 30-second poll, most ticks carry seven commands,
every tenth also carries `transceiver`, and every thirtieth carries everything.

#### Why one batch rather than spread requests

Sending each command separately, spaced out inside the poll interval, would lower the peak the switch sees for the
same total work. It was measured rather than argued, and the peak turned out not to be there.

Two samples of `show processes top once` typed at the console 44 seconds apart, on a switch arex was polling every
30 seconds:

| | idle | busy | load avg 1m / 5m / 15m |
| --- | --- | --- | --- |
| first | 85.2% | 14.8% | 1.04 / 1.08 / 1.02 |
| second | 96.9% | 3.0% | 0.98 / 1.08 / 1.02 |

Twelve points of swing between two hand-typed samples, and a load average flat at ~1.0 across all three windows.
The switch's own agents account for the load; it is steady, not bursty. Samples arex itself reports average 89.8%
idle over the same period, statistically indistinguishable from the console's 91.1% — and arex samples CPU from
*inside* its own batch, so if the batch cost 10 points those samples would sit systematically lower. They do not.

Against that, one request per command would multiply authentications by the command count. A switch that rejects
credentials would then fail nine times per poll instead of once, which is how account lockouts happen — the
amplification `arista_eapi_requests_total{attempt=...}` exists to make visible. So batching stays, and the peak is
addressed by the intervals above: the full 1.4-second batch now runs every fifteen minutes rather than every
thirty seconds.

`stalenessLimit` widens automatically to `3 × interval` for any group polled less often than the limit itself.
Without that, enabling a 15-minute group under the default 90-second limit would publish its metrics once and then
suppress them for ever. An explicit `stalenessLimit` longer than that is still honoured — the per-group bound
raises the floor, it does not override a deliberate choice.

arex logs the resolved schedule for each switch at startup, which is the only way to confirm what a deployment
actually polls:

```json
{"level":"INFO","msg":"switch schedule","switch":"leaf1",
 "modules":"show version=30s show interfaces=30s show ip bgp summary vrf all=30s
             show interfaces transceiver detail=5m0s show interfaces phy detail=15m0s"}
```

#### What actually detects a degrading link

The defaults come from asking what each group contributes to early detection, which is the main reason to collect
transceiver and PHY data at all.

**Fast, and the reason `interfaces` stays at `pollInterval`.** Hard failures and error bursts show up first in
`show interfaces`: `arista_interface_link_up`, `arista_interface_in_errors_detail_total{cause="fcsErrors"}` and
`{cause="symbolErrors"}`, and the flap counters. These are genuine monotonic counters refreshed on every read, so
`rate()` and `increase()` behave normally on them. Everything else is confirmation or diagnosis.

**Transceiver DOM — trends, not samples.** The useful signals, in rough order of value:

| metric | what it tells you | how it moves |
| --- | --- | --- |
| `arista_transceiver_rx_power_dbm` | The single best degradation signal. Falls as a connector contaminates, a fibre bends or ages, or the far-end laser weakens. | Fractions of a dB over days to weeks |
| `arista_transceiver_tx_bias_milliamps` | Leading indicator of laser aging: bias current rises to hold output power constant, *before* Tx power visibly falls. | Slowly, monotonically, over months |
| `arista_transceiver_tx_power_dbm` | Local laser output. A fall confirms what bias current predicted. | Slowly, then suddenly at end of life |
| `arista_transceiver_temperature_celsius` | High optic temperature accelerates aging and causes errors directly. | With ambient and load, over minutes |
| `arista_transceiver_voltage_volts` | Supply rail. Rarely moves; catches board-level power problems. | Almost never |

The thresholds (`*_threshold_*`, labelled `highAlarm`, `lowAlarm`, `highWarn`, `lowWarn`) are the optic's own
limits, read from its EEPROM. They are what makes the readings actionable, because acceptable Rx power depends on
the optic and the link: compute margin rather than comparing against a fixed number.

Two consequences for interval choice. First, a value that moves by fractions of a dB per week does not need
sampling every 30 seconds — 5 minutes still gives 288 samples a day, which is far more than any `deriv()` or
`avg_over_time()` window needs. Second, a *sudden* Rx power drop is not something you detect from DOM anyway: the
link goes down or starts erroring, and `interfaces` catches that on the next 30-second poll. DOM tells you which
end and by how much, moments later.

One field caveat: an optic with no light — an unused or administratively down port — reads about **-30 dBm** and
therefore breaches its own low alarm. Gate any rule on `arista_interface_link_up`, or every dark port alerts.

**PHY — diagnosis, not detection.** PHY splits into two kinds of series, and neither justifies a fast interval:

- *Live status bits* — `arista_phy_pcs_high_ber`, `arista_phy_mac_local_fault`, `arista_phy_pcs_block_lock`,
  `arista_phy_fec_alignment_lock`. These do move quickly, but a link bad enough to trip them is already erroring
  in `show interfaces`, which is polled ten to thirty times more often for a third of the cost.
- *FEC counters* — the data that exists nowhere else, and the actual reason to collect PHY. But EOS does not
  refresh these registers on a timer: on a switch up for 123 days, most had last changed 16 days after boot and
  had been frozen for 107 days. Polling them every 30 seconds re-reads an unchanged register 2,880 times a day.
  See [FEC counters are diagnostic, not an alerting source](#fec-counters-are-diagnostic-not-an-alerting-source).

So PHY is what you consult once something else has flagged a link — which lane, corrected versus uncorrected, and
when the register last moved. Fifteen minutes is ample for that, and the `*_last_change_timestamp_seconds` series
tell you when the change happened regardless of when arex noticed.

#### What the defaults cost

Measured: a full poll is 654 kB and 1.4 s of eAPI time, and the full set at 30 seconds runs the eAPI process at a
5.0% duty cycle. Projected from those measurements, per switch per hour with a 30-second `pollInterval`:

| schedule | requests | bytes | eAPI time |
| --- | --- | --- | --- |
| everything at 30s | 120 | ~78 MB | ~168 s |
| defaults (`transceiver` 5m, `phy` 15m) | 120 | ~36 MB | ~77 s |

Roughly a 54% reduction in both, while still collecting every series. The request count is unchanged because due
groups share one batch — the batches simply get smaller.

### Limiting which interfaces are polled

`interfaceScope` is passed to the switch verbatim as the interface argument of the three interface commands:

```json
{ "interfaceScope": "Ethernet1/1-4,Ethernet29/1-4" }
```

produces `show interfaces Ethernet1/1-4,Ethernet29/1-4`, and the same scope on the `transceiver detail` and
`phy detail` variants. Commands that take no interface argument are untouched.

Verbatim, because EOS accepts forms that are not worth modelling, and the details matter:

- a **subinterface range** tolerates gaps — `Ethernet29/1-4` on a cage that is not broken out returns just
  `Ethernet29/1`, with no error, so a scope survives breakout changes
- a range **before** the slash is rejected outright: `Ethernet1-32/1-4` is invalid
- a **non-existent cage** fails the whole command: `Ethernet1/1,Ethernet99/1` returns `% Invalid input`

That last one is the useful failure. A typo in a scope takes down its three commands loudly, reporting
`arista_command_success 0` for each with the switch's own complaint in the log, rather than quietly monitoring
nothing. The other six
commands keep working, and the per-command staleness bound removes the affected series.

The `command` label on `arista_command_success` stays the unscoped name, so switches with different scopes do not
each produce their own series for the same logical command.

Per-switch fields:

| Field | Description |
| --- | --- |
| `host` | Scheme and address of the switch, e.g. `https://10.10.0.11` |
| `username` | eAPI username |
| `password` | eAPI password, inline. See [Credentials](#credentials) |
| `passwordFile` | File to read the password from, instead of `password` |
| `name` | Value for the `switch` label on every metric. Optional, falls back to `host` |
| `collect` | Overrides the top-level collect set for this switch, wholesale |
| `interfaceScope` | Interface argument for the three interface commands, passed verbatim |
| `caFile` | PEM bundle to verify this switch's certificate against. See [TLS](#tls) |
| `pinnedCertSha256` | SHA-256 of this switch's leaf certificate. See [TLS](#tls) |

Point `host` at the switch's management address — on a typical switch that address lives in the `management` VRF.
Keep `name` unique across switches: two entries sharing one label collapse into a single metric series.

There is no VRF setting in arex's own config. It runs off-box, so the VRF is purely a switch-side concern, handled
by the `vrf management` stanza in [switch configuration](#switch-configuration).

### Credentials

`password` puts the secret in the config file, which is fine for a quick test and wrong for anything else.
`passwordFile` points at a file instead:

```json
{
  "passwordFile": "/run/credentials/arex.service/switch-password",
  "switches": [
    { "host": "https://10.10.0.11", "username": "prometheus", "name": "leaf1" },
    { "host": "https://10.10.0.12", "username": "prometheus", "name": "leaf2" }
  ]
}
```

A fleet normally shares one monitoring account, so the top-level `passwordFile` covers every switch. A switch may
name its own `passwordFile`, or carry a `password` inline, and either wins over the fleet default. Setting both
`password` and `passwordFile` on one switch is an error rather than a precedence rule nobody will remember, and a
switch with no credential at all is rejected at startup.

**Trailing line endings are stripped.** `echo secret > file` appends a newline, and EOS rejects a password with one
attached — a failure that looks exactly like a wrong password. Only `\r` and `\n` are removed; a trailing space
could conceivably be part of a real secret.

The file is read at startup, so a missing path, an unreadable file or one holding only a newline fails immediately
with the switch named, rather than surfacing as an unexplained scrape failure on the first poll. A file readable
beyond its owner is a warning, not an error — the External Secrets Operator mounts secrets `0644` by default, and
refusing to start would be worse than saying so:

```json
{"level":"WARN","msg":"configuration warning",
 "detail":"config: switch[0] (leaf1): passwordFile /etc/arex/pw is mode 0644, readable beyond its owner"}
```

#### Rotation is handled without a restart

When a switch rejects the credentials with a 401, arex re-reads the password file before giving up. If the secret on
disk has changed, it retries the request once with the new one; the poll succeeds and nothing is lost but a single
rejected request. That is what makes a Vault rotation propagate on its own: the secret store updates the file, and
arex picks it up on its next poll.

If the secret has **not** changed, arex does not retry. The credential is simply wrong, and a second request would
double the failed authentications a locked-out account sees — the same amplification the per-command retry
classification exists to prevent. A wrong password therefore costs exactly one request per poll, no matter how long
it stays wrong.

A read failure during reload keeps the previous secret. A partial write or a remounted volume would otherwise turn a
transient glitch into an authentication failure against a switch that was working a moment ago.

Watch `arista_credential_reloads_total`:

```promql
# rotations picked up automatically
increase(arista_credential_reloads_total{outcome="rotated"}[1h]) > 0

# the credential is wrong, not stale — someone has to fix it
rate(arista_credential_reloads_total{outcome="unchanged"}[15m]) > 0

# the file itself cannot be read: check the mount
increase(arista_credential_reloads_total{outcome="failed"}[15m]) > 0
```

#### systemd

Use `LoadCredential=`, which puts the secret on tmpfs as `0400` owned by the service user and does **not** pass it
to child processes. The runtime path is stable, so it can be named directly in the config:

```ini
[Service]
LoadCredential=switch-password:/etc/arex/switch-password
ExecStart=/usr/local/bin/arex -config /etc/arex/config.json
```

The credential then appears at `/run/credentials/arex.service/switch-password`. See
[deploy/arex.service](deploy/arex.service) for a hardened unit.

#### Kubernetes

Mount the secret as a volume and point `passwordFile` at the mounted path. With the External Secrets Operator, the
`ExternalSecret` syncs Vault into a `Secret` and the kubelet updates the mounted file in place — no pod restart, and
arex's reload-on-401 completes the loop.

**Do not mount it with `subPath`.** A `subPath` mount is populated once at pod start and is never updated, so
rotation silently stops working. The same applies to a `ConfigMap` holding the config file. See
[deploy/kubernetes.yaml](deploy/kubernetes.yaml).

Run **one replica**. arex has no leader election, so two replicas poll every switch twice — doubling eAPI load — and
Prometheus then holds two series per switch differing only by `pod`, which quietly double-counts any aggregation.
Use `strategy: Recreate` for the same reason: a rolling update briefly runs both.

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

      # Interface counters, not FEC: these refresh every poll, whereas FEC
      # registers on the tested switch were static for 107 days. See
      # "FEC counters are diagnostic, not an alerting source".
      - alert: LinkFCSErrors
        expr: rate(arista_interface_in_errors_detail_total{cause="fcsErrors"}[15m]) > 0
        for: 10m

      # FEC as a rule at all is weak; if you want one, require the counter to
      # have actually moved recently rather than testing the gauge for > 0.
      - alert: OpticFECUncorrectedErrors
        expr: |
          delta(arista_phy_fec_uncorrected_codewords[1h]) > 0
          and on(switch, interface) arista_interface_link_up == 1
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
- **Repaired fibre.** FEC error values persist after the fault is fixed, so
  `arista_phy_fec_uncorrected_codewords > 0` never clears. Requiring `delta()` over the gauge instead means the
  rule fires only when the counter actually moves. Note that FEC is a weak alerting source in general — see
  [FEC counters are diagnostic](#fec-counters-are-diagnostic-not-an-alerting-source).
