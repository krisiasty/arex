# Running arex

Endpoints, filtered scrapes, logging, shutdown behaviour, and how the poll loop paces itself.

```bash
# Build
go build -o arex .

# Validate a config without starting: exits non-zero and says what is wrong
./arex -check -config config.yaml

# Run
./arex -config config.yaml

# Run with per-request eAPI logging
./arex -config config.yaml -debug

# Print the licences of everything linked into this binary
./arex -licenses
```

`-check` loads the config and builds each switch's client without connecting, so it catches an unreadable CA bundle,
a malformed certificate pin and a credential file that has stopped being readable — the failures that otherwise
surface as a service that starts and immediately exits. [deploy/arex.service](../deploy/arex.service) runs it as
`ExecStartPre`, so a bad edit stops the restart instead of taking the exporter down.

## Endpoints

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

## Scraping one switch at a time

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

## Ad-hoc filtering

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
unknown module "nope": expected one of bgp, cooling, interfaces, ntp, phy,
power, processes, temperature, transceiver, version

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

## Logging

Logs are JSON Lines on stdout, one object per event, with UTC millisecond timestamps so records from different
hosts sort together. Debug raises the level rather than changing the format: a format that varied with verbosity
could not be parsed by anything downstream, and the per-request output is only worth having if it can be queried.

```json
{"time":"2026-08-28T09:19:18.537Z","level":"INFO","msg":"arex starting",
 "version":"v0.0.0-20260828085310-7a01577d0334+dirty","revision":"7a01577d0334",
 "go_version":"go1.27.0","switches":3,"poll_interval":"30s","staleness_limit":"90s","debug":false}
```

### When a switch cannot be reached

A poll that collects nothing is logged at `ERROR` under `collection failed`, with the cause in `detail` and the
switch in `switch`. The cause names what happened and what to check, rather than repeating the Go error:

```json
{"time":"2026-08-29T11:59:56.120Z","level":"ERROR","msg":"collection failed",
 "switch":"sw146-frontend-leaf-1",
 "detail":"no response from 10.0.0.1 within 10s: check routing and firewall rules to TCP 443,
 and that the switch is reachable from this host"}
```

| Cause | Usually means |
| --- | --- |
| `no response from <host> within <timeout>` | Packets are being dropped — a firewall, an ACL, or no return path. A refusal would be immediate. |
| `<host> refused the connection on TCP <port>` | Reached the switch, nothing listening. Check `management api http-commands` is enabled in the management VRF. |
| `no route to <host>` | Routing on this host, or the management VRF has no path. |
| `cannot resolve "<name>"` | DNS. Configuring the switch by address avoids the dependency. |
| `<host> presented a certificate we do not trust` | Set `caFile` to the signing CA, or `pinnedCertSha256` to the certificate. See [TLS](tls.md). |
| `the certificate presented by <host> has expired` | Renew the switch's eAPI certificate. |
| `the certificate presented by <host> is for a different name` | The certificate does not cover the name used to connect. |
| `<host> closed the connection before responding` | The switch accepted the request and then dropped it. |
| `<host> closed the connection it was holding open` | A keep-alive connection was closed between polls. Harmless once; if it repeats, look for an idle timeout. |

The underlying Go error is kept but not printed — `-debug` logs it verbatim alongside the request, which is where
to look when a message is not enough.

## Shutdown

arex stops cleanly on `SIGINT` or `SIGTERM`: pollers stop, and the HTTP server is given up to five seconds to
finish a scrape already in progress. Without that grace period a restart can cut a `/metrics` response mid-write,
which Prometheus records as a *failed scrape* rather than a clean gap — a distinction that matters if you alert on
scrape failures.

A second signal exits immediately. A poll already in flight is not interrupted; the eAPI request has its own
`scrapeTimeout`, and the process exits once the server has drained.

## Poll staggering

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

## Debug logging

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

## Securing the endpoint

arex serves plain HTTP by default, which is the usual arrangement for an exporter on a trusted network. Its
metrics are not neutral, though: they carry switch names, serials, BGP peer addresses and optic serials. On a
shared cluster, or anywhere the scrape crosses a boundary you do not control, both halves below are worth turning
on.

