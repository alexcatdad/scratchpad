# Scratchpad — V1 Architecture

**Version:** 0.1  
**Date:** 29 September 2026  
**Status:** Initial implementation baseline

---

## 1. Purpose

This document defines the initial technical architecture for Scratchpad.

Scratchpad is a private, self-hosted developer-memory system with:

- a central stateful application;
- a browser dashboard;
- a local stdio MCP;
- optional repository dual-write;
- optional AI capabilities;
- SQLite as the default persistence layer;
- PostgreSQL as an alternative persistence layer.

The architecture prioritizes:

- low operational complexity;
- local/self-hosted deployment;
- agent-first workflows;
- restart safety;
- immutable raw history;
- optional rather than mandatory AI;
- clear separation between local developer context and central application logic.

---

## 2. System Overview

```text
┌──────────────────────────────┐
│ Coding Agent                 │
│ Codex / ChatGPT / other MCP  │
└──────────────┬───────────────┘
               │ stdio
               ▼
┌──────────────────────────────┐
│ scratchpad-mcp               │
│ Go                           │
│                              │
│ - Git/project discovery      │
│ - Git provenance             │
│ - SSH auth challenge         │
│ - optional repo dual-write   │
│ - HTTP client                │
└──────────────┬───────────────┘
               │ HTTPS / HTTP
               ▼
┌─────────────────────────────────────────────┐
│ Scratchpad Application                      │
│ TypeScript / Node 24 / TanStack Start       │
│                                             │
│ ┌─────────────────────────────────────────┐ │
│ │ HTTP API                                │ │
│ │ Dashboard server functions             │ │
│ │ WebAuthn                               │ │
│ │ Project resolution                     │ │
│ │ Record lifecycle                       │ │
│ │ Search                                 │ │
│ │ Audit                                  │ │
│ │ Scheduler / jobs                       │ │
│ └─────────────────────────────────────────┘ │
│                    │                        │
│                    ▼                        │
│          SQLite / PostgreSQL                │
└────────────────────┬────────────────────────┘
                     │
                     ▼
        Optional OpenAI-compatible services
          - LM Studio
          - Ollama
          - vLLM
          - compatible remote APIs
```

---

## 3. Technology Decisions

### 3.1 Web application and API

Use:

- TypeScript;
- Node.js 24 LTS;
- TanStack Start;
- React;
- Drizzle ORM.

TanStack Start owns:

- dashboard routes;
- dashboard server functions;
- external HTTP API routes;
- authentication endpoints;
- configuration UI;
- background-job integration.

A separate HTTP framework is not required for V1.

---

### 3.2 MCP

The local MCP is a separate Go executable.

Use:

- Go;
- official MCP Go SDK;
- stdio transport.

The MCP is intentionally thin.

It owns integration with the local development environment but does not own Scratchpad business logic.

---

### 3.3 Database

Supported persistence engines:

### Default

SQLite.

Recommended for normal single-user deployments.

### Optional

PostgreSQL.

Useful where the owner prefers PostgreSQL, expects increased concurrency, or wants PostgreSQL-native extensions such as pgvector later.

Both databases implement the same logical Scratchpad model.

Transparent runtime migration between engines is not supported.

Portability is provided by Scratchpad export/import.

---

## 4. Application Boundaries

### 4.1 Scratchpad server owns

The central application owns:

- project identities;
- records;
- record revisions;
- relationships;
- evidence;
- audit history;
- user profile;
- credentials;
- WebAuthn;
- project settings;
- search;
- background-job state;
- optional AI configuration;
- derived knowledge;
- imports and exports.

---

### 4.2 MCP owns

The MCP owns:

- current working-directory discovery;
- Git repository detection;
- repository-root detection;
- remote discovery;
- repository-identity normalization;
- branch discovery;
- commit discovery;
- worktree discovery;
- developer SSH/signing-key access;
- API authentication;
- optional repository dual-write;
- MCP protocol handling.

---

### 4.3 MCP must not own

The MCP must not independently decide:

- project settings;
- cross-project permissions;
- AI configuration;
- record lifecycle rules;
- record relationship semantics;
- canonical summaries;
- retention;
- audit history.

These belong to the server.

---

## 5. Project Resolution

### 5.1 Git repositories

When running inside Git:

