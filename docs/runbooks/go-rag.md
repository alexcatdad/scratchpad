# Go PostgreSQL RAG

Issue #36 introduces passage RAG separately from the existing whole-record
semantic search. The Go development backend supports authenticated prerequisite
discovery, per-record durable passage indexing, versioned rebuild and bounded
semantic evidence, atomic capture enqueue and eligible-history reconciliation.
Healthy partial indexes also retain raw lexical evidence. Retrieval evaluation
remains required for full acceptance. The stdio evidence tool is implemented.

## Configuration and availability

`SCRATCHPAD_RAG_ENABLED` accepts `true`, `false`, or an empty/unset value (disabled).
Other values fail server startup with `CONFIG_INVALID`. It controls the new Go
RAG feature; it does not change the existing semantic-search configuration.

Authenticated `GET /api/v1/rag/status` returns `enabled`, `available`, `database`
and `reason`. The reason is `disabled`, `postgres_required`, `pgvector_required`,
`embedding_dimensions_unsupported` or `available`. When available,
`pgvectorVersion` reports the installed version.
Availability means prerequisites exist, not that records are indexed or evidence
is complete. This response must not be used as indexing coverage.

Use a dedicated application PostgreSQL database with pgvector installed in its
application schema. The application checks the live extension catalog on each
enabled status request. It does not install extensions or provision services.
Missing pgvector or SQLite selection leaves ordinary memory available.

After pgvector is installed on a running instance, coverage and retrieval prepare
the optional passage projection before reading it. This works with AI disabled;
an empty projection reports incomplete coverage and retains lexical fallback.

