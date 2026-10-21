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

## Upload the draft and dispatch publication

After local signing and notarization, upload the exact finished outputs. The
following example uses a new `v1.0.2` tag; choose an unused version and set
`RELEASE_TAG` consistently with the local build.

```sh
export RELEASE_TAG=v1.0.2
gh release create "$RELEASE_TAG" --repo alexcatdad/scratchpad \
  --verify-tag --draft --title "Scratchpad app $RELEASE_TAG" \
  --notes 'Apple Silicon macOS application with bundled MCP and offline capture service.'
gh release upload "$RELEASE_TAG" --repo alexcatdad/scratchpad \
  "dist/release/scratchpad-$RELEASE_TAG-darwin-arm64.zip" \
  "dist/release/notarization-app-$RELEASE_TAG.json" \
  dist/release/macos-app-release.json
gh workflow run release-macos-app.yml --repo alexcatdad/scratchpad --ref main \
  -f tag="$RELEASE_TAG"
```

The workflow requires successful CI at the tagged main-branch source, verifies
archive/receipt hashes and declared source, validates all nested signing teams,
checks Apple notarization and Gatekeeper, then publishes the existing draft.
Its source identity is a signer-supplied manifest; it does not claim reproducible
Swift binary comparison. It neither signs in CI nor builds a new server image.
Do not invoke the historical combined MCP/container workflow for an app release.

The workflow then opens a cask PR in `alexcatdad/homebrew-tap`; publication of
the app and merging the tap PR are separate steps. It runs only when manually
dispatched on `main`. Merging this preparation PR does not publish the app.

### Required permissions

The existing `action-runners` environment supplies `APP_ID`, `APP_SECRET` and
the public `APPLE_TEAM_ID`. Install the GitHub App only on the target tap with
**Contents: read/write** and **Pull requests: read/write**, accepting any pending
installation permission update. `release-access.yml` requests both permissions
for that one repository without writing it. The app release job uses its own
repository token's contents-write access to read draft assets and publish the
release; the tap App token cannot publish Scratchpad releases. Apple signing
credentials stay local. No administration or workflow-write permission is needed.

Token minting precedes publication so missing app permissions stop the workflow
before the draft becomes public. If publication succeeds but tap PR creation
fails, preserve the public release and prepare only the cask update below;
do not overwrite assets or rerun draft publication against a public release.

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
checks. The workflow explicitly grants only cask trust before loading it:

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
intentional open descriptor. The workflow scopes its style exceptions to
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