```text
working directory
      ↓
git root
      ↓
remote candidates
      ↓
normalized repo identity
      ↓
Scratchpad project resolution
```

Example equivalent remotes:

```text
git@github.com:alexcatdad/scratchpad.git
https://github.com/alexcatdad/scratchpad.git
```

normalize to the same project source identity:

```text
github.com/alexcatdad/scratchpad
```

The API maps that source identity to a stable internal project ID.

---

### 5.2 Worktrees

Multiple worktrees for the same repository map to one project.

The individual record preserves:

- branch;
- commit;
- worktree path or identifier where useful.

Worktree identity never creates a new project automatically.

---

### 5.3 Multiple remotes

The MCP should use deterministic rules where possible.

Suggested priority:

1. `origin`;
2. unique fetch remote;
3. unique push remote;
4. otherwise report ambiguity.

If competing remotes could represent different projects, the MCP must not guess.

It reports structured ambiguity to the agent.

The agent asks the user.

---

### 5.4 Non-Git environments

Resolution hints, in order:

1. known project manifest;
2. package/project name;
3. current directory name.

These are weak identities.

If the API cannot resolve them safely, it returns:

```text
PROJECT_IDENTITY_REQUIRED
```

The agent asks the user for a project name and retries.

---

## 6. Authentication Architecture

Scratchpad has one owner with multiple possible credentials.

Credentials authenticate the owner, not separate user accounts.

---

### 6.1 MCP authentication

Preferred mechanism:

```text
MCP → request challenge
API → nonce
MCP → signs canonical challenge using developer key
API → verifies enrolled public key
API → issues short-lived bearer token
```

The private key never enters the Scratchpad server.

Potential credential sources:

- existing SSH signing key;
- SSH authentication key;
- hardware-backed SSH key;
- Scratchpad-generated local identity key as fallback.

---

### 6.2 Browser authentication

Use WebAuthn/passkeys.

No password requirement.

Browser authentication flow:

```text
browser
  ↓
WebAuthn challenge
  ↓
Touch ID / security key / passkey
  ↓
verified owner
  ↓
server-side session
```

Sessions use secure HTTP-only cookies.

---

### 6.3 GitHub

This historical baseline is superseded for optional owner authentication by §32.

GitHub is not an authentication provider.

GitHub may enrich the owner profile with:

- username;
- avatar;
- display name;
- profile link;
- public identity metadata.

GitHub profile linkage must be visually distinct from authenticated identity.

---

## 7. Stateful Application Model

Scratchpad is a stateful application.

Persistent state includes:

- records;
- projects;
- revisions;
- settings;
- credentials;
- sessions;
- authentication challenges;
- jobs;
- derived data.

The application process itself must be restart-safe.

Required correctness must never depend solely on process memory.

In-memory caching is allowed only when losing the cache changes performance rather than correctness.

---

## 8. Record Architecture

Scratchpad separates:

```text
RAW KNOWLEDGE
      ↓
CURATED KNOWLEDGE
      ↓
DERIVED KNOWLEDGE
```

---

### 8.1 Raw knowledge

Raw captured records are immutable.

Examples:

- an explicit user decision;
- an agent observation;
- a failure;
- a Q&A;
- project state.

Raw content is never automatically rewritten.

---

### 8.2 Curated knowledge

Human-curatable representations may include:

- corrected titles;
- tags;
- relationships;
- classifications;
- summaries.

Changes are mutable but fully audited.

---

### 8.3 Derived knowledge

Generated/rebuildable information includes:

- embeddings;
- clusters;
- AI summaries;
- similarity matches;
- pattern suggestions;
- duplicate candidates;
- recommendation candidates.

Derived data can be deleted and regenerated without losing authoritative history.

---

## 9. Record Types

V1 supports distinct types:

```text
decision
adr
business_decision
finding
qa
failure
constraint
project_state
```

All share a common record envelope while supporting type-specific payloads.

---

## 10. Dual-Write Architecture

Central Scratchpad storage is authoritative.

Repository writes are optional mirrors.

Default repo-eligible types:

```text
decision
adr
business_decision
```

The MCP performs repository writes because it has direct local filesystem access.

Flow:

```text
agent
  ↓
MCP
  ├── central API write
  └── optional repository write
```

The central record records whether repo mirroring succeeded.

Repo-write failure must not destroy a successfully created central record.

