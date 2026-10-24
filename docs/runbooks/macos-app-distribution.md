# Publish the macOS application through Homebrew

The primary macOS installation is the Scratchpad app cask. It bundles the
collector, keychain helper and stdio MCP adapter. The historical standalone
`scratchpad-mcp` formula remains separate; do not silently replace its binary
or remove existing client configuration.

## Prepare the app release

Use a clean Apple Silicon checkout of an exact main-branch commit with passing
canonical CI. The first app cask candidate is version 1.0.2, build 4. Confirm
that the stable tag is unused before creating it. Follow
[native packaging](macos-native-release.md) to build, Developer ID sign,
notarize, staple and verify that exact tagged source. Keep signing credentials
and provisioning material local.

The release must include these outputs from `scripts/release-macos-app.sh`:

- `scratchpad-v1.0.2-darwin-arm64.zip`
- `notarization-app-v1.0.2.json`
- `macos-app-release.json`

## Publication status

The native app is not part of the local release contract in `release.json`, and
no workflow publishes it: `release-macos-app.yml`, its draft-upload step and the
tap-writer permissions it used were removed with the move to local releases (see
[release](release.md)). `scripts/release-macos-app.sh` still produces the signed
and notarized archive from an exact tag, but it has never been run with owner
credentials, no app release exists and the tap owns no `scratchpad` cask. Whether
to add an app target to `release.json`, add a cask to the tap, or keep the app
unpublished is an open decision. Until that is decided, the cask steps below are
a record of the prepared design, not a procedure to run.

## Prepare and publish the cask

In a clean checkout of the current `alexcatdad/homebrew-tap` default branch:

```sh
node /path/to/scratchpad/scripts/release-cask.mjs v1.0.2 \
  /path/to/scratchpad/dist/release/scratchpad-v1.0.2-darwin-arm64.zip \
  /path/to/homebrew-tap
ruby -c /path/to/homebrew-tap/Casks/scratchpad.rb
```

The generator pins the archive checksum, declares Apple Silicon/macOS 13 and
preserves the existing collector upgrade handoff. The referenced release asset
must be publicly downloadable before publishing the cask. Add the app command
to the tap README and leave unrelated packages unchanged.

Validate the cask with the installed Homebrew's developer-mode loader and style
checks. The prepared design explicitly granted only cask trust before loading it:

```sh
brew trust --cask alexcatdad/tap/scratchpad
HOMEBREW_DEVELOPER=1 brew info --cask alexcatdad/tap/scratchpad
```

Homebrew's [Tap Trust](https://docs.brew.sh/Tap-Trust) policy distinguishes
adding a tap from trusting executable definitions. Fully qualified installation
trusts only this cask; do not grant whole-tap trust or disable trust enforcement.
Commit and deliver the scoped tap change through its repository policy.

The cask retains third-party Ruby flight hooks because its transaction lock
must survive until the Homebrew process exits. Structured flight steps cannot
express that descriptor lifetime. Homebrew's [Cask Cookbook](https://docs.brew.sh/Cask-Cookbook#stanza-flight_steps)
still permits legacy hooks for third-party taps. The generator documents this
intentional open descriptor. The prepared design scoped its style exceptions to
`Cask/InstallSteps`, `Style/AutoResourceCleanup` and `Style/FileOpen` for this
cask; all remaining style checks and the hook/locking tests
remain required. This is separate from cask trust and does not disable trust.
After publication, replace the publication-pending notices in the README and
installation guide with a link to the exact app release.

## Verify installation and connection

```sh
brew update
brew install --cask alexcatdad/tap/scratchpad
open -a Scratchpad
```

On a disposable account or new machine, verify Gatekeeper acceptance, first
launch, local queue/key readiness, server authentication, and the app's MCP
configuration preview. The configured command must resolve to the bundled
adapter. Restart the client and verify an authenticated project read and a
synthetic capture receipt. Follow the native runbook for offline capture,
login-item and upgrade/rollback acceptance. Preserve existing queues and keys.

Report publication, installed-app acceptance and live server acceptance
separately. A generated cask or successful signature check does not establish
that Homebrew installation or client connection works.
