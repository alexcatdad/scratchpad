# Remaining product development and acceptance

## Purpose

Implement and verify the complete remaining product described in [the verification ledger](../post-mvp-readiness.md). Read the canonical PRD, architecture, API contract and accepted clarifications before changing behavior. Keep the owner's running local instance and its data intact; use disposable databases, volumes and synthetic fixtures for automated acceptance.

## Implementation procedure

1. Inspect `git status`, active branch and current remote revision. Coordinate file ownership before parallel work. Preserve unrelated changes and the selected local models in [local AI verification](local-ai.md).
2. Verify current stable dependency releases from their official registries/documentation before adding dependencies; pin exact versions. Use the configured latest Node 24 LTS patch in canonical builds.
3. Implement PostgreSQL persistence and full-text parity behind the existing central API boundary. Add versioned migrations without silently repairing unsupported/incomplete databases. Preserve synchronous transaction semantics or explicitly adapt callers so no unawaited mutation can escape a transaction.
4. Implement database-backed settings, jobs/leases, derived artifacts and rebuildable embeddings. Keep secrets out of API responses, logs, audit snapshots and knowledge exports. Worker claims and record retries must work under concurrent processes.
5. Add OpenAI-compatible generation and embeddings. Use `qwen/qwen3.8-27b` for completion and `text-embedding-qwen3-embedding-4b` for embeddings. Their names are configurable; keep each embedding's actual model and dimension identity. From Docker, the selected LM Studio endpoint is `http://host.docker.internal:1234/v1`.
6. Add all named suggestion types, semantic/related retrieval, patterns, cleanup review and the five intentional document exports. Validate model output and source IDs centrally. Filter project participation before provider requests and similarity computation. Regeneration may replace derived state but cannot replace raw captures.
7. Add dashboard settings, owner presentation, generation/search controls, source inspection, job status and suggestion review. Preserve deterministic capture/retrieval when optional AI is disabled or unavailable.
8. Update API contracts, database operation runbooks and public guides. Append important decisions to `decisions.jsonl`. Commit coherent changes with only intended files.

## Local checks

Run from the repository root:

```sh
npm ci
npm run lint
npm run typecheck
npm test
npm run build
npm run test:e2e
```

Run Go checks from `mcp`:

```sh
go test -race ./...
go vet ./...
go build ./cmd/scratchpad-mcp
```

Use [container acceptance](container-acceptance.md) for real browser and stdio MCP checks against a disposable image and volume. PostgreSQL acceptance must target a disposable database through the application's selected connection setting; use the implemented PostgreSQL runbook for its exact configuration rather than inventing environment-variable names here. Run the same capture, auth, history, settings, retry and import/export scenarios against both engines.

## Required failure and restart scenarios

- Provider unavailable, timeout, malformed output and invalid source IDs: analysis fails visibly, while core capture/search and `/ready` stay available.
- Restart after a job is queued or leased: queued work remains present, abandoned leases are recovered, and bounded retries are visible.
- Two workers: a job is claimed only once at a time. Duplicate capture requests remain idempotent; stale curated edits return a conflict.
- Change embedding model or dimensions: incompatible old vectors are not compared as if compatible. Rebuilding preserves original raw captures.
- Disable cross-project participation after generation: excluded raw data and stale derived outputs no longer appear in cross-project results. A project-specific request remains scoped to the selected project.
- Accept/reject every suggestion family: review is auditable; no automatic human acceptance, raw deletion, destructive merge or supersession occurs.
- Export/import SQLite → PostgreSQL → SQLite: compare knowledge, original provenance, revisions, relationships and audit; credentials are handled only by operational backup/restore.
- Backup/restore each engine: include authentication, settings and jobs; verify browser/MCP access and immutable history after restoration.

## Real local AI acceptance

First follow [local AI verification](local-ai.md) using synthetic text. Then verify integration through the application, rather than calling LM Studio alone:

1. Configure the provider and models in the authenticated dashboard and enable AI for a synthetic project.
2. Capture source records with an explicit synthetic label, including a paraphrase pair, an unrelated record, a disagreement, a decision revision, repeated failures and relevant architecture context.
3. Generate summaries, classification, duplicate/relationship/contradiction candidates, topic clusters and decision-chain observations. Inspect source attribution, generator metadata and timestamps.
4. Build embeddings and use semantic search to recover the paraphrase through the dashboard and agent interface. Inspect stored model/dimension identity.
5. Create another participating synthetic project plus an excluded external project. Generate cross-project patterns and prove the excluded project is absent from requests and results.
6. Review cleanup suggestions and download handoff, architecture summary, decision report, client history and ADR documents. Inspect downloaded content and original records after review.
7. Stop the provider and repeat deterministic retrieval and readiness. Restore provider availability and retry a failed job through the product.

The already verified 2,560-dimensional embedding response is provider compatibility evidence only. Completion requires application behavior and source-preserving review as well.

## Canonical CI, release and deployment

Push the coherent tested revision and inspect GitHub Actions for that exact SHA; monitor terminal results rather than treating workflow definitions as success. New PostgreSQL and AI/job tests must be included in canonical CI. Follow [release](release.md) for GHCR, native MCP archives, local macOS signing/notarization and Homebrew publication. Record code/CI success, publication, local deployment and browser acceptance separately.

Before replacing the owner's local deployment, create and validate an operational backup, preserve its compose configuration and external provider, and use the database migration path. Verify the owner's existing projects and passkey session remain accessible. Never replace the live volume with a synthetic test volume.

Update every ledger row with authoritative evidence or an explicit remaining gap. Mark the full product complete only when all required behaviors are proved at their actual scope.
