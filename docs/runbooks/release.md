# Release and image publication

## Status and authority

Releases are explicit, local and owner-initiated. An invocation of the Go Task
entry points below, on the coordinator Mac, builds the distributables from the
selected commit, signs and notarizes the macOS binaries, publishes a release on
Forgejo (`https://forgejo.triceratops-dory.ts.net/alexcatdad/scratchpad`) and
updates the Homebrew tap. No CI workflow builds release artifacts, signs, tags,
publishes, deploys, dispatches a release or updates the tap, and releases
neither wait for nor read CI status.

Releases go to Forgejo only. `github.com/alexcatdad/scratchpad` stays a source
push mirror: it receives the release tag through the mirror, but no GitHub
release is created. Release tags do not trigger the homelab deployment; a
deployment is a separate, owner-initiated step (see
[Kubernetes deployment](homelab-deploy.md)).

The tool is [release-tools](https://github.com/alexcatdad/release-tools)
(`reltool`), pinned in `Taskfile.yml` to a tag and the commit it must resolve to.
The app-owned configuration is `release.json`; the build and validation recipes
are `Taskfile.yml` tasks that call `scripts/release-app-build.sh` and
`scripts/release-validate.sh`.

Published versions are daily CalVer `YYYY.MM.DD.N` (UTC date, month and day
zero-padded, a daily counter starting at 1) with tag `vYYYY.MM.DD.N`, allocated
by release-tools (v0.4.0 and later; pinned to v0.4.1). Earlier `v0.x.y` tags and the monthly `vYYYY.M.N`
releases (`v2026.10.1`) stay as history on GitHub; the first daily version sorts
above them. The
TypeScript/Go SDK packages are not part of this release contract (see
[Open decisions](#open-decisions)).

## What a release publishes

- `scratchpad-YYYY.MM.DD.N-darwin-arm64.zip`: `Scratchpad.app` for Apple Silicon,
  macOS 13 or later; Developer ID signed, notarized and stapled. It bundles the
  capture service, keychain helper and `scratchpad-mcp`.

release-tools additionally uploads `SHA256SUMS` and
`scratchpad-<version>.manifest.json`, then updates `Casks/scratchpad.rb` in
`alexcatdad/homebrew-tap`. That cask is the only tap entry this app owns. Its
download URL points at the Forgejo release
(`https://forgejo.triceratops-dory.ts.net/alexcatdad/scratchpad/releases/download/<tag>/...`),
so the cask installs and upgrades only on machines on the tailnet. The
`scratchpad-mcp` formula is deprecated in favor of the cask and receives no
further releases; its v2026.10.1 assets remain as history.

The app's versions come only from the release-tools environment; nothing is
derived from CI variables, Git refs or commit messages. Apple bundle versions
allow at most three integers, so `CFBundleShortVersionString` and
`CFBundleVersion` both get `MACOS_SHORT_VERSION` (`2026.11.05.1` becomes
`2026.1105.1`). The full `RELEASE_VERSION` goes into the app's `ReleaseVersion`
Info.plist key, which the app shows, and into the bundled `scratchpad-service`
and `scratchpad-mcp` (`--version`). `scripts/release-validate.sh` checks all
three.

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

Also required locally: Go Task, Git, Go (the version in `mcp/go.mod`), a Forgejo
access token with the `write:repository` scope for this repository, `gh`
authenticated with push access to `alexcatdad/homebrew-tap`,
Xcode command-line tools (`codesign`, `notarytool`), Node 24 (see
[Release](#release)), and the release-tools repository with its tags fetched.
`Taskfile.yml` looks for it at `../../../infrastructure/release-tools` relative
to this checkout (the owner's `alexcatdad/infrastructure/release-tools` layout);
set `RELTOOL_SRC` to use another path. `task release:*` stops with an error if
neither exists and the pinned reltool is not already built in the cache. reltool reads the Forgejo token from `FORGEJO_TOKEN`, then the `fj`
key store (`fj -H forgejo.triceratops-dory.ts.net auth login`), then the macOS
Keychain item `release-tools-forgejo`; it never prints the token. Secret values
never go into this repository, this documentation or logs; the config stores
names only.

## Release

From a clean checkout of the commit to release, already pushed to Forgejo.
Run the release under Node 24: the SDK integration test in `task check`
requires a `v24.` runtime, so a newer default Node fails the check step. With
fnm, prefix each command, for example `fnm exec --using 24 -- task release`.

```sh
task release:check     # read-only: next version, signing identity, tools, disk (does not contact Forgejo)
task release:prepare   # reserve version, run checks, build, sign, notarize, verify
task release           # prepare, then draft release, SHA-256 verification, publish, tap
```

`task release:check` and `task release:prepare` do not publish. `task release`
creates a draft Forgejo release, uploads every asset, verifies each by
downloading it and comparing its SHA-256 with the local final artifact,
publishes, confirms the tag, and updates the tap cask from the published release
with the owner's local `gh` and Git. It runs unattended once started. Pass
reltool flags after `--`, for example `task release:prepare -- --workers`.

The `check` task (the full local gate: lint, build and tests with coverage
floors for npm, Go and Swift) runs as a required step in a clean checkout.

## Recovery

```sh
task release:resume            # continue an interrupted release with the same version and artifacts
task release:abandon -- VERSION  # only before publication; the counter stays consumed
```

A failed tap step is retried by `task release:resume` without rebuilding or
reserving a new version. Published assets are never replaced. Forgejo has no
immutable releases, so this is reltool's guarantee: never edit a published
release's assets by hand. State lives under
`$XDG_STATE_HOME/release-tools/scratchpad/`; back it up with the local config.

## Container image

No container image is published. The owner's homelab deployment builds the
`go-runtime` target of the `Dockerfile` itself from the release tag when the
owner deploys it; publishing a release or pushing its tag does not start a
deployment; see [Kubernetes deployment](homelab-deploy.md). Images published
before calendar versioning (up to `v0.3.0`) remain in the registry as history.

## Verify a published release

Installed-release acceptance is manual and local: install the cask on a Mac,
check the version, the signature and notarization (`spctl -a -vv`), and run the
end-to-end suite against the installed executable. The former GitHub
`release-acceptance.yml` workflow was removed; there is no hosted release CI.

A green validation run, local signing, publication and installed acceptance are
separate outcomes; record each one.

## Open decisions

1. **Container images.** Not published (decided): the homelab builds the image
   from the release tag at deployment.
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
