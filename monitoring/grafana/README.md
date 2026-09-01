# Grafana dashboards

This directory holds the source and validation contract for arex's official Grafana dashboards. The dashboard
design, panel inventory and delivery order are in [the design document](../../docs/grafana-dashboards.md).

## Compatibility

The minimum supported version is Grafana 12.4. Dashboard sources use the classic JSON model at `schemaVersion: 42`,
the final schema version of Grafana's v1 dashboard model. Grafana 12.4 can import and file-provision it directly,
and newer Grafana versions migrate it when loading.

Classic JSON is deliberate. Grafana's v2 resource format uses a different provisioning path and does not provide a
portable file for Grafana 12.4 and Grafana 13. The repository ships the exact artifact users import or provision
instead of adding a dashboard compiler and its dependency lifecycle.

See Grafana's documentation for the [support policy][support], [dashboard JSON model][json-model] and
[file provisioning][provisioning].

[json-model]:
  https://grafana.com/docs/grafana/latest/visualizations/dashboards/build-dashboards/view-dashboard-json-model/
[provisioning]: https://grafana.com/docs/grafana/latest/administration/provisioning/#dashboards
[support]: https://grafana.com/docs/grafana/latest/upgrade-guide/when-to-upgrade/

## Source format

Dashboard JSON in this directory is canonical source, not generated output. Files use two-space indentation, retain
their meaningful Grafana field order and end with one newline. Run the formatter after exporting or editing JSON:

```bash
python3 hack/check_grafana.py --format
```

Dashboard files omit Grafana's database-assigned numeric `id`. Their string UID is stable, so imports and
provisioning update the existing dashboard and preserve links.

The canonical filenames and identities are reserved before panel implementation begins:

| File | UID | Title | Default range |
| --- | --- | --- | --- |
| `fleet-overview.json` | `arex-fleet-overview` | arex / Fleet overview | 6 hours |
| `switch-detail.json` | `arex-switch-detail` | arex / Switch detail | 24 hours |
| `interfaces-optics.json` | `arex-interfaces-optics` | arex / Interfaces and optics | 24 hours |
| `routing-overlay.json` | `arex-routing-overlay` | arex / Routing and overlay | 6 hours |
| `arex-health.json` | `arex-exporter-health` | arex / Exporter health | 24 hours |

The shared folder title and UID are both `arex`. Every dashboard carries the `arex` and `arista` tags. Additional
dashboard-specific tags are allowed, but these two are the navigation and discovery contract.

## Variables

Variables use these names and appear in dependency order:

| Name | Type | Contract |
| --- | --- | --- |
| `datasource` | Datasource | Selects a Prometheus-compatible datasource with `query: prometheus`. |
| `job` | Query | Reads `job` from `arex_build_info`; panels match it with `job=~"$job"`. |
| `switch` | Query | Reads `switch` from `arista_info`, restricted by `$job`. |
| `interface` | Query | Restricted by `$job` and `$switch`. |
| `vrf` | Query | Restricted by `$job` and `$switch`. |
| `peer` | Query | Restricted by `$job`, `$switch` and `$vrf`. |
| `fabric` | Query | Reads ESI metrics; `fabric` is not general switch metadata. |

Every Prometheus query variable and panel datasource uses this reference:

```json
{
  "type": "prometheus",
  "uid": "$datasource"
}
```

No source may contain an environment-specific datasource UID. Dashboard-specific issues decide whether their
entity variables allow multiple values or `All`; the variable names and dependency order do not change.

## Links

Every dashboard has one tag-based dashboard link with `includeVars: true`, `keepTime: true` and the `arex` tag. It
provides navigation across the suite while preserving common variable selections and the selected time range.

Data links use stable UIDs rather than titles. They preserve time and shared selectors, then add the object selected
in the panel. For example, an interface table can link to Switch detail with this shape:

```text
/d/arex-switch-detail?${__url_time_range}&${datasource:queryparam}&${job:queryparam}&var-switch=${__field.labels.switch}
```

Use `${__all_variables}` only when the source and target have the same variable set and the link does not override
one of them. Grafana documents the interpolation rules under [data-link variables][data-links].

[data-links]: https://grafana.com/docs/grafana/latest/visualizations/panels-visualizations/configure-data-links/

## Presentation

Use these semantic colors throughout the suite:

| State | Grafana color | Meaning |
| --- | --- | --- |
| Healthy | Green | Operating normally. |
| Degraded | Orange | Reduced redundancy or approaching a device threshold. |
| Failed | Red | Unavailable or beyond a critical threshold. |
| Informational | Blue | State that is not itself healthy or unhealthy. |
| Unavailable | Grey | Stale, absent or not collected. |

Use device and optic thresholds where metrics expose them. Fixed thresholds are acceptable only for policy choices
documented beside the panel. Units use Grafana's built-in unit IDs and match the metric: bit/s, bytes, seconds,
percent, watts, volts, amps, degrees Celsius or dBm.

Every visualization panel sets `fieldConfig.defaults.noValue`. Use `Not collected` for an optional module, `No data`
for an expected series whose absence is separately explained, and `Unavailable` for stale collection. Do not map
absence to numeric zero. Collection failures remain visible through scrape and command-health panels.

## Kubernetes resource panels

The exporter-health dashboard always provides process-level memory and runtime metrics. Its optional Kubernetes
resource panels additionally require:

- cAdvisor `container_cpu_usage_seconds_total` and `container_memory_working_set_bytes` metrics; and
- kube-state-metrics `kube_pod_container_resource_requests` and `kube_pod_container_resource_limits` metrics; and
- canonical `namespace`, `pod` and `container` workload labels, preserved by setting `honorLabels: true` on the
  cAdvisor and kube-state-metrics monitors.

The Kubernetes queries select `container="arex"` and join to the selected `arex_build_info` series on `namespace` and
`pod`. Usage, requests and limits therefore describe only the arex container in the selected arex workloads; injected
sidecars are excluded. If these metrics or labels are unavailable, the optional panels show no data while the
process-level panels continue to work.

## Validation

Run the same structural validation as CI:

```bash
python3 hack/check_grafana.py
python3 -m unittest discover -s hack/tests -p 'test_check_grafana.py'
```

The validator checks JSON syntax and canonical formatting, stable identities, unique UIDs, common tags, required
variables and navigation, datasource indirection and explicit no-value text on visualization panels. It requires no
Grafana server and no package outside Python's standard library.

The minimal source in `testdata/minimal.json` exercises the contract and is a starting point for a new dashboard. It
is a test fixture, not a dashboard to provision.

## Provisioning

`provisioning.yaml` is a ready-to-copy file provider. Mount only the completed dashboard JSON files at
`/var/lib/grafana/dashboards/arex`; do not mount this directory wholesale because `testdata/minimal.json` is not a
user-facing dashboard.

The provider disables UI saves so the committed files remain authoritative. Importing a dashboard manually remains
supported; the datasource variable is resolved by the receiving Grafana instance.

---

Back to the [monitoring documentation](../README.md).
