---
title: Optional extensions & API clients
description: Native PostgreSQL vector search, public GitHub profiles and generated HTTP clients.
---

These additions are implemented in the current source after v0.2.0. Check [release verification](/scratchpad/guides/releases/) before expecting them in an installed release.

## Native PostgreSQL vectors

An operator may install pgvector in the application database and enable `SCRATCHPAD_PGVECTOR=true` in the server deployment. SQLite remains available and does not require an extension. Follow the [pgvector runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/pgvector.md) for setup, readiness and rebuild checks.

Search preserves project participation, model compatibility and source identity. The selected Qwen embedding keeps all 2,560 dimensions. Native search uses exact cosine similarity; this is not a claim of approximate HNSW indexing. Original vectors remain available for rebuilding the derived database index.

## Public GitHub profile

In **Settings → Your profile and storage**, choose a public GitHub username to link its public profile. Refresh updates the snapshot; unlink removes the association. The profile label is descriptive: GitHub does not authenticate you, enroll credentials or grant access to your instance. Your existing passkey and SSH credentials remain the authentication mechanism.

See the [profile runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/github-profile.md) for validation and failure behavior.

## Generated API clients

TypeScript and Go clients cover the documented HTTP API, including authentication, project resolution, capture, retrieval, optional AI and public profile operations. Configure your instance origin and authenticate using its existing credential flow. Version checks and project permissions still apply on the server.

The clients are generated from [OpenAPI](https://github.com/alexcatdad/scratchpad/blob/main/docs/openapi.json). Follow the [API client runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/api-clients.md) for installation and examples. Regenerate with `npm run sdk:generate`; `npm run sdk:check` verifies freshness and compilation, and `npm run sdk:test` exercises actual authenticated requests.
