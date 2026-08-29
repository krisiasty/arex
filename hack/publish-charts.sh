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

# warn is notice for something that should be looked at. It does not fail the
# run: declining to publish is the correct outcome of the check below, not an
# error, and the release job publishes the chart once the images exist.
warn() {
	if [[ -n "${GITHUB_ACTIONS:-}" ]]; then
		printf '::warning::%s\n' "$*"
	else
		printf 'publish-charts: %s\n' "$*" >&2
	fi
}

# imageExists reports whether a chart's appVersion names an image that is
# actually in the registry.
#
# A chart whose appVersion has never been built installs nothing: helm resolves
# image.tag to appVersion by default, and the pull fails. That is worse than not
# publishing, because a published chart version is never overwritten -- the fix
# would be a new chart version or deleting the package by hand.
#
# So the bump merges, this declines to publish, and the release job's own run of
# this script publishes it after GoReleaser has pushed the images. A chart-only
# change, whose appVersion still names a release that exists, is unaffected and
# still publishes on merge.
imageExists() {
	local ref="$1" tag="$2" host path

	host="${ref%%/*}"
	path="${ref#*/}"
	if [[ "$host" != "ghcr.io" ]]; then
		notice "cannot check ${ref}:${tag}: only ghcr.io is understood here"
		return 0
	fi
	if [[ -z "${GITHUB_TOKEN:-}" ]]; then
		notice "cannot check ${ref}:${tag}: GITHUB_TOKEN is not set"
		return 0
	fi

	# ghcr takes the token base64-encoded as a bearer credential. base64 wraps
	# its output at 76 columns on GNU coreutils, which would split the header.
	local bearer
	bearer="$(printf '%s' "$GITHUB_TOKEN" | base64 | tr -d '\n')"

	curl -sf -o /dev/null \
		-H "Authorization: Bearer ${bearer}" \
		-H "Accept: application/vnd.oci.image.index.v1+json" \
		-H "Accept: application/vnd.docker.distribution.manifest.list.v2+json" \
		-H "Accept: application/vnd.oci.image.manifest.v1+json" \
		"https://${host}/v2/${path}/manifests/${tag}"
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

	# What this chart installs when nobody sets image.tag.
	appVersion="$(awk '$1=="appVersion:"{gsub(/"/,"",$2); print $2}' <<<"$metadata")"
	imageRepo="$(helm show values "$chart" | awk '
		/^image:/ { inImage = 1; next }
		/^[^[:space:]]/ { inImage = 0 }
		inImage && $1 == "repository:" { print $2; exit }
	')"

	if [[ -n "$appVersion" && -n "$imageRepo" ]] && ! imageExists "$imageRepo" "$appVersion"; then
		warn "${name} ${version} names ${imageRepo}:${appVersion}, which is not in the registry;" \
			"not publishing a chart that would install nothing. Tag ${appVersion} to build it."
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
