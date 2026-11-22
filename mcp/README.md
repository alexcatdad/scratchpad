# Scratchpad CLI and capture service

Go module for the local side of Scratchpad: the `scratchpad` command coding
agents call, the private `scratchpad-service` capture service, and the shared
tool registry both use. Captures go to the service over its local socket and are
acknowledged only after durable local intake; reads and supported non-capture
operations also use the service. The service owns server configuration and SSH
authentication. Requires Go 1.27.3 to build and Git 2.36 or later plus OpenSSH
`ssh-keygen` at runtime. Dependencies are pinned in `go.mod` / `go.sum`.

| Command                  | Purpose                                                                       |
| ------------------------ | ----------------------------------------------------------------------------- |
| `cmd/scratchpad`         | The CLI. Shipped in the app as `Contents/Helpers/scratchpad`.                 |
| `cmd/scratchpad-service` | The capture service. Shipped as `Contents/Helpers/scratchpad-service`.        |
| `cmd/scratchpad-mcp`     | A stdio MCP server used as a test harness. Not shipped and not a client path. |

## `scratchpad` CLI

`cmd/scratchpad` hosts the tool registry in-process and calls it over an
in-memory MCP transport, so every tool, its validation and its output are the
same everywhere the registry runs. Each call is a short process that runs the
installed version, so app upgrades never leave stale servers behind.

```sh
scratchpad tools                          # list tools
scratchpad tools record-decision          # one tool's input schema
scratchpad get-project-context -F version=2
scratchpad search-memory -f query="release signing"
scratchpad record-finding --input finding.json   # or --input - for stdin
scratchpad status                         # service status and saved connection
scratchpad connect --server https://memory.example.com
scratchpad hooks print --agent codex      # or claude-code
```

`-f key=value` sets a string field and `-F key=value` a JSON field. The project
resolves from the current directory. `status` prints
`{"service": …, "configuration": …}`; `configuration` appears only once one is
saved.

The CLI never starts the capture service: from a sandboxed agent shell a started
service would inherit the sandbox. It reports a stopped service instead, and the
app or its login item starts it. `connect` also requires a running service.

`connect --server <https-origin> [--public-key <file.pub>] [--agent-socket <path>]`
configures the service without the app. It reads public keys from the SSH agent,
the 1Password agent and `~/.ssh/*.pub` (at most 16), asks the server's challenge
endpoint which it accepts, proves possession through the agent and saves the
configuration only after the proof succeeds. The app's **Connection** page does
the same.

`hooks print --agent claude-code|codex` prints the hook configuration for that
agent and changes no file. The hooks call `scratchpad hook --agent <name>`,
which reads one event from stdin, keeps only mistake signals, never prints to
stdout and always exits 0. Hooks exist only for Claude Code and Codex.

The Homebrew cask does not link the CLI onto `PATH`: call it by its full path or
add a symlink, for example
`ln -sf /Applications/Scratchpad.app/Contents/Helpers/scratchpad ~/.local/bin/scratchpad`.

## Development runbook

Run from `mcp/`:

```sh
go mod download
gofmt -w .
go vet ./...
go test -race ./...
go build -o ../dist/scratchpad ./cmd/scratchpad
go build -o ../dist/scratchpad-service ./cmd/scratchpad-service
```

CI must verify formatting without rewriting it (`test -z "$(gofmt -l .)"`), then run vet, race tests, vulnerability checks, and build. The protocol tests use real MCP SDK client/server transports and exercise all eight typed captures. Authentication tests create an ephemeral key and verify the generated SSH signature using OpenSSH. Git tests use temporary repositories and worktrees. Mirror tests race concurrent retries.

## Tools and project scope

