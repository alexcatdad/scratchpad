# Build, sign and verify the native macOS app

This runbook describes reproducible local packaging and the checks required before a later owner-approved release. Running it does not publish a Homebrew cask or change a live MCP client configuration.

## Declared v1 platform support

| macOS                | Architecture            | v1 status        | Evidence                                                                                                                      |
| -------------------- | ----------------------- | ---------------- | ----------------------------------------------------------------------------------------------------------------------------- |
| 13 Ventura and later | Apple Silicon (`arm64`) | Supported target | The package deployment target is macOS 13. `SMAppService` is available from macOS 13, and Go 1.27 requires macOS 13 or later. |
| Any                  | Intel (`amd64`)         | Deferred         | No Intel build or acceptance lane is provided.                                                                                |
| Any                  | Linux                   | Deferred         | The native menu app and its `SMAppService` login agent are macOS-only.                                                        |

Packaging currently builds on the host architecture: SwiftPM and `go build` use their default host targets, and the bundle does not combine architectures. Build distribution candidates on an Apple Silicon Mac; do not describe a host build as universal. The current native build/test evidence is from macOS 27.0.1 on `arm64`. Installed acceptance on the declared macOS 13 floor remains pending, so the supported floor is a declared compatibility target, not a claim of lower-bound runtime acceptance.

Record the build host and selected toolchains with release evidence using the same commands as the current verification:

```sh
sw_vers -productVersion
uname -m
DEVELOPER_DIR=/Library/Developer/CommandLineTools xcrun --sdk macosx --show-sdk-version
DEVELOPER_DIR=/Library/Developer/CommandLineTools swift --version
go version
```

The observed values for the current native checks are macOS `27.0.1`, `arm64`, macOS SDK `27.0`, Swift `6.4.0.34.1`, and Go `1.27.2 darwin/arm64`. Keep these as host evidence only; they do not establish the macOS 13 floor or installed-app acceptance.

The platform floor follows both runtime requirements: Apple's [SMAppService documentation](https://developer.apple.com/documentation/servicemanagement/smappservice) says the API is for macOS 13 and later, and the [Go 1.27 release notes](https://go.dev/doc/go1.27#darwin) require macOS 13 Ventura or later.

## Portable checks

From the repository root:

```sh
macos/Scripts/build-and-test.sh
macos/Scripts/test-release-validator.sh
plutil -lint macos/Resources/Info.plist macos/Resources/com.alexcatdad.scratchpad.service.plist
```

SwiftPM tests exercise configuration merging, key-helper argument parsing and service lifecycle policies with synthetic values. They do not exercise Keychain access, SMAppService approval, a live server, or an installed app.

For a workspace-only native UI or menu-bar icon change, run the package tests with the service smoke fixture disabled. In this environment SwiftPM needs the installed SwiftUI macro plugin paths and a populated private dependency cache:

```sh
env -u SCRATCHPAD_TEST_SERVICE \
  DEVELOPER_DIR=/Library/Developer/CommandLineTools \
  CLANG_MODULE_CACHE_PATH=/private/tmp/scratchpad-clang-module-cache \
  SWIFTPM_MODULECACHE_OVERRIDE=/private/tmp/scratchpad-swift-module-cache \
  swift test --disable-sandbox --package-path macos \
    --scratch-path /private/tmp/scratchpad-native-policy-build \
    -Xswiftc -load-plugin-library -Xswiftc /Applications/Xcode.app/Contents/Developer/Platforms/MacOSX.platform/Developer/usr/lib/swift/host/plugins/libSwiftUIMacros.dylib \
    -Xswiftc -load-plugin-library -Xswiftc /Library/Developer/CommandLineTools/usr/lib/swift/host/plugins/testing/libTestingMacros.dylib
```

Leaving `SCRATCHPAD_TEST_SERVICE` unset makes the service smoke test return before spawning a process. This verifies compilation and portable policy tests only; it does not verify rendered menu appearance or launch the app.

