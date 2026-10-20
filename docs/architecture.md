# Scratchpad architecture

Current source architecture, consolidated 20 October 2026. The [PRD](prd.md)
defines product requirements; [API contracts](api.md) and [OpenAPI](openapi.json)
define wire behavior. [Earlier architecture](archive/p0-baseline/architecture.md)
and existing [ADRs](adr/0001-github-owner-access.md) preserve prior decisions.
Published releases and installed deployments require their own version and
acceptance evidence; source support does not imply a live cutover.

## Runtime and trust boundaries

```text
Coding agent
  -> local Go stdio MCP (checkout discovery, provenance, mirror consent)
  -> private local Go capture service (encrypted queue, receipts, SSH sessions)
  -> HTTPS Go backend (authentication, API, persistence, workers)
       -> SQLite or PostgreSQL
       -> private loopback Node renderer -> React dashboard
       -> optional OpenAI-compatible completion and embedding providers
```

The `backend` module is the authoritative implementation of application behavior.
The Go process owns the public origin, `/api/v1`, `/health`, `/ready`, migrations,
authentication, projects, captures, curation, audit, imports/exports and optional
AI jobs. The default container and `npm run dev` select Go. The private renderer
runs with `SCRATCHPAD_UI_ONLY=true`; it must not initialize a database or implement
application behavior. Go strips credentials and forwarding identity before
proxying dashboard GET/HEAD requests and prevents renderer cookies from replacing
Go sessions. Renderer failure leaves the API available.

The TypeScript implementation has been removed from the current source tree.
Rollback tests extract its pinned historical revision into disposable fixtures.
Its schema-1 restriction makes it unsuitable for running against a Go-upgraded
database. Behavior-focused tests retain restored authentication, numeric import
identity and old-schema refusal. Track acceptance in the
[P0 acceptance ledger](runbooks/p0-consolidation.md).

The native macOS SwiftUI app manages collector configuration, credentials and
lifecycle. Homebrew cask updates replace the earlier in-app updater direction;
Developer ID signing, notarization and installed-upgrade acceptance remain
required. The command-only bundled MCP receives operation results from the
collector, never private keys or session tokens.

## Identity and authorization

One installation has one owner and multiple credentials. Independent passkeys,
administrator setup/recovery, optional GitHub OAuth and manually enrolled SSH
keys authenticate that owner. Git names and public profile snapshots are
presentation only. The [owner-access ADR](adr/0001-github-owner-access.md) governs
verified GitHub identity and synchronized published SSH keys.

Browser sessions use HttpOnly, SameSite=Strict cookies with Secure on HTTPS.
Machine sessions follow recipient-bound OpenSSH SSHSIG proof v2 and expire within
24 hours. Challenges and hashed sessions persist through restart; revocation,
credential provenance and local blocks are checked on every request. GitHub keys
require complete authentication/signing-key snapshots every five minutes and
expire 24 hours after the last complete successful refresh. Failed refreshes and
restarts never extend freshness. Independent credentials and recovery remain
available when GitHub is unavailable.

The public origin must remain stable for passkeys and proof recipients. Neither
Git transport credentials nor personal access tokens substitute for Scratchpad
proof. The central server never receives client private keys. See
[authentication operations](runbooks/github-oauth.md) and the
[migration contract](runbooks/go-backend-migrations.md).

## Captures, local delivery and provenance

All eight typed captures enter the collector's private encrypted durable queue
before acknowledgment. `saved_pending` means local intake with a receipt and no
invented central ID; `delivered` retains central project/record IDs. Local intake
works offline, while reads and non-capture operations require the central API.
There is no direct-server capture fallback when the collector is unavailable.

Delivery remains pinned to the original server, credential fingerprint, request
identity, client save time and per-call checkout context. Authentication must
advertise `clientSavedAt-v1` before central capture. Same identity and content
replay the original capture; changed content conflicts. Separate intentional
captures remain separate even when their text matches.

Raw input, payloads, source claims, timestamps and provenance stay immutable.
Server-assigned actor identity remains distinct from descriptive attribution.
Curation creates revisions/audits; derived knowledge cites sources and never
becomes an explicit decision automatically. Unknown historical payload fields
and exact JSON numbers survive supported import/export and retrieval.

Worktrees share normalized repository project identity while retaining checkout,
branch and commit provenance. Git discovery uses trusted Git with executable
repository hooks such as filesystem monitors disabled. Non-Git and ambiguous
identities require explicit resolution. Optional repository mirrors require
saved local consent and project permission, occur after central persistence,
report failure independently and never commit or push automatically.

## Persistence and migrations

