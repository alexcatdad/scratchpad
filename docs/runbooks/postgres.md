# PostgreSQL operations and verification

Scratchpad uses SQLite unless `SCRATCHPAD_DATABASE_URL` is set. PostgreSQL uses the same API, authentication, immutable captures, audit history, optimistic concurrency, persisted retries, optional AI jobs and portable knowledge format.

## Start an instance

Use a dedicated empty database and a database role with permission to create tables and indexes in its selected schema. Scratchpad initializes schema 1 transactionally. An existing unsupported, unrelated or incomplete database is refused without repair. PostgreSQL 18.6 is the verified stable test version; the driver is pinned to `postgres` 3.4.9 and entity operations use Drizzle ORM.

Set the server environment through your private Compose environment file or deployment secret:

```dotenv
SCRATCHPAD_DATABASE_URL=postgresql://scratchpad:REPLACE_WITH_PRIVATE_PASSWORD@postgres:5432/scratchpad
SCRATCHPAD_PUBLIC_URL=https://scratchpad.example.com
```

URL-encode reserved characters in credentials. Use the appropriate PostgreSQL TLS connection settings for a remote database. Never commit the environment file or connection URL. `SCRATCHPAD_DATABASE_PATH` remains the SQLite setting and is ignored when PostgreSQL is selected. The dashboard reports the storage engine without exposing the URL or password.

```sh
docker compose up -d
curl --fail https://scratchpad.example.com/ready
docker compose exec scratchpad npm run admin -- setup
```

Complete browser passkey enrollment, then enroll the MCP public key as described in the server runbook. Readiness confirms storage and schema validity; an unavailable AI provider does not make core storage unready.

## Move knowledge between SQLite and PostgreSQL

1. Export a native Scratchpad archive through the authenticated dashboard on the source instance.
2. Start the destination instance on a fresh database and enroll its owner and MCP credentials.
3. Import the archive through the destination dashboard.
4. Verify raw captures, metadata, revisions, source identities, evidence, relationships, settings, profile and source-linked AI/curated artifacts.

Native archives preserve record IDs and audit entries, but exclude authentication, sessions, retry tables, provider secrets, queued jobs and embeddings. Configure the provider separately and rebuild embeddings if desired. For an operational restore preserving the entire installation, use a database backup instead.

## Back up the complete installation

The `admin backup` command creates SQLite snapshots. For PostgreSQL, use PostgreSQL's native tools. Use a client version compatible with the server. A custom-format `pg_dump` produces a transactionally consistent backup, including all tables and indexes: credentials, sessions, challenges, retry state, AI settings, queued jobs and authoritative memory.

The example below uses a PostgreSQL Compose service named `postgres`. The dump contains private memory and provider credentials: protect it like the database. If password authentication is needed, use the PostgreSQL client's private password file or deployment secret; avoid putting a password in a command line.

```sh
umask 077
docker compose exec -T postgres pg_dump -U scratchpad -d scratchpad --format=custom > scratchpad.dump
```

Verify a restore into a **fresh database**, never over a running installation. Stop the application, create a new empty destination and restore:

```sh
docker compose stop scratchpad
docker compose exec -T postgres createdb -U scratchpad scratchpad_restored
docker compose exec -T postgres pg_restore -U scratchpad -d scratchpad_restored --exit-on-error < scratchpad.dump
```

Change the private server connection setting to the restored database, then start and verify:

```sh
docker compose up -d scratchpad
curl --fail https://scratchpad.example.com/ready
```

Confirm browser authentication, MCP authentication, an existing capture retry, deterministic search, audit history and AI job state. Keep the original database until the restored instance is verified. Avoid running two instances against separate copies with scheduled processing enabled during restoration.

## Disposable PostgreSQL verification

The tests create isolated databases with random names and drop only those databases. The test database role therefore needs `CREATE DATABASE`. The optional backup test also needs the container ID/name in `TEST_POSTGRES_CONTAINER` so it can run that container's `pg_dump` and `pg_restore` clients. Tests never target the user's live SQLite volume.

```sh
docker run --detach --rm --name scratchpad-postgres-test \
  --publish 127.0.0.1:55432:5432 \
  --env POSTGRES_PASSWORD=scratchpad-test-only \
  --env POSTGRES_DB=scratchpad_test postgres:18.6-alpine
```

After `pg_isready` succeeds:

```sh
TEST_POSTGRES_URL=postgresql://postgres:scratchpad-test-only@127.0.0.1:55432/scratchpad_test \
TEST_POSTGRES_CONTAINER=scratchpad-postgres-test npm run test -w @scratchpad/web
```

This covers native full-text search, restart-persistent capture/auth/retry state, concurrent retries and stale edits, failed transaction rollback, refused unrelated/incomplete schemas, complete SQLite → PostgreSQL → SQLite knowledge portability and full `pg_dump`/`pg_restore` recovery including provider configuration and queued jobs.

Build the production application and run the actual browser/stdio MCP workflow against another empty disposable database:

```sh
npm run build -w @scratchpad/web
docker exec scratchpad-postgres-test createdb -U postgres scratchpad_browser_test
SCRATCHPAD_E2E_DATABASE_URL=postgresql://postgres:scratchpad-test-only@127.0.0.1:55432/scratchpad_browser_test \
  npx playwright test tests/workflow.spec.ts
```

That workflow verifies owner passkey enrollment, capture/retrieval/curation, SSH enrollment, real Go stdio MCP and restart persistence. It also exercises the five optional AI tools against a clearly synthetic OpenAI-compatible provider with default checkout scope, explicit cross-project consent, source citations and private document generation. PostgreSQL mode uses process restart; SQLite Docker mode additionally exercises volume snapshot restore.

When all users of the disposable container have finished:

```sh
docker rm --force scratchpad-postgres-test
```

GitHub Actions supplies its own disposable PostgreSQL service and is the canonical runner. Local verification does not establish deployment or release completion.

## Native vector startup permissions

The released native vector startup creates its derived table and compatibility
index on every opt-in initialization using `IF NOT EXISTS`. The application role
needs CREATE in its dedicated schema and ownership of the derived vector table.
It does not need superuser, database creation or role creation. Existing
operational tables can retain a separate migration owner with runtime DML grants.
After a restore without ownership, reapply derived table ownership and schema
permissions before starting with `SCRATCHPAD_PGVECTOR=true`.

The owner instance enabled this mode on 2 October 2026. Native 2560-dimensional
cosine ranking, compatibility exclusion and transaction rollback passed against
the released Store; existing entities were preserved. Restart and backup copy
passed. Enabling native storage does not enable AI or create embeddings.

The owner subsequently enabled local Qwen AI for the two Scratchpad projects.
Initial embedding and analysis jobs completed, semantic search returned indexed
source records, and original captures and metadata remained unchanged. Other
project permissions and new-project defaults remain separate from global enablement.
