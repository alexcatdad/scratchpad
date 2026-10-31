# Publish the macOS application through Homebrew

The macOS installation is the `scratchpad` app cask in `alexcatdad/homebrew-tap`.
The app bundles the capture service, keychain helper and stdio MCP adapter. The
historical standalone `scratchpad-mcp` formula is deprecated with
`replacement_cask: "scratchpad"` and receives no further releases; existing
installs keep working, and `brew info` points users to the cask.

## Release

Releases run locally through release-tools; see [release](release.md). One
`task release` builds and signs `Scratchpad.app` from the selected commit,
notarizes and staples `scratchpad-<version>-darwin-arm64.zip`, publishes the
GitHub release and updates `Casks/scratchpad.rb` through the tap's
`scripts/apply-release-update`.

The tap's updater rewrites only the version, URL and SHA-256 of an existing
cask. The cask therefore lands in the tap at a placeholder version: merge that
tap change immediately before the first app release so no broken version is
visible for longer than the release run.

## Install and upgrade

```sh
brew install --cask alexcatdad/tap/scratchpad
open -a Scratchpad
```

An app already in `/Applications` from a local build blocks installation; quit
the tray interface and use `brew install --cask --force alexcatdad/tap/scratchpad`
to replace it. Queue data, credentials and lifecycle settings stay in
`~/Library/Application Support/Scratchpad`.

`brew upgrade --cask scratchpad` replaces the app bundle. The running capture
service keeps its previous binary until **Restart Capture Service** in the
menu-bar popover or the next login; the cask caveats say so. The service's
`--prepare-upgrade` / `--finish-upgrade` handoff is not wired into the cask
because Homebrew 7 rejects Ruby install hooks.

Installing the app does not deploy a server or transfer another computer's local
configuration, signer or queued captures.
