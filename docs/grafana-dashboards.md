# Grafana dashboards

This document defines the proposed official Grafana dashboard suite for arex. It is the design source of truth for
the dashboards; implementation issues track delivery and the dashboard files will live under
`monitoring/grafana/`.

The suite is designed as a drill-down path:

```text
Fleet overview -> Switch detail -> Interfaces and optics
                              \-> Routing and overlay

Exporter symptoms -> arex exporter health
```

The first view answers whether the fleet is healthy. The detail views explain why it is not. Dashboards complement
the rules in [`monitoring/`](../monitoring/) and do not replace alerting.

## Goals

- Make the unhealthy part of a fleet visible without plotting every time series at once.
- Separate switch failures from exporter and collection failures.
- Preserve metric semantics: absence, zero, stale data, disabled collection and healthy state must not be presented
  as the same thing.
- Lead from fleet symptoms to one switch, interface, peer or overlay object with dashboard links that preserve the
  selected time range.
- Use the switch's and optic's reported thresholds where arex exposes them instead of encoding hardware-specific
  limits in Grafana.

## Shared behavior

### Variables

All dashboards use a Prometheus-compatible datasource variable rather than a hard-coded datasource UID. Variables
cascade so a broad selection does not populate every peer and interface in the fleet.

| Variable | Scope | Notes |
| --- | --- | --- |
| `$datasource` | All dashboards | Prometheus-compatible datasource. |
| `$job` | All dashboards | Optional when a Grafana instance reads more than one arex scrape job. |
| `$switch` | Detail dashboards | From `switch`; fleet permits all values and detail defaults to one. |
| `$interface` | Interfaces and optics | Restricted by `$switch`. |
| `$vrf` | Routing and overlay | Restricted by `$switch`. |
| `$peer` | Routing and overlay | Restricted by `$switch` and `$vrf`. |
| `$fabric` | ESI panels | Available only on ESI metrics; it is not currently general switch metadata. |

Dashboard links carry the datasource, job, switch and selected time range. Links to interface and routing detail
also carry the narrowest applicable interface, VRF or peer selection.

### State and missing data

Binary health belongs in stats, status tables or state timelines. Identity and mappings belong in tables. Rates and
analogue measurements belong in time series. Capacity belongs in sorted bar gauges.

Optional modules produce no series when disabled. A missing optional metric is therefore grey and labelled
`not collected`, not red and not zero. A failed command is reported separately by `arista_command_success`.

Panels use consistent colors:

- Green: healthy.
- Amber: degraded or approaching a threshold.
- Red: failed or beyond a critical threshold.
- Blue: informational state.
- Grey: unavailable, stale or not collected.

### Alerts and annotations

The fleet dashboard shows active arex alert rules when the datasource makes rule state available. Related time-series
panels use alert-state annotations, so an operator can correlate a change with the time an alert started and cleared.

Alert evaluation remains outside Grafana dashboards. The dashboards visualize and explain rule state; they do not
duplicate the rules from [`monitoring/alerts.yaml`](../monitoring/alerts.yaml).

## Fleet overview

Purpose: answer "Is the network healthy, and where should I look first?"

Default range: 6 hours. Refresh: 30 seconds.

### Panels

| Panel | Data | Form |
| --- | --- | --- |
| Monitored switches | Count of distinct `switch` values | Stat |
| Unreachable switches | `arista_scrape_success == 0` | Stat, red when non-zero |
| Stale switches | `arista_scrape_age_seconds` beyond the deployment's freshness boundary | Stat |
| Failed commands | `arista_command_success == 0` | Stat |
| Down BGP and EVPN peers | Peer state excluding peers under maintenance | Stat |
| Faulty links | Interfaces with live errors, high BER or MAC faults | Stat |
| Hardware and environment faults | Temperature, fan, PSU and capacity rule state | Stat |
| Active alerts | Firing rules grouped by severity and switch | Table with dashboard links |
| Switch health | Identity, poll health, uptime, CPU, memory, NTP and active alert count | Status table |
| Poll health | `arista_scrape_success` by switch | State timeline |
| Worst interface utilization | Traffic rate divided by negotiated speed | Horizontal bars, top 10 to 20 |
| Interfaces with errors or discards | Non-zero rates with switch, interface and description | Table |
| Routing health | Down BGP and EVPN sessions and VXLAN interfaces | Status table |
| Hardware capacity | Highest row-level `used / limit` values | Bar gauge, highest first |
| Environmental problems | Only non-OK sensors, fans and PSUs | Table |

The switch-health table sorts first by severity and then by scrape age. Each switch links to Switch detail.
Interfaces and peers link directly to their corresponding detail dashboards.

The overview deliberately avoids large multi-series traffic graphs. Fleet-wide traffic is less actionable than the
worst current links and usually hides a single saturated interface.

## Switch detail

Purpose: show the complete operational state of one selected switch.

Variables: `$switch`, with optional `$vrf`. Default range: 24 hours. Refresh: 30 seconds.

### Identity and collection

