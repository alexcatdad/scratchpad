# Build, sign and verify the native macOS app

This runbook describes reproducible local packaging and the checks required before a later owner-approved release. Running it does not publish an appcast or change a live MCP client configuration.

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

SwiftPM tests exercise configuration merging, the schema compatibility gate, key-helper argument parsing, and the update approval/quiescence gate with synthetic values. They do not exercise Keychain access, SMAppService approval, a live server, or an installed app.

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
or downloaded updates.

## Assemble an app bundle

Use a macOS host with the pinned SwiftPM dependencies, Go toolchain from `mcp/go.mod`, and the selected signing toolchain. The script builds the menu app, keychain helper, local service and stdio MCP binary, then assembles `Contents/MacOS`, `Contents/Helpers`, the Sparkle framework and the login-agent plist.

```sh
APP_VERSION=1.0.0 \
BUILD_NUMBER=1 \
SIGNING_IDENTITY='Developer ID Application: Example (TEAMID)' \
KEYCHAIN_PROFILE='/secure/path/to/Scratchpad-Keychain.provisionprofile' \
KEYCHAIN_ACCESS_GROUP='TEAMID.com.alexcatdad.scratchpad.keychain-helper' \
SPARKLE_FEED_URL='https://updates.example.test/scratchpad/appcast.xml' \
SPARKLE_PUBLIC_KEY='REPLACE_WITH_SPARKLE_PUBLIC_VERIFICATION_KEY' \
macos/Scripts/package-app.sh /tmp/Scratchpad.app
```

An unsigned local bundle can be assembled by omitting `SIGNING_IDENTITY`; it is not distribution-ready and does not establish Data Protection Keychain access. Signed builds require an owner-provided provisioning profile and access group authorized for the nested `com.alexcatdad.scratchpad.keychain-helper` app; the script validates its team, helper application identifier and access-group authorization before replacing an output app. It derives native `com.apple.application-identifier` from the profile's `ApplicationIdentifierPrefix` (which can differ from its team identifier), embeds the profile in that helper app, and signs its entitlement claim. Omitting either Sparkle value intentionally leaves the updater visibly unconfigured. The bundle declares the current SQLite queue writer/readable schema as version 1; update those values only with a reviewed service schema migration and compatibility plan.