The API and MCP should return partial-success information clearly.

---

## 11. Repository Mirror Format

V1 should use a simple append-friendly structured format.

Recommended initial representation:

```text
scratchpad/decisions.jsonl
```

or an equivalent configurable repository path.

Each mirrored record contains:

- stable Scratchpad record ID;
- type;
- title;
- decision content;
- rationale where applicable;
- recorded date;
- central provenance reference where practical.

The repository representation is a mirror, not a second authority.

Two-way synchronization is out of scope.

---

## 12. Search Architecture

Search has three levels.

### 12.1 Structured filtering

Always available.

Filters include:

- project;
- type;
- date;
- tag;
- source;
- branch;
- status;
- relationship.

---

### 12.2 Full-text search

Always available.

SQLite:

```text
FTS5
```

PostgreSQL:

```text
native full-text search
```

---

### 12.3 Semantic search

Optional.

Requires embeddings.

Must not replace deterministic full-text search.

Semantic results are derived search results.

---

## 13. Background Jobs

No Redis or external queue is required.

Jobs are persisted in the application database.

Conceptual schema:

```text
id
type
status
payload
run_after
attempts
started_at
completed_at
last_error
created_at
updated_at
```

A scheduler identifies runnable jobs.

A worker leases and executes them.

For V1, scheduler and worker may run inside the same application process.

The job model must permit separating the worker later without schema redesign.

---

## 14. Optional AI Architecture

The AI layer uses an internal provider interface.

Conceptually:

```ts
interface CompletionProvider {
  complete(input: CompletionInput): Promise<CompletionResult>;
}

interface EmbeddingProvider {
  embed(input: string[]): Promise<number[][]>;
}
```

V1 supports OpenAI-compatible endpoints.

Primary expected local use:

```text
LM Studio
```

Other compatible implementations may work without dedicated adapters.

---

## 15. AI Safety Boundary

AI processing may produce suggestions.

It may not automatically:

- delete raw data;
- modify raw data;
- mark a human decision accepted;
- supersede a decision;
- merge historical evidence destructively.

AI output must include:

- derivation metadata;
- source record IDs;
- model/provider metadata where useful;
- generation timestamp.

---

## 16. Cross-Project Analysis

Cross-project analysis is an optional project capability.

Per-project configuration:

```text
crossProjectAnalysis: enabled | disabled
```

External/client projects default to disabled.

Cross-project jobs must filter out projects that do not permit participation before providing records to the model or similarity layer.

---

## 17. Dashboard Architecture

Primary screens expected for V1:

```text
/
  overview

/projects
/projects/:id
/projects/:id/records
/projects/:id/settings

/records/:id

/search

/settings
/settings/auth
/settings/ai
/settings/export
```

Exact information architecture may evolve.

---

## 18. Deployment Architecture

### 18.1 SQLite deployment

Minimum:

```text
Scratchpad container
└── /data/scratchpad.sqlite
```

One image.

One volume.

No supporting service required.

---

### 18.2 PostgreSQL deployment

```text
Scratchpad container
      ↓
PostgreSQL
```

Optional local LLM remains independent.

---

### 18.3 AI-enabled deployment

Example:

```text
Scratchpad
   ↓
LM Studio OpenAI-compatible endpoint
```

Scratchpad must remain healthy if the LLM endpoint is unavailable.

AI jobs should fail/retry independently.

---

## 19. CI

GitHub Actions is used for Scratchpad itself.

PR CI:

```text
TypeScript:
- lint
- typecheck
- test
- build

Go:
- fmt verification
- lint
- test
- race tests where appropriate
- govulncheck
- build
```

---

## 20. Releases

A release produces two independent deliverables.

### Application

```text
ghcr.io/alexcatdad/scratchpad:<version>
```

### MCP

GitHub Release binaries:

```text
scratchpad-mcp-darwin-arm64
scratchpad-mcp-darwin-amd64
scratchpad-mcp-linux-amd64
scratchpad-mcp-linux-arm64
```

Plus:

```text
checksums.txt
```

macOS binaries are Developer ID signed and notarized locally by the owner. A draft release transfers those artifacts to CI, which verifies the expected signing team, Apple trust assessment, source revision, checksums, and rebuilt executable content before publication. Apple signing credentials remain in the local Keychain.

The existing Homebrew tap is automatically updated.

---

