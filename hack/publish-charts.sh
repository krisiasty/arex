#!/usr/bin/env bash
# Publish every chart in charts/ to ghcr.io, skipping any version already there.
#
# Charts are versioned independently of arex, so most pushes carry an unchanged
# chart. Publishing regardless would either overwrite a published artifact or
# fail on a commit that changed no chart, so each is published only if its
# version is not already in the registry.
#
# That check is the whole reason this is safe to run unattended on every merge
# to main, which is what decouples the chart's release cadence from arex's: a
# chart-only fix ships when it merges rather than waiting for the next version
# tag. The release workflow runs it too, as a backstop for a tag whose chart
# never went through main.
#
# The caller is expected to have authenticated to the registry already.
set -euo pipefail

export LC_ALL=C

repo_dir="$(cd "$(dirname "$0")/.." && pwd)"
cd "$repo_dir"

owner="${1:-${GITHUB_REPOSITORY_OWNER:-}}"
if [[ -z "$owner" ]]; then
	printf 'publish-charts: no registry owner given; pass one or set GITHUB_REPOSITORY_OWNER\n' >&2
	exit 1
fi

registry="oci://ghcr.io/${owner}/charts"

# ::notice:: puts the line in the run summary rather than only in the log.
# Outside Actions it is noise, so it is only added there.
notice() {
	if [[ -n "${GITHUB_ACTIONS:-}" ]]; then
		printf '::notice::%s\n' "$1"
	else
		printf '%s\n' "$1"
	fi
}

mkdir -p dist/charts

published=0
for chart in charts/*/; do
	metadata="$(helm show chart "$chart")"
	name="$(awk '$1=="name:"{print $2}' <<<"$metadata")"
	version="$(awk '$1=="version:"{print $2}' <<<"$metadata")"

	if [[ -z "$name" || -z "$version" ]]; then
		printf 'publish-charts: %s has no name or version\n' "$chart" >&2
		exit 1
	fi

	if helm show chart "${registry}/${name}" --version "$version" >/dev/null 2>&1; then
		notice "${name} ${version} is already published, skipping"
		continue
	fi

	helm package "$chart" --destination dist/charts
	helm push "dist/charts/${name}-${version}.tgz" "$registry"
	notice "published ${name} ${version}"
	published=$((published + 1))
done

# Publishing nothing is a legitimate outcome, but it should be visible rather
# than inferred from the absence of output.
notice "${published} chart(s) published"