The service uses its default per-user Application Support database and Keychain item. The updater obtains the queue schema with the service's read-only `--schema-version` operation; it never asks for the queue key. Automatic checks use Sparkle 2.10.0's scheduled driver and invoke the custom `SPUUserDriver.showUpdateFound` callback even when `automaticUpdateDownloading` is false: the pinned `SPUUpdater.m` selects `SPUScheduledUpdateDriver` unless automatic downloading is enabled, that driver constructs `SPUUIBasedUpdateDriver`, and the UI-based driver calls the supplied user driver. For a schema-compatible item at `.notDownloaded`, the custom callback replies `.install`, which starts the download; the public `SPUUserDriver` contract documents this choice as beginning or resuming download/extraction/installation. It holds the separate ready-to-install callback until explicit owner approval. See [the pinned background-driver selection](https://github.com/sparkle-project/Sparkle/blob/2.10.0/Sparkle/SPUUpdater.m), [the scheduled driver](https://github.com/sparkle-project/Sparkle/blob/2.10.0/Sparkle/SPUScheduledUpdateDriver.m), [the UI-based callback path](https://github.com/sparkle-project/Sparkle/blob/2.10.0/Sparkle/SPUUIBasedUpdateDriver.m), and [the public user-driver contract](https://github.com/sparkle-project/Sparkle/blob/2.10.0/Sparkle/SPUUserDriver.h). Sparkle's automatic-install mode stays disabled. After approval, service `quiesce` drains serialized work, removes the socket and exits without changing durable Pause/Stop. A relaunch starts the service without `--explicit-start`, preserving durable Pause/Stop. Quitting the tray app without an update approval leaves the independent service alone. This is pinned-source evidence; installed scheduled-update behavior has not been acceptance-tested.

The bundled helper accepts exactly `get|create` and the fixed identifier `com.alexcatdad.scratchpad.capture-queue.v1`; it emits only the raw 32-byte queue key on success. Exit codes are 0 for success, 2 for a locked/authentication-required keychain, 3 for a missing key, and 1 for invalid arguments or another helper error. Diagnostics never include key bytes or Security.framework error text.

The helper targets the Data Protection Keychain and must fail closed if macOS denies access. Apple documents that access groups come from code-signing entitlements and that restricted `keychain-access-groups` entitlements require a provisioning profile; command-line tools that claim them need an app-like bundle to embed that profile. For native macOS, the application ID entitlement is `com.apple.application-identifier`; the iOS `application-identifier` key is not emitted. See [TN3137](https://developer.apple.com/documentation/technotes/tn3137-on-mac-keychains), [TN3125](https://developer.apple.com/documentation/technotes/tn3125-inside-code-signing-provisioning-profiles), and [Keychain sharing](https://developer.apple.com/documentation/security/sharing-access-to-keychain-items-among-a-collection-of-apps). Local synthetic profile-metadata tests, argument tests and compilation do not validate the app/helper signing relationship or a Keychain read/write. Before distribution, establish the owner-controlled profile/entitlement arrangement for both executables and perform Keychain acceptance in a disposable macOS user context; do not switch to a file-based key or create a real queue key as a workaround.

## Validate bundle and appcast declarations

The appcast must declare the custom `scratchpad` namespace and both schema values on every update item:

```xml
<rss version="2.0"
     xmlns:sparkle="http://www.andymatuschak.org/xml-namespaces/sparkle"
     xmlns:scratchpad="https://scratchpad.alexcatdad.com/xml-namespaces/tray-update">
  <channel>
    <item>
      <sparkle:version>1.0.1</sparkle:version>
      <scratchpad:minReadableQueueSchema>1</scratchpad:minReadableQueueSchema>
      <scratchpad:maxReadableQueueSchema>1</scratchpad:maxReadableQueueSchema>
      <!-- Sparkle enclosure, EdDSA archive signature, release notes, etc. -->
    </item>
  </channel>
</rss>
```

Validate every item against the candidate bundle's declared readable range before making a feed available:

```sh
macos/Scripts/validate-release.sh /tmp/Scratchpad.app /path/to/appcast.xml
codesign --verify --deep --strict --verbose=2 /tmp/Scratchpad.app
codesign -dv --verbose=2 /tmp/Scratchpad.app
spctl --assess --type execute --verbose=2 /tmp/Scratchpad.app
```

`test-release-validator.sh` confirms that a synthetic compatible feed passes and missing or mismatched declarations fail. This metadata check does not establish archive-signature validity, candidate bundle authenticity, Gatekeeper acceptance, or old-to-new installed acceptance. Sparkle's updater separately checks the configured public verification key and archive signature before installation. Never accept an update when its schema metadata is missing, malformed or excludes the local queue schema.

## Notarize a later distribution candidate

Use an owner-managed `notarytool` Keychain profile so credentials never enter command arguments or repository files. These commands submit only a locally built artifact; do not run them without a separate release authorization.

```sh
ditto -c -k --sequesterRsrc --keepParent /tmp/Scratchpad.app /tmp/Scratchpad.zip
xcrun notarytool submit /tmp/Scratchpad.zip --keychain-profile "$NOTARY_PROFILE" --wait
xcrun stapler staple /tmp/Scratchpad.app
xcrun stapler validate /tmp/Scratchpad.app
spctl --assess --type execute --verbose=2 /tmp/Scratchpad.app
```

Retain the notarization submission ID and accepted result in the controlled release record. Installed acceptance is a separate step: on a disposable macOS account or machine, verify first launch, login-item approval, Start/Pause/Stop/Quit persistence, local key access, synthetic queued receipts, authenticated online verification, MCP client restart, offline capture behavior, downloaded-update approval, queue schema rejection and failed-update recovery. Do not use real capture content in fixtures. Do not claim installed acceptance from portable tests or successful notarization alone.
