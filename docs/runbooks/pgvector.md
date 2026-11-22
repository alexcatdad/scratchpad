# pgvector for passage RAG

pgvector is required only by the optional PostgreSQL passage RAG feature. Whole
record semantic search (`POST /api/v1/search/semantic`) does not use it: on both
SQLite and PostgreSQL it ranks stored embeddings in the server process. Original
captures and stored embeddings are unchanged by enabling pgvector.

[Go PostgreSQL RAG](go-rag.md) describes indexing, retrieval, coverage and the
optional projection schema.

## Enable

Back up the dedicated database using [the PostgreSQL runbook](postgres.md).
Install the extension package on the PostgreSQL server, then use an operator
connection to that dedicated database:

```sql
CREATE EXTENSION vector;
SELECT extversion FROM pg_extension WHERE extname = 'vector';
```

The extension must be installed in the application schema. Scratchpad does not
install or upgrade it and does not need superuser access. Set the application
environment, keeping the existing connection URL in protected deployment
configuration:

```text
SCRATCHPAD_RAG_ENABLED=true
```

Values other than `true`, `false` or empty fail startup with `CONFIG_INVALID`.
With RAG enabled and the extension present, startup creates the
`rag_projection_migrations` and `rag_passages` tables, so the application role
needs CREATE in its schema. An extension installed later is picked up without a
restart. Missing pgvector or a SQLite database leaves ordinary memory available.

## Verify

Authenticated `GET /api/v1/rag/status` reports `available: true` with
`pgvectorVersion`, or a `reason` such as `postgres_required`,
`pgvector_required` or `embedding_dimensions_unsupported`. Availability means the
prerequisites exist, not that records are indexed; use
`GET /api/v1/rag/coverage` for indexing coverage.

## Disable

Set `SCRATCHPAD_RAG_ENABLED=false` and restart. The optional projection tables
remain and are ignored; captures, embeddings and AI consent are unchanged.

## Disposable integration verification

Use a disposable PostgreSQL server with database-creation permission and
pgvector 0.8.7 available to install. Credentials are synthetic and must not
target a live service:

```sh
export TEST_POSTGRES_URL=postgres://postgres:scratchpad-test@127.0.0.1:5432/postgres
export TEST_PGVECTOR_URL="$TEST_POSTGRES_URL"
go -C backend test -race -count=1 -run TestRAG ./...
```

The tests create and drop their own databases and install the extension only
there. Without `TEST_PGVECTOR_URL` the native-extension tests are skipped.
