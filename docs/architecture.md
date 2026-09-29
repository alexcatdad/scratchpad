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

# 2. System Overview

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

# 3. Technology Decisions

## 3.1 Web application and API

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

## 3.2 MCP

The local MCP is a separate Go executable.

Use:

- Go;
- official MCP Go SDK;
- stdio transport.

The MCP is intentionally thin.

It owns integration with the local development environment but does not own Scratchpad business logic.

---

## 3.3 Database

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

# 4. Application Boundaries

## 4.1 Scratchpad server owns

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

## 4.2 MCP owns

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

## 4.3 MCP must not own

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

# 5. Project Resolution

## 5.1 Git repositories

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

## 5.2 Worktrees

Multiple worktrees for the same repository map to one project.

The individual record preserves:

- branch;
- commit;
- worktree path or identifier where useful.

Worktree identity never creates a new project automatically.

---

## 5.3 Multiple remotes

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

## 5.4 Non-Git environments

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

# 6. Authentication Architecture

Scratchpad has one owner with multiple possible credentials.

Credentials authenticate the owner, not separate user accounts.

---

## 6.1 MCP authentication

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

## 6.2 Browser authentication

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

## 6.3 GitHub

GitHub is not an authentication provider.

GitHub may enrich the owner profile with:

- username;
- avatar;
- display name;
- profile link;
- public identity metadata.

GitHub profile linkage must be visually distinct from authenticated identity.

---

# 7. Stateful Application Model

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

# 8. Record Architecture

Scratchpad separates:

```text
RAW KNOWLEDGE
      ↓
CURATED KNOWLEDGE
      ↓
DERIVED KNOWLEDGE
```

---

## 8.1 Raw knowledge

Raw captured records are immutable.

Examples:

- an explicit user decision;
- an agent observation;
- a failure;
- a Q&A;
- project state.

Raw content is never automatically rewritten.

---

## 8.2 Curated knowledge

Human-curatable representations may include:

- corrected titles;
- tags;
- relationships;
- classifications;
- summaries.

Changes are mutable but fully audited.

---

## 8.3 Derived knowledge

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

# 9. Record Types

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

# 10. Dual-Write Architecture

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

# 11. Repository Mirror Format

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

# 12. Search Architecture

Search has three levels.

## 12.1 Structured filtering

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

## 12.2 Full-text search

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

## 12.3 Semantic search

Optional.

Requires embeddings.

Must not replace deterministic full-text search.

Semantic results are derived search results.

---

# 13. Background Jobs

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

# 14. Optional AI Architecture

The AI layer uses an internal provider interface.

Conceptually:

```ts
interface CompletionProvider {
  complete(input: CompletionInput): Promise<CompletionResult>
}

interface EmbeddingProvider {
  embed(input: string[]): Promise<number[][]>
}
```

V1 supports OpenAI-compatible endpoints.

Primary expected local use:

```text
LM Studio
```

Other compatible implementations may work without dedicated adapters.

---

# 15. AI Safety Boundary

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

# 16. Cross-Project Analysis

Cross-project analysis is an optional project capability.

Per-project configuration:

```text
crossProjectAnalysis: enabled | disabled
```

External/client projects default to disabled.

Cross-project jobs must filter out projects that do not permit participation before providing records to the model or similarity layer.

---

# 17. Dashboard Architecture

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

# 18. Deployment Architecture

## 18.1 SQLite deployment

Minimum:

```text
Scratchpad container
└── /data/scratchpad.sqlite
```

One image.

One volume.

No supporting service required.

---

## 18.2 PostgreSQL deployment

```text
Scratchpad container
      ↓
PostgreSQL
```

Optional local LLM remains independent.

---

## 18.3 AI-enabled deployment

Example:

```text
Scratchpad
   ↓
LM Studio OpenAI-compatible endpoint
```

Scratchpad must remain healthy if the LLM endpoint is unavailable.

AI jobs should fail/retry independently.

---

# 19. CI

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

# 20. Releases

A release produces two independent deliverables.

## Application

```text
ghcr.io/alexcatdad/scratchpad:<version>
```

## MCP

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

macOS binaries are Developer ID signed and notarized.

The existing Homebrew tap is automatically updated.

---

# 21. Source Repository Structure

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

# 22. Architectural Constraints

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

# 23. First Vertical Slice

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