## 21. Source Repository Structure

Recommended initial structure:

```text
scratchpad/
├── apps/
│   └── web/
│       ├── app/
│       ├── routes/
│       ├── server/
│       └── ...
│
├── mcp/
│   ├── cmd/
│   │   └── scratchpad-mcp/
│   ├── internal/
│   │   ├── api/
│   │   ├── auth/
│   │   ├── gitctx/
│   │   ├── mirror/
│   │   └── project/
│   ├── go.mod
│   └── go.sum
│
├── packages/
│   ├── db/
│   ├── domain/
│   └── ai/
│
├── migrations/
├── docs/
│   ├── architecture.md
│   ├── api.md
│   └── decisions/
│
├── Dockerfile
├── compose.yaml
└── README.md
```

Go and TypeScript remain separate build domains.

The API contract is their integration boundary.

---

## 22. Architectural Constraints

The following should remain true unless explicitly superseded:

1. Scratchpad remains usable without AI.
2. Scratchpad remains usable without PostgreSQL.
3. Git is not the primary datastore.
4. Raw captured records remain immutable.
5. The MCP remains a thin local-integration layer.
6. Business logic remains server-side.
7. Repository mirroring remains optional.
8. Project discovery normally requires no manual MCP project configuration.
9. Browser and MCP authentication remain independent mechanisms.
10. No enterprise multi-user architecture is required for V1.
11. No external queue/cache/vector service is required for V1.
12. Restarting the server must not lose required state.

---

## 23. First Vertical Slice

Implementation should first prove:

```text
agent
→ stdio MCP
→ detect Git repo
→ authenticate
→ POST record
→ SQLite
→ dashboard shows record
```

Before implementing:

- embeddings;
- AI cleanup;
- Postgres;
- pattern detection;
- complex summaries.

This vertical slice proves the central product architecture.

## 24. Accepted clarification — owner enrollment and recovery

Accepted by the owner on 2026-09-29 (decision `scratchpad-20260929-010`). This section supplements the supplied v0.1 baseline.

- Use browser-first enrollment. An administrator command inside the container generates a short-lived, single-use setup token.
- The owner opens the setup page at the configured public URL, presents the token, and registers the first passkey. Initial setup is disabled after enrollment.
- Use SimpleWebAuthn for browser registration/authentication verification. Exact dependencies are verified and pinned during implementation.
- Enroll additional passkeys and MCP public keys through the authenticated dashboard. MCP key enrollment requires proof of possession via a signed challenge; private keys remain local.
- Lost-credential recovery uses a separate administrator command inside the container to authorize replacement-passkey registration. Recovery is explicit and audited, preserves project data, and does not reopen ordinary first-time setup.
- Server administrative access is the recovery authority; no email delivery or external identity provider is required.

Exact command names, endpoints, token lifetimes, and existing-session handling on recovery remain implementation details. This approval does not yet select the MCP signature encoding or token lifetime.

## 25. Accepted clarification — simple MCP sessions

Accepted on 2026-09-29 (decision `scratchpad-20260929-011`).

The server issues a random, short-lived, single-use challenge. MCP signs it with an enrolled key; successful verification returns a session token. The session lasts up to 24 hours and the client keeps its token in MCP process memory. MCP restart requires authentication again. Credential revocation immediately invalidates associated sessions. No refresh-token mechanism is required initially.

Server-side session validity and revocation state remain persistent and restart-safe, consistent with §7. Client memory storage does not imply server process-local session authority.

This replaces the earlier unaccepted 15-minute-token proposal. Exact signature encoding, signed fields, challenge lifetime, and server token representation remain engineering details; the two-minute challenge suggestion was not adopted as a requirement.

## 26. Accepted clarification — MCP working directory

Accepted on 2026-09-29 (decision `scratchpad-20260929-012`).

Use the MCP process launch directory as the default discovery context. Project-scoped tool calls may supply an optional `workingDirectory` override when the agent is operating elsewhere. The override applies to that call; it does not change a shared current-project setting. Resolve Git/project identity from the selected directory using the existing discovery rules and include the resolved project in results. Missing or ambiguous project identity still produces structured resolution errors. Stdio transport alone is not a guarantee that a client launches the server in the active project folder.

The earlier proposal to require a directory on every call was not adopted.

## 27. Accepted clarification — flexible typed capture

