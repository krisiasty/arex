# Install on Kubernetes with Helm

arex runs as a single Deployment beside your monitoring stack. It talks to switches, not to the API server, so it
needs no RBAC and no ServiceAccount token.

For a GitOps install, read this first for the values, then see [ArgoCD and External Secrets](install-argocd.md).

## Before you start

eAPI has to be reachable from the cluster's pod network, in the switch's management VRF, with a read-only account
that is genuinely restricted — see [switch configuration](switch-configuration.md). Collect each switch's
certificate fingerprint, since a stock EOS certificate cannot be verified by hostname:

```bash
openssl s_client -connect 10.10.0.11:443 </dev/null 2>/dev/null \
  | openssl x509 -noout -fingerprint -sha256
```

## 1. Create the Secret

The chart never creates it. If it did, and something else — the External Secrets Operator, say — also managed that
Secret, the two would fight over the same object.

```bash
kubectl create namespace monitoring
kubectl -n monitoring create secret generic arex-switch-password \
  --from-literal=password='the-switch-password'
```

For anything beyond a trial, sync it from a secret store instead of typing it: see
[ArgoCD and External Secrets](install-argocd.md).

## 2. Write values

```yaml
# arex-values.yaml
credentials:
  existingSecret: arex-switch-password
  key: password

config:
  pollInterval: 30s
  stalenessLimit: 90s

  collect:
    processes:
      enabled: true
    temperature:
      enabled: true
    power:
      enabled: true
    cooling:
      enabled: true
    interfaces:
      enabled: true
    bgp:
      enabled: true
    transceiver:
      enabled: true
      interval: 5m
    phy:
      enabled: true
      interval: 15m

  switches:
    - host: https://10.10.0.11
      username: prometheus
      name: leaf1
      pinnedCertSha256: "A1:B2:..."
    - host: https://10.10.0.12
      username: prometheus
      name: leaf2
      pinnedCertSha256: "C3:D4:..."

serviceMonitor:
  enabled: true    # requires prometheus-operator
```

`config` is arex's own configuration verbatim — everything in
[the configuration reference](configuration.md) belongs there. Two exceptions the chart manages for you:

- **`passwordFile`** is set to the mounted secret path automatically. The path is decided by the volumeMount, so
  letting it be typed separately would only create a way for the two to disagree.
- **`listenAddress`** should stay `:9100`, which the Service and probes assume.

The chart refuses to render if `config.switches` is empty, or if a switch has no credential and no
`credentials.existingSecret` — a config that cannot poll anything is a mistake worth catching at template time.

## 3. Install

```bash
helm install arex oci://ghcr.io/krisiasty/charts/arex \
  --namespace monitoring \
  --values arex-values.yaml
```

Or from a checkout, which is also how to try an unreleased change:

```bash
helm install arex ./charts/arex -n monitoring -f arex-values.yaml
```

The image tag defaults to the chart's `appVersion`. Pin it explicitly with `image.tag` to hold a version across
chart upgrades.

## 4. Verify

```bash
kubectl -n monitoring rollout status deploy/arex
kubectl -n monitoring logs deploy/arex | jq
```

The startup log names every switch and its resolved schedule, which is the quickest way to confirm the config
arrived as intended:

```json
{"level":"INFO","msg":"switch schedule","switch":"leaf1",
 "modules":"show version=30s show interfaces=30s show interfaces transceiver detail=5m0s ..."}
```

A pod that stays `NotReady` means arex cannot reach a switch: readiness waits until every switch has been polled
once. Ask arex which one:

```bash
kubectl -n monitoring port-forward svc/arex 9100
curl -s localhost:9100/status | jq
```

Then confirm Prometheus has the series — one target, all switches, each series carrying its own `switch` label:

```promql
count by (switch) (arista_info)
```

## Why one replica

`replicaCount` defaults to 1 and the strategy is `Recreate`, both deliberately. arex has no leader election, so two
instances poll every switch twice — doubling eAPI load — and Prometheus then holds two series per switch that
differ only by `pod`, quietly double-counting any aggregation. A rolling update briefly runs both, which is why the
strategy is `Recreate`: a short gap is the cheaper failure, and arex drains scrapes in flight before exiting.

Raising `replicaCount` is not a supported way to get availability. If a node failing is a concern, let the
Deployment reschedule — a poll interval of downtime costs one gap in the series.

## Upgrading

```bash
helm upgrade arex oci://ghcr.io/krisiasty/charts/arex -n monitoring -f arex-values.yaml
```

A change to `config` rolls the pod even though nothing else in the pod spec changed: the Deployment carries a
`checksum/config` annotation over the rendered config. Without it, arex — which reads its config once, at startup —
would keep running the old one, and a switch added in git would look deployed while never being polled.

## Rotating the password

Update the Secret; the kubelet updates the mounted file in place, usually within a minute:

```bash
kubectl -n monitoring create secret generic arex-switch-password \
  --from-literal=password='the-new-password' --dry-run=client -o yaml | kubectl apply -f -
```

**No pod restart is needed.** When a switch rejects the credentials, arex re-reads the file, and if the secret
changed it retries immediately — so a rotation costs one rejected request rather than every poll until someone
notices. Watch it happen:

```promql
increase(arista_credential_reloads_total{outcome="rotated"}[1h])
```

This only works because the Secret is mounted as a **volume, without `subPath`**. A `subPath` mount is populated
once at pod start and never updated, so rotation would silently stop reaching arex. The chart mounts it correctly;
do not override that with `extraVolumeMounts`.

## Without prometheus-operator

Leave `serviceMonitor.enabled` false and scrape the Service directly:

```yaml
scrape_configs:
  - job_name: arista
    static_configs:
      - targets: ["arex.monitoring.svc:9100"]
```

## Without Helm

[`deploy/kubernetes.yaml`](../deploy/kubernetes.yaml) is the same deployment as plain manifests, with the reasoning
in comments. It has no `checksum/config`, so a ConfigMap change needs
`kubectl -n monitoring rollout restart deploy/arex`.

---

Back to the [README](../README.md). See also [configuration](configuration.md),
[ArgoCD and External Secrets](install-argocd.md) and [running arex](operations.md).