| Panel | Data | Form |
| --- | --- | --- |
| Identity | Model, serial, EOS version, architecture and MAC from `arista_info` | Metadata table |
| Uptime | `time() - arista_boot_timestamp_seconds` | Stat |
| Poll state | Last poll success and `arista_scrape_age_seconds` | Status stats |
| Failed commands | Failed `arista_command_success` series | Table |
| Clock | NTP synchronization and selected-source offset | Status stats |

Identity values are current metadata and are not plotted over time. NTP status is visible near every timestamp-based
diagnostic because boot, counter-clear, DOM-refresh and link-transition times come from the switch clock.

### System resources

| Panel | Data | Form |
| --- | --- | --- |
| CPU utilization | `100 - arista_cpu_idle_percent` | Time series |
| CPU modes | User, system, I/O wait, IRQ and other modes | Stacked time series |
| Load | One-, five- and fifteen-minute load averages | Time series |
| Memory utilization | `(1 - available / total) * 100` | Time series |

Available memory is the primary signal. Free memory is not used as the health indicator because EOS, like Linux
generally, uses otherwise idle memory for reclaimable cache.

### Hardware and environment

| Panel | Data | Form |
| --- | --- | --- |
| Temperature | Sensor readings and their reported overheat and critical thresholds | Time series |
| PSU state and load | State plus `output power / capacity` | Status table and time series |
| Fan state and speed | Actual and configured speed, stable state and health | Status table and time series |
| Hardware capacity | Row-level used, limit and high-water mark | Bar gauge and table |

Hardware capacity stays unaggregated by table row. An individual ACL or forwarding-table slice can fill while an
aggregate remains low, and the tightest row determines whether new entries fit.

### Network summary

The switch page shows interface state counts, total traffic, the most utilized interfaces, interfaces with errors or
flaps, BGP and EVPN peer state, and VXLAN or ESI summaries when their modules are enabled.

These are summaries with links. Detailed link, optical, peer and overlay history remains in the specialized
dashboards to keep the switch page quick to load.

## Interfaces and optics

Purpose: find problematic links across the fleet and diagnose one selected interface and optic.

Variables: `$switch`, `$interface`. Default range: 24 hours. Refresh: 30 seconds.

### Interface inventory

The first panel is a filterable table rather than one graph per interface.

| Column | Presentation |
| --- | --- |
| Switch, interface, description and membership | Text |
| Link state | Colored state |
| Negotiated speed | Human-readable bit rate |
| Inbound and outbound utilization | Percentage |
| Error and discard rates | Per-second values |
| Flaps in the selected range | Count |
| Last state change | Relative time |
| Optical receive margin | dB |
| PHY or MAC fault | Colored state |

Utilization is normalized by the negotiated speed:

```promql
rate(arista_interface_in_octets_total[5m]) * 8
  / on(switch, interface) arista_interface_speed_bits_per_second * 100
```

### Interface drill-down

| Panel | Data | Form |
| --- | --- | --- |
| Traffic | Inbound and outbound octet rates | Time series in bit/s and percentage |
| Packet mix | Unicast, multicast and broadcast packet rates | Stacked time series |
| Errors and discards | Counter rates | Time series |
| Detailed errors | Rates grouped by `cause` | Stacked time series |
| Link state and transitions | Link state, transition count and last change | State timeline and stats |
| Optical power | Receive and transmit power with optic thresholds | Time series |
| Receive margin | Receive power minus the optic's low-warning threshold | Time series in dB |
| Laser bias | Transmit bias by lane | Time series |
| Optic environment | Module temperature and voltage | Time series |
| PHY state | PCS, PMA, lock, high-BER and MAC-fault state | State timeline |
| FEC diagnostics | Values and their last-change times | Table |

Low receive power is evaluated only while `arista_interface_link_up == 1`. A populated but unplugged cage commonly
reports the EOS floor of approximately -30 dBm and must not be presented as a degrading live link.

Module temperature and voltage are grouped by optical `slot` when counting or summarizing optics. Breakout
interfaces share one physical optic and otherwise repeat those readings for each child interface.

FEC values are diagnostic, not a live error rate. EOS may leave the registers unchanged for long periods. Live
interface FCS and symbol errors, PCS high-BER state and MAC fault state remain the primary failure signals.

## Routing and overlay

Purpose: separate underlay BGP, EVPN control-plane, VXLAN data-plane and ESI multihoming failures.

Variables: `$switch`, `$vrf`, `$peer`, `$fabric`. Default range: 6 hours. Refresh: 30 seconds.

### BGP underlay

| Panel | Data | Form |
| --- | --- | --- |
| Peer state | Established and maintenance state | State timeline |
| Peer inventory | Switch, VRF, peer, ASN, description, state and session uptime | Status table |
| Prefixes | Received, accepted and advertised | Time series |
| Rejected prefixes | Received minus accepted | Time series and current table column |

Peers under maintenance are visually distinct rather than unhealthy. Session uptime is derived from the peer
state-change timestamp.

### EVPN control plane

EVPN stays separate from IPv4 BGP because either address family can fail while the other session remains established.

