# Go retrieval evaluation

Issue #36 requires representative evidence recall, stale/forbidden evidence,
response size, duplication and latency measurements. The public acceptance seam
is the actual Go subprocess with authenticated HTTP capture, relationships,
retrieval and record-detail inspection. PostgreSQL/pgvector is disposable;
assertions never inspect projection tables or replace database queries.

`backend/rag_evaluation_test.go` defines a labeled synthetic corpus independently
of the ranking implementation. Its 35 sources include 24 irrelevant build
observations, deployment-key evidence, credential retirement, a long appendix,
a replaced backup policy, contradictory observations, a partly replaced policy,
a participating second project and a project without processing consent.

## Local admission and lexical evaluation

Use a disposable PostgreSQL instance with pgvector 0.8.7 available. The helper
creates and drops its own application database and installs the extension there.
The examples use synthetic local credentials; never target a live database.

```sh
export TEST_PGVECTOR_URL=postgres://postgres@127.0.0.1:55481/postgres
go -C backend test -race -count=1 -run '^TestRAG(RepresentativeEvaluation|PartialIndexCombinesSemanticEvidenceWithPendingLexicalSources)$' -v .
```

The representative local lane disables provider processing while preserving the
durable passage queue. It checks:

- Exact identifier recall and a matching citation deep inside a long source.
- Current retrieval excluding a fully replaced policy; history retaining both.
- Both contradictory observations and their explicit contradiction relationship.
- Partial supersession retaining its applicability annotation.
- Default project isolation and an explicitly consenting project union.
- Rejection of a requested project without AI-processing consent.
- An absent identifier producing no lexical result while reporting incomplete,
  unavailable semantic indexing.

Each labeled case requires source recall@5 of 1. Duplicate passage identities,
forbidden project evidence and fully superseded current evidence fail immediately.
Every returned excerpt must equal the UTF-16 range of its immutable source, be
inspectable at its record-detail URL, and preserve source authority/confidence.
Responses must fit 12,000 serialized UTF-8 bytes including the trailing newline.
Recall is reported as `n/a` for an unanswerable request. Per-query logs include
result count, response bytes and elapsed retrieval latency; record-detail
verification is outside the timed interval.

The paired existing partial-index workflow checks a healthy semantic query
provider with an indexed source and a pending exact-match source. Its controlled
vectors prove merging and admission behavior, not embedding-model quality.

Local verification on October 12, 2026 passed all eight representative lexical
cases: seven labeled cases had recall@5 of 1, the absent identifier had zero
results, and no duplicate passages, forbidden evidence or stale current evidence
were returned. Responses were 117–2,054 bytes. One race-enabled run measured
37–44 ms per request while other agent work was running. Those observations are
functional evaluation results, not an uncontended performance baseline, CI proof
or deployed acceptance.

## Real embedding-provider evaluation

The same corpus can run against an explicitly supplied OpenAI-compatible local
embedding provider. It never selects a remote endpoint, downloads a model, reads
credentials or uses a stored provider token. The supplied base URL must be
credential-free loopback HTTP. Set the actual served embedding model and exact
output dimensions; the server routes ordinary passage indexing and query
embedding requests to that provider.

```sh
export TEST_PGVECTOR_URL=postgres://postgres@127.0.0.1:55481/postgres
export TEST_RAG_EVALUATION_BASE_URL=http://127.0.0.1:1234/v1
export TEST_RAG_EVALUATION_MODEL=your-served-embedding-model
export TEST_RAG_EVALUATION_DIMENSIONS=768
go -C backend test -race -count=1 -timeout 10m -run '^TestRAGRepresentativeEvaluation/model$' -v .
```

The model lane requires completed corpus indexing, healthy semantic availability
and complete assessed coverage. It adds a labeled paraphrase about republishing a
retired machine credential and requires the credential-retirement source within
the first five passages. The long-source label counts as recalled only when a
returned excerpt actually includes the relevant recovery identifier, so returning
an unrelated passage from the same source cannot satisfy the expectation.
The fixed similarity threshold is 0.5. Provider requests time out after 30 seconds;
corpus indexing has a three-minute deadline. Poor model recall or slow indexing
fails the evaluation rather than changing labels or substituting synthetic vectors.

An unanswerable semantic query reports candidate count, with recall `n/a`;
nearest evidence alone is not an answerability classifier. Its citations remain
subject to all scope, lifecycle and source-integrity checks. False-positive
candidate quality and threshold calibration must be assessed with the actual
provider before treating this small corpus as a production quality gate.

Without `TEST_RAG_EVALUATION_BASE_URL`, the model lane explicitly skips.
Real-provider paraphrase quality has not been verified by the local lexical run.
The corpus is a small reproducible acceptance sample; broader provider-specific
recall, precision, latency percentiles and concurrency measurement remain needed
for full issue #36 acceptance. Preserve verbose output with the model/version,
dimensions and execution conditions when running that gate.
