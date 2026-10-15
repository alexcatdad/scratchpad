# Cross-process integration runbook

The CLI launches the actual `scratchpad-mcp` executable over stdio. Captures require the durable local service; they fail closed if it is missing or stopped and never fall through to direct HTTP writes. `service_stdio_test.go` is supplementary synthetic-HTTP protocol coverage. The primary real-backend boundary is `backend/startup_test.go::TestRealMCPDurableServiceToScratchpadSQLiteRestartAndReceipts`, which starts the actual Go server, MCP executable and service executable against disposable SQLite databases and synthetic SSH/queue keys. It verifies all eight types, independent per-call Git scopes, a fresh-process retry after the caller discards its acknowledgement, backend outage, service restart, durable receipt delivery/replay and no direct fallback. Fixture data is not USB Boop historical evidence.

The existing capture and verification phases deliberately use separate MCP processes. Restart the server between phases while retaining its database to prove backend persistence and fresh client authentication. The automated browser workflow also invokes a project-boundary phase; see [MCP acceptance](../../docs/runbooks/mcp-acceptance.md) for its owner API setup and scope. All dependencies and native SQLite bindings must already be installed.

## Real service-to-backend acceptance

From `backend/`, run the self-contained disposable end-to-end test:

```sh
go test ./ -run '^TestRealMCPDurableServiceToScratchpadSQLiteRestartAndReceipts$' -count=1 -v
```

It builds the actual server, local service, MCP executable and integration client; creates synthetic SSH and queue keys plus isolated repositories; and verifies actual central SQLite records before cleanup. It does not use the host's service database, keychain, credentials or real repository.

## Production-build smoke

Start the built server with a disposable database. Complete owner/passkey bootstrap and public-key enrollment through the actual browser authentication flow, then create a disposable Git repository with a unique remote. Build both executables from `mcp/`, and start/configure the durable service for the same server and enrolled key on `/tmp/scratchpad-service.sock` before capturing. The bundled service uses its keychain helper; do not substitute a raw key or point this smoke at an existing queue database.

```sh
go build -o /tmp/scratchpad-mcp ./cmd/scratchpad-mcp
go build -o /tmp/scratchpad-service ./cmd/scratchpad-service
go run ./integration -url http://localhost:3000 -public-key /tmp/scratchpad-key.pub -signing-key /tmp/scratchpad-key -workspace /tmp/scratchpad-test-repo -binary /tmp/scratchpad-mcp -service-socket /tmp/scratchpad-service.sock -fingerprint "$(ssh-keygen -lf /tmp/scratchpad-key.pub | awk '{print $2}')" -phase configure-service
go run ./integration -url http://localhost:3000 -public-key /tmp/scratchpad-key.pub -signing-key /tmp/scratchpad-key -workspace /tmp/scratchpad-test-repo -binary /tmp/scratchpad-mcp -service-socket /tmp/scratchpad-service.sock -state /tmp/scratchpad-smoke.json -phase capture
```

Restart the built server against the same SQLite file, then run:

```sh
go run ./integration -url http://localhost:3000 -public-key /tmp/scratchpad-key.pub -signing-key /tmp/scratchpad-key -workspace /tmp/scratchpad-test-repo -binary /tmp/scratchpad-mcp -service-socket /tmp/scratchpad-service.sock -state /tmp/scratchpad-smoke.json -phase verify
```

Enable project mirroring through the dashboard first and add `-mirror` if mirror success should also be required. The default captures do not change project settings.

## Automated browser workflow

The repository-level `tests/workflow.spec.ts` provisions the production server with a disposable SQLite database, registers a real virtual-authenticator passkey through the browser, enrolls the MCP SSH key through the dashboard, invokes capture, restarts the server, and invokes verify. No production authentication bypass or seeded bearer token is used.

Tray captures return a durable local receipt. `saved_pending` means only that
the local service accepted the capture; no central IDs are reported until
delivery. A delivered receipt retains its central record and project IDs across
service restart. The full server `record` or `project` object can be omitted
after restart because only the original HTTP response supplied that transient
data; call `get_record` for the central record. All eight capture tools require
the local service and do not fall back to direct HTTP capture.

`backend/startup_test.go::TestRealMCPDurableServiceToScratchpadSQLiteRestartAndReceipts`
is the full local boundary test. It starts the actual server, service and MCP
executables against one temporary SQLite database. A test-owned proxy drops one
successful central capture response after the backend commits it, then the
same request is retried across MCP and service restarts to confirm one central
record and the original `clientSavedAt`. The test also covers all eight capture
types, per-call project scope, server outage receipts, and fail-closed behavior
when local intake is stopped. `service_stdio_test.go` remains synthetic
transport-only coverage.
