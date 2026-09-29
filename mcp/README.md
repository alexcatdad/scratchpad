# Scratchpad MCP

Local stdio MCP for the central Scratchpad API. Requires Go 1.27.1 to build and Git plus OpenSSH `ssh-keygen` at runtime. Dependencies are pinned in `go.mod` / `go.sum`. The official Go MCP SDK v1.8.0 was verified against the official Go module proxy on 2026-09-29.

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

Enroll an SSH public key from the authenticated dashboard first. Private key material is never sent to the server. Configure your MCP client with the binary as its stdio command and these environment variables:

| Variable                 | Meaning                                                                                         |
| ------------------------ | ----------------------------------------------------------------------------------------------- |
| `SCRATCHPAD_URL`         | Required server origin, e.g. `https://memory.example.com`; HTTP allowed only on loopback        |
| `SCRATCHPAD_PUBLIC_KEY`  | Required absolute path to the enrolled OpenSSH public key                                       |
| `SCRATCHPAD_SIGNING_KEY` | Optional absolute private-key path; defaults to public-key path for signing through `ssh-agent` |
| `SCRATCHPAD_MIRROR`      | `true` enables optional repository mirroring locally; default off                               |
| `SCRATCHPAD_MIRROR_PATH` | Repository-relative mirror file; default `scratchpad/decisions.jsonl`                           |

Launch the binary from the project folder. Every project-scoped tool accepts `workingDirectory` to override that folder for one call. No mutable current-project setting is shared between concurrent calls. Non-Git and ambiguous contexts require an explicit `projectId` or owner-confirmed `resolve_project` call. Git `origin` takes precedence; otherwise one unique fetch identity is required. Equivalent SSH/HTTPS remote forms and worktrees map to the same source identity. Credentials embedded in Git remotes are stripped from transmitted remote context.

Capture tools: `record_decision`, `record_adr`, `record_business_decision`, `record_finding`, `record_qa`, `record_failure`, `record_constraint`, `record_project_state`. Each takes common `title`, `authority`, `confidence`, and `requestId` fields alongside natural type-specific fields. Reuse the request ID and input for retries. A new intentional capture needs a new ID, even when text matches. The server generates readable content from payloads.

Read tools: `search_memory`, `get_record`, `get_project_context`, `get_decision_history`, `find_related`. Search supports optional type, tag, date/time bounds, branch, lifecycle status, relationship type/related record, descriptive source, Git path hint, authority, confidence, cursor, and limit filters. Decision history includes all three decision types by default, with optional type selection and pagination; related records are based on stored relationships, not an AI inference. `resolve_project` creates/resolves an owner-confirmed name for weak discovery contexts.

## Authentication wire contract

`POST /api/v1/auth/mcp/challenge` with `{publicKey}` returns `{challengeId, nonce, namespace, expiresAt}`. MCP signs the exact nonce string, **without a newline**, using OpenSSH armored SSHSIG and namespace `scratchpad-auth`. `POST /api/v1/auth/mcp/verify` submits `{challengeId, publicKey, signature}` and receives `{accessToken, expiresAt}`. Tokens stay in process memory only and are reused until expiry (up to 24 hours). Revocation/invalid authentication clears cached credentials; the next call starts a fresh challenge. There are no refresh tokens or retry loops for failed writes.

## Mirroring and recovery

The central capture completes before local mirroring. Both local enablement and the server's `mirror.eligible` policy must allow it. Only the full server-returned immutable record is appended. Stable IDs prevent duplicate append on replay, including concurrent MCP processes. Mirror status is reported separately with `POST /api/v1/records/:id/mirror`; a failed status report is visible in the MCP result without hiding central success.

Mirroring never commits or pushes Git. Paths must stay inside the selected repository; symlink escapes are rejected. A per-file `.lock` directory coordinates writers. An interrupted process can leave a lock behind: stop all MCP writers, confirm no writer remains, then remove that specific empty lock directory and retry the original capture. Locks are deliberately not expired automatically, because a paused live writer must not be mistaken for a dead one. Malformed/partial JSONL is reported for repair rather than silently truncating historical data. Save a copy before repairing any malformed tail.

Capture responses always identify the resolved project and distinguish central success from mirror/report failure. A retry after a mirror failure preserves the centrally captured Git context rather than rewriting it with the checkout's later dirty state.

## Acceptance and release identity

See [the MCP acceptance runbook](../docs/runbooks/mcp-acceptance.md) for the PRD scenario coverage and the distinction between local boundary tests and the full production-server workflow. Git inspection disables optional Git index locks so central-only capture does not refresh repository metadata. Explicit project selection retains checkout provenance even when remotes are ambiguous.

Local builds identify as `dev`. Release builds inject the tag with `go build -ldflags '-X main.version=<version>'`; both `--version` and MCP initialization report that value.
