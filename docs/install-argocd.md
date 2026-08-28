# Install with ArgoCD and External Secrets

The GitOps shape: the switch inventory lives in git, the password lives in Vault, and neither ArgoCD nor the
External Secrets Operator owns the other's object.

Read [Install on Kubernetes with Helm](install-kubernetes.md) first — the values are the same. This covers what
changes when ArgoCD applies them.

## The division of ownership

| object | owned by | why |
| --- | --- | --- |
| `Secret` | ESO, from Vault | the chart never templates it |
| `ExternalSecret` | ArgoCD | it is configuration, not a secret |
| everything else | ArgoCD, via the chart | |

That split is the whole design. If the chart also rendered the `Secret`, ArgoCD would apply the placeholder from git
over the value ESO synced from Vault, then report `OutOfSync` for ever while arex authenticated with the wrong
password. The chart has no `Secret` template, so this cannot happen — but do not add one through
`extraVolumes` or a kustomize patch either.

## 1. The ExternalSecret

Committed to git, alongside the Application. It creates and maintains the `Secret` the chart mounts.

```yaml
apiVersion: external-secrets.io/v1
kind: ExternalSecret
metadata:
  name: arex-switch-password
  namespace: monitoring
spec:
  refreshInterval: 1h
  secretStoreRef:
    name: vault
    kind: ClusterSecretStore
  target:
    name: arex-switch-password
    creationPolicy: Owner
  data:
    - secretKey: password
      remoteRef:
        key: network/arista/monitoring
        property: password
```

`refreshInterval` is the upper bound on how long a rotation takes to reach the cluster. Add the kubelet's own sync
delay for the mounted file, then arex notices on its next poll — so a rotation lands within roughly
`refreshInterval + 1m + pollInterval`, with no restart anywhere in that chain.

## 2. The Application

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: arex
  namespace: argocd
spec:
  project: default
  destination:
    server: https://kubernetes.default.svc
    namespace: monitoring
  sources:
    # The chart, from the OCI registry.
    - repoURL: ghcr.io/krisiasty/charts
      chart: arex
      targetRevision: 0.1.0
      helm:
        valueFiles:
          - $values/clusters/prod/arex-values.yaml
    # Your values and the ExternalSecret, from your own repo.
    - repoURL: https://github.com/your-org/your-gitops-repo.git
      targetRevision: main
      ref: values
      path: clusters/prod/arex
  syncPolicy:
    automated:
      prune: true
      selfHeal: true
    syncOptions:
      - CreateNamespace=true
```

`targetRevision` pins the **chart** version; `image.tag` in values pins arex itself. They move independently on
purpose — a chart fix should not imply a new exporter.

## 3. Leave the Secret alone

Even though the chart does not create the `Secret`, ArgoCD will notice ESO writing to it if the `ExternalSecret`
lives in the same Application. Tell ArgoCD that the data is not its business:

```yaml
spec:
  ignoreDifferences:
    - group: ""
      kind: Secret
      name: arex-switch-password
      jsonPointers:
        - /data
```

Without this, `selfHeal: true` and a rotation can end up arguing, and the Application flaps between `Synced` and
`OutOfSync` on every refresh.

## 4. What a config change looks like

Editing `arex-values.yaml` — adding a switch, changing an interval — changes the rendered ConfigMap, which changes
the `checksum/config` annotation on the Deployment, which rolls the pod. So the sync genuinely takes effect rather
than reporting `Synced` while arex keeps running the config it read at startup.

Expect a brief gap in the series across the roll: `Recreate` stops the old pod before starting the new one,
deliberately, because two arex instances would poll every switch twice.

## 5. Verify the whole chain

```bash
# ESO produced the Secret
kubectl -n monitoring get externalsecret arex-switch-password
kubectl -n monitoring get secret arex-switch-password -o jsonpath='{.data.password}' | base64 -d | wc -c

# ArgoCD is synced and healthy
argocd app get arex

# arex reached the switches
kubectl -n monitoring logs deploy/arex | jq 'select(.msg=="switch schedule")'
```

Then from Prometheus, which is the real test:

```promql
# every configured switch reporting
count by (switch) (arista_info)

# nothing stale
max(arista_scrape_age_seconds) < 90
```

## Pitfalls worth knowing

**`subPath` breaks rotation.** A `subPath` mount is populated once at pod start and never updated. The chart does
not use it; if you patch one in, rotation silently stops working and the only symptom is authentication failures
after the next Vault rotation.

**Two replicas double-count.** `replicaCount` stays 1. See
[why one replica](install-kubernetes.md#why-one-replica).

**`prune: true` and the Secret.** If you later move the `ExternalSecret` out of this Application, prune can remove
the `Secret` it created. Keep them in the same Application, or exclude the `Secret` from pruning.

**Certificate pins are not secrets.** They belong in values, in git. A pin is a public fingerprint; treating it as
a secret only makes the config harder to review.

---

Back to the [README](../README.md). See also [Install on Kubernetes with Helm](install-kubernetes.md),
[configuration](configuration.md) and [TLS](tls.md).