They are independent. TLS without authentication is a reasonable posture where the network already restricts who
can connect; authentication without TLS sends the password in clear on every scrape, so arex warns about it at
startup rather than refusing to start.

### TLS

```yaml
listenTLS:
  certFile: /etc/arex/tls/tls.crt
  keyFile: /etc/arex/tls/tls.key
```

Both or neither — half a TLS configuration is rejected at load. The files are checked at startup, so a wrong path
fails immediately instead of at the first scrape, and a private key readable beyond its owner produces a warning.

**Certificates are re-read when they change on disk.** cert-manager renews well before expiry without restarting
anything, and a certificate read only at startup would expire in memory while a valid one sat on disk. arex
re-stats the pair at most every 30 seconds and reloads when it changes. A half-written pair during a renewal keeps
the working certificate rather than taking the endpoint down.

### Mutual TLS

```yaml
listenTLS:
  certFile: /etc/arex/tls/tls.crt
  keyFile: /etc/arex/tls/tls.key
  clientCAFile: /etc/arex/tls/ca.crt
```

With `clientCAFile`, a caller must present a certificate signed by that bundle. This is the stronger control: no
shared secret exists to leak, rotate or land in a values file, and a client without a certificate is refused
during the handshake, before any request is served. Certificates are verified, not merely requested.

### Basic authentication

```yaml
listenAuth:
  basic:
    username: prometheus
    passwordFile: /run/credentials/arex.service/web-password
```

A file rather than an inline password, for the same reasons as the switch credentials: it can be delivered by
systemd's credential store or a Kubernetes secret, given restrictive permissions, and **re-read after a rejection,
so a rotated password needs no restart**. Credentials are compared in constant time, and both halves are compared
even when the username is already wrong, so a response time cannot reveal that the username was right.

**`/livez` and `/readyz` are never covered.** A Kubernetes liveness probe sends no credentials, so requiring them
there would turn a health check into a restart loop. Those endpoints report only whether arex is up. `/status` is
covered, because it names the switches.

| endpoint | with `listenAuth` set |
| --- | --- |
| `/metrics` | requires credentials |
| `/status` | requires credentials |
| `/livez` | open |
| `/readyz` | open |

### A separate port for probes

Exemption is enough for basic auth, but not for mutual TLS: `RequireAndVerifyClientCert` applies to the listener
rather than to a path, so a caller with no client certificate — a kubelet, for instance — is refused during the
handshake, before arex sees a path to exempt.

`probeAddress` puts `/livez` and `/readyz` on a second listener with neither TLS nor authentication:

```yaml
listenAddress: ":9100"
probeAddress: ":9101"
```

That listener serves **only** those two endpoints; `/metrics`, `/status` and `/health` return 404 there. Both
answer `ok` or a fixed error string and carry no switch data, so an unauthenticated plain-HTTP port gives away
only whether arex is up.

Without it, mutual TLS forces probes down to checking that the port accepts a connection, which loses the readiness
gate that waits for every switch to be polled once.

### Telling Prometheus

```yaml
scrape_configs:
  - job_name: arista
    scheme: https
    tls_config:
      ca_file: /etc/prometheus/arex-ca.crt
      # and, for mutual TLS:
      cert_file: /etc/prometheus/prometheus.crt
      key_file: /etc/prometheus/prometheus.key
    basic_auth:
      username: prometheus
      password_file: /etc/prometheus/arex-password
    static_configs:
      - targets: ["arex-host:9100"]
```

Under the Helm chart, `listen.tls` and `listen.basicAuth` take existing Secret names and the ServiceMonitor's
scheme follows automatically; what Prometheus needs in order to trust the certificate is set through
`serviceMonitor.tlsConfig` and `serviceMonitor.basicAuth`. See
[Install on Kubernetes with Helm](install-kubernetes.md).

### What arex logs at startup

```json
{"level":"INFO","msg":"listening","address":":9100",
 "endpoints":["/metrics","/livez","/readyz","/status"],
 "tls":true,"client_certificate":true,"auth":true}
```

Three booleans, so the posture is visible without inferring it from the config.

---

Back to the [README](../README.md). See also [metrics](metrics.md) and [configuration](configuration.md).
