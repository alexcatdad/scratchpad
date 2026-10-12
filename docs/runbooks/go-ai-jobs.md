# Go durable AI job controls

The development Go backend implements `GET /api/v1/ai/jobs`, `POST /ai/jobs` and `POST /ai/jobs/{id}/retry`, plus embedding execution and `POST /search/semantic`. The TypeScript application remains the supported runtime until issue 36 passes full parity and delivery acceptance. Analysis/export execution, artifact review and automatic scheduling remain pending; those job types retain their durable queued state.

Enqueue accepts `type: analyze | embed | export`, one `projectId` or explicit `projectIds`, optional `crossProject` and the existing export formats. It returns HTTP 202 with the persisted job. Jobs start queued with attempts zero, version one, a current run-after time and canonical selected project IDs. Default export format is `handoff`. Provider configuration must be enabled, and a configured token must be usable through the retained key; enqueue does not contact the provider or wait for model availability.

Single-project operations require one selected project and AI consent. Explicit cross-project selections require every selected project to permit AI and cross-project participation; denied or missing projects fail instead of being silently dropped. Implicit cross-project requests select only participating projects. Explicit lookup uses selected identities rather than scanning unrelated records. Jobs remain operational state and are excluded from portable knowledge export.

Retry requires a failed job, its current expected version/`If-Match`, usable configuration and renewed current scope consent. It retains identity, original scope/type/format and creation provenance, increments the version and resets queued state, attempts, error/completion and lease fields. Queued/running/completed jobs cannot be retried through this interface. Enqueue/retry and their acting-owner audits share one serialized write transaction.

Each server checks for embedding work every second and executes one job at a time. Serialized claims persist a distinct lease token, increment attempts and recover expired running leases. Leases last the configured provider timeout plus thirty seconds and renew before each embedding request. At most three attempts run automatically, with exponential retry delays. Shutdown cancels provider work and leaves interrupted leases recoverable. Worker failures do not affect core readiness.

Consent, usable secret state, configuration generation and lease ownership are checked before each provider dispatch and before result activation. Provider waits occur outside database writes. Embedding upserts, completion state and the completion audit commit together. Configuration changes or consent revocation discard in-flight results; no later request is admitted under stale settings. Provider failures retain safe fixed errors, without response bodies or tokens.

This slice preserves the existing whole-record semantic projection on SQLite and PostgreSQL. It processes all content in bounded chunks, preserving Unicode characters at boundaries, and combines chunk vectors only for this legacy projection. Compatible fingerprints and canonical source hashes determine semantic eligibility. Query embeddings require current consent; results are revalidated after ranking. An absent compatible index returns `indexRequired: true`. The new PostgreSQL RAG projection still requires separate passages and embedding compatibility independent of completion-model selection; these legacy vectors do not fulfill that requirement.

## Verification

```sh
npm run test:backend
```

Supply disposable `TEST_POSTGRES_URL` for PostgreSQL. Real-process HTTP tests cover durable enqueue/list/restart, failed-job optimistic retry, scope consent, provider-outage-independent enqueue and late audit rollback. Embedding workflows verify semantic retrieval across restart, abandoned-lease recovery with competing servers and consent revocation during provider waits on both engines. A long-record fixture verifies Unicode preservation. Operational SQLite backup/restore verifies queued state independently of the live source. Synthetic provider responses replace only the external network boundary. These checks do not establish PostgreSQL operational restore, analysis/export processing, new passage RAG or deployed acceptance.

Next implement analysis/export execution, derived artifact review and scheduling, then extend the durable queue to PostgreSQL passage RAG. Verify interrupted execution, provider failure recovery, configuration changes and result-audit rollback further before full parity acceptance.
