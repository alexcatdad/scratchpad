# Full operational SQLite to PostgreSQL migration

This is an administrator maintenance procedure for the published v0.3.0 schema,
not a transparent live migration feature. Native knowledge export omits credentials,
sessions, retries, provider secrets, jobs and embeddings; it is unsuitable when
preserving the owner's whole installation.

1. Provision a dedicated empty PostgreSQL database and migration identity. Use a
   restricted runtime role for the application, and preserve the public origin.
   Verify connectivity from the actual application container.
2. Take an online SQLite administrator snapshot and protect an off-machine copy.
   Rehearse the operational transfer in a separate, disposable application database.
3. Initialize the target with the released `Store`, without starting the application
   or background AI worker. Copy `schema_migrations`, every `entities` kind,
   `identities`, `retries`, and the three stored `record_search` columns in one
   transaction. Refuse nonempty targets. PostgreSQL rebuilds its generated search
   document and index; SQLite FTS shadow tables are not copied.
4. Verify all logical rows, versions, timestamps and retry strings. Compare JSON
   entity content recursively with sorted object keys; JSONB formatting differs
   from SQLite JSON text. Verify row counts and canonical digests before target
   application activity legitimately changes session/audit state.
5. Stop the source, take a final consistent SQLite snapshot with a one-off
   administrator container, and import it into the empty authoritative target.
   Keep that snapshot and the SQLite volume for rollback. Only one writable
   instance may remain active.
6. Select the protected PostgreSQL URL in deployment configuration and preserve
   the HTTPS origin. Start with the same released image. Verify readiness, the
   existing browser session/passkey, enrolled MCP key, capture/retrieval/search,
   a saved retry and persistence after restart.
7. Switch daily operational backups to protected custom-format `pg_dump` files.
   Test `pg_restore` into a separate empty database and compare every logical
   table. Keep off-machine copies and documented retention.
8. Record the database roles, secret names and rebuild/rollback procedure in the
   private infrastructure repository. Commit no URLs, passwords, owner snapshots
   or test artifacts. Disable the migration login after successful acceptance.

For rollback after PostgreSQL writes, take a fresh PostgreSQL backup and perform
an explicit reverse operational conversion while the application is stopped.
Do not reactivate an old SQLite snapshot and silently discard new captures.

PostgreSQL 16 supports the application's generated full-text document and GIN
index. Use the existing maintained server version and deployment checks; the
release's PostgreSQL 18 test results do not themselves prove PostgreSQL 16
acceptance. See [PostgreSQL operations](postgres.md) for the persistence contract.

The owner instance completed this conversion on 2 October 2026 with the released
v0.3.0 image and existing PostgreSQL16.15 server. All five logical table digests
matched before activation and after an isolated native dump restore. Existing
browser session, installed MCP capture/retry/retrieval/full-text search and app
restart passed. Frozen SQLite state remains available for explicit rollback.

For WAL-format snapshots, bind the source read-only and copy it into writable
disposable container storage before opening it read-only, so SQLite can create
sidecars. Native vector mode remains disabled with the restricted DML runtime;
the current release performs vector DDL on startup. The vector extension may be
installed independently. AI consent and enabled settings remain unchanged.
