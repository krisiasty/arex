# Install on bare metal or a VM

arex runs off the switch, so it needs nothing but outbound HTTPS to the management addresses and an inbound port
for Prometheus. This guide puts it under systemd with the switch password held in systemd's credential store.

## 1. Install the binary

```bash
VERSION=v0.2.0
ARCH=amd64   # or arm64

curl -fsSLO "https://github.com/krisiasty/arex/releases/download/${VERSION}/arex_${VERSION}_linux_${ARCH}.tar.gz"
curl -fsSLO "https://github.com/krisiasty/arex/releases/download/${VERSION}/checksums.txt"
sha256sum --check --ignore-missing checksums.txt

tar xzf "arex_${VERSION}_linux_${ARCH}.tar.gz"
sudo install -o root -g root -m 0755 arex /usr/local/bin/arex
arex -licenses | head -1
```

The tarball also carries `LICENSE`, `NOTICE`, `THIRD_PARTY_NOTICES` and the `deploy/` examples used below.

## 2. Prepare the switch

Nothing here works until eAPI is enabled in the management VRF and a read-only account exists. See
[switch configuration](switch-configuration.md), and note that a role only restricts anything once local command
authorization is enabled.

Get the certificate fingerprint while you are on the switch — a stock EOS certificate has no subject alternative
names, so pinning is the only way to verify it:

```bash
openssl s_client -connect 10.10.0.11:443 </dev/null 2>/dev/null \
  | openssl x509 -noout -fingerprint -sha256
```

## 3. Write the config

```bash
sudo install -d -o root -g root -m 0755 /etc/arex
sudo tee /etc/arex/config.yaml >/dev/null <<'YAML'
listenAddress: ":9100"
pollInterval: 30s
stalenessLimit: 90s

# Where systemd will place the credential. The path is fixed and predictable,
# so it can be named literally.
passwordFile: /run/credentials/arex.service/switch-password

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
YAML
```

See [configuration](configuration.md) for what each group costs and why `transceiver` and `phy` default to slower
intervals.

## 4. Hand the password to systemd, not to the config

The password never goes in `config.yaml`. `LoadCredential=` places it on tmpfs as `0400`, owned by the service
user, and — unlike an environment variable — it is not inherited by child processes and does not appear in
`/proc/<pid>/environ`.

### Plaintext on disk, protected by permissions

The simplest form. The file is readable only by root; systemd copies it into the unit's credential directory.

```bash
printf '%s' 'the-switch-password' | sudo tee /etc/arex/switch-password >/dev/null
sudo chmod 0400 /etc/arex/switch-password
```

`printf` rather than `echo`, deliberately: `echo` appends a newline, and while arex strips trailing line endings,
not every tool does.

The unit then carries:

```ini
LoadCredential=switch-password:/etc/arex/switch-password
```

### Encrypted at rest with systemd-creds

Better: the secret on disk is unreadable even to root without the host key, and can be sealed to the TPM.

```bash
# host key only, or --with-key=auto to use the TPM when one is present
sudo systemd-creds encrypt --name=switch-password \
  /etc/arex/switch-password /etc/arex/switch-password.cred

sudo shred -u /etc/arex/switch-password    # the plaintext is no longer needed
sudo chmod 0400 /etc/arex/switch-password.cred
```

Swap the unit's directive:

```ini
LoadCredentialEncrypted=switch-password:/etc/arex/switch-password.cred
```

Either way the credential lands at `/run/credentials/arex.service/switch-password`, which is what `config.yaml`
above points at. Nothing else in the unit changes.

Two caveats. A host-key credential cannot be decrypted on a different machine, so a rebuilt host needs the secret
re-encrypted — keep the source of truth elsewhere. And `systemd-creds encrypt --pretty` prints a ready-made
`SetCredentialEncrypted=` line if you would rather embed the blob in the unit than keep a file.

## 4b. Optional: serve over TLS, and require a password

