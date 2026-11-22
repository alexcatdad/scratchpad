# Publish the macOS application through Homebrew

The macOS installation is the `scratchpad` app cask in `alexcatdad/homebrew-tap`.
The app bundles the `scratchpad` command, the capture service and the keychain
helper in `Scratchpad.app/Contents/Helpers`. It does not bundle the
`scratchpad-mcp` stdio server. The historical standalone `scratchpad-mcp`
formula is deprecated with `replacement_cask: "scratchpad"` and receives no
further releases; existing installs keep working, and `brew info` points users
to the cask.

## Release

Releases run locally through release-tools; see [release](release.md). One
`task release` builds and signs `Scratchpad.app` from the selected commit,
notarizes and staples `scratchpad-<version>-darwin-arm64.zip`, publishes the
GitHub release and updates `Casks/scratchpad.rb` through the tap's
`scripts/apply-release-update`. The cask downloads from the GitHub release.
The tap's updater rewrites only the version, URL and SHA-256 of the existing
cask.

## Install and upgrade

```sh
brew install --cask alexcatdad/tap/scratchpad
open -a Scratchpad
```

An app already in `/Applications` from a local build blocks installation; quit
the menu bar app and use `brew install --cask --force alexcatdad/tap/scratchpad`
to replace it. The encrypted capture queue and connection settings stay in
`~/Library/Application Support/Scratchpad`; the queue key stays in the macOS
Keychain.

`brew upgrade --cask scratchpad` replaces the app bundle. The running capture
service keeps its previous binary until **Restart Capture Service** (in the
app's **General** page, or offered in the menu bar panel) or the next login; the cask caveats say so. The
`scratchpad` command runs from the new bundle on its next call. The service's
`--prepare-upgrade` / `--finish-upgrade` handoff is not wired into the cask
because Homebrew 7 rejects Ruby install hooks.

Installing the app does not deploy a server or transfer another computer's local
configuration, signer or queued captures.
