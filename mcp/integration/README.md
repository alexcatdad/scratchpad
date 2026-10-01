# Cross-process integration runbook

The Go command launches the real `scratchpad-mcp` executable and drives its stdio protocol against an independently running HTTP server. It tests eight typed captures, automatic project discovery, SSH challenge authentication, deterministic content, retrieval, project context, search, decision history, relationships, persistent retry identities, and changed-payload conflicts. The context check verifies the disposable project's current paused state, its reason, previous state and follow-up work, plus source-linked decisions, findings, failures and constraints before and after restart or restore. Fixture data is not USB Boop historical evidence.

The capture and verification phases deliberately use separate MCP processes. Restart the server between phases while retaining its database to prove both server persistence and fresh client authentication. The automated workflow also invokes a project-boundary phase; see [MCP acceptance](../../docs/runbooks/mcp-acceptance.md) for its owner API setup and scope. All dependencies and native SQLite bindings must already be installed.

## Production-build smoke

Start the built server with a disposable database. Complete owner/passkey bootstrap and public-key enrollment through the actual browser authentication flow, then create a disposable Git repository with a unique remote. Build the executable from `mcp/`:

```sh
go build -o /tmp/scratchpad-mcp ./cmd/scratchpad-mcp
go run ./integration -url http://localhost:3000 -public-key /tmp/scratchpad-key.pub -signing-key /tmp/scratchpad-key -workspace /tmp/scratchpad-test-repo -binary /tmp/scratchpad-mcp -state /tmp/scratchpad-smoke.json -phase capture
```

Restart the built server against the same SQLite file, then run:

```sh
go run ./integration -url http://localhost:3000 -public-key /tmp/scratchpad-key.pub -signing-key /tmp/scratchpad-key -workspace /tmp/scratchpad-test-repo -binary /tmp/scratchpad-mcp -state /tmp/scratchpad-smoke.json -phase verify
```

Enable project mirroring through the dashboard first and add `-mirror` if mirror success should also be required. The default captures do not change project settings.

## Automated browser workflow

The repository-level `tests/workflow.spec.ts` provisions the production server with a disposable SQLite database, registers a real virtual-authenticator passkey through the browser, enrolls the MCP SSH key through the dashboard, invokes capture, restarts the server, and invokes verify. No production authentication bypass or seeded bearer token is used.
