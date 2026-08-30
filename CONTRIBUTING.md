# Contributing

Thanks for helping improve arex. Small, focused changes are easiest to review and safest to operate against network
infrastructure.

## Before opening an issue

- Search existing issues first.
- Use a GitHub issue form for bugs and feature requests.
- Report suspected vulnerabilities privately as described in [SECURITY.md](SECURITY.md). Do not open a public issue
  or pull request for them.
- Remove credentials, switch names, addresses, VLAN and VRF names, ASNs, certificate fingerprints, and other site
  identifiers from logs, configurations, screenshots, and EOS output.

Questions that are specific to operating arex should include the arex version, deployment model, EOS version, and a
minimal redacted configuration.

## Making a change

1. Fork the repository and create a focused branch. Names such as `fix/short-description`,
   `feat/short-description`, and `docs/short-description` fit the existing convention.
2. Add tests for behavior changes and update the relevant documentation.
3. Keep generated files current:

   ```bash
   ./hack/gen-monitoring.sh
   ./hack/gen-notices.sh
   ```

   Run only the generator relevant to your change. `gen-notices.sh` is needed when the dependency graph changes;
   `gen-monitoring.sh` is needed when `monitoring/alerts.yaml` changes.
4. Run the local checks:

   ```bash
   go test -race ./...
   go vet ./...
   golangci-lint run
   markdownlint-cli2 "**/*.md"
   typos
   ```

   Changes to shell scripts or the Helm chart should also run `shellcheck hack/*.sh` or `helm lint charts/arex` as
   appropriate.
5. Open a pull request describing the problem, the chosen behavior, and how it was verified.

## Test data

Real EOS output is valuable, but checked-in fixtures must be safe to publish. Replace every site-specific identifier,
including switch and interface descriptions, IP and MAC addresses, ASNs, VLANs, VNIs, VRFs, route targets, ESI
values, certificate material, serial numbers, and customer or tenant names. Use documentation address ranges from
RFC 5737 and non-identifying names such as `fabric-a`, `leaf-1`, and `tenant-a`.

Preserve the JSON shape and relationships that exercise the behavior. Anonymization must not turn a realistic fixture
into an unrelated synthetic one.

## Pull requests

Pull requests must pass the required checks and resolve review conversations before merging. A maintainer may ask for
a change to be split when independent behavior would be easier to review or revert separately.

By contributing, you agree that your contribution is licensed under the repository's Apache License 2.0.
