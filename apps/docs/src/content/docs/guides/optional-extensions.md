---
title: Optional extensions & API clients
description: Native PostgreSQL vector search, public GitHub profiles and generated HTTP clients.
---

These additions are published in **v0.3.0**. See [release verification](/scratchpad/guides/releases/) for artifact and installation evidence.

## Native PostgreSQL vectors

An operator may install pgvector in the application database and enable `SCRATCHPAD_PGVECTOR=true` in the server deployment. SQLite remains available and does not require an extension. Follow the [pgvector runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/pgvector.md) for setup, readiness and rebuild checks.

Search preserves project participation, model compatibility and source identity. The selected Qwen embedding keeps all 2,560 dimensions. Native search uses exact cosine similarity; this is not a claim of approximate HNSW indexing. Original vectors remain available for rebuilding the derived database index.

## Public GitHub profile

In **Settings → Your profile and storage**, choose a public GitHub username to link its public profile. Refresh updates the snapshot; unlink removes the association. This profile label is descriptive and grants no authority. Verified [GitHub owner sign-in](/scratchpad/reference/security/) is a separate optional current-source feature; existing presentation snapshots are never promoted into authentication. Existing passkey and independent SSH credentials remain available.

See the [profile runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/github-profile.md) for validation and failure behavior.

## Generated API clients

TypeScript and Go clients cover the documented HTTP API, including authentication, project resolution, capture, retrieval, optional AI and public profile operations. Configure your instance origin and authenticate using its existing credential flow. Version checks and project permissions still apply on the server.

The clients are generated from [OpenAPI](https://github.com/alexcatdad/scratchpad/blob/main/docs/openapi.json). Follow the [API client runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/api-clients.md) for installation and examples. Regenerate with `npm run sdk:generate`; `npm run sdk:check` verifies freshness and compilation, and `npm run sdk:test` exercises actual authenticated requests.

The release includes the compiled `scratchpad-api-client-0.3.0.tgz` and its checksum.
The Go module can be installed with
`go get github.com/alexcatdad/scratchpad/packages/clients/go@v0.3.0`.