Client setup is described in the public [agent integration guide](https://alexcatdad.github.io/scratchpad/guides/agents/), with walkthroughs for [Codex](https://alexcatdad.github.io/scratchpad/guides/codex/) and [ChatGPT macOS](https://alexcatdad.github.io/scratchpad/guides/chatgpt/). The Scratchpad dashboard/REST API is not a remote MCP endpoint.

Configure the server and SSH key once, in the app or with `scratchpad connect`.
The tools ignore legacy server URL and SSH-key environment settings. They use the
service's private socket for reads and captures; no token or key is returned to
them. An unconfigured service requires setup first. Server-backed reads fail when
offline; captures retain the existing durable local receipts. For isolated tests,
`SCRATCHPAD_SERVICE_SOCKET` selects a private service. Optional
`SCRATCHPAD_MIRROR` and `SCRATCHPAD_MIRROR_PATH` still express repository
consent; neither configures server authentication.

Every project-scoped tool accepts `workingDirectory` to override the current
folder for that call; project and Git provenance are snapshotted per capture. No
mutable current-project setting is shared between concurrent calls. Non-Git and
ambiguous contexts require an explicit `projectId` or owner-confirmed
`resolve_project` call. Git `origin` takes precedence; otherwise one unique fetch
identity is required. Equivalent SSH/HTTPS remote forms and worktrees map to the
same source identity. Credentials embedded in Git remotes are stripped from
transmitted remote context.

Capture tools: `record_decision`, `record_adr`, `record_business_decision`, `record_finding`, `record_qa`, `record_failure`, `record_constraint`, `record_project_state`. Each takes common `title`, `authority`, `confidence`, and `requestId` fields alongside natural type-specific fields. Reuse the request ID and input for retries. A new intentional capture needs a new ID, even when text matches. The server generates readable content from payloads.

Record secret references, such as the synthetic `secret://provider/main`, with useful non-secret context. Keep API tokens, passwords, private keys and installation encryption keys in owner-controlled secret configuration. The registry's server instructions include this guidance. Free-text capture remains flexible; guidance does not guarantee that arbitrary captures contain no secrets, and provider-token encryption does not encrypt project records or mirrors.

Read tools: `search_memory`, `get_record`, `get_project_context`, `get_decision_history`, `find_related`. Search supports optional type, tag, date/time bounds, branch, lifecycle status, relationship type/related record, descriptive source, Git path hint, authority, confidence, cursor, and limit filters. Decision history includes all three decision types by default, with optional type selection and pagination; related records are based on stored relationships, not an AI inference. `resolve_project` creates/resolves an owner-confirmed name for weak discovery contexts. `get_capture_receipt` reports the local delivery state of a capture.

The portable [scratchpad-connect skill](../skills/scratchpad-connect/SKILL.md) lets an agent discover existing public keys with its preferred Git/SSH tooling, connect the capture service and verify an authenticated scoped read. Each machine keeps its own private key; no GitHub OAuth token is needed by the tools.

## Authentication wire contract

`POST /api/v1/auth/mcp/challenge` with `{publicKey}` returns `{version, recipient, purpose, challengeId, nonce, namespace, expiresAt}`. The service validates version 2 and the recipient against its locally configured URL origin, then signs the [canonical recipient-bound transcript](../docs/api.md#accepted-clarification-recipient-bound-ssh-proof-2026-10-09), **without a newline**, using OpenSSH armored SSHSIG and namespace `scratchpad-auth-v2`. Upgrade server and signing clients together; nonce-only challenges are rejected without fallback. `POST /api/v1/auth/mcp/verify` submits `{challengeId, publicKey, signature}` and receives `{accessToken, expiresAt}`. Tokens stay in process memory only and are reused until expiry (up to 24 hours). Revocation/invalid authentication clears cached credentials; the next call starts a fresh challenge. There are no refresh tokens or retry loops for failed writes.

GitHub-managed sessions are also gated on every request by the synchronized key set, persistent local blocks and a 24-hour limit from the last complete successful synchronization. Failed refreshes and restarts do not extend that deadline. Detected removals and account unlink/replacement invalidate derived access; manual independent credentials retain local authority. Machines may renew sessions against valid cached keys during an outage. See the [OAuth runbook](../docs/runbooks/github-oauth.md).

## Mirroring and recovery

Each capture snapshots its mirror consent and checkout/Git context into the durable service request. After central capture succeeds, the service delivery adapter performs the local mirror only when the saved consent and server's `mirror.eligible` policy both allow it. Only the full server-returned immutable record is appended. Stable IDs prevent duplicate append on replay, including concurrent processes. The service reports mirror status with `POST /api/v1/records/:id/mirror`; a failed status report is visible in the tool result without hiding central success.

Mirroring never commits or pushes Git. Paths must stay inside the selected repository; symlink escapes are rejected. A per-file `.lock` directory coordinates writers. An interrupted process can leave a lock behind: stop all Scratchpad writers, confirm no writer remains, then remove that specific empty lock directory and retry the original capture. Locks are deliberately not expired automatically, because a paused live writer must not be mistaken for a dead one. Malformed/partial JSONL is reported for repair rather than silently truncating historical data. Save a copy before repairing any malformed tail.

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

See [the MCP acceptance runbook](../docs/runbooks/mcp-acceptance.md) for the PRD scenario coverage and the distinction between local boundary tests and the full production-server workflow. Git inspection disables optional Git index locks so central-only capture does not refresh repository metadata. Scope-only reads and AI operations with an explicit `projectId` bypass checkout discovery. Capture invocations inspect the supplied or current working directory for local provenance and optional mirroring; explicit owner project selection retains known checkout provenance even when remotes are ambiguous. Git inspection continues to prohibit repository-selected helper programs.

Local builds identify as `dev`. Release builds inject the tag with `go build -ldflags '-X main.version=<version>'`; `--version` reports that value.

## `scratchpad-mcp` test harness

`cmd/scratchpad-mcp` serves the same tool registry over stdio MCP. The browser
end-to-end tests and backend startup tests drive it; the app does not ship it and
it is not a supported client path. At launch it checks the service socket and
starts a sibling `scratchpad-service` when the socket is absent, waiting up to 12
seconds; a service the owner stopped stays stopped. If startup fails it still
lists its tools, reports the problem on stderr and fails reads and captures
closed.

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
request deadline. Ordinary API calls retain the client's 30-second timeout.
