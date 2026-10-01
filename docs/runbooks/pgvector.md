# Optional native pgvector search

pgvector is an explicit PostgreSQL deployment option. SQLite and ordinary
PostgreSQL continue using the existing JavaScript cosine comparator. Original
captures and persisted embedding JSON remain unchanged.

## Representation and ranking

Scratchpad stores a rebuildable `pgvector_embeddings` secondary table and uses
native exact cosine distance. Candidate IDs, model, dimension, provider/model
fingerprint and source content hash are matched before ranking. The AI service
checks project consent before the query provider request and again before
retrieval. There is no ANN/HNSW recall or performance claim.

The selected Qwen embedding model has 2,560 dimensions. All dimensions are stored
and searched. pgvector's `vector` storage supports 16,000 dimensions, whereas its
HNSW/IVFFlat `vector` indexes support only 2,000. Scratchpad does not truncate
vectors or use half-precision indexes to fit that limit. The secondary table has
a B-tree compatibility index, followed by exact distance evaluation.

Native `vector` uses float32 components. Canonical embedding JSON retains the
provider's original numbers; native cosine scores may differ slightly from the
JavaScript double-precision comparator. Tests compare scores with a tolerance of
`1e-6`; results are not claimed to be bit-identical. Configurations from 16,001 to
16,384 dimensions retain the existing JavaScript path. Finite vectors that
cannot be represented as finite, nonzero float32 vectors also use that path.
Missing secondary candidates cause a complete JavaScript fallback rather than
silently omitting results.

## Enable and verify

Back up the dedicated database using [the PostgreSQL runbook](postgres.md).
Install the extension package on the PostgreSQL server, then use an operator
connection to that dedicated database:

```sql
CREATE EXTENSION vector;
SELECT extversion FROM pg_extension WHERE extname = 'vector';
```

The extension must be installed in the application schema (normally `public`).
Scratchpad does not install or upgrade the extension and does not require
superuser access to run. Set the application environment, keeping the existing
connection URL in your protected deployment configuration:

```text
SCRATCHPAD_PGVECTOR=true
```

Restart the application and verify `/ready` returns HTTP 200. Enabling this option
without a PostgreSQL URL is a configuration error. A missing extension or
incomplete native table produces database-not-ready behavior rather than a
silent change of deployment mode. Readiness checks the secondary schema while
enabled. The extension's functions/types must remain available.

Startup creates the secondary table and compatibility index under the existing
transactional advisory lock, then rebuilds its contents from persisted embedding
entities. The rebuild is atomic and runs on each enabled restart, including
after a period with the option disabled. Job embedding updates, deletion and
native table writes share the entity transaction; rollback preserves coherence.
Changing an embedding model or dimensions still requires an explicit embedding
job for the newly compatible model. Startup rebuilding does not call the model
provider or grant project consent.

For recovery, stop application instances before dropping a damaged secondary
table, restart with the option enabled, and verify readiness and semantic search.
Full operational `pg_dump` backups include the extension declaration and secondary
table; the restore server must have the extension package installed. Native
knowledge exports exclude embeddings, so rebuilding those after knowledge import
requires an explicit embedding job.

To disable, remove the environment setting and restart. Derived secondary state
may remain in PostgreSQL and is ignored while disabled; a later enabled restart
rebuilds it. This does not modify capture data or AI consent.

## Disposable integration verification

The upstream primary repository was checked on 2 October 2026. Stable tag
`v0.8.7` resolves to commit `f37c13f68b57d2c3472b2214fbcff699d6d34876`.
See [upstream installation and vector limits](https://github.com/pgvector/pgvector/tree/v0.8.7).
The tested image is pinned by version and digest:

```sh
docker run -d --name scratchpad-pgvector-test \
  -e POSTGRES_PASSWORD=synthetic-test \
  -p 127.0.0.1:55439:5432 \
  pgvector/pgvector:0.8.7-pg18-trixie@sha256:9d9c930220cb9bf2f956d10a8f909cf9973d9672ca0278aef2c1e6facccad2e0

docker exec scratchpad-pgvector-test pg_isready -U postgres

TEST_PGVECTOR_URL=postgres://postgres:synthetic-test@localhost:55439/postgres \
  npm run test -w @scratchpad/web -- src/server/pgvector.test.ts

docker rm -f -v scratchpad-pgvector-test
```

Use only a disposable server with database-creation permission. Tests create
random dedicated databases, install the extension there and drop them during
cleanup. They exercise all 2,560 dimensions, native consent-filtered AI retrieval,
model/content compatibility, restart rebuilding, updates, deletion, rollback,
missing-extension/schema readiness and the unchanged SQLite/large-vector paths.
