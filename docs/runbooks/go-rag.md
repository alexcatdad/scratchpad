# Go PostgreSQL RAG

Issue #36 introduces passage RAG separately from the existing whole-record
semantic search. The Go development backend supports authenticated prerequisite
discovery, per-record durable passage indexing, versioned rebuild and bounded
semantic evidence. Automatic backfill/capture indexing, hybrid ranking, lexical
fallback, assessed coverage and the RAG MCP tool remain required for the full feature.

## Configuration and availability

`SCRATCHPAD_RAG_ENABLED` accepts `true`, `false`, or an empty/unset value (disabled).
Other values fail server startup with `CONFIG_INVALID`. It controls the new Go
RAG feature; it does not change the existing semantic-search configuration.

Authenticated `GET /api/v1/rag/status` returns `enabled`, `available`, `database`
and `reason`. The reason is `disabled`, `postgres_required`, `pgvector_required`
or `available`. When available, `pgvectorVersion` reports the installed version.
Availability means prerequisites exist, not that records are indexed or evidence
is complete. This response must not be used as indexing coverage.

Use a dedicated application PostgreSQL database with pgvector installed in its
application schema. The application checks the live extension catalog on each
enabled status request. It does not install extensions or provision services.
Missing pgvector or SQLite selection leaves ordinary memory available.

The existing TypeScript application remains the supported runtime until full
backend parity and delivery acceptance. This new route is Go-only during that
transition. Discovery does not rewrite records or install an extension.

## Index and rebuild a source

Authenticated `POST /api/v1/rag/index` accepts `{projectId, recordId}` and returns
`202 {job}`. The project must permit AI processing and own the immutable record.
Derived generated records are excluded from this evidence corpus. Explicit
cross-project scopes use the existing participation consent boundary.

The job identity binds source ID, source hash and embedding compatibility. A
repeated enqueue returns that job without duplicating provider work. Jobs remain
durable while AI is disabled or a provider is unavailable; there is no network
request during enqueue. The existing worker claims an exclusive renewable lease,
rechecks consent before each request, and atomically activates the separate
passages with completion and its audit. Failed jobs use the existing bounded
retry policy and authenticated `/ai/jobs/{id}/retry` interface.

Sources split into at most 6000 UTF-16 units per passage, preserving surrogate
pairs. Each passage retains stable identity, source record/project, exact content
range, source hash, processing version, compatibility fingerprint and its own
native vector. Long sources are not averaged. Native vectors are scaled without
changing cosine direction to preserve finite pgvector storage. The native vector
dimension ceiling is 16000; legacy semantic search retains its existing bounds.

Compatibility depends on provider URL, embedding model, dimensions and passage
processing version. Changing the completion model preserves completed and
in-flight compatible passage work. Changing the provider, embedding model or
token, disabling AI or revoking consent prevents activation of admitted output.
Raw source hashes and the current lease are checked again at activation.

For an explicit rebuild, submit the same source with `rebuild: true` and the
current job `expectedVersion`. Only completed/failed jobs may be rebuilt. This
requeues the existing job; it cannot steal an active lease. Rebuilding discarded
projection rows restores their source citation identities without editing history.

## Optional projection migration

When RAG is enabled and the application-schema extension exists, startup applies
projection migration 1 before starting workers or serving requests. Late extension
installation is supported by the indexing route under the same serialized write
transaction. `rag_projection_migrations` versions the optional `rag_passages`
table independently of core schema 2. The marker and table creation commit
together; competing instances use the existing PostgreSQL advisory lock.

Unknown versions, malformed timestamps and missing required projection objects
are refused without automatic repair. Disable the optional RAG feature to access
ordinary memory while preparing an authorized projection recovery. Retain the
database for operational restore; knowledge exports contain original knowledge,
not vector projections or operational jobs. PostgreSQL operational restore and
full cutover acceptance remain outstanding for issue #36.

## Retrieve semantic passage evidence

Authenticated `POST /api/v1/rag/retrieve` accepts project scope and a nonempty
`query` of at most 4000 UTF-16 units. Default scope is one project; cross-project
requests require explicit scope and each selected project's current consent.
`intent` is `current` by default or `history`; `limit` defaults to 20 (1–100), and
`maxChars` defaults to 12000 (4096–64000 serialized UTF-8 bytes including newline).

The current implementation ranks at most 100 compatible native-vector candidates
exactly. Current-only lifecycle filtering precedes the candidate limit. It validates
source hashes and locations, returns at most 1000 UTF-16 units per cited excerpt,
and applies the response budget. Results carry record/project identifiers,
source URL and range, authority, confidence, applicability, processing version
and content hash. Governing lifecycle is independent of the ten-edge preview cap.
Accepted replacement/contradiction previews have priority, and both endpoints
must belong to selected eligible projects. Original records remain available at
the cited record-detail route.

At least one fitting source excerpt is preserved by dropping preview references
and then shortening Unicode text when needed. `textTruncated` identifies shortened
text and its citation range remains exact. `relationshipsOmitted` counts preview
references removed to fit the byte budget; `relationshipPreviewLimitReached`
signals that the ten-edge fetch cap was reached and further references may exist.
`resultsOmitted` counts eligible candidates excluded by the result or byte limits.

The response includes `results`, `resultsOmitted` and
`semantic: {available: true, indexCoverage: "not_assessed"}`. Scores express
similarity, not decision authority. Coverage is not yet assessed, and no lexical
fallback is implemented in this slice. Missing results or a provider failure
must not be treated as proof that source evidence is absent. Full hybrid retrieval,
automatic indexing and explicit assessed uncertainty remain required work.

## Verify with an isolated database

Use an existing disposable PostgreSQL instance with pgvector 0.8.7 available to
install. Test credentials below are synthetic and must not target a live service:

```sh
export TEST_POSTGRES_URL=postgres://postgres:scratchpad-test@127.0.0.1:5432/postgres
export TEST_PGVECTOR_URL="$TEST_POSTGRES_URL"
go -C backend test -race -count=1 -run TestRAG ./...
```

The test creates and drops its own database. It installs and removes the extension
only there, verifies live availability changes through authenticated HTTP, and
checks that ordinary project retrieval remains usable without the extension.
Without `TEST_PGVECTOR_URL`, the native-extension workflow is explicitly skipped.
The existing pgvector GitHub Actions lane supplies the pinned pgvector image and
runs this workflow in addition to TypeScript native-vector tests.

On macOS, a disposable fixture can be built from PostgreSQL and pgvector sources
under a temporary installation prefix when no local extension is available. Keep
the installation, cluster and listener isolated; stop the owned cluster after
verification. Do not install into the system PostgreSQL directories or use a
remote Docker context implicitly. Local verification does not prove CI, a real
provider, deployed acceptance or completed RAG retrieval.
