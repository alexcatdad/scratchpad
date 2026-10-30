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
The app-owned configuration is `release.json`; the build and validation recipes
are `Taskfile.yml` tasks that call `scripts/release-app-build.sh` and
`scripts/release-validate.sh`.

Published versions are `YYYY.M.N` (UTC year and month without leading zeros, a
monthly counter starting at 1) with tag `vYYYY.M.N`. Earlier `v0.x.y` tags and
releases stay as history; the first calendar version sorts above them. The
TypeScript/Go SDK packages are not part of this release contract (see
[Open decisions](#open-decisions)).

## What a release publishes

- `scratchpad-YYYY.M.N-darwin-arm64.zip`: `Scratchpad.app` for Apple Silicon,
  macOS 13 or later; Developer ID signed, notarized and stapled. It bundles the
  capture service, keychain helper and `scratchpad-mcp`.

release-tools additionally uploads `SHA256SUMS` and
`scratchpad-<version>.manifest.json`, then updates `Casks/scratchpad.rb` in
`alexcatdad/homebrew-tap`. That cask is the only tap entry this app owns. The
`scratchpad-mcp` formula is deprecated in favor of the cask and receives no
further releases; its v2026.10.1 assets remain as history.

The app's marketing and bundle versions come only from `RELEASE_VERSION`
(`MACOS_SHORT_VERSION`, `MACOS_BUNDLE_VERSION`); nothing is derived from CI
variables, Git refs or commit messages.

The build signs with the keychain helper's Developer ID provisioning profile at
`~/.config/scratchpad/keychain-helper.provisionprofile` (override with
`SCRATCHPAD_KEYCHAIN_PROFILE`). The profile must include the certificate of the
configured signing identity; regenerate it in the Apple Developer portal when
the certificate changes.

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

## Container image

No container image is published. The owner's homelab deployment builds the
`go-runtime` target of the `Dockerfile` itself from the release tag when it is
deployed; see [Kubernetes deployment](homelab-deploy.md). Images published
before calendar versioning (up to `v0.3.0`) remain in the registry as history.

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

1. **Container images.** Not published (decided): the homelab builds the image
   from the release tag at deployment. `release-acceptance.yml` no longer
   verifies image pulls.
2. **TypeScript client package.** `scratchpad-api-client-X.Y.Z.tgz` was attached
   to v0.3.0 and is no longer published. The tap updater now ignores assets that
   no formula slot uses, so it could be added to the matrix. It is not a
   straightforward step yet: the build needs Node 24, `npm ci`, a package version
   set per release (`packages/clients/typescript/package.json` is a fixed `0.3.0`
   and the old workflow required it to equal the tag), and `npm pack`, and a
   target must own an architecture-neutral file. Decide the owning target (for
   example `linux-amd64`) and whether the coordinator may require Node, or use a
   separate release app or npm publication. The Go client module is consumed by Go
   tag (`packages/clients/go/vX.Y.Z`), which is not created by this tooling; the
   former `verify-released-clients.sh` check is not run by any workflow.
3. **Native macOS app and cask.** Decided 2026-10-30: releases publish the app
   archive and update the `scratchpad` cask; the MCP-only formula is deprecated.
4. **Fedora package.** Linux ships as tar.gz for Homebrew only; no RPM is built.

## Historical releases

v0.1.2 through v0.3.0 were published by the earlier workflow-assisted procedure
(locally signed archives uploaded to a draft, then published and delivered to the
tap by GitHub Actions). Their tags and assets are immutable history and are never
retagged. Their accounts remain in the [readiness ledger](mvp-readiness.md).