SQLite is the default: owner-private file/directory storage, WAL and bounded
connections. PostgreSQL is optional with a dedicated application database and
bounded pool. Both implement the same domain contracts. Full-text search uses
SQLite FTS5 or PostgreSQL native facilities; ordinary memory needs neither AI nor
pgvector. Export/import provides knowledge portability, not live engine migration
or credential migration.

Go recognizes historical schema 1. Ordered transactional migration 2 adds entity
ordering and migration 3 adds indexed, trigger-maintained context headers.
Optional PostgreSQL passage projections have their own prerequisites. Startup
serializes migrations (`BEGIN IMMEDIATE` on SQLite and the shared transaction
advisory lock on PostgreSQL), validates installed objects and refuses malformed
or unsupported schemas without automatic repair. Migration markers and DDL commit
together. Raw captures are not rewritten.

An application role that does not own PostgreSQL tables cannot apply DDL. Run the
image's `scratchpad-server migrate` with the explicitly scoped migration role
before rollout, then serve with the ordinary application role. Readiness proves
core storage/schema availability; it does not assert provider health, indexing
coverage or complete application acceptance.

The TypeScript schema-1 executable refuses Go schemas 2/3. Rollback to that
artifact requires the verified pre-upgrade database snapshot, the retained
installation key and the old artifact. Never delete migration markers or run old
and new runtimes against the upgraded database. See
[upgrade and recovery](runbooks/go-backend-migrations.md).

## Optional AI and retrieval

Database-backed leased jobs implement analysis, embeddings, private documents,
schedules, restart recovery and bounded retries without Redis. Owner settings
and per-project participation gate dispatch, persistence and retrieval. External
projects default to AI and cross-project participation disabled. Configuration
changes fence subsequent requests and discard incompatible outputs.

Provider secrets use versioned authenticated encryption with a separately retained
protected key file, `SCRATCHPAD_ENCRYPTION_KEY_FILE`. Never silently replace an
installation key. Legacy plaintext conversion is separately reportable from
schema upgrades. Missing/wrong keys block secret-dependent operations without
plaintext fallback; ordinary memory and providers needing no token remain usable.
Backups of encrypted settings require the same key. See
[secret recovery](runbooks/go-secret-configuration.md).

SQLite and PostgreSQL retain whole-record semantic search. Optional PostgreSQL
passage RAG uses operator-installed pgvector and `SCRATCHPAD_RAG_ENABLED=true`.
Durable per-record jobs index eligible history/new captures asynchronously.
Passages, source hashes, UTF-16 locations and vectors are rebuildable derivatives;
immutable captures remain authoritative. Changing completion models preserves
compatible embedding generations.

`retrieve_evidence` forwards bounded `/rag/retrieve` requests. Retrieval combines
bounded lexical and exact semantic candidates through reciprocal rank fusion,
deduplicates passages, applies live lifecycle/consent, and returns citations plus
omission and coverage information. Lexical fallback handles provider/index
unavailability. Explicit cross-project intent never overrides participation.
Scores do not establish authority. See [RAG operations](runbooks/go-rag.md) and
[compact context](runbooks/go-context.md).

## Delivery and operational ownership

Scratchpad owns its images, application manifests, workflows, migrations,
credential-file interface and upgrade/backup/restore/rollback procedures. Generic
cluster networking, storage provisioning, secret management and access policy
remain platform responsibilities. Existing databases, persistent volumes,
credentials, origins and installation keys must survive adoption.

Container publication, code merge and live production rollout are distinct.
Production migration/cutover requires reviewed operator authorization and a
verified disposable restore. Restoring a pre-upgrade snapshot loses subsequent
writes, so define the maintenance/write-admission boundary before cutover.
[Deployment procedures](runbooks/homelab-deploy.md) own the executable steps.

GitHub Actions is the canonical runner for Go race/vet/build, HTTP/MCP parity,
frontend/docs/SDK checks, PostgreSQL 18, pgvector, container restore and macOS
packaging. Local tests, canonical CI, published artifacts and deployed acceptance
are separate claims. [P0 evidence](runbooks/p0-consolidation.md) records outstanding
gates without implying acceptance from compilation alone.

## Planned enterprise architecture — P1

The following is planned and is not implemented authorization:

- GitHub App user identity and repository installation integration;
- multiple users, teams, organizations and project membership;
- per-project read/write/administration authorization;
- agent credentials bound to an individual user's eligible authority;
- permission-aware search, exports, context and RAG, including derived inputs.

Existing owner OAuth and published-key synchronization do not provide these
capabilities. Future repository permissions need stable GitHub IDs, explicit
Scratchpad enforcement, bounded revalidation and revocation, transfer/removal
handling, and explicit sharing of previously personal history. No enterprise
RBAC, GitHub App migration or new RAG feature is part of P0.
