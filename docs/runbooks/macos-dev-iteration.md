# macOS menu app development loop

`macos/Scripts/dev-app.sh` builds the SwiftUI menu app into
`macos/.build/dev/Scratchpad Dev.app`. It incrementally builds only the
`Scratchpad` SwiftPM product. The first invocation needs a complete signed
Scratchpad app as a seed so the dev bundle can reuse its service, MCP, Keychain
helper, and Sparkle framework:

```sh
macos/Scripts/dev-app.sh --base-app /path/to/Scratchpad.app
```

Later build-only iterations use:

```sh
macos/Scripts/dev-app.sh
```

Build-only never checks processes, opens the app, starts the service, registers
a login item, or changes client configuration. It replaces only the dev bundle
on disk. If the dev app is open, its running process continues using the
already-loaded version; use the explicit restart command to test a replacement:

```sh
macos/Scripts/dev-app.sh --run
```

`--run` prepares and verifies the replacement first, sends TERM only to a
process whose executable path exactly matches the dev bundle, replaces that
bundle, and opens it with Launch Services. If the dev process does not exit,
the replacement is abandoned and the user can close that dev app manually. If
Launch Services refuses the new app, the previous workspace bundle is restored.
The installed/release app and capture service are never targeted by the
script.

The dev bundle uses `com.alexcatdad.scratchpad.dev`, so its standard
`UserDefaults` and updater recovery flags are separate from the release app.
Its Sparkle feed and public update key are removed. The rebuilt menu app and
outer dev bundle receive local ad-hoc signatures; the signed service, MCP,
Keychain helper, and Sparkle framework copied from the seed are preserved.
This is a local UI iteration bundle, not a distributable or notarized release.
Service and MCP changes can use the opt-in helper rebuild path, which builds
the current Go executables into the staged candidate and ad-hoc signs only
those helpers:

```sh
macos/Scripts/dev-app.sh --rebuild-helpers
```

Combine it with `--run` to explicitly restart the dev app only after both Go
helpers are built and the replacement bundle is verified. This restarts only
the tray app; restarting a running collector is a separate service action.

The UI-only path remains the default. Keychain helper or Sparkle changes still
require a full package and a fresh `--base-app` seed.

The dev app uses Scratchpad's normal local service socket and capture storage.
Opening it refreshes service status but does not start the service. Its manual
Start, Pause, Stop, queue, and connection controls still affect the real local
service and data. Keep **Start at Login** disabled in the dev app: its embedded
LaunchAgent uses the production service label. The development script itself
never registers that login item or invokes those controls.

The script uses Command Line Tools through a per-command `DEVELOPER_DIR` and
loads SwiftUI's macro plugin from the standard Xcode installation path. Set
`SCRATCHPAD_XCODE_DEVELOPER_DIR` only when Xcode is installed elsewhere; this
does not change the machine's selected developer directory.

Run the focused shell workflow test with:

```sh
macos/Scripts/test-dev-app.sh
```

## Signing-key fallback regression

An omitted `SCRATCHPAD_SIGNING_KEY` uses `SCRATCHPAD_PUBLIC_KEY` as the
SSH-agent signing reference. The MCP resolves that default before authentication
and local capture configuration validation. Explicit references must still match
the collector; a different reference must fail before intake.

Verify source changes without replacing the running app:

```sh
go -C mcp test -race ./internal/scratchpad -run '^TestCaptureSigningKeyFallback$' -count=1
go -C mcp vet ./...
go -C mcp test -race ./...
go -C mcp build -o /private/tmp/scratchpad-mcp-validation ./cmd/scratchpad-mcp
```

These checks use synthetic fixtures. Rebuilding the dev bundle, reloading Codex,
and repeating live offline acceptance are separate steps requiring the owner's
approval for the affected local processes and settings.

## Command-only Codex acceptance

Configure server URL and key references once through the app. The Codex entry
contains the bundled MCP command; remove the legacy Scratchpad URL/public-key/
signing-key environment entries after reviewing the configuration diff. Rebuild
with `--rebuild-helpers --run` only with approval for the dev tray restart. The
collector must also restart to load its new online-request operation. Keep its
queue and settings intact, and verify fresh Keychain access before restarting.

Verify an actual Codex context/record read, then the approved bounded collector
proxy outage: capture must return a durable pending receipt, server-backed reads
must fail, and reconnect must deliver the same receipt. Verify retry identity and
restore normal collector launch settings. Check the loaded MCP executable, not
just its path on disk. Source tests use disposable sockets/stores and a reachable
HTTP trap to establish that missing collector IPC never triggers direct HTTP.
