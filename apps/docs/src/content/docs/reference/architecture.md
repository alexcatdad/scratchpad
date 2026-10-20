---
title: Architecture
description: Current Go backend, retained dashboard and local capture boundaries.
---

Current source uses a Go backend and retains the React/TypeScript dashboard.
Published releases and installed deployments have separate acceptance evidence.

```text
Coding agent -> local Go stdio MCP
  -> private Go capture service / encrypted queue
  -> HTTPS Go API -> SQLite or PostgreSQL
       -> private Node renderer -> React dashboard
       -> optional completion / embedding providers
```

Go owns authentication, API/business logic, persistence/migrations, project
resolution, capture, search/context, audit, relationships, imports/exports and
background jobs. Node renders dashboard pages in UI-only mode on loopback; it
receives no credential or database authority. Go owns public cookies, health and
readiness. Renderer failure does not disable the API.

The native macOS app owns collector configuration, credentials and lifecycle.
Its bundled MCP requires only its command path. Captures first enter encrypted
durable local storage: pending receipts are local; delivered receipts retain
central IDs. Offline reads fail explicitly. Optional repository mirrors follow
central persistence and never commit/push automatically.

SQLite is the default. PostgreSQL and pgvector passage RAG are optional. Core
memory works without AI; background jobs and schedules are database-backed.
Provider secrets require a separately retained installation-key file. Restoring
encrypted settings requires that same key, without replacing existing credentials.

| Layer   | Meaning                                | Change policy                                             |
| ------- | -------------------------------------- | --------------------------------------------------------- |
| Raw     | Original captured or imported evidence | Immutable                                                 |
| Curated | Deliberately maintained interpretation | Audited edits with conflict detection                     |
| Derived | Generated summaries or relationships   | Explicit provenance; never silently replaces raw evidence |

One installation still has one owner with multiple credentials: passkeys, SSH
proof, administrator recovery and optional GitHub owner OAuth. GitHub App identity,
users/teams, project membership, user-bound agent credentials and permission-aware
retrieval are **planned P1 work**, not current multi-user authorization.

Scratchpad owns application images, manifests, migrations, credential integration
and recovery procedures. Generic platform infrastructure remains external.
Production cutover is separately authorized after disposable restore validation;
a main-branch merge does not establish deployed acceptance.

See the [authoritative architecture](https://github.com/alexcatdad/scratchpad/blob/main/docs/architecture.md),
[P0 acceptance](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/p0-consolidation.md),
[optional AI](/scratchpad/guides/ai/) and [PostgreSQL operations](/scratchpad/guides/postgresql/).
