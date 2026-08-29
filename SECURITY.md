# Security policy

## Reporting a vulnerability

Report it privately through GitHub, not as an issue: open the repository's
**Security** tab and choose **Report a vulnerability**. That opens a private
advisory only the maintainers can see, so the problem is not public before there
is a fix.

Please do not open a public issue, a pull request, or a discussion for anything
you believe is a security problem.

Include enough to reproduce it: the output of `arex -version`, which names the
version, the commit and the build time, the relevant configuration with secrets
removed, and what an attacker would gain. A concrete sequence beats a scanner
finding.

**Response is best effort.** arex is maintained by one person alongside other
work. Reports are read and taken seriously, but no response time is promised —
a commitment nobody can keep is worth less than an honest statement that there
is none.

## Supported versions

The latest release. Fixes go into a new release rather than being backported;
there is no long-term support branch.

## What counts as a vulnerability

arex holds read-only credentials for network switches and exposes an inventory
of them. The classes below are in scope:

- **Credential disclosure.** A switch password or scrape password appearing in
  logs at any verbosity, in metrics, in an error message, or in `/status`.
- **Authentication bypass.** Reaching `/metrics` or `/status` without
  credentials when `listenAuth` is set — particularly through the probe
  exemption, which matches on path.
- **TLS verification defeated.** A certificate accepted that should not be: a
  flaw in the pin comparison, in CA verification, in client-certificate
  verification, or a reload that serves a certificate the configuration does
  not name.
- **Timing attacks** against credential comparison.
- **A panic or hang reachable from switch input.** arex polls many switches from
  one process, so a single malformed response that stops it takes monitoring
  down for the whole fleet. The decoders are fuzzed for exactly this.
- **Unbounded resource consumption** driven by a switch response or a scrape.
- **Privilege problems** in the container image or the systemd unit.

## What is not a vulnerability

These are documented, deliberate, and opt-in. Reporting them is welcome as an
issue, not as an advisory:

- `tlsSkipVerify: true` on a switch not verifying that switch's certificate. It
  exists because a stock EOS certificate has no subject alternative names and
  cannot be verified by hostname; arex refuses to start without an explicit
  choice per switch, and [TLS](docs/tls.md) explains the alternatives.
- Metrics being served over plain HTTP without authentication by default. Both
  are available and off unless configured — see
  [securing the endpoint](docs/operations.md#securing-the-endpoint).
- `/livez` and `/readyz` answering without credentials, including on a separate
  plain-HTTP `probeAddress`. They report only whether arex is running.
- The switch password being readable by the process that has to authenticate
  with it.
- Anything that requires root on the host arex runs on, or write access to its
  configuration.

## What arex already does

So a report need not re-cover this ground:

- Credentials are never logged, at any verbosity. The `Authorization` header is
  not logged, and neither is the password.
- Credentials are compared in constant time, and both username and password are
  compared even when the first has already failed.
- An HTTP-level rejection is never retried per command, so a wrong password
  costs one authentication attempt per poll rather than one per command — the
  difference between a failing account and a locked one.
- Passwords and certificates are read from files, so they can be delivered by
  systemd credentials or a Kubernetes secret and rotated without a restart.
- The config, the eAPI decoders and the metric renderer are fuzzed, and the
  decoders run on the poll goroutines where a panic would be fatal.
- Released binaries carry their dependencies' licences and notices, and an SPDX
  SBOM listing every module compiled in, covered by the same `checksums.txt`.
  The images carry an SBOM and SLSA provenance as attestations, attached at
  build time.
- The container image runs as a non-root UID on a digest-pinned distroless base,
  with no shell and nothing writable.

## Scope

This policy covers arex itself: the binary, the container image, the Helm chart,
and the deployment examples in `deploy/`. It does not cover Arista EOS, the
switches arex polls, or Prometheus.
