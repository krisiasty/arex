# TLS

How to verify a switch's certificate, and why a stock EOS switch cannot be verified by hostname.

Every switch needs exactly one of `caFile`, `pinnedCertSha256`, or `tlsSkipVerify`, set on that switch. arex
refuses to start otherwise, rather than defaulting to something wrong in either direction: verifying fails against
every stock switch, and silently skipping hides that nothing is being checked.

## Why the stock certificate cannot be verified

A factory switch serves `ARISTA_DEFAULT_SELF_SIGNED_PROFILE`. Inspect it with:

```text
show management security ssl certificate | json
show management security ssl profile detail | json
```

On EOS 4.35.4M that certificate has:

- a common name of `ARISTA_DEFAULT_SELF_SIGNED_PROFILE` — the profile name, not a hostname
- **no `subjectAltName` extension at all**, its only extension being `subjectKeyIdentifier`
- validity from 1970 to 9999, so it never expires and never rotates on its own

EOS diagnoses this itself: the profile reports `errorType: "hostnameMismatch"` while still calling itself `valid`,
so the switch knowingly serves a certificate that does not identify it.

Go has matched hostnames against SANs only, with no common-name fallback, since 1.15. With no SANs there is no
hostname and no IP address that can ever match, so **adding this certificate to a trust store does not help** —
the failure is naming, not trust:

```text
x509: cannot validate certificate for 192.0.2.33 because it doesn't contain any IP SANs
```

That is why arex offers pinning. It is not a workaround for self-signed certificates in general; it is the only
verification possible against a switch whose certificate names nothing.

## Option 1: replace the certificate (recommended)

Issue certificates from your own CA, or self-signed with correct SANs covering the address arex connects to, bind
them to an SSL profile, and point eAPI at that profile. Then give arex the CA bundle:

```json
{ "host": "https://10.10.0.11", "caFile": "/etc/arex/switch-ca.pem" }
```

This is the only option that survives certificate rotation without reconfiguring arex, and the only one where a
compromised switch key does not go unnoticed indefinitely.

## Option 2: pin the certificate

Works against a stock switch with no changes on the switch at all. Read the fingerprint from the host arex will
run on, which also proves the network path works:

```bash
openssl s_client -connect 10.10.0.11:443 </dev/null 2>/dev/null \
  | openssl x509 -noout -fingerprint -sha256
```

```json
{ "host": "https://10.10.0.11", "pinnedCertSha256": "A1:B2:...:90" }
```

Colons and case are ignored, so the `openssl` output can be pasted verbatim. arex pins the leaf certificate's DER
digest and enforces it on resumed TLS sessions as well as fresh ones.

Read the fingerprint over a path you trust — reading it over the network you are trying to protect only tells you
what the network says today. Ideally take it from the switch console. A pin here is unusually stable because the
default certificate never expires, so it changes only if someone regenerates it; when that happens arex logs both
the presented and the expected digest so the new value can be verified and configured.

## Option 3: skip verification

```json
{ "host": "https://10.10.0.11", "tlsSkipVerify": true }
```

Applies to that switch alone, so some switches can be pinned while others are skipped. Setting it alongside
`caFile` or `pinnedCertSha256` is rejected: the two are contradictory instructions, and quietly preferring either
one would hide the mistake.

It was a single fleet-wide setting once. That made it too easy to add a switch that inherited it without anyone
deciding to — the file said nothing about that switch's verification, and nothing was verified. Stating it per
switch means an unverified switch is visible in the config as a line someone wrote.

Defensible on an isolated out-of-band management network, but anything able to intercept that network can feed
arex whatever it likes — including metrics showing every optic healthy.

---

Back to the [README](../README.md). See also [switch configuration](switch-configuration.md).
