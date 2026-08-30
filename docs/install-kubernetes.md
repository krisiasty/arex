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

  # Both switches below are BGP/EVPN leaves with Vxlan1 and ESI multihoming.
  # Leave each topology-dependent group disabled on switches that do not run it.
  collect:
    processes:
      enabled: true
    temperature:
      enabled: true
    power:
      enabled: true
    cooling:
      enabled: true
    ntp:
      enabled: true
    capacity:
      enabled: true
    vxlan:
      enabled: true
    evpn:
      enabled: true
    esi:
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
      fabric: fabric-a
      pinnedCertSha256: "A1:B2:..."
    - host: https://10.10.0.12
      username: prometheus
      name: leaf2
      fabric: fabric-a
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

## Optional: serve over TLS, and require a password

Skip this if the cluster network already restricts who can reach the pod. Worth
doing on a shared cluster — the metrics carry switch names, serials and BGP
peers. See [securing the endpoint](operations.md#securing-the-endpoint).

### The certificate

With cert-manager, a `Certificate` produces the Secret the chart mounts. This
resource is configuration, not a secret, so it belongs in git:

```yaml
apiVersion: cert-manager.io/v1
kind: Certificate
metadata:
  name: arex-tls
  namespace: monitoring
spec:
  secretName: arex-tls
  duration: 2160h      # 90 days
  renewBefore: 360h    # 15 days
  commonName: arex.monitoring.svc
  dnsNames:
    - arex.monitoring.svc
    - arex.monitoring.svc.cluster.local
  issuerRef:
    name: internal-ca
    kind: ClusterIssuer
```

cert-manager writes `tls.crt`, `tls.key` and `ca.crt` into that Secret, which is
exactly what the chart's defaults expect:

```yaml
listen:
  tls:
    existingSecret: arex-tls
```

**Renewal needs no restart.** cert-manager rewrites the Secret, the kubelet
updates the mounted files, and arex re-reads the pair when it changes. As with
the switch password, this works because the chart mounts the Secret as a volume
**without `subPath`** — a `subPath` mount is populated once at pod start and
never updated, so renewal would silently stop reaching arex until the pod
happened to restart, and then only until the next renewal.

### Without cert-manager

arex takes two file paths and re-reads them when they change. It has no opinion about what wrote them, so anything
that produces a Secret with a certificate and key will do.

| approach | who issues | who renews |
| --- | --- | --- |
| cert-manager with a **Vault issuer** | Vault PKI | cert-manager, before expiry |
| **ESO generator** against Vault PKI | Vault PKI | ESO, on `refreshInterval` |
| **Vault Agent** templating to files | Vault PKI | the Agent — and this one works on bare metal too |
| a Secret you create yourself | your CA, out of band | you |
| a service mesh | the mesh | the mesh — arex then serves plain HTTP and configures no TLS at all |

**If Vault is already your secret store**, you can issue from its PKI engine with the External Secrets Operator you
are running anyway, and skip cert-manager entirely. Its generator calls any Vault path:

```yaml
apiVersion: generators.external-secrets.io/v1alpha1
kind: VaultDynamicSecret
metadata:
  name: arex-tls
  namespace: monitoring
spec:
  path: /pki/issue/arex
  method: POST
  parameters:
    common_name: arex.monitoring.svc
    alt_names: arex.monitoring.svc.cluster.local
    ttl: 720h
  resultType: Data
  provider:
    server: https://vault.example.com
    path: kv
    auth: { ... }
```

An `ExternalSecret` referencing it through `sourceRef.generatorRef` maps the response into the keys the chart
expects — `certificate` to `tls.crt`, `private_key` to `tls.key`, `issuing_ca` to `ca.crt`.

**The trade against cert-manager:** that generator *issues on refresh*, it does not *renew before expiry*. Every
`refreshInterval` mints a brand-new certificate and key, so set the interval comfortably under the TTL and accept
that rotation follows ESO's clock rather than the certificate's remaining life. cert-manager's Vault issuer exists
because renew-before-expiry is better lifecycle management than reissue-on-a-timer. Same trust root either way;
the difference is who decides when to rotate.

**Vault Agent** is the option that covers both deployment models: it templates the certificate and key into files
and rewrites them on renewal. No reload hook is needed — arex notices within 30 seconds by itself.

One subtlety common to all of them. A Kubernetes Secret volume update is atomic: the kubelet swaps a symlink, so
the certificate and key change together. A tool writing two files separately has a window where they do not match,
and arex handles that by keeping the working certificate in memory and retrying, rather than dropping the endpoint.

Whatever produces it, create the Secret with the same three keys, or point `certKey`/`keyKey`/`clientCAKey` at
whatever names you used.

### The scrape password

```bash
kubectl -n monitoring create secret generic arex-web-password \
  --from-literal=password="$(openssl rand -base64 32)" \
  --from-literal=username=prometheus
```

```yaml
listen:
  basicAuth:
    existingSecret: arex-web-password
    username: prometheus
```

`username` is in values because it is not a secret; the `username` key in the
Secret above exists only because prometheus-operator's `basicAuth` wants both
halves as secret references.

### Telling Prometheus

The chart sets the ServiceMonitor's `scheme` from `listen.tls` automatically.
What it cannot infer is how Prometheus should trust that certificate, so pass
that through:

```yaml
serviceMonitor:
  enabled: true
  tlsConfig:
    ca:
      secret:
        name: arex-tls
        key: ca.crt
    serverName: arex.monitoring.svc
  basicAuth:
    username:
      name: arex-web-password
      key: username
    password:
      name: arex-web-password
      key: password
```

`serverName` matters: the Service DNS name has to be in the certificate's SANs,
which is why the `Certificate` above lists it.

**A caveat worth checking against your operator version.** prometheus-operator
resolves these Secret references subject to its own namespace rules, and where
the Prometheus instance runs in a different namespace from the ServiceMonitor it
may not be able to read them. The usual answer is
[trust-manager](https://cert-manager.io/docs/trust/trust-manager/), which
distributes a CA bundle into every namespace that needs it. Confirm the target
is actually being scraped rather than assuming:

```promql
up{job=~".*arex.*"}
```

### Mutual TLS instead of a password

The stronger option: no shared secret exists at all. Issue Prometheus a client
certificate from the same CA, point arex at that CA, and drop basic auth
entirely.

```yaml
listen:
  tls:
    existingSecret: arex-tls
    clientCAKey: ca.crt        # require a client certificate signed by this CA
  basicAuth:
    existingSecret: ""         # not needed

serviceMonitor:
  enabled: true
  tlsConfig:
    ca:
      secret: {name: arex-tls, key: ca.crt}
    cert:
      secret: {name: prometheus-client-tls, key: tls.crt}
    keySecret:
      name: prometheus-client-tls
      key: tls.key
    serverName: arex.monitoring.svc
```

A caller without a certificate is refused during the handshake, before any request is served.

**That includes the kubelet**, which is why mutual TLS needs a probe port. `RequireAndVerifyClientCert` applies to
the listener rather than to a path, and a kubelet probe presents no client certificate — so with mTLS on a single
listener, every `httpGet` probe fails at the handshake and the pod restart-loops.

The answer is a second listener carrying only `/livez` and `/readyz`, in plain HTTP:

```yaml
listen:
  tls:
    existingSecret: arex-tls
    clientCAKey: ca.crt
  probePort: 9101
```

The chart then points the probes at that port, opens a `probes` container port, and sets `probeAddress` in the
config. Those two endpoints answer `ok` or a fixed error string — no switch names, no metrics, nothing else is
served there at all — so exposing them without TLS gives away only whether arex is up.

The chart **refuses to render** mutual TLS with `httpGet` probes and no probe port, rather than letting you
discover it as a restart loop.

The alternative, if you would rather not open a second port, is `tcpSocket` probes:

```yaml
livenessProbe:
  httpGet: null          # Helm merges maps; the default handler must be removed
  tcpSocket:
    port: metrics
readinessProbe:
  httpGet: null
  tcpSocket:
    port: metrics
```

`httpGet: null` is not optional there — Kubernetes rejects a probe carrying two handlers, and Helm merges values
maps rather than replacing them. But it costs the readiness gate: `tcpSocket` proves only that the port is open,
not that every switch has been polled. The probe port is the better trade.

## Why one replica

`replicaCount` defaults to 1. arex has no leader election, so two instances running indefinitely would poll every
switch twice — doubling eAPI load — and Prometheus would hold two series per switch differing only by `pod`,
quietly double-counting any aggregation.

Raising `replicaCount` is therefore not a way to get availability. If a node failing is a concern, let the
Deployment reschedule.

**A deploy is the exception, and it is deliberate.** The strategy is `RollingUpdate` with `maxUnavailable: 0`, so
the old pod keeps serving until the new one is Ready — and Ready, for arex, means every switch has been polled
once. No metrics are lost across an upgrade. The cost is an overlap of roughly one poll interval where both
instances poll and both are scraped.

During that window, aggregations double. Use `max by (switch)` rather than `sum` if a dashboard or alert spans
deploys:

```promql
max by (switch) (arista_interface_link_up)
```

Two useful consequences of the readiness gate. A rollout **stalls** rather than completing if the new pod cannot
reach a switch, leaving the working pod in place — a broken config or an unreachable switch does not take
monitoring down. And a stalled rollout is visible: `kubectl rollout status` blocks, and the new pod sits
`0/1 Running`.

If something on the switch side objects to concurrent eAPI sessions, set `strategy.type: Recreate` instead. That
removes the overlap and accepts a gap of one startup instead.

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
