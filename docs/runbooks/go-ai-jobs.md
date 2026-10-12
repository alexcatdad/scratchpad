# Go durable AI job controls

The development Go backend implements `GET /api/v1/ai/jobs`, `POST /ai/jobs` and `POST /ai/jobs/{id}/retry`. The TypeScript application remains the supported runtime until issue 36 passes full parity and delivery acceptance. These routes establish durable control state; Go worker claims, leases, scheduling and processing are still pending. A queued Go job does not yet imply generated artifacts or embeddings.

Enqueue accepts `type: analyze | embed | export`, one `projectId` or explicit `projectIds`, optional `crossProject` and the existing export formats. It returns HTTP 202 with the persisted job. Jobs start queued with attempts zero, version one, a current run-after time and canonical selected project IDs. Default export format is `handoff`. Provider configuration must be enabled, and a configured token must be usable through the retained key; enqueue does not contact the provider or wait for model availability.

Single-project operations require one selected project and AI consent. Explicit cross-project selections require every selected project to permit AI and cross-project participation; denied or missing projects fail instead of being silently dropped. Implicit cross-project requests select only participating projects. Explicit lookup uses selected identities rather than scanning unrelated records. Jobs remain operational state and are excluded from portable knowledge export.

Retry requires a failed job, its current expected version/`If-Match`, usable configuration and renewed current scope consent. It retains identity, original scope/type/format and creation provenance, increments the version and resets queued state, attempts, error/completion and lease fields. Queued/running/completed jobs cannot be retried through this interface. Enqueue/retry and their acting-owner audits share one serialized write transaction.

## Verification

```sh
npm run test:backend
```

Supply disposable `TEST_POSTGRES_URL` for PostgreSQL. HTTP tests cover durable enqueue/list/restart, failed-job optimistic retry, consent revocation/restoration, explicit and implicit project scope, provider-outage-independent enqueue and late audit rollback. Operational SQLite backup/restore verifies queued state independently of the live source. These checks do not establish Go worker execution, PostgreSQL operational restore, provider-generated output or deployed acceptance.

The next worker slice must preserve existing lease/retry/scheduling behavior, validate consent and configuration before requests and result activation, recover abandoned leases, and bound provider work without holding database writes. Existing SQLite semantic behavior and derived artifact review must remain supported before extending the queue to PostgreSQL passage RAG.