Passage RAG supports up to 16,000 embedding dimensions. The legacy embedding and
semantic-search configuration still accepts up to 16,384. An incompatible passage
configuration leaves captures, retries, metadata, audits and lexical retrieval
available without enqueuing unusable passage work. Explicit passage indexing and
coverage report `RAG_UNAVAILABLE`; retrieval returns lexical fallback. Restoring a
compatible configuration allows preserved sources to be reconciled normally.

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
not vector projections or operational jobs. Native PostgreSQL dump/restore tests
now verify passage coverage and retrieval after restoration, including unavailable
installation keys. See the [restore procedure](go-backend-migrations.md#postgresql-snapshot-and-fresh-database-restoration).
Deployed recovery and full cutover acceptance remain outstanding for issue #36.

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
`semantic: {available: true, indexCoverage: "complete" | "incomplete"}`. Scores express
similarity, not decision authority. Coverage is assessed separately from provider availability. No lexical
fallback is implemented in this slice. Missing results or a provider failure
must not be treated as proof that source evidence is absent. Hybrid retrieval and
assessed uncertainty are described below.

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

## Automatic indexing

With PostgreSQL RAG enabled, each eligible capture queues its local source
generation inside the capture transaction. Record, metadata, lexical projection,
record/queue audits and retry identity commit together. Queue/audit failure rolls
them all back; retrying the same request cannot duplicate either source or job.
No provider connection or secret decryption occurs during enqueue. Projects
without AI consent and derived generated sources do not enqueue.

A separate reconciliation loop discovers eligible existing/imported history and
newly consenting projects without checking provider availability. It continues
while embedding requests are blocked and shares the server cancellation lifetime. Discovery selects at most 16
missing source generations outside the shared writer lock; activation rechecks
current embedding compatibility and project consent under that lock. Empty
reconciliation batches do not acquire the writer lock. Repeated batches and
restart reuse persisted jobs. Changing embedding compatibility queues distinct
generations; completion-only changes do not. This query may still scan historical
rows to locate missing generations, so history-independent reconciliation and
measured contention remain future performance work.

AI disablement, unavailable provider secrets and absent pgvector keep work durable.
Absent pgvector prevents passage-job claims while ordinary AI behavior remains
independent; late installation initializes the optional projection before dispatch.
Existing retry/failure and consent/lease activation rules continue to apply.

## Indexing coverage and generation priority

Authenticated `GET /api/v1/rag/coverage` accepts one `projectId` by default, or
explicit repeated `projectIds` with `crossProject=true` and participation consent.
It reads a repeatable snapshot without contacting or unlocking the provider.
The response reports `eligibleSources`, `indexedSources`, `queuedSources`,
`runningSources`, `failedSources`, `unindexedSources`, `incompatibleSources`,
`passageCount`, the current `fingerprint`, and `status` (`complete` or `incomplete`).

Coverage rehashes immutable sources and checks current processing/embedding
compatibility, completed jobs and matching activated passage counts. Missing
passages cannot be inferred complete from the job alone. Older jobs/projections
count as incompatible source evidence, not current indexing. Incompatible counts
may remain nonzero after the current generation is complete. Completeness proves
index coverage, not an answer or provider availability. Retrieval carries its
assessed coverage status alongside evidence in the same admission transaction.

Workers select only the current passage generation, preserving older queued work
without letting it delay current indexing. Restoring compatible settings can
resume that retained work. Legacy AI-job selection is unchanged. Coverage still
processes eligible source history for exact hashes/counts; query/index performance
remains required delivery work.

Coverage validates the exact current source passage set: deterministic IDs,
Unicode-safe UTF-16 ranges, original text, dimensions and nonzero vectors. The
current generation must contain exactly the expected passages and match the
completed job count. Missing, overlapping, corrupt or extra passages leave the
source unindexed, even if a completed job remains. `passageCount` counts valid
individual passages; a partially valid source does not count as indexed.
Retrieval independently checks passage identity before returning citations.

An explicit version-checked rebuild can restore corrupted derivatives without
rewriting immutable sources. Coverage reads never repair projections. HTTP
regressions exercise corrupt text, noncanonical duplicates, zero vectors,
overlapping ranges, rebuild recovery and unchanged original record details.

## Hybrid ranking and lexical fallback

Compatible indexed passages now combine two separately bounded native candidate
lists: at most 100 exact semantic candidates above the configured similarity
threshold and at most 100 lexical candidates. Reciprocal-rank fusion sums
`1 / (60 + rank)` and deduplicates passage IDs before selecting at most 100 fused
candidates. `rankScore` reports fusion order; `score` retains cosine similarity.
A lexical candidate can survive a low semantic score. Scores never confer authority.

Provider disablement/outage, unavailable provider secrets, changed configuration,
missing pgvector and an empty incomplete semantic result trigger immutable-source
lexical fallback. Authentication/consent failures do not. Fallback rechecks current
authentication and participation in a repeatable snapshot, applies lifecycle before
its 100-source limit, chooses a matching Unicode-safe source passage, and centers
its cited excerpt near the first matched term. It preserves exact source ranges
and the response budget without inventing generated evidence.

Fallback reports `semantic.available=false`, a fixed `reason` code and assessed
coverage, or `indexCoverage=unavailable` when pgvector is absent. Index-unavailable
lexical evidence can include sources that have never been embedded. A successful
query with no results is not proof that incomplete or unavailable indexing has no
evidence. Fallback includes bounded accepted relationship previews.

With nonempty indexed results and incomplete coverage, retrieval also selects up
to 100 native raw-source lexical candidates in the same authorized transaction.
It merges them with indexed lexical candidates, deduplicates passage IDs, ranks
the lexical union and retains its best 100 before reciprocal-rank fusion with
semantic candidates. Indexed sources retain cosine scores; raw lexical sources
retain lexical scores and `retrieval: "lexical"`. Both carry fusion `rankScore`.
The final 100 fused candidates share one result-count and byte-budget selection.
Semantic availability stays true while index coverage stays incomplete.

HTTP regression coverage includes a completed semantic source alongside a pending
exact match, overlapping indexed/raw passage identities and a 4096-byte budget.
Broader dedup/recall evaluation remains required before full acceptance.

## Relationships in lexical fallback

Lexical fallback includes up to ten accepted relationship previews. Both endpoints
must belong to the selected eligible project scope. Replacement and contradiction
previews are prioritized before other relationships; private/out-of-scope IDs and
notes are excluded. Governing lifecycle remains independent of preview limits,
so a source can be correctly historical even when a governing edge is not visible
within selected participation.

Preview notes are capped at 128 UTF-16 units. `relationshipPreviewLimitReached`
signals the fetch cap, and `relationshipsOmitted` counts previews removed to fit
the byte budget. References are removed before shortening the first source
excerpt, including when that excerpt is already short. Current-only retrieval
continues to exclude accepted replacements; history retains the superseded source
and its accessible citations.

## Real stdio acceptance seam

`retrieve_evidence` is the twentieth local MCP tool and uses the existing SSH
proof/client HTTP connection. `mcp/rag-integration` drives the actual MCP executable
from a synthetic Git checkout, verifies discovered local and explicitly scoped
cross-project fallback evidence, source citations, response bytes and uncertainty,
and rejects denied participation, implicit multi-project scope, invalid intent
and explicit zero budgets/limits.

Run the public workflow with disposable PostgreSQL/pgvector:

```sh
TEST_PGVECTOR_URL=postgres://postgres:scratchpad-test@127.0.0.1:5432/postgres go -C backend test -race -count=1 -run TestRAGRealStdio ./...
go -C mcp test -race -count=1 ./...
```

It uses synthetic owner sessions, locally generated SSH keys, actual enrollment
and signatures, and a real Git checkout. These fixtures do not copy private keys
to the server, enable a live provider, or establish deployed/model-quality
acceptance. Existing SQLite/Go legacy semantic and AI workflows remain separate.
