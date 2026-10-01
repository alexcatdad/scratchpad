# Native, container, and Homebrew release

## Status and authority

The release pipeline is implemented in `.github/workflows/release.yml`. The first distribution release, [v0.1.2](https://github.com/alexcatdad/scratchpad/releases/tag/v0.1.2), is published; its signed-artifact verification and fresh-runner installation acceptance passed. See the [readiness ledger](mvp-readiness.md) for exact source revisions and run links. The owner signs and notarizes macOS MCP archives locally; GitHub Actions verifies those finished archives, builds Linux binaries and the multiarchitecture image, publishes the release, and updates the existing Homebrew tap.

The prior MVP patch release is [v0.1.3](https://github.com/alexcatdad/scratchpad/releases/tag/v0.1.3), correcting exact timestamp ordering and SQLite readiness/startup validation. Its tagged source is `ef5cb1a30594b43657c5bc327101cdd0463cc4a4`; [publication run 36859022794](https://github.com/alexcatdad/scratchpad/actions/runs/36859022794) passed native verification, image publication, release publication and tap delivery. Keep prior published versions immutable.

The complete optional AI and PostgreSQL product is published as [v0.2.0](https://github.com/alexcatdad/scratchpad/releases/tag/v0.2.0), tagged at `1e860e4f2f02978deda95f26051642f961fd86eb`. [Publication run 36929564159](https://github.com/alexcatdad/scratchpad/actions/runs/36929564159) passed all native verification, multiarchitecture image publication, release assets and Homebrew delivery. Both local notarization submissions were accepted. Installed MCP 0.2.0 passes the local formula test and Apple notarization requirement; the owner dashboard runs the published image with its original volume and login intact. [Fresh-runner acceptance 36930356827](https://github.com/alexcatdad/scratchpad/actions/runs/36930356827) passed macOS/Linux Homebrew installation, installed MCP authentication, the optional AI workflow, both image architectures and published-image backup/restore.

Apple private keys and notarization credentials stay in the owner's macOS Keychain. GitHub does not need an Apple certificate, certificate password, Apple account password, or notarization secret. This follows the owner's accepted local-signing instruction.

## One-time configuration

Use the existing `action-runners` GitHub environment, restricted to `main` and `v*` tags:

| Kind     | Name            | Purpose                                                                         |
| -------- | --------------- | ------------------------------------------------------------------------------- |
| Variable | `APP_ID`        | GitHub App client ID for tap delivery                                           |
| Secret   | `APP_SECRET`    | App private key with contents-write access to `alexcatdad/homebrew-tap`         |
| Variable | `APPLE_TEAM_ID` | Public team identifier used to verify the locally signed binaries independently |

On 1 October 2026, `APP_ID` was set to the owner-supplied GitHub App client ID, the owner replaced the `APP_SECRET` placeholder, and `APPLE_TEAM_ID` was set to `CX6D6KGCT5`. The [access check](https://github.com/alexcatdad/scratchpad/actions/runs/36850995620) authenticated the App successfully; the later release run delivered the formula to the tap.

Run the dedicated access check before preparing a release:

```sh
gh workflow run release-access.yml --repo alexcatdad/scratchpad --ref main
gh run list --repo alexcatdad/scratchpad --workflow release-access.yml
gh run watch RUN_ID --repo alexcatdad/scratchpad --exit-status
```

This check requests a short-lived GitHub App token with contents-write permission on `alexcatdad/homebrew-tap` and reads repository metadata. It does not modify the tap or publish artifacts. The action revokes the token at the end of the job.

Configure the Developer ID Application identity and a `notarytool` credential profile in the local Keychain using Apple's [notarization workflow](https://developer.apple.com/documentation/security/customizing-the-notarization-workflow). Never paste secret values into commands recorded in history, issues, or agent messages. The scripts use an existing profile by name.

The repository has not selected a license. The tap formula does not invent one.

## Prepare and sign locally

Use a clean checkout of the intended commit with all relevant checks passing. CI must be successful at that exact SHA on `main`; an earlier green revision is insufficient. Use the pinned Go toolchain. Native builds disable automatic VCS metadata so local and CI binaries can be compared after normalizing signatures.

The remaining-product release is `v0.2.0`. The example below documents the completed procedure; do not rerun tag creation or overwrite its published assets. Use a new version when preparing another release.

```sh
export RELEASE_TAG=v0.2.0
git tag "$RELEASE_TAG"
git push origin "$RELEASE_TAG"
export APPLE_SIGNING_IDENTITY='Developer ID Application: YOUR NAME (TEAMID)'
export APPLE_TEAM_ID=TEAMID
export NOTARYTOOL_PROFILE=your-existing-keychain-profile
bash scripts/release-macos.sh
```

The helper refuses a dirty or mismatched checkout, builds ARM64 and AMD64, signs with hardened runtime and a secure timestamp, verifies the signing team, and submits each ZIP using the local Keychain profile. Both submissions must return `Accepted`. It writes the two archives, receipts, and a `macos-release.json` manifest binding the tag, source SHA, signing team, and archive/receipt checksums. These generated files stay under ignored `dist/release/`.

Bare Mach-O command-line binaries and ZIPs cannot receive stapled tickets. Publish the exact accepted ZIPs; online Apple trust assessment remains required. Do not repackage the archives after notarization.

## Upload the draft and dispatch

Pushing a tag intentionally does not start publication: local signing and upload must finish first.

```sh
gh release create "$RELEASE_TAG" --repo alexcatdad/scratchpad \
  --verify-tag --draft --title "$RELEASE_TAG" --notes 'Release preparation in progress.'
gh release upload "$RELEASE_TAG" --repo alexcatdad/scratchpad \
  "dist/release/scratchpad-mcp-$RELEASE_TAG-darwin-arm64.zip" \
  "dist/release/scratchpad-mcp-$RELEASE_TAG-darwin-amd64.zip" \
  dist/release/notarization-darwin-arm64.json \
  dist/release/notarization-darwin-amd64.json \
  dist/release/macos-release.json
gh workflow run release.yml --repo alexcatdad/scratchpad --ref main -f tag="$RELEASE_TAG"
```

Use `gh release view` before retrying creation/upload. Do not overwrite assets while a workflow is running. A retry may repair an unpublished draft; a published release is immutable.

## Pipeline verification

1. Preflight checks stable tag syntax, ancestry on `main`, exact-source CI, draft state, and GitHub App access. It fixes the uploaded manifest checksum for the run. GitHub requires contents-write access to see unpublished drafts, including asset downloads; only the preflight, native verification, and publication jobs receive that repository permission.
2. macOS runners rebuild unsigned reference binaries from the selected commit. They download the exact archive/receipt/manifest names and reject changed manifests, mismatched source/team/checksums, unexpected ZIP contents, invalid signatures, missing hardened runtime, and failed Apple trust assessment.
3. Signature-normalized copies of the downloaded binary and rebuilt reference must match byte for byte. The original signed archive is never modified. An uploaded `Accepted` JSON alone is not proof of notarization: `codesign --test-requirement '=notarized'` must succeed. `spctl` first triggers online ticket retrieval, but its application assessment rejects standalone command-line tools as “not an app”. The subsequent explicit code requirement is the mandatory CLI verification gate.
4. Linux ARM64 and AMD64 are built in CI. The image job publishes both Linux platforms to `ghcr.io/alexcatdad/scratchpad:vX.Y.Z` only after native verification succeeds.
5. Publication checks the complete inventory, generates checksums, revalidates the tag SHA, and publishes the draft. The tap job downloads the published assets, verifies checksums, and updates `Formula/scratchpad-mcp.rb` using the GitHub App. Downgrades and force pushes are prohibited.

## Optional-extension release preparation

The optional-extension release is published as v0.3.0 for pgvector, public GitHub profiles and generated clients. [Publication 36935307686](https://github.com/alexcatdad/scratchpad/actions/runs/36935307686) and [fresh acceptance 36935929030](https://github.com/alexcatdad/scratchpad/actions/runs/36935929030) passed. The following procedure records its delivery; use a new version for later releases.
Before tagging, require successful CI at its exact source. Create both `v0.3.0`
and `packages/clients/go/v0.3.0` at that source; push both without changing older
tags. The second tag makes the separate Go module consumable through normal Go
module resolution. Follow the same local signing, draft upload and publication
procedure using the new product tag.

The release pipeline builds and packs the TypeScript workspace at the tagged source,
checks its package version matches the release, and includes the compiled tarball
in the release checksum inventory. Fresh release acceptance installs this asset
and the matching remote Go module into separate temporary consumer projects and
exercises real authenticated HTTP calls. No npm registry publication is required.

## Artifact inventory

- `scratchpad-mcp-vX.Y.Z-darwin-arm64.zip`
- `scratchpad-mcp-vX.Y.Z-darwin-amd64.zip`
- `scratchpad-mcp-vX.Y.Z-linux-arm64.tar.gz`
- `scratchpad-mcp-vX.Y.Z-linux-amd64.tar.gz`
- `notarization-darwin-arm64.json`
- `notarization-darwin-amd64.json`
- `macos-release.json`
- `scratchpad-api-client-X.Y.Z.tgz` (v0.3.0 and later)
- `checksums.txt`

## Recovery and acceptance

For a failed unpublished release, fix the cause and dispatch again after completing the draft. If publication succeeded but tap delivery failed, dispatch with `-f homebrew_only=true`; this uses published assets without rebuilding or resigning. Publication across GHCR, GitHub Releases and the tap is not atomic. Record each outcome separately.

After publication verify all downloaded checksums, both macOS signatures and Apple acceptance, both GHCR platforms starting with readiness on matching hosts or verified emulation, clean macOS/Linux Homebrew installation and `brew test`, version output, and a fresh authenticated MCP retrieval session. Check GHCR visibility and repository linkage before promising anonymous pulls. Neither a green ordinary CI run nor local signing alone proves full release acceptance.

## Validate changes

```sh
actionlint
shellcheck scripts/release-*.sh scripts/verify-macos-release.sh
node --test scripts/release-formula.test.mjs
npm run lint
```

The `v0.1.2` local-signing handoff passed Apple notarization assessment and source-byte comparison on CI. Future failures must still be investigated; never bypass either check or publish unsigned substitutes.

## Initial preparation correction

The unpublished `v0.1.0` preparation exposed a ZIP layout mismatch and an application-only trust check. Its Apple submissions were accepted, but no assets were published. The tag is preserved. The unpublished `v0.1.1` preparation exposed cold-cache ticket retrieval. Both preparation tags remain preserved. Published `v0.1.2` uses root-level ZIP binaries without resource sidecars, online ticket retrieval, and Apple's explicit notarization code requirement for command-line tools.

## Verify the published release on fresh runners

After publication, dispatch `release-acceptance.yml` with the published tag. It installs and tests Homebrew on fresh macOS and Linux runners, exercises the installed executable against disposable authenticated server fixtures, verifies all published checksums, anonymously pulls both GHCR architectures, and tests the published Linux binary with the published image through backup/restore.

```sh
gh workflow run release-acceptance.yml --repo alexcatdad/scratchpad --ref main -f tag=v0.1.3
gh run list --repo alexcatdad/scratchpad --workflow release-acceptance.yml
```
