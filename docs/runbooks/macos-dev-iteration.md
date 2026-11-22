# macOS menu app development loop

`macos/Scripts/dev-app.sh` builds the SwiftUI menu app into
`macos/.build/dev/Scratchpad Dev.app`. It incrementally builds only the
`Scratchpad` SwiftPM product. The first invocation needs a complete signed
Scratchpad app as a seed so the dev bundle can reuse its capture service,
`scratchpad` command and Keychain helper:

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
The rebuilt menu app and outer dev bundle receive local ad-hoc signatures; the
signed Keychain helper copied from the seed is preserved. The retired updater
framework and a seed's retired `scratchpad-mcp` helper are removed.
This is a local UI iteration bundle, not a distributable or notarized release.
Capture service and `scratchpad` command changes can use the opt-in helper
rebuild path, which builds the current Go executables
(`mcp/cmd/scratchpad-service` and `mcp/cmd/scratchpad`) into the staged
candidate and ad-hoc signs only those helpers:

```sh
macos/Scripts/dev-app.sh --rebuild-helpers
```

Combine it with `--run` to explicitly restart the dev app only after both Go
helpers are built and the replacement bundle is verified. This restarts only
the menu bar app; restarting a running capture service is a separate service
action. Agents that call the dev bundle's `scratchpad` command by path use the
rebuilt command on their next call.

The UI-only path remains the default and keeps the seed's helpers. Keychain
helper changes still require a full package and a fresh `--base-app` seed.

The dev app uses Scratchpad's normal local service socket and capture storage.
Opening it refreshes service status but does not start the service. Its manual
Start, Pause, Stop, queue, and connection controls still affect the real local
service and data. Keep **Start capture service at login** disabled in the dev
app: its embedded LaunchAgent uses the production service label. The
development script itself never registers that login item or invokes those
controls.

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
The workspace pages are Overview, Captures, Connection and General. In the
development workspace, verify that Overview starts selected, then click each
other page and confirm both the selection highlight and detail content change.
Return to General without changing configuration.

## Settings window regression

Run the native tests with
`DEVELOPER_DIR=/Applications/Xcode.app/Contents/Developer swift test --package-path macos`.
Set `SCRATCHPAD_TEST_SERVICE` to a built `scratchpad-service` executable to
include the isolated process test, which uses synthetic storage, keys and
connection settings.

Verify the rendered UI:

1. Choose **Settings…** in the menu bar panel while another app is active.
   The workspace must become the foreground window with General selected.
2. Repeat the action. The same workspace window must remain open, with General
   selected. Repeat after selecting another page and after minimizing the
   workspace.
3. Choose **Scratchpad → Settings…** or press Command-comma. These must select
   General in the same window. The File menu must not offer a new workspace.
4. In **Connection**, edit the server **Address** with a synthetic value, then
   restore the original text without saving the synthetic destination.
   **Advanced → Save** must use the existing capture service configuration
   operation and retain the other connection fields. Check saved configuration only against an approved
   destination or a disposable capture service.
5. Close the workspace and reopen it with **Open Scratchpad**. Closing it must
   leave the menu bar app and capture service running.

The SwiftPM policy suite does not exercise native scene creation, foreground
activation or editable controls. Record rendered UI checks separately from tests.
The native window tests verify that presentation raises only the identified
workspace, even when a menu bar window appears first in the app's window list.

If foreground or menu bar visibility regresses, inspect the safe lifecycle
events:

```sh
log show --last 5m --info --style compact \
  --predicate 'subsystem == "com.alexcatdad.scratchpad" AND category == "Windowing"'
```

These report workspace presence, visibility, key status and app activation, plus
tray-label attachment. They omit server URLs, credentials and capture contents.
Distinguish a focus change from process termination using current process state
and a fresh crash report; an older crash report does not explain a new incident.

## Command and capture service check

The `scratchpad` command never starts the capture service and never talks to the
server directly; with the service stopped it says so and exits. Verify source
changes without replacing the running app:

```sh
go -C mcp vet ./...
go -C mcp test -race ./...
```

These checks use synthetic fixtures and disposable sockets and stores. Rebuilding
the dev bundle with `--rebuild-helpers --run`, restarting the capture service
and repeating a live offline check are separate steps that need the owner's
approval for the affected local processes and settings.

For the live check, run `scratchpad status`, then a real
`get-project-context` read with the dev bundle's command. During an approved,
bounded server outage, a capture must return a durable pending receipt,
server-backed reads must fail, and reconnecting must deliver the same receipt.
Keep the capture service's queue and settings intact.
