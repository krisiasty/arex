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
| `arista_eapi_requests_total` | counter | eAPI requests made, by `outcome` and `attempt` |
| `arista_eapi_response_bytes_total` | counter | Total response bytes received from this switch |
| `arista_eapi_request_duration_seconds_total` | counter | Total time spent on eAPI requests to this switch |

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

# Run
./arex -config config.json

# Run with per-request eAPI logging
./arex -config config.json -debug
```

### Debug logging

`-debug` logs one line per eAPI request:

```text
[leaf-1] eapi POST /command-api -> 200 duration=412ms cmds=9 req=350B resp=486.2kB
    proto=HTTP/1.1 conn=reused tls=1.2
[leaf-1] eapi POST /command-api -> 200 duration=38ms cmds=1[show interfaces phy detail]
    req=94B resp=1.1kB conn=reused eapi_error=1002 msg="invalid command"
[leaf-2] eapi POST /command-api -> 401 duration=6ms cmds=9 req=350B conn=new tls=1.2
```

| Field | Meaning |
| --- | --- |
| `duration` | Round trip including reading the response body |
| `cmds` | Number of commands. The list is shown only for small batches, i.e. when a retry is isolating a failure |
| `req` / `resp` | Payload sizes. `show interfaces phy detail` alone runs to hundreds of kilobytes per poll |
| `proto` | Negotiated HTTP version |
| `conn` | `new` or `reused` — see the note below on reused connections |
| `tls` | Negotiated TLS version, on new connections |
| `eapi_error` | JSON-RPC error code. A 200 can still carry an eAPI rejection, so it is separate from the status |
| `error` | Transport failure, when the request never completed |

A **reused** connection can outlive a switch-side configuration change, so if a credential or role change appears
not to take effect, that field is the first thing to check.

Credentials never appear at any verbosity: the Authorization header is not logged, and neither is the password.

Response sizes are worth watching before scaling out — nine commands against one switch is a few hundred kilobytes
every poll interval, and interface-heavy commands dominate it.

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