| Panel | Data | Form |
| --- | --- | --- |
| EVPN peer state | Established and maintenance state | State timeline and table |
| Underlay and EVPN mismatch | IPv4 established while EVPN is down | Status table |
| EVPN prefixes | Received, accepted and advertised per peer | Time series |
| EVPN routes | Path counts grouped by route type | Stacked time series |

### VXLAN data plane

| Panel | Data | Form |
| --- | --- | --- |
| VXLAN interface | Line-protocol state | Stat and state timeline |
| Remote VTEPs | Known VTEP count | Time series |
| Remote MACs | MAC count per VTEP | Table or horizontal bars |
| VTEP inventory | Address and tunnel types | Table |
| VNI mappings | VLAN-to-VNI and VRF-to-VNI mappings | Table |
| Flood lists | VTEPs participating in each VLAN flood list | Table |

EOS removes a per-VTEP series when that VTEP disappears. The remote VTEP count is therefore the reliable fleet trend;
a threshold on an individual VTEP series cannot distinguish removal from collection absence.

### ESI multihoming

The status table is keyed by `fabric`, `esi` and `evpn_instance`. It shows the local attachment, forwarding-peer
count, local designated-forwarder state, whether any forwarder is elected, interface and redundancy mode.

Fewer than two forwarding peers is amber. No elected designated forwarder across a fabric, ESI and EVPN-instance
group is red. A local designated-forwarder value of zero is normal when the peer owns the role.

## arex exporter health

Purpose: determine whether a network symptom is real or caused by polling, credentials, the exporter process or its
scrape path.

Variables: `$datasource`, `$job`, exporter target and `$switch`. Default range: 24 hours. Refresh: 30 seconds.

| Panel | Data | Form |
| --- | --- | --- |
| Deployed versions | Version, revision and Go version from `arex_build_info` | Table |
| Poll health | Success and data age per switch | State timeline and time series |
| Failed commands | Command state per switch | Status table |
| eAPI requests | Request rate by outcome and attempt | Stacked time series |
| Retry amplification | Batch versus retry request rate | Time series |
| Mean eAPI latency | Duration-total rate divided by request rate | Time series |
| Response bandwidth | Response-byte rate per switch | Time series and top table |
| Credential reloads | Rotated, unchanged and failed outcomes | Event-like bars |
| Process resources | Resident memory, heap, goroutines and GC | Time series |
| Expensive switches | Highest response bandwidth and accumulated request time | Table |

The current metrics provide request count and accumulated duration, so they support mean latency but not latency
percentiles. A duration histogram is required before a p95 or p99 panel can be correct.

## Visualization rules

- Counters are displayed with `rate()` or `increase()`, never as raw cumulative lines.
- Interface traffic is shown in bit/s and as a percentage of negotiated speed.
- Mutable descriptions stay in tables and legends through `_info` metrics; they do not become time-series identity.
- Device and optic thresholds are preferred over hard-coded temperatures, power values or bias-current limits.
- Tables and bar gauges sort the worst state first and limit initial rows. Filters and drill-down expose the remainder.
- State timelines are used when the transition matters. A current stat is used when only the present state is
  actionable.
- Units are explicit: bit/s, bytes, seconds, percent, watts, volts, amps, degrees Celsius and dBm.
- Time-derived panels are accompanied by NTP state because their timestamps originate on the switch.

## Known metric gaps

The first dashboard release should use the current catalogue. These additions would improve later releases but do
not block the initial suite:

- Interface administrative state, to distinguish an intentionally disabled port from an unexpectedly down link.
- General per-switch site, rack, role, region and fabric metadata. The current `fabric` label applies only to ESI
  metrics.
- Expected peer and VTEP counts for topology-aware comparisons instead of deployment-wide static thresholds.
- eAPI request-duration histograms for latency percentiles.
- Per-command data age to complement command success after partial poll failures.
- Maintenance and change-event annotations from an external source.

## Delivery

Dashboard design and implementation are deliberately tracked separately. This document holds shared behavior and
scope; one tracking issue records overall progress, and one implementation issue owns each dashboard's acceptance
criteria.

The recommended order is:

1. Fleet overview.
2. Switch detail.
3. Interfaces and optics.
4. Routing and overlay.
5. arex exporter health.

The first three form the minimum useful operational suite. Routing and exporter health follow independently without
blocking that initial delivery.

Before dashboard implementation, a small foundation task must settle the supported Grafana version, stable dashboard
UIDs, datasource-variable convention, source format, validation command and dashboard-link contract.

Dashboard sources will live in:

```text
monitoring/grafana/
├── fleet-overview.json
├── switch-detail.json
├── interfaces-optics.json
├── routing-overlay.json
└── arex-health.json
```

Each dashboard issue must verify that its PromQL names existing metrics, that disabled optional modules render as
unavailable rather than failed, and that dashboard links preserve variables and the selected time range.

---

Back to the [README](../README.md). See also the [metrics reference](metrics.md), [PromQL guidance](promql.md) and
[alerting rules](../monitoring/README.md).