Accepted on 2026-09-29 (decision `scratchpad-20260929-013`).

Keep explicit typed MCP capture tools, with natural fields such as decision/rationale or question/answer. Persist their submitted fields as immutable payload plus common metadata. Generate readable content deterministically from those fields, without AI or requiring the caller to compose duplicate text. Server-owned IDs, receipt timestamps, and authenticated credential identity are assigned by the server; descriptive source attribution remains distinguishable from verified identity. Use the resolved project ID for both Git and non-Git record creation.

Treat the initial payload shapes as an evolving starting point. Keep required fields minimal, add optional structure as real usage warrants, and preserve original captures when schemas or renderers evolve. Historical records must remain readable without rewriting raw evidence to satisfy a newer shape. Avoid freezing a comprehensive taxonomy or building a dynamic schema platform in advance of demonstrated needs. Exact schema-versioning and compatibility mechanics remain implementation details.

## 28. Accepted clarification — retries and concurrent edits

Accepted on 2026-09-29 (decision `scratchpad-20260929-014`). Retrying the same capture with the same request identity and content returns the existing record. Reusing that identity with changed content produces a conflict. Separate intentional captures remain separate even when text matches; do not perform automatic content-based deduplication.

Mutable edits must detect stale revisions and return a conflict rather than silently overwrite intervening changes. No automatic merge is required. Raw captures remain immutable. Persist retry state as required by the API baseline; exact key scope, comparison rules, retention, and concurrency representation remain implementation details.

## 29. Accepted clarification — simple local mirroring

Accepted on 2026-09-29 (decision `scratchpad-20260929-015`).

- Mirror only when both local MCP configuration and server-owned project settings permit it. Default eligible types remain `decision`, `adr`, and `business_decision`.
- Persist centrally first, then append eligible captures to the repository JSONL file.
- If the local write fails, keep the central record and report “saved centrally; mirror failed.” A retry must not duplicate either the central record or its local mirror.
- Mirroring covers captures made through that MCP instance into its selected checkout. No automatic browser-to-repository synchronization is required.
- Never automatically commit or push mirrored files.

The central API remains authoritative. Exact append coordination and mirror-result reporting are implementation details to resolve when building mirroring.

## 30. Remaining-product implementation

The source implementation extends the SQLite MVP with optional PostgreSQL, database-backed AI processing and intentional private document generation. Published v0.1.3 packages retain their original SQLite-only feature set; source support, canonical CI, release publication and deployment acceptance are separate evidence.

### Persistence

A single asynchronous Store boundary uses Drizzle with SQLite or PostgreSQL. `SCRATCHPAD_DATABASE_URL` selects PostgreSQL; otherwise SQLite remains the default. Domain behavior, authentication, retries, auditing and optimistic edits stay in the central server. Database-native full-text facilities implement deterministic retrieval. SQLite remains one file/volume; PostgreSQL requires a dedicated application database and its own operational backup tooling. Native export/import provides knowledge portability, not transparent live migration or credential migration.

### Optional processing

OpenAI-compatible completion and embedding adapters operate independently of core capture/search/readiness. Provider configuration is owner-controlled in the dashboard; global enablement alone does not authorize processing a project. Each project opts into AI, and cross-project analysis additionally requires its separate permission. External/client projects default to both permissions disabled. Consent is rechecked before provider requests, before persistence and when retrieving derived results.

Jobs and schedules are database entities, with runnable time, attempt counts, leases and bounded failure/retry handling. The scheduler/worker currently runs inside the server process and can recover abandoned leased jobs after restart. No Redis or dedicated vector service is required. Embeddings store finite vectors together with source identity, model, dimensions and configuration fingerprint; incompatible or stale indexes cannot participate in similarity search.

### Derivation and review

Schema-validated completions produce source-linked summaries, classifications, duplicate/relationship/contradiction candidates, topic clusters, patterns and cleanup recommendations. Source record content is untrusted input, not instructions for the worker. Artifact review records an audit event and can create curated interpretation or an explicitly reviewed relationship/classification; original captures and historical lifecycle evidence remain immutable.

The dashboard exposes provider/model/schedule/analysis settings, project participation, job visibility, semantic search, suggestion review and five private Markdown document formats. The MCP exposes server-backed processing, semantic retrieval, job inspection and document requests without duplicating provider business logic. The owner intentionally downloads and shares derived documents; generation never publishes the private instance.