Skip this on a trusted management network. Worth doing where the scrape crosses
a boundary you do not control — the metrics carry switch names, serials and BGP
peers. See [securing the endpoint](operations.md#securing-the-endpoint) for what
each half does.

**The private key cannot simply be dropped in `/etc/arex`.** The unit runs with
`DynamicUser=yes`, so arex has a transient UID that owns nothing: a key at
`0400 root:root` is unreadable, and a key at `0444` is readable by every user on
the box. Deliver it the same way as the switch password — through systemd's
credential store, which places it on tmpfs owned by the service user:

```ini
LoadCredential=switch-password:/etc/arex/switch-password
LoadCredential=tls-key:/etc/arex/tls/tls.key
LoadCredential=web-password:/etc/arex/web-password
```

The certificate is public, so it can stay in `/etc/arex` as an ordinary
world-readable file. Only the key and the scrape password need the credential
store:

```bash
sudo install -d -o root -g root -m 0755 /etc/arex/tls
sudo install -o root -g root -m 0444 tls.crt /etc/arex/tls/tls.crt
sudo install -o root -g root -m 0400 tls.key /etc/arex/tls/tls.key
printf '%s' 'a-long-random-string' | sudo tee /etc/arex/web-password >/dev/null
sudo chmod 0400 /etc/arex/web-password
```

Then point the config at the certificate directly and at the credentials
directory for the two secrets:

```yaml
listenTLS:
  certFile: /etc/arex/tls/tls.crt
  keyFile: /run/credentials/arex.service/tls-key
  # Optional. With it, callers must present a certificate signed by this CA and
  # no shared secret exists at all -- see below.
  # clientCAFile: /etc/arex/tls/ca.crt

listenAuth:
  basic:
    username: prometheus
    passwordFile: /run/credentials/arex.service/web-password
```

Renewal is picked up without a restart: arex re-reads the pair when it changes
on disk. That applies to the certificate under `/etc/arex`; a key delivered
through `LoadCredential` is copied at start, so **rotating the key needs a
restart** while rotating only the certificate does not. If your CA reissues both
together, restart on renewal.

Verify what arex ended up with:

```bash
journalctl -u arex -n 20 --output=cat | jq 'select(.msg=="listening")'
```

```json
{"msg":"listening","address":":9100","tls":true,"client_certificate":false,"auth":true}
```

Then from outside:

```bash
curl -s --cacert /etc/arex/tls/ca.crt -u prometheus:... https://arex-host:9100/metrics | head -1
curl -s -o /dev/null -w '%{http_code}\n' --cacert /etc/arex/tls/ca.crt https://arex-host:9100/livez
```

The second should be `200` without credentials: the probes are deliberately not
authenticated.

## 5. Install the unit

[`deploy/arex.service`](../deploy/arex.service) is hardened: `DynamicUser`, `ProtectSystem=strict`, an empty
capability set, `SystemCallFilter=@system-service`, and no write access anywhere.

```bash
sudo cp deploy/arex.service /etc/systemd/system/arex.service
# If you chose the encrypted form, switch the directive:
sudo sed -i 's|^LoadCredential=|LoadCredentialEncrypted=|; s|switch-password$|switch-password.cred|' \
  /etc/systemd/system/arex.service

sudo systemd-analyze verify /etc/systemd/system/arex.service
sudo systemctl daemon-reload
sudo systemctl enable --now arex
```

`DynamicUser=yes` means there is no account to create: systemd allocates a transient UID with no home and no
shell. The credential is readable by that UID and nothing else.

## 6. Verify

```bash
systemctl status arex
journalctl -u arex -n 20 --output=cat | jq
curl -s localhost:9100/readyz
curl -s localhost:9100/status | jq
curl -s localhost:9100/metrics | grep '^arista_info'
```

`/readyz` returns 503 until every switch has been polled once, so a persistent 503 means arex cannot reach one —
`/status` says which, and the log line names the eAPI error verbatim.

The unit runs `arex -check` as `ExecStartPre`, so a broken config or an unreadable credential stops the start
rather than taking the exporter down. Use it before restarting after an edit:

```bash
sudo arex -check -config /etc/arex/config.yaml
```

## 7. Scrape it

```yaml
scrape_configs:
  - job_name: arista
    static_configs:
      - targets: ["arex-host:9100"]
```

One arex serves every switch it polls, and each series carries its own `switch` label, so this is one target
regardless of fleet size. Metrics come from cache, so the scrape interval is independent of `pollInterval`.

## Applying a configuration change

arex reads its config once, at startup, so an edit needs a restart:

```bash
sudo arex -check -config /etc/arex/config.yaml   # catch the mistake first
sudo systemctl restart arex
```

There is deliberately no `systemctl reload`. arex ignores `SIGHUP` rather than
dying — it logs that the signal was ignored and what to do instead — but a unit
advertising a reload that reloads nothing would be worse than not having one.

## Upgrading

```bash
sudo systemctl stop arex
# install the new binary as in step 1
sudo arex -check -config /etc/arex/config.yaml
sudo systemctl start arex
```

Gaps in the series are the only consequence: arex holds no state that survives a restart, and Prometheus records
the gap rather than a failed scrape because shutdown drains any scrape in flight.

## Rotating the password

Write the new secret, then reload the credential — arex re-reads the file itself after a rejection, but the file
systemd exposes is a copy made at start, so the unit has to restart to see a new one:

```bash
printf '%s' 'the-new-password' | sudo tee /etc/arex/switch-password >/dev/null
sudo systemctl restart arex
```

The self-healing re-read matters where the file is updated underneath a running process, which is the Kubernetes
case rather than this one.

---

Back to the [README](../README.md). See also [configuration](configuration.md),
[switch configuration](switch-configuration.md) and [running arex](operations.md).