The **macOS local-capture acceptance** workflow runs on relevant pull requests
and on manual dispatch using an ephemeral `macos-15` runner. It builds an
explicit disposable service fixture, runs the Swift IPC suite and focused Go
collector lifecycle checks, then exercises the real MCP → collector → SQLite
backend boundary. It records a workflow summary and retains test logs as an
artifact. The fixture is mandatory for the Swift smoke; a missing binary fails
the job instead of silently skipping that test.

It also runs the synthetic dev-app script test, covering default UI-only
replacement and the opt-in Go helper rebuild without launching a macOS app.

This lane automates synthetic functional checks, not installed acceptance. It
does not inspect rendered UI, access a real Keychain or login item, configure
Codex or Claude, authenticate to a live server, or test signing, notarization,
or Homebrew upgrades.

## Assemble an app bundle

Use a macOS host with the pinned SwiftPM dependencies, Go toolchain from `mcp/go.mod`, and the selected signing toolchain. The script builds the menu app, keychain helper, local service and stdio MCP binary, then assembles `Contents/MacOS`, `Contents/Helpers`, the login-agent plist.

```sh
APP_VERSION=1.0.0 \
BUILD_NUMBER=1 \
SIGNING_IDENTITY='Developer ID Application: Example (TEAMID)' \
KEYCHAIN_PROFILE='/secure/path/to/Scratchpad-Keychain.provisionprofile' \
KEYCHAIN_ACCESS_GROUP='TEAMID.com.alexcatdad.scratchpad.keychain-helper' \
macos/Scripts/package-app.sh /tmp/Scratchpad.app
```

An unsigned local bundle can be assembled by omitting `SIGNING_IDENTITY`; it is not distribution-ready and does not establish Data Protection Keychain access. Signed builds require an owner-provided provisioning profile and access group authorized for the nested `com.alexcatdad.scratchpad.keychain-helper` app; the script validates its team, helper application identifier and access-group authorization before replacing an output app. It derives native `com.apple.application-identifier` from the profile's `ApplicationIdentifierPrefix` (which can differ from its team identifier), embeds the profile in that helper app, and signs its entitlement claim. The bundle declares the current SQLite queue writer/readable schema as version 1; update those values only with a reviewed service schema migration and compatibility plan.

The service uses its default per-user Application Support database and Keychain item. Homebrew and the project tap own installation and updates. The app has no update checks, downloads, appcast or in-app installation controls. Homebrew replacement must preserve queue data, credentials and Pause/Stop state. Installed upgrade and collector restart acceptance remains a separate release gate.

The bundled helper accepts exactly `get|create` and the fixed identifier `com.alexcatdad.scratchpad.capture-queue.v1`; it emits only the raw 32-byte queue key on success. Exit codes are 0 for success, 2 for a locked/authentication-required keychain, 3 for a missing key, and 1 for invalid arguments or another helper error. Diagnostics never include key bytes or Security.framework error text.

