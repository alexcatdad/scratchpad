# Desktop SSH onboarding

The desktop uses the server's existing SSH challenge and session protocol. Link
the owner's GitHub account in the server dashboard first and synchronize its
published authentication and signing keys. Independently enrolled SSH keys remain
supported. Each desktop needs a matching private key through an SSH agent.

## Connect

1. Open **Connection** and enter the server's HTTPS origin. Private Tailscale
   origins work when the desktop can reach them. Loopback development may use HTTP.
2. Choose **Find Keys**. Discovery reads public keys from the inherited SSH
   agent, the standard macOS 1Password agent socket and `.ssh/*.pub`. Advanced
   settings allow an explicit agent socket. No private key files are inspected.
3. Choose an accepted key if several match. The saved identity is preferred when
   available; a single match is selected automatically. A public file marked
   **load in SSH agent** needs its matching private key loaded into that agent.
4. Choose **Connect**. Approve or unlock the agent when prompted.
   Explicit verification permits up to 90 seconds for the signer. Scratchpad saves
   a public-key reference and agent socket only after possession proof and an
   authenticated project-list read succeed. Failed proof preserves existing
   settings; concurrent configuration changes reject an older verification.
5. Restart the capture service through its usual controls and verify saved access.
   MCP clients continue using the command-only collector connection. Existing
   queued captures retain their original server and credential binding.

Discovery sends at most 16 distinct local public keys to the selected server's
existing challenge endpoint. The server applies normal GitHub freshness, local
blocks and revocation checks. These short-lived discovery challenges are not
sessions. Connect performs a separate fresh recipient-bound signature. No new
public key-list endpoint, desktop OAuth callback or loopback HTTP listener exists.

The UI reports network or rate-limit failures separately from an empty eligible
set. For no matches, unlock the agent, confirm the server's GitHub link and key
refresh, or use advanced settings for an independently enrolled key. GitHub login
does not copy a private key onto another machine. File-only encrypted keys require
loading into an agent; the guided flow does not collect passphrases.

## Validation

Run the native test suite with the documented toolchain in
[the native release runbook](macos-native-release.md). Set
`SCRATCHPAD_TEST_SERVICE` to a freshly built disposable service executable to
exercise the actual Swift-to-Go configuration and failure-preservation boundary.

```sh
go -C mcp test ./internal/scratchpad ./internal/localcapture ./internal/delivery
go -C mcp vet ./internal/scratchpad ./internal/localcapture ./internal/delivery
sh macos/Scripts/build-and-test.sh
```

Tests use disposable keys, agents, sockets, databases and synthetic servers.
Installed-app acceptance separately checks discovery from a Finder/login launch,
agent approval/unlock, connection persistence after restart, failed-proof retention
and an authenticated scoped MCP read. Never test with another user's keys or
point fixture writers at the live collector. Source tests and a development-bundle
build do not establish Developer ID distribution or live GitHub acceptance.

## Connect from a terminal

The bundled `scratchpad-mcp` binary configures the same capture service without
the tray app, for terminals and agents:

```sh
/Applications/Scratchpad.app/Contents/Helpers/scratchpad-mcp connect --server https://memory.example.com
/Applications/Scratchpad.app/Contents/Helpers/scratchpad-mcp status
```

`connect` uses the same key sources, server eligibility check and possession
proof as the app, and saves the configuration only after the proof succeeds.
Pass `--public-key <file.pub>` to choose among several accepted keys and
`--agent-socket <path>` for a non-default agent. `status` prints the service
status and saved configuration as JSON.
