# arex

Prometheus exporter for Arista switches via eAPI.

Arista EOS ships no Prometheus exporter. Custom binaries can be installed on EOS, but the supported methods for
installing and managing them assume the switch can reach the outside world from the default VRF — and management
interfaces normally sit in the `management` VRF instead, which makes that approach impractical.

arex sidesteps the problem by running off-box. It connects out to each switch over eAPI, polls a set of `show`
commands on an interval, caches the results, and serves them as Prometheus metrics. Nothing is installed on the
switch, so the VRF the management interface lives in only has to be reachable from wherever arex runs — see
[switch configuration](docs/switch-configuration.md) for the one VRF-specific step this requires on the switch.

One arex serves an entire fleet: every series carries a `switch` label, so Prometheus scrapes one target no matter
how many switches there are. Scrapes read from cache, so the scrape interval is independent of how often switches
are actually polled.

## Documentation

| | |
| --- | --- |
| [Install on bare metal or a VM](docs/install-systemd.md) | systemd, with the password in systemd's credential store |
| [Install on Kubernetes with Helm](docs/install-kubernetes.md) | the chart, values, rotation, and why one replica |
| [Install with ArgoCD and External Secrets](docs/install-argocd.md) | GitOps: the inventory in git, the password synced from Vault |
| [Configuration](docs/configuration.md) | what to collect, how often, which interfaces, credentials |
| [Switch configuration](docs/switch-configuration.md) | enabling eAPI in a VRF, and a read-only role that actually restricts |
| [TLS](docs/tls.md) | why a stock EOS certificate cannot be verified, and what to do instead |
| [Metrics reference](docs/metrics.md) | every series, and which ones are worth alerting on |
| [Running arex](docs/operations.md) | endpoints, filtered scrapes, logging, shutdown, poll pacing |
| [PromQL and alerting](docs/promql.md) | queries worth having, and rules that fire on real problems |
| [Grafana dashboards](docs/grafana-dashboards.md) | proposed dashboard suite, panel forms, navigation and delivery order |
| [Releasing](docs/releasing.md) | how a version of arex and a version of the chart get published |
| [`charts/arex`](charts/arex) | the Helm chart itself, with the reasoning in `values.yaml` |
| [`monitoring/`](monitoring/) | alerting rules, for Prometheus, prometheus-operator and VictoriaMetrics |

## Quick start

```bash
VERSION=v0.9.0   # substitute the current release: https://github.com/krisiasty/arex/releases/latest
curl -fsSL "https://github.com/krisiasty/arex/releases/download/${VERSION}/arex_${VERSION}_linux_amd64.tar.gz" \
  | tar xz

cat > config.yaml <<'YAML'
collect:
  interfaces:
    enabled: true
switches:
  - host: https://10.10.0.11
    username: prometheus
    password: secret         # use passwordFile for anything real
    name: leaf1
    tlsSkipVerify: true      # replace with a pin or a CA — see docs/tls.md
YAML

./arex -check -config config.yaml   # validates without connecting
./arex -config config.yaml
curl -s localhost:9100/metrics | grep '^arista_info'
```

Then follow one of the installation guides above, which cover credentials properly, certificate verification, and
what to collect at which interval.

Container images are published for `linux/amd64` and `linux/arm64`:

```bash
docker pull ghcr.io/krisiasty/arex:v0.9.0
```

Every release archive has an SPDX SBOM beside it, covered by the same
`checksums.txt`. The images carry an SBOM and SLSA provenance as attestations
instead of files:

```bash
docker buildx imagetools inspect ghcr.io/krisiasty/arex:latest --format "{{ json .SBOM }}"
```

## Flags

| Flag | Description |
| --- | --- |
| `-config` | Path to the config file. Default `config.yaml`. YAML, and JSON is accepted since JSON is valid YAML |
| `-check` | Validate the config and every switch's TLS material and credential, then exit. Connects to nothing |
| `-log-level` | Minimum level to log: `debug`, `info`, `warn` (or `warning`), `error`. Default `info`. Also settable as `logLevel` in the config. `warn` drops the per-poll success record and keeps everything else |
| `-debug` | One structured log record per eAPI request. Also settable as `debug: true` in the config. Implies `-log-level=debug` |
| `-version` | Print the version, commit, build time and Go version, then exit |
| `-licenses` | Print the licences of everything linked into the binary, then exit |

## Design notes

A few decisions that are easy to misread as accidents:

- **Collection is opt-in.** An absent `collect` block is an error, not a default, because defaulting it either way
  would silently change what a deployment gathers.
- **Modules have their own intervals.** Optical power drifts over days and the PHY's FEC registers are not
  refreshed by EOS on a timer at all, so polling everything at one rate wastes the switch's eAPI budget. See
  [how often to poll each module](docs/configuration.md#how-often-to-poll-each-module).
- **Staleness is bounded per command, not per scrape.** One working command must not hold the whole scrape "fresh"
  while everything else silently ages.
- **A poll is one batched request.** Spreading commands out would lower the peak the switch sees, but the peak was
  measured and is not there, while one request per command would multiply authentications by the command count.
- **Credentials come from files and survive rotation.** On a rejection arex re-reads the file, and retries only if
  the secret actually changed — so a wrong password still costs exactly one request per poll.

## Building

```bash
go build -o arex .
go test ./...
./hack/gen-notices.sh    # regenerate the embedded third-party notices
```

Requires Go 1.27. `docker build -t arex .` builds the container from source; released images come from
`Dockerfile.goreleaser`, which copies an already-built binary onto the same pinned distroless base.

## Security

Report a vulnerability privately through the repository's **Security** tab, not as an issue. What is in scope, what
is deliberately not, and what arex already does are in [SECURITY.md](SECURITY.md).

## Licensing

arex is Apache-2.0. `LICENSE` and `NOTICE` apply to arex itself.

`internal/legal/THIRD_PARTY_NOTICES` reproduces the licence texts, copyright notices and upstream `NOTICE`
contents of everything linked into the binary. It is embedded, so a container with no shell can still answer for
itself — as it can about which build it is:

```bash
arex -version
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
