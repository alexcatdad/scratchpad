# Release and image publication

## Status and authority

Releases are explicit, local and owner-initiated. An invocation of the Go Task
entry points below, on the coordinator Mac, builds the distributables from the
selected commit, signs and notarizes the macOS binaries, publishes a GitHub
release and updates the Homebrew tap. GitHub Actions only validates: no workflow
builds release artifacts, signs, tags, publishes, deploys, dispatches a release
or updates the tap, and releases neither wait for nor read CI status.

The tool is [release-tools](https://github.com/alexcatdad/release-tools)
(`reltool`), pinned in `Taskfile.yml` to a tag and the commit it must resolve to.
The app-owned configuration is `release.json`; the build, validation and deploy
recipes are `Taskfile.yml` tasks that call `scripts/release-build.sh`,
`scripts/release-validate.sh` and `scripts/deploy-image.sh`.

Published versions are `YYYY.M.N` (UTC year and month without leading zeros, a
monthly counter starting at 1) with tag `vYYYY.M.N`. Earlier `v0.x.y` tags and
releases stay as history; the first calendar version sorts above them. The
native macOS application and the TypeScript/Go SDK packages are not part of this
release contract (see [Open decisions](#open-decisions)).

## What a release publishes

- `scratchpad-mcp-vYYYY.M.N-darwin-arm64.zip` (macOS 13 or later; Developer ID signed, notarized)
- `scratchpad-mcp-vYYYY.M.N-darwin-amd64.zip` (macOS 13 or later; Developer ID signed, notarized)
- `scratchpad-mcp-vYYYY.M.N-linux-arm64.tar.gz` (Fedora 42 baseline; unsigned)
- `scratchpad-mcp-vYYYY.M.N-linux-amd64.tar.gz` (Fedora 42 baseline; unsigned)

The Linux binaries are static (`CGO_ENABLED=0`) and also serve Homebrew on Linux.
release-tools additionally uploads `SHA256SUMS` and
`scratchpad-<version>.manifest.json`. These replace the former
`checksums.txt`, `notarization-darwin-*.json` and `macos-release.json` assets;
notarization status is recorded in the manifest. File names keep the previous
`scratchpad-mcp-v<version>-<os>-<arch>` form so the tap updater can match them
to `Formula/scratchpad-mcp.rb`. That formula (`scratchpad-mcp`) is the only tap
entry this app owns; there is no `scratchpad` cask.

Bare Mach-O binaries and ZIPs cannot receive stapled tickets, so `staple` is not
enabled; Apple trust is checked online. Version strings come only from
`RELEASE_VERSION` (injected as `main.version`); nothing is derived from CI
variables, Git refs or commit messages.

## One-time local configuration

The coordinator needs a config file outside any checkout at
`$XDG_CONFIG_HOME/release-tools/config.json` (default
`~/.config/release-tools/config.json`; schema `release-tools/local/v1`, example
in the release-tools repository). Provide:

- `coordinator`, `limits`, and optionally `state_dir`;
- `signing.scratchpad.identity`: the name of the Developer ID Application
  identity in the login Keychain;
- `signing.scratchpad.notary_profile`: the name of an existing `notarytool`
  Keychain profile;
- optional `workers[]` (SSH build workers). The matrix builds entirely on the
  coordinator; Linux targets are `runs: any`, so workers are used only with
  `--workers`, and none is required.

Also required locally: Go Task, Git, Go (the version in `mcp/go.mod`), `gh`
authenticated with push access to this repository and `alexcatdad/homebrew-tap`,
Xcode command-line tools (`codesign`, `notarytool`), and the release-tools
repository checked out next to this one at `../release-tools` with its tags
fetched. Secret values never go into this repository, this documentation or
logs; the config stores names only.

## Release

From a clean checkout of the commit to release, already pushed to GitHub:

```sh
task release:check     # read-only: next version, signing identity, tools, disk
task release:prepare   # reserve version, run checks, build, sign, notarize, verify
task release           # prepare, then draft release, SHA-256 verification, publish, tap
```

`task release:check` and `task release:prepare` do not publish. `task release`
creates a draft release, uploads every asset, verifies each by SHA-256 against
the local final artifacts, publishes, confirms the tag, and updates the tap
formula from the published release with the owner's local `gh` and Git. Pass
reltool flags after `--`, for example `task release:prepare -- --workers`.

The `check` task (Go format, vet and race tests for `mcp/`) runs as a required
step. The wider CI suite is independent validation; the release does not depend
on it. Run `npm run lint`, `npm test` and the other [quality gates](../../README.md)
yourself when the change warrants it.

## Recovery

```sh
task release:resume            # continue an interrupted release with the same version and artifacts
task release:abandon -- VERSION  # only before publication; the counter stays consumed
```

A failed tap step is retried by `task release:resume` without rebuilding or
reserving a new version. Published assets are never replaced. State lives under
`$XDG_STATE_HOME/release-tools/scratchpad/`; back it up with the local config.

## Image publication

`task deploy` replaces the former manually dispatched image workflow. It runs
`scripts/deploy-image.sh` from a clean checkout of a published release commit
(the newest by default; `task deploy -- VERSION` selects another) and pushes the
`go-runtime` image to `ghcr.io/alexcatdad/scratchpad:sha-<commit>` for
`linux/amd64` and `linux/arm64`, with provenance and SBOM attestations, then
prints the immutable digest. It does not touch a cluster; see
[Kubernetes deployment](homelab-deploy.md). Run it only when the owner intends to
publish an image.

Local prerequisites, never committed: `docker login ghcr.io` with a token that can
write packages to the repository's package, and a `docker buildx` builder that can
build both platforms with attestations (for example a `docker-container` builder
with QEMU). Set `SCRATCHPAD_IMAGE` to publish to another repository.

## Verify a published release

The manually dispatched `release-acceptance.yml` workflow (read-only, never a
release gate) installs `alexcatdad/tap/scratchpad-mcp` on fresh macOS and Linux
runners, checks the version, the macOS signature and the notarization
requirement, and runs the end-to-end suite against the installed executable:

```sh
gh workflow run release-acceptance.yml --repo alexcatdad/scratchpad --ref main -f tag=vYYYY.M.N
```

A green validation run, local signing, publication and installed acceptance are
separate outcomes; record each one.

## Open decisions

1. **Container images.** The former `release.yml` also pushed
   `ghcr.io/alexcatdad/scratchpad:vX.Y.Z` (multi-arch, provenance, SBOM) for every
   release. Registry publishing is outside the GitHub-release contract and is not
   done by `task release`. `task deploy` publishes only the `sha-<commit>` image.
   Decide whether to also publish a version-tagged image from `task deploy`.
   `release-acceptance.yml` no longer verifies image pulls because of this.
2. **TypeScript client package.** `scratchpad-api-client-X.Y.Z.tgz` was attached to
   v0.3.0. release-tools sends every non-RPM artifact of a target to the tap
   updater, which rejects assets that the `scratchpad-mcp` formula does not use,
   so the tarball cannot join the matrix as it stands. It is not published
   anymore. Options: a separate release app, a tap-ignored asset kind in
   release-tools, or npm publication. The Go client module is consumed by Go tag
   (`packages/clients/go/vX.Y.Z`), which is not created by this tooling; the
   former `verify-released-clients.sh` check is not run by any workflow.
3. **Native macOS app and cask.** The app has never been released (v0.3.0 has no
   app asset) and the tap owns no `scratchpad` cask, so the app is not in
   `release.json`. See [macOS app distribution](macos-app-distribution.md). Decide
   whether to keep, add or remove an app target and cask; `scripts/release-macos-app.sh`
   and `scripts/release-cask.mjs` are retained unchanged, still expect a
   `vX.Y.Z` tag, and are not wired to any task.
4. **Fedora package.** Linux ships as tar.gz for Homebrew only; no RPM is built.

## Historical releases

v0.1.2 through v0.3.0 were published by the earlier workflow-assisted procedure
(locally signed archives uploaded to a draft, then published and delivered to the
tap by GitHub Actions). Their tags and assets are immutable history and are never
retagged. Their accounts remain in the [readiness ledger](mvp-readiness.md).
