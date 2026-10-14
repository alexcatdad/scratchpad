# AI dispatch snapshot verification

Issue #14 is independently based on main. No provider settings or deployment are changed by these checks.

1. Use Node 24.21.0 and Go 1.27.2. Run npm run test, npm run lint, npm run typecheck and npm run build.
2. Run npm run test:e2e to verify ordinary private AI settings, analysis, semantic search and document generation.
3. The PostgreSQL CI job runs postgres.test.ts using its disposable test database. It verifies persisted dispatch generations and fencing across two service/store instances. No live owner database is needed.

Synthetic interleaving tests reproduce an old endpoint receiving a replacement key on the previous implementation. Paused first-request tests cover embedding, analysis, document export and connectivity for endpoint replacement, key removal and key replacement. After an update, later requests are rejected; failed jobs retain a safe message and can be explicitly retried. Requests admitted before an update cannot be recalled. Embedding compatibility is separate from dispatch generation, so key-only changes preserve usable embeddings.

Storage uses the existing ai_settings entity JSON and optimistic entity version in SQLite and PostgreSQL. Existing settings without dispatchGeneration are generation zero; the next atomic configuration write increments it. There is no destructive schema migration. Configuration snapshots and API keys never enter reports or audit payloads.