The helper targets the Data Protection Keychain and must fail closed if macOS denies access. Apple documents that access groups come from code-signing entitlements and that restricted `keychain-access-groups` entitlements require a provisioning profile; command-line tools that claim them need an app-like bundle to embed that profile. For native macOS, the application ID entitlement is `com.apple.application-identifier`; the iOS `application-identifier` key is not emitted. See [TN3137](https://developer.apple.com/documentation/technotes/tn3137-on-mac-keychains), [TN3125](https://developer.apple.com/documentation/technotes/tn3125-inside-code-signing-provisioning-profiles), and [Keychain sharing](https://developer.apple.com/documentation/security/sharing-access-to-keychain-items-among-a-collection-of-apps). Local synthetic profile-metadata tests, argument tests and compilation do not validate the app/helper signing relationship or a Keychain read/write. Before distribution, establish the owner-controlled profile/entitlement arrangement for both executables and perform Keychain acceptance in a disposable macOS user context; do not switch to a file-based key or create a real queue key as a workaround.

## Validate the Homebrew release candidate

```sh
macos/Scripts/validate-bundle.sh /tmp/Scratchpad.app
macos/Scripts/validate-release.sh /tmp/Scratchpad.app
codesign --verify --deep --strict --verbose=2 /tmp/Scratchpad.app
spctl --assess --type execute --verbose=2 /tmp/Scratchpad.app
```

The release validator checks version and queue-schema metadata and rejects retired updater configuration or framework files. Homebrew cask publication, archive checksums and actual installation/upgrade acceptance remain separate work. No appcast or update-signing key is required.

## Create a signed and notarized release archive

Run only from a clean checkout of the exact stable tag on an Apple Silicon Mac. The owner supplies the Developer ID identity, team ID, provisioning profile and authorized access group used by `package-app.sh`, plus an existing `notarytool` Keychain profile. Credential values remain local; do not put secrets in the command or repository.

```sh
RELEASE_TAG=v1.0.0 \
APPLE_SIGNING_IDENTITY='Developer ID Application: Example (TEAMID)' \
APPLE_TEAM_ID='TEAMID' \
KEYCHAIN_PROFILE='/secure/path/to/Scratchpad-Keychain.provisionprofile' \
KEYCHAIN_ACCESS_GROUP='TEAMID.com.alexcatdad.scratchpad.keychain-helper' \
NOTARYTOOL_PROFILE='scratchpad-notary' \
bash scripts/release-macos-app.sh
```

The script calls the executable `macos/Scripts/package-app.sh`, verifies the signed bundle, submits a temporary ZIP to Apple, staples and assesses the accepted app, then creates the final `dist/release/scratchpad-vX.Y.Z-darwin-arm64.zip`, notarization receipt and `macos-app-release.json` checksum manifest. It does not upload or publish them. This release script has not yet been run with owner signing credentials; the commands above describe the prepared path, not completed signing or notarization evidence.

Generate a local draft cask after the release archive exists:

```sh
node scripts/release-cask.mjs v1.0.0 \
  dist/release/scratchpad-v1.0.0-darwin-arm64.zip \
  /path/to/homebrew-tap
```

This writes `Casks/scratchpad.rb` in the supplied local tap checkout, computes its SHA-256, and refuses a version downgrade. It does not publish or update the remote tap. The cask prepares the collector before replacement, keeps a private Homebrew transaction lock until the `brew` process exits, and leaves service recovery to the installed app after the transaction is complete.

The existing collector executable provides the upgrade handoff used by a future cask hook:

```sh
/Applications/Scratchpad.app/Contents/Helpers/scratchpad-service --prepare-upgrade
# Homebrew replaces the app bundle here.
/Applications/Scratchpad.app/Contents/Helpers/scratchpad-service --finish-upgrade
```

The cask's `uninstall_preflight` invokes `--prepare-upgrade` through the currently installed helper before Homebrew moves the old bundle. The new cask's `preflight` repeats preparation idempotently and covers a first install or an older cask without the handoff hook. Preparation drains a running or paused collector and records whether it should resume; saved Stop remains stopped. The marker blocks MCP auto-start throughout replacement. The cask holds `~/Library/Application Support/Scratchpad/homebrew-upgrade.lock` until Homebrew exits, so `--finish-upgrade` returns a retryable pending result while Homebrew may still roll back. No cask postflight starts the service.

The tray app checks for the marker on launch and during its existing refresh cycle. It retries `--finish-upgrade` after the Homebrew lock is released. Successful upgrades and failed upgrades therefore use the helper from whichever bundle Homebrew leaves installed. The finish command starts the collector only when preparation recorded prior activity, waits for readiness, and removes the marker after success; a failed restart leaves the marker for retry. If the app is not opened automatically after a rollback, launch the restored app or run `--finish-upgrade` from that bundle. The same recovery runs after reinstalling a cask that was uninstalled while the collector was active. Queue data, credentials and lifecycle settings remain in the user's application support directory.

This recovery path is part of the first published cask release. Upgrades from earlier development bundles that lack the tray recovery loop are not supported.

Before distribution, retain the notarization submission ID and accepted result in the controlled release record. Installed acceptance is a separate step: on a disposable macOS account or machine, verify first launch, login-item approval, Start/Pause/Stop/Quit persistence, local key access, synthetic queued receipts, authenticated online verification, MCP client restart, offline capture behavior, Homebrew upgrade, queue compatibility and failed-upgrade recovery. Do not use real capture content in fixtures. Do not claim installed acceptance from portable tests or successful notarization alone.
