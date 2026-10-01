# Optional capability development and acceptance

The owner requested pgvector, public GitHub profile enrichment and generated API clients on 2 October 2026. These extend published v0.2.0; source changes do not establish release publication or a local upgrade.

## Scope and boundaries

- PostgreSQL may opt into pgvector for native cosine similarity. SQLite retains its existing search. Preserve model, dimensions, content compatibility and project consent before scoring. Never truncate embeddings to fit an index.
- The owner may explicitly link a public GitHub username, refresh its public snapshot or unlink it. GitHub is presentation only and must not become authentication or alter credentials.
- Generate usable TypeScript and Go clients from the OpenAPI contract. Regeneration must be deterministic; exercise authenticated calls against the actual server. The MCP continues to depend on HTTP rather than internal application types.

## Development procedure

1. Read current PRD, architecture, API and accepted clarifications. Keep feature ownership separate while working in parallel.
2. Verify maintained tool/extension versions against primary sources and pin new dependencies exactly.
3. Update the contract before final client generation. Add narrow integration tests for each capability and preserve the authenticated SQLite and PostgreSQL loops.
4. Run client generation/checks, lint, type, unit, browser, Go and build checks appropriate to the changes. Test pgvector against an actual disposable extension-enabled database.
5. Record important decisions in `decisions.jsonl`; make coherent commits, then require canonical GitHub Actions at the exact source.
6. For release delivery, follow the release runbook: immutable new tag, local signing/notarization, verified publication and fresh installation acceptance.
7. Back up the owner deployment before upgrading. Preserve its origin, Compose project, volume, raw records and credentials. Verify actual browser/MCP behavior separately from provider smoke checks.

Specialized commands and configuration belong in the pgvector, GitHub profile and API client runbooks. Keep published-release instructions tied to the version that actually ships them.
