# Releasing

How a version of arex and a version of the chart get published, and in which order.

Two things are released, on two different triggers:

| Artifact | Published by | Goes to |
| --- | --- | --- |
| Binaries, SBOMs, container images, GitHub release | pushing a `v*` tag | GitHub releases and `ghcr.io/krisiasty/arex` |
| Helm chart | merging to `main` | `ghcr.io/krisiasty/charts/arex` |

They are separate because the chart is versioned independently: most releases carry an unchanged chart, and a
chart-only fix should not have to wait for a version of arex it has nothing to do with.

## Releasing a new version of arex

**1. Merge the work.** An ordinary pull request. Nothing about it is release-specific.

**2. Bump the chart, in its own pull request or the last one before the tag.** In `charts/arex/Chart.yaml`:

```yaml
version: 0.7.0          # the chart's own version
appVersion: "v0.7.0"    # the image tag an install gets when it does not set image.tag
```

Both, together. `appVersion` lives inside `Chart.yaml`, so changing it changes the chart, and a chart version
already in the registry is never overwritten — so `appVersion` cannot move without `version` moving too.

**3. Merge it.** That publishes the chart. Nothing to run: the `publish-chart` job on `main` pushes any chart
whose version is not already in the registry, and skips otherwise.

**4. Tag, and push the tag.** This is the release.

```bash
git checkout main && git pull
git tag -a v0.7.0 -m "v0.7.0"
git push origin v0.7.0
```

Everything else follows from the tag: binaries for linux and darwin on amd64 and arm64, an SBOM beside each,
multi-arch images tagged `v0.7.0`, `v0.7` and `latest`, and a GitHub release whose changelog is built from the
commits since the previous tag.

**Do steps 3 and 4 together.** See [the ordering hazard](#the-ordering-hazard) for what happens otherwise.

## Releasing a chart-only change

A template fix or a new values key, with no new arex: bump `version`, leave `appVersion` pointing at the release
it was already pointing at, and merge. No tag. The chart is published by the merge.

This is the case the split exists for. Before it, chart publishing happened only on a tag, so a template fix sat
unpublished until someone cut a release of arex that had nothing to do with it.

## Releasing arex without touching the chart

Legitimate, and sometimes right: a change that affects no chart default can ship without republishing the chart.
Skip step 2 and tag as usual.

The consequence is that the chart keeps naming the previous image, so an install that does not set `image.tag`
gets the previous version. The release workflow emits a warning when it notices:

```text
::warning::charts/arex/ appVersion is v0.6.0, but this release is v0.7.0;
           an install without image.tag will get v0.6.0
```

That is a warning rather than a failure because the chart is allowed to lag. Read the run summary and decide.

## The ordering hazard

Bumping `appVersion` to a version that is never tagged publishes a chart pointing at an image that does not
exist. The merge publishes chart 0.8.0 with `appVersion: v0.8.0` immediately, and if `v0.8.0` is never tagged,
`ghcr.io/krisiasty/arex:v0.8.0` is never built.

It cannot be fixed in place. Publishing skips any version already in the registry, so correcting it means
publishing 0.8.1, or deleting the package version in ghcr.io by hand.

What it breaks is narrow — running deployments already have their image, and anyone setting `image.tag` is
unaffected. A *new* install, or an upgrade to that chart version without a pinned tag, gets `ImagePullBackOff`.

So keep the gap between merging the bump and pushing the tag to minutes.

## Verifying

A green workflow means its steps exited zero, not that the artifacts are what you meant. Ask the registries.

```bash
# the release itself
gh run watch "$(gh run list --workflow=Release --limit 1 --json databaseId -q '.[0].databaseId')"
gh release view v0.7.0

# every image tag should be the same digest
for t in v0.7.0 v0.7 latest; do
  docker buildx imagetools inspect "ghcr.io/krisiasty/arex:${t}" | head -2
done

# the chart, and what it will install
helm show chart oci://ghcr.io/krisiasty/charts/arex --version 0.7.0
```

Four archives and four SBOMs, one image digest shared by all three tags, and a chart whose `appVersion` is the
tag you just pushed.

## Rehearsing

`gh workflow run Release` on `main` runs the dry run: the same checkout, the same third-party notices check, the
same binaries and the same multi-arch image builds, with no registry login and no write permissions. It proves
the release path works without holding the ability to use it.

Worth running before a release that touches `.goreleaser.yaml`, `Dockerfile.goreleaser`, or the workflow itself.
Nothing about the release path is exercised by ordinary CI, so otherwise the first time it runs is the time it
matters.

## Where the pieces live

| File | What it does |
| --- | --- |
| [`.github/workflows/release.yaml`](../.github/workflows/release.yaml) | the tag-triggered release, and the dry run |
| [`.github/workflows/check.yaml`](../.github/workflows/check.yaml) | CI, and the `publish-chart` job on `main` |
| [`hack/publish-charts.sh`](../hack/publish-charts.sh) | the chart publish itself, shared by both workflows |
| [`.goreleaser.yaml`](../.goreleaser.yaml) | what gets built, which image tags, changelog filters |
