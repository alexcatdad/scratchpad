---
title: Architecture
description: Components, ownership boundaries, and the first vertical slice.
---

Scratchpad has two runtime components and one documentation site.

```text
Coding agent
    │ stdio
Local Go MCP ─── optional JSONL mirror in the checkout
    │ HTTP API
TypeScript server + browser dashboard
    │
SQLite persistent storage
```

The public documentation site runs independently on GitHub Pages. It does not connect to a private instance or carry user records.

## Server

The server uses Node.js 24 LTS, TypeScript, React, TanStack Start, and Drizzle ORM. It owns authentication, project resolution, record validation and persistence, search, retry semantics, audit history, and eventually optional processing jobs.

SQLite is the default. PostgreSQL is an alternative target after the initial capture/retrieval loop. There is no mandatory cache, queue, vector service, or AI service.

## Local MCP

The Go process owns local integration: Git discovery, signing, HTTP communication, and eligible mirror writes. It does not own central storage or duplicate server business logic.

The HTTP contract connects the two languages. Neither side imports the other's internal types.

## Knowledge layers

| Layer   | Meaning                                | Change policy                                             |
| ------- | -------------------------------------- | --------------------------------------------------------- |
| Raw     | Original captured or imported evidence | Immutable                                                 |
| Curated | Deliberately maintained interpretation | Audited edits with conflict detection                     |
| Derived | Generated summaries or relationships   | Explicit provenance; never silently replaces raw evidence |

## First milestone

Prove project discovery, authentication, capture, durable SQLite storage, browser visibility, and retrieval from a new agent session. Include failures and retries. A passing isolated unit test is not proof of this complete path.

The [full architecture baseline](https://github.com/alexcatdad/scratchpad/blob/main/docs/architecture.md) includes accepted clarifications and later V1 capabilities.
