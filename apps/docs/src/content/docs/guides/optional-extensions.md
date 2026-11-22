---
title: Optional extensions & API clients
description: Passage retrieval with PostgreSQL and pgvector, public GitHub profiles and generated HTTP clients.
---

## Passage retrieval with pgvector

Whole-record semantic search works on SQLite and PostgreSQL without an extension. On PostgreSQL, the server can additionally index passages of each record for cited evidence retrieval. An operator installs pgvector in the application database's schema and sets `SCRATCHPAD_RAG_ENABLED=true` in the server deployment. Scratchpad does not install the extension; without it, or on SQLite, ordinary memory keeps working.

Authenticated `GET /api/v1/rag/status` reports whether the prerequisites exist (`disabled`, `postgres_required`, `pgvector_required`, `embedding_dimensions_unsupported` or `available`). Availability does not mean records are indexed. Indexing runs as durable background jobs for projects that allow AI processing; passages support up to 16,000 embedding dimensions, so the selected Qwen embedding keeps all 2,560.

Retrieval combines lexical and exact semantic candidates and returns citations to the original records; this is not a claim of approximate HNSW indexing. When the provider or index is unavailable, it falls back to lexical results. Agents use it through the `retrieve_evidence` tool. Passages and vectors are rebuildable derivatives; original captures remain authoritative. Follow the [RAG runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/go-rag.md) for setup, coverage and rebuild checks.

## Public GitHub profile

In **Settings → Profile**, under **Public GitHub profile**, enter a public GitHub username to link its public profile. Refresh updates the snapshot; unlink removes the association. This profile label is descriptive and grants no authority. Verified [GitHub owner sign-in](/scratchpad/reference/security/) is a separate optional feature; existing presentation snapshots are never promoted into authentication. Existing passkey and independent SSH credentials remain available.

See the [profile runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/github-profile.md) for validation and failure behavior.

## Generated API clients

TypeScript and Go clients cover the documented HTTP API, including authentication, project resolution, capture, retrieval, optional AI and public profile operations. Configure your instance origin and authenticate using its existing credential flow. Version checks and project permissions still apply on the server.

The clients are generated from [OpenAPI](https://github.com/alexcatdad/scratchpad/blob/main/docs/openapi.json) into `packages/clients/`. Follow the [API client runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/api-clients.md) for installation and examples. Regenerate with `npm run sdk:generate`; `npm run sdk:check` verifies freshness and compilation, and `npm run sdk:test` exercises actual authenticated requests.

v0.3.0 attached a compiled `scratchpad-api-client-0.3.0.tgz`. Current releases publish only the macOS app, so use the clients from a source checkout.
