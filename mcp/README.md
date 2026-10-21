# Scratchpad MCP

Local stdio MCP for Scratchpad. Captures use the private `scratchpad-service`
over its local socket and are acknowledged only after durable local intake;
reads and supported non-capture operations also use the collector. The collector
owns server configuration and SSH authentication. Requires Go
1.27.3 to build and Git 2.36 or later plus OpenSSH `ssh-keygen` at runtime.
Dependencies are pinned in `go.mod` / `go.sum`.

## Development runbook

Run from `mcp/`:

```sh
go mod download
gofmt -w .
go vet ./...
go test -race ./...
go build -o ../dist/scratchpad-mcp ./cmd/scratchpad-mcp
```

CI must verify formatting without rewriting it (`test -z "$(gofmt -l .)"`), then run vet, race tests, vulnerability checks, and build. The protocol tests use real MCP SDK client/server transports and exercise all eight typed captures. Authentication tests create an ephemeral key and verify the generated SSH signature using OpenSSH. Git tests use temporary repositories and worktrees. Mirror tests race concurrent retries.

## Connect

Client walkthroughs: [Codex](https://alexcatdad.github.io/scratchpad/guides/codex/) and [ChatGPT macOS](https://alexcatdad.github.io/scratchpad/guides/chatgpt/). Both use the local stdio binary. ChatGPT web's hosted plugin path requires a separate integration; the Scratchpad dashboard/REST API is not a remote MCP endpoint.

Configure the server and enrolled SSH key once in Scratchpad's app. Codex needs
only the bundled stdio command:

```toml
[mcp_servers.scratchpad]
command = "/Applications/Scratchpad.app/Contents/Helpers/scratchpad-mcp"
```

The MCP ignores legacy server URL and SSH-key environment settings. It uses the
collector's private socket for reads and captures; no token or key is returned to
the MCP. An unconfigured collector requires setup in the app. Server-backed
reads fail when offline; captures retain the existing durable local receipts.
For isolated tests, `SCRATCHPAD_SERVICE_SOCKET` selects a private collector.
Optional `SCRATCHPAD_MIRROR` and `SCRATCHPAD_MIRROR_PATH` still express
repository consent; neither configures server authentication.

At MCP launch, `scratchpad-mcp` checks the private service socket and starts the
bundled sibling `scratchpad-service` when it is absent. It waits up to 12 seconds
for the collector and encrypted storage to become ready. If startup fails, MCP
still exposes its tools and reports the problem safely on stderr; reads and
captures fail closed until the local service is available. Concurrent MCP
clients converge on the same per-user service, and exiting a client does not
stop it. Startup uses the ordinary service path, so a persistent Stop remains
in effect and is never cleared by MCP. The owner must choose Start from the tray
before captures can succeed. The MCP never falls back to a direct capture
request. Configure the service with the same server URL and credential
references. Every project-scoped tool accepts `workingDirectory` to override the
launch folder for that call; project and Git provenance are snapshotted per
capture. No mutable current-project setting is shared between concurrent calls.
Non-Git and ambiguous contexts require an explicit `projectId` or
owner-confirmed `resolve_project` call. Git `origin` takes precedence; otherwise
one unique fetch identity is required. Equivalent SSH/HTTPS remote forms and
worktrees map to the same source identity. Credentials embedded in Git remotes
are stripped from transmitted remote context.

Capture tools: `record_decision`, `record_adr`, `record_business_decision`, `record_finding`, `record_qa`, `record_failure`, `record_constraint`, `record_project_state`. Each takes common `title`, `authority`, `confidence`, and `requestId` fields alongside natural type-specific fields. Reuse the request ID and input for retries. A new intentional capture needs a new ID, even when text matches. The server generates readable content from payloads.

Record secret references, such as the synthetic `secret://provider/main`, with useful non-secret context. Keep API tokens, passwords, private keys and installation encryption keys in owner-controlled secret configuration. MCP initialization includes this guidance. Free-text capture remains flexible; guidance does not guarantee that arbitrary captures contain no secrets, and provider-token encryption does not encrypt project records or mirrors.

Read tools: `search_memory`, `get_record`, `get_project_context`, `get_decision_history`, `find_related`. Search supports optional type, tag, date/time bounds, branch, lifecycle status, relationship type/related record, descriptive source, Git path hint, authority, confidence, cursor, and limit filters. Decision history includes all three decision types by default, with optional type selection and pagination; related records are based on stored relationships, not an AI inference. `resolve_project` creates/resolves an owner-confirmed name for weak discovery contexts.

The portable [scratchpad-connect skill](../skills/scratchpad-connect/SKILL.md) lets an agent discover existing public keys with its preferred Git/SSH tooling, configure the client and verify an authenticated scoped read. Each machine keeps its own private key; no GitHub OAuth token is needed by the MCP.

## Authentication wire contract

`POST /api/v1/auth/mcp/challenge` with `{publicKey}` returns `{version, recipient, purpose, challengeId, nonce, namespace, expiresAt}`. The collector validates version 2 and the recipient against its locally configured URL origin, then signs the [canonical recipient-bound transcript](../docs/api.md#accepted-clarification-recipient-bound-ssh-proof-2026-10-09), **without a newline**, using OpenSSH armored SSHSIG and namespace `scratchpad-auth-v2`. Upgrade server and signing clients together; nonce-only challenges are rejected without fallback. `POST /api/v1/auth/mcp/verify` submits `{challengeId, publicKey, signature}` and receives `{accessToken, expiresAt}`. Tokens stay in process memory only and are reused until expiry (up to 24 hours). Revocation/invalid authentication clears cached credentials; the next call starts a fresh challenge. There are no refresh tokens or retry loops for failed writes.

GitHub-managed sessions are also gated on every request by the synchronized key set, persistent local blocks and a 24-hour limit from the last complete successful synchronization. Failed refreshes and restarts do not extend that deadline. Detected removals and account unlink/replacement invalidate derived access; manual independent credentials retain local authority. Machines may renew sessions against valid cached keys during an outage. See the [OAuth runbook](../docs/runbooks/github-oauth.md).

## Mirroring and recovery

The MCP snapshots per-capture mirror consent and checkout/Git context into the durable service request. After central capture succeeds, the service delivery adapter performs the local mirror only when the saved consent and server's `mirror.eligible` policy both allow it. Only the full server-returned immutable record is appended. Stable IDs prevent duplicate append on replay, including concurrent MCP processes. The service reports mirror status with `POST /api/v1/records/:id/mirror`; a failed status report is visible in the MCP result without hiding central success.

Mirroring never commits or pushes Git. Paths must stay inside the selected repository; symlink escapes are rejected. A per-file `.lock` directory coordinates writers. An interrupted process can leave a lock behind: stop all MCP writers, confirm no writer remains, then remove that specific empty lock directory and retry the original capture. Locks are deliberately not expired automatically, because a paused live writer must not be mistaken for a dead one. Malformed/partial JSONL is reported for repair rather than silently truncating historical data. Save a copy before repairing any malformed tail.

Capture responses include a durable receipt. Pending responses do not claim a
central record ID; delivered receipts retain record and project IDs. Full
central response objects can be absent after a service restart, in which case
use `get_record` with the receipt's record ID. Central delivery requires the
server's authenticated `clientSavedAt-v1` capability; local capture remains
available while that server is unavailable or incompatible. Explicit tray
verification also performs an authenticated `GET /projects` read with the
verified session. Periodic health probes remain authentication-only. Older
servers leave captures queued until an upgraded compatible server is selected;
configuration changes do not redirect already queued items. A retry after a
mirror failure preserves the centrally captured Git context rather than
rewriting it with the checkout's later dirty state.

## Acceptance and release identity

See [the MCP acceptance runbook](../docs/runbooks/mcp-acceptance.md) for the PRD scenario coverage and the distinction between local boundary tests and the full production-server workflow. Git inspection disables optional Git index locks so central-only capture does not refresh repository metadata. Scope-only reads and AI operations with an explicit `projectId` bypass checkout discovery. Capture invocations inspect the supplied or configured working directory for local provenance and optional mirroring; explicit owner project selection retains known checkout provenance even when remotes are ambiguous. Git inspection continues to prohibit repository-selected helper programs.

Local builds identify as `dev`. Release builds inject the tag with `go build -ldflags '-X main.version=<version>'`; both `--version` and MCP initialization report that value.

## Compact project context

`get_project_context` defaults to legacy context. Supply `version: 2` for compact
context; `limit`, `maxChars` and `previewChars` require that version. Other
combinations fail instead of silently ignoring the supplied limits.

## Suggestion pages

`get_suggestions` accepts optional `limit` (1–200, Go default 100) and an opaque
`cursor` from the previous page in the same scope. Follow `nextCursor` only when
`hasMore` is true; hidden artifacts do not create continuation hints. Omitting
pagination preserves legacy response compatibility. Explicit pagination requires
a supporting backend and returns a clear error when unsupported.

## Cited RAG evidence

`retrieve_evidence` forwards a question or identifier to the Go PostgreSQL RAG
backend over the existing authenticated HTTP connection. It defaults to checkout
project discovery, or accepts an explicit project/working directory. Cross-project
retrieval requires `crossProject: true` and optional `projectIds`; current server
participation consent remains authoritative. SQLite semantic search remains the
separate existing `semantic_search` tool.

Inputs are `query`, optional `intent` (`current` or `history`), `limit` (1–100)
and `maxChars` (4096–64000 serialized response bytes). Omitted limits use server
defaults; explicit invalid values fail. Results retain source identifiers/links,
quoted locations, authority, applicability, relationship previews and indexing
uncertainty. Inspect sources with `get_record`; similarity does not establish
accepted policy, and incomplete indexing is not absent evidence. Backend support
is required; unsupported deployments return a tool error.

`retrieve_evidence` follows caller cancellation and the server's configured
request deadline. Ordinary MCP API calls retain the client's 30-second timeout.
