# arex's configuration file

The config file reference: what to collect, how often, which interfaces, and where credentials come from.

See `config.example.yaml`. All durations are Go duration strings (`30s`, `1m`, etc.).

The file is YAML. JSON is accepted too, since JSON is valid YAML — the same parser reads both, so there is no
extension rule and an older JSON config keeps working. YAML is the default because a Kubernetes ConfigMap can hold
it natively, instead of a JSON blob embedded in YAML, and because a config people have to reason about is worth
being able to comment.

A misspelled top-level key is an error rather than a setting that silently does nothing, which is the same rule the
`collect` block already followed.

Values that look like they need quoting mostly do not: `:9100`, `https://10.10.0.11:8443`, `30s` and an
`A1:B2:...` fingerprint all parse as strings unquoted, and a switch named `yes` stays a string. A switch `name`
that reads as a *number* is rejected, since it could not become a metric label.

The example points `passwordFile` at a systemd credential path, so trying it locally means either creating that
file or replacing it with an inline `password`.

| Field | Default | Description |
| --- | --- | --- |
| `listenAddress` | `:9100` | Address to serve `/metrics` on |
| `pollInterval` | `30s` | How often to poll each switch |
| `scrapeTimeout` | `10s` | eAPI request timeout |
| `tlsSkipVerify` | `false` | Skip TLS verification for switches with no per-switch method set. See [TLS](tls.md) |
| `stalenessLimit` | `90s` | Stop emitting metrics if data is older than this |
| `debug` | `false` | Log one record per eAPI request. See [Debug logging](operations.md#debug-logging) |
| `passwordFile` | — | Credential file for every switch that does not name its own. See [Credentials](#credentials) |
| `collect` | required | Optional command groups to collect. See [below](#choosing-what-to-collect) |
| `switches` | required | List of switch connection configs |

## Choosing what to collect

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

## How often to poll each module

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

### Why one batch rather than spread requests

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

### What actually detects a degrading link

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
  See [FEC counters are diagnostic, not an alerting
source](metrics.md#fec-counters-are-diagnostic-not-an-alerting-source).

So PHY is what you consult once something else has flagged a link — which lane, corrected versus uncorrected, and
when the register last moved. Fifteen minutes is ample for that, and the `*_last_change_timestamp_seconds` series
tell you when the change happened regardless of when arex noticed.

### What the defaults cost

Measured: a full poll is 654 kB and 1.4 s of eAPI time, and the full set at 30 seconds runs the eAPI process at a
5.0% duty cycle. Projected from those measurements, per switch per hour with a 30-second `pollInterval`:

| schedule | requests | bytes | eAPI time |
| --- | --- | --- | --- |
| everything at 30s | 120 | ~78 MB | ~168 s |
| defaults (`transceiver` 5m, `phy` 15m) | 120 | ~36 MB | ~77 s |

Roughly a 54% reduction in both, while still collecting every series. The request count is unchanged because due
groups share one batch — the batches simply get smaller.

## Limiting which interfaces are polled

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
| `caFile` | PEM bundle to verify this switch's certificate against. See [TLS](tls.md) |
| `pinnedCertSha256` | SHA-256 of this switch's leaf certificate. See [TLS](tls.md) |

Point `host` at the switch's management address — on a typical switch that address lives in the `management` VRF.
Keep `name` unique across switches: two entries sharing one label collapse into a single metric series.

There is no VRF setting in arex's own config. It runs off-box, so the VRF is purely a switch-side concern, handled
by the `vrf management` stanza in [switch configuration](switch-configuration.md).

## Credentials

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

### Rotation is handled without a restart

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

### systemd

Use `LoadCredential=`, which puts the secret on tmpfs as `0400` owned by the service user and does **not** pass it
to child processes. The runtime path is stable, so it can be named directly in the config:

```ini
[Service]
LoadCredential=switch-password:/etc/arex/switch-password
ExecStart=/usr/local/bin/arex -config /etc/arex/config.yaml
```

The credential then appears at `/run/credentials/arex.service/switch-password`. See
[deploy/arex.service](../deploy/arex.service) for a hardened unit.

### Kubernetes

Mount the secret as a volume and point `passwordFile` at the mounted path. With the External Secrets Operator, the
`ExternalSecret` syncs Vault into a `Secret` and the kubelet updates the mounted file in place — no pod restart, and
arex's reload-on-401 completes the loop.

**Do not mount it with `subPath`.** A `subPath` mount is populated once at pod start and is never updated, so
rotation silently stops working. The same applies to a `ConfigMap` holding the config file. See
[deploy/kubernetes.yaml](../deploy/kubernetes.yaml).

Run **one replica**. arex has no leader election, so two running indefinitely poll every switch twice — doubling
eAPI load — and Prometheus then holds two series per switch differing only by `pod`, which quietly double-counts any
aggregation.

A deploy is the deliberate exception: the rolling update sets `maxUnavailable: 0`, so the old pod serves until the
new one is Ready and no metrics are lost, at the cost of a brief overlap where both poll. See
[why one replica](install-kubernetes.md#why-one-replica).

---

Back to the [README](../README.md). See also [metrics](metrics.md), [TLS](tls.md), and the installation guides for
[systemd](install-systemd.md), [Kubernetes](install-kubernetes.md) and [ArgoCD](install-argocd.md).
