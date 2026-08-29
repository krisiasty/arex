# Alerting rules

Rules for arex's metrics, in the format each rule evaluator wants.

They are deliberately **not** part of the Helm chart. arex runs where the
switches are reachable, which is often not where the metrics are stored and
evaluated: a cluster that ships metrics onward with Vector or Alloy has no rule
evaluator at all, and a `PrometheusRule` there would be an object nothing reads.
Rules belong wherever the series live.

## Which file

| your setup | file | how |
| --- | --- | --- |
| Prometheus, plain | `alerts.yaml` | add it to `rule_files:` |
| prometheus-operator | `prometheusrule.yaml` | `kubectl apply -f` |
| Thanos Ruler, via the operator | `prometheusrule.yaml` | selected by your `ThanosRuler` |
| Thanos Ruler, standalone | `alerts.yaml` | mount it and pass `--rule-file` |
| VictoriaMetrics operator | `vmrule.yaml` | `kubectl apply -f` |
| vmalert, standalone | `alerts.yaml` | pass `-rule=alerts.yaml` |
| Mimir, Cortex, Grafana Cloud | `alerts.yaml` | `mimirtool rules load alerts.yaml` |
| Grafana Alloy | `prometheusrule.yaml` | picked up by `mimir.rules.kubernetes` |

`alerts.yaml` is the source. The other two are generated from it by
[`hack/gen-monitoring.sh`](../hack/gen-monitoring.sh) — they wrap the same
`groups:` document in a custom resource and differ in nothing else. CI fails if
they have drifted.

## Before you apply them

**Check the selectors.** `prometheusrule.yaml` carries
`prometheus: kube-prometheus` and `role: alert-rules`, which is what a default
kube-prometheus-stack selects on. If your Prometheus resource has a different
`ruleSelector`, the object will apply cleanly and be ignored — the most annoying
possible outcome. Confirm with:

```bash
kubectl get prometheus -A -o jsonpath='{.items[*].spec.ruleSelector}'
```

**Thresholds are a starting point.** What counts as too many interface errors
depends on the link; what counts as stale depends on your poll interval. Read
them before adopting them.

**Two rules are shaped oddly on purpose**, and the comments explaining why
travel with them into the cluster:

- `SwitchOpticRxPowerLow` is gated on `arista_interface_link_up`. A cage with an
  optic installed but nothing plugged in reports the -30 dBm floor, below its own
  low alarm, and an ungated rule alerts for ever.
- `SwitchOpticFECUncorrected` uses `delta()` rather than `> 0`. The value
  persists after a repair, so the obvious form never clears. FEC is a weak
  alerting source in general — see
  [the metrics reference](../docs/metrics.md#fec-counters-are-diagnostic-not-an-alerting-source).

## Editing

Edit `alerts.yaml`, then:

```bash
./hack/gen-monitoring.sh
```

A test checks that every metric the rules name exists in arex's catalogue, and
that every label the annotations interpolate is one some metric carries. A rule
naming a renamed metric fires never and is noticed by nobody, which is the worst
way for an alert to fail.

---

Back to the [README](../README.md). See also [PromQL and alerting](../docs/promql.md)
for queries and the reasoning behind these rules.
