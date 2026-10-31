# macOS menu app development loop

`macos/Scripts/dev-app.sh` builds the SwiftUI menu app into
`macos/.build/dev/Scratchpad Dev.app`. It incrementally builds only the
`Scratchpad` SwiftPM product. The first invocation needs a complete signed
Scratchpad app as a seed so the dev bundle can reuse its service, MCP, Keychain
helper:

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
`UserDefaults` are separate from the release app.
Its menu-bar icon has a visible `DEV` label, and its menu and default workspace
title use `Scratchpad DEV`. Use these labels to distinguish it from the
installed app when both are running.
The rebuilt menu app and outer dev bundle receive local ad-hoc signatures; the signed Keychain helper copied from the seed is preserved. The retired updater framework is removed when reusing an older seed.
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

The UI-only path remains the default. Keychain helper changes still
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

## Sidebar navigation regression

Keep the sidebar row identity type equal to its selection value type
(`WorkspacePage`). SwiftPM policy tests do not exercise rendered selection.
In the development workspace, verify that Overview starts selected,
then click Captures and General and confirm both the selection highlight and
detail content change. Return to General without changing configuration.

## Settings window regression

When integrating these fixes with desktop SSH onboarding, fetch current main and
merge it into the feature branch before requesting CI. Preserve all decision-log
entries, use `ServiceClient.saveConfiguration` for the empty acknowledgement,
and retain both IPC smoke cases: changed URL persistence and failed proposed
connection preserving the saved configuration. Run the native package with an
isolated service fixture, then require the PR's GitHub Actions checks to pass.
After merging, fetch main and fast-forward the local main checkout. If another
clean worktree holds main, preserve its current commit with a detached checkout
before moving this workspace to main; retain unrelated untracked files.

Build and sign the app with the existing `package-app.sh` workflow, then run the
native tests with `DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer swift test --package-path macos`.
Set `SCRATCHPAD_TEST_SERVICE` to the built collector executable to include the
isolated process test. It uses synthetic storage, keys and connection settings,
and checks that saving a changed URL preserves the other connection fields.
Use the normal upgrade handoff when replacing an installed bundle and retain the
previous bundle for rollback.

Verify the installed UI:

1. Open **Settings & connections…** from the tray while another app is active.
   Settings must become the foreground window.
2. Repeat the action. The same workspace window must remain open, with Settings
   selected. Repeat after selecting Projects and after minimizing the workspace.
3. Open **Scratchpad → Settings…** or press Command-comma. These must select
   Settings in the same window. The File menu must not offer a new workspace.
4. Edit **Server URL** with a synthetic value, then restore the original text
   without saving the synthetic destination. **Save connection** must use the
   existing collector configuration operation and retain the other connection
   fields. Check saved configuration only against an approved destination or a
   disposable collector.
5. Close the workspace and reopen it from the tray. Closing it must leave the
   tray and collector running.

The SwiftPM policy suite does not exercise native scene creation, foreground
activation or editable controls. Record rendered UI checks separately from tests.
The native window tests verify that presentation raises only the identified
workspace, even when a tray window appears first in the app's window list.

If foreground or tray visibility regresses, inspect the safe lifecycle events:

```sh
log show --last 5m --info --style compact \
  --predicate 'subsystem == "com.alexcatdad.scratchpad" AND category == "Windowing"'
```

These report workspace presence, visibility, key status and app activation, plus
tray-label attachment. They omit server URLs, credentials and capture contents.
Distinguish a focus change from process termination using current process state
and a fresh crash report; an older crash report does not explain a new incident.

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