### Operations and evidence

Portable knowledge exports include source/audit/derived knowledge and owner presentation, excluding authentication state, provider secrets, jobs and embeddings. Full operational backups preserve the entire selected database: SQLite's administrator snapshot command or PostgreSQL's `pg_dump`/`pg_restore`. Follow the respective runbooks and verify a restore in a disposable environment.

Mock-provider browser acceptance proves application wiring, consent boundaries, review, downloads and failure isolation. Real selected-provider acceptance separately proves model interoperability and source-linked output. Neither substitutes for the other or for exact-revision canonical CI/release verification. Track final evidence in the remaining-product ledger.

## 31. Optional extensions

The owner requested these additions on 2 October 2026. PostgreSQL may explicitly opt into pgvector through `SCRATCHPAD_PGVECTOR=true`; the operator installs the extension in the application database. SQLite retains its existing comparator. A derived secondary vector table supports native exact cosine search after candidate eligibility checks. The original JSON embedding remains authoritative for rebuilds and precision preservation. Native vector float32 representation can differ slightly from the original JavaScript numbers; unsupported native vectors retain the existing comparator rather than being truncated. Approximate indexes are not enabled implicitly.

Public GitHub profile enrichment uses the fixed public GitHub user API with bounded requests. An authenticated owner selects a username; the server stores a validated public snapshot separately from authentication state. Link, refresh and unlink use optimistic profile versioning and audited writes. GitHub downtime does not affect capture, sign-in or readiness.

Generated TypeScript and Go clients are derived from OpenAPI, with deterministic regeneration and actual authenticated HTTP integration. Their source and build artifacts are independently consumable. They do not replace the MCP SDK or its HTTP compatibility boundary.

## 32. Accepted clarification — GitHub owner authentication

The accepted [owner-access ADR](adr/0001-github-owner-access.md) supersedes the presentation-only GitHub restriction in §6.3 and §31. GitHub OAuth browser authentication is optional alongside passkeys; both authenticate the same owner. The existing local SSH challenge remains the MCP boundary. A verified GitHub account binding and its synchronized SSH authentication/signing keys provide machine eligibility; an old public profile snapshot grants no authority.

The central server owns OAuth state, account binding, synchronization and authorization. Persist stable GitHub account identity, complete normalized key snapshots, last-success time, local blocks, credential provenance and sessions in the selected database. Fetch both paginated categories completely before applying removals or advancing freshness. A failed endpoint/page or malformed response is a failed refresh, not an empty key set. Restart preserves freshness and blocks. Local blocking, detected removal and the 24-hour cache deadline apply to existing requests as well as new authentication.

Synchronization runs every five minutes; public-key discovery does not require private GitHub repository access. Browser OAuth state is short-lived, single-use and bound to its initiating browser and action. Verify identity on the server through GitHub before binding/signing in; bind stable account ID rather than username. Keep authorization codes, OAuth secrets and tokens out of logs, public API responses and knowledge exports. Fixed GitHub endpoints and bounded response/pagination handling contain the external-service boundary.

GitHub outages leave complete cached keys usable for at most 24 hours since last success, independently of the machine session's lifetime. Browser sign-in requires GitHub; independent passkeys and administrator recovery remain available. Unlinking requires fresh independent authentication; replacement requires fresh existing authentication. Both invalidate GitHub-derived sessions and permissions and preserve data and independent credentials. Do not silently convert credential provenance.

Machine onboarding is an instruction-only skill using existing client configuration and SSH tooling. It verifies a real authenticated MCP read with explicit project scope. The HTTP API remains authoritative; no dedicated setup command, client OAuth secret or new machine authentication mechanism is introduced.

## Accepted clarification: non-executing checkout discovery (2026-10-09)

MCP checkout discovery requires a trusted installed Git 2.36 or later, disables core.fsmonitor, submodule recursion and status submodule summaries for every provenance command, and explicitly ignores submodule changes in status. Repository-selected filesystem monitor programs are never invoked by memory discovery. This preserves parent repository identity, branch, commit, root and parent worktree dirtiness; nested submodule dirtiness is intentionally outside the provenance snapshot. Unsupported or unparseable Git versions fail before inspecting the checkout. Explicit project IDs do not authorize running checkout helpers.
