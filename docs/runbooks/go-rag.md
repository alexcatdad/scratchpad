# Go PostgreSQL RAG

Issue #36 introduces passage RAG separately from the existing whole-record
semantic search. The current slice establishes authenticated prerequisite
discovery. Passage storage, durable indexing, coverage and hybrid cited retrieval
remain required before RAG can serve evidence.

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
transition. No database migration or record rewrite is introduced by discovery.

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
