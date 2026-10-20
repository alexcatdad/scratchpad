# Go backend startup, upgrades and recovery

Go is the default source backend and owns the public API, authentication,
persistence, workers and migrations. Published releases and installed deployments
have separate acceptance evidence. [Historical implementation-slice evidence](../archive/p0-baseline/go-backend-migrations.md)
is preserved; current domain contracts are in [the API](../api.md).

## Supported data and startup

`scratchpad-server serve` and `migrate` finish ordered transactional migrations
before serving or starting workers. Historical schema-1 SQLite/PostgreSQL
installations are recognized. Migration 2 adds entity ordering; migration 3 adds
indexed, trigger-maintained context headers. Neither rewrites raw captures,
identifiers, provenance, retries or credential identity. Optional passage RAG
projections have separate prerequisites and catalog validation.

SQLite inspection/DDL uses `BEGIN IMMEDIATE`. PostgreSQL uses transaction advisory
lock `1397772880`, shared with historical migration coordination. Each migration
and its marker commit together; competing startups validate committed state after
acquiring the lock. Startup validates supported contiguous markers, required
columns, keys, indexes and projection definitions. Malformed/newer installations
fail with fixed safe `DATABASE_NOT_READY` diagnostics, without automatic repair.
Never edit markers or drop objects to make a binary accept unsupported data.

SQLite requires private directories/files, normal ownership, regular single-link
files, no symlinks or unsafe sidecars, and no macOS ACL. Existing unsafe storage
is refused rather than chmodded automatically. File databases use WAL and at most
four connections after migration; in-memory/startup retain one. PostgreSQL uses a
dedicated application database and bounded pool.

For PostgreSQL installations whose application role does not own tables, run
`migrate` with the scoped owning/migration role first, then serve with the ordinary
application role. Do not grant DDL rights to production merely to bypass a failed
upgrade. Startup classes/SQLSTATE are safe diagnostics; connection strings and
credentials must remain private. See [project-owned deployment](homelab-deploy.md).

## Preserve authentication and installation identity

Keep the exact `SCRATCHPAD_PUBLIC_URL`, database and retained installation key.
Passkeys depend on the stable RP origin. SSH proof v2 binds its recipient and
purpose; machine private keys remain on clients. Hashed sessions, challenges,
credential provenance, blocks and GitHub freshness are persistent. Recovery
preserves knowledge and independent credentials while invalidating old sessions.

Reusable provider secrets require the same protected file at
`SCRATCHPAD_ENCRYPTION_KEY_FILE`. Schema migration and legacy plaintext-secret
conversion are separate operations. Missing/wrong keys block secret-dependent
providers without plaintext fallback, while core memory remains usable. Never
generate a replacement key during adoption. See [key retention and recovery](go-secret-configuration.md).

## Prepare an existing installation

1. Record the exact current image/artifact, origin, database engine, schema,
   credential references, installation-key reference and volume identity without
   printing secret values. Review the selected new image digest.
2. Take a full operational backup and retain the original installation key
   independently. Portable knowledge export excludes authentication state and is
   not an operational backup.
3. Restore into an isolated fresh database/volume. Verify owner authentication,
   revoked-key denial, immutable record details, search/context, stable capture
   replay, optional provider settings and restart behavior with the same origin.
4. Migrate that disposable restoration with the selected Go artifact. Verify
   readiness and actual browser/MCP behavior, not readiness alone. Test the matching
   rollback procedure before accepting a production cutover.
5. Review maintenance/write-admission limits, backup freshness and the exact live
   commands with the owner. Code merge and image publication do not authorize
   production migration, credential replacement or live runtime switch.

## SQLite operational backup

The `backup <absolute-destination>` command opens an existing supported source
read-only without applying migration. It creates a consistent self-contained
snapshot including committed WAL state, credentials, sessions, retries, settings,
knowledge and indexes, with private permissions and no overwrite. Its parent
must satisfy the private-storage policy. Cancellation removes unfinished staging
files. Do not copy a live main database file as a substitute for online backup.

```sh
npm run admin -- backup /absolute/private-backups/pre-go.sqlite
# In the source container, the same wrapper invokes the installed Go binary:
docker compose exec scratchpad npm run admin -- backup /data/pre-go.sqlite
```

Use the same database configuration as the server. The source wrapper uses Go;
historical released artifacts retain their released command behavior. Setup and
recovery print short-lived operator capabilities, so keep their output private.

For restoration, stop writers and use a fresh volume/file. Copy only the completed
snapshot, preserve private ownership/permissions and retain the original volume.
Do not copy live WAL/SHM files into the fresh destination. Start the selected
artifact with the same origin and key, then verify authenticated behavior.

## PostgreSQL operational backup

Use native `pg_dump --format=custom` and `pg_restore` against a dedicated
application database. Keep connection credentials in an owner-controlled file or
existing scoped execution environment, outside arguments/logs. The Go `backup`
command returns `BACKUP_EXTERNAL` for PostgreSQL.

Restore into a new empty disposable database using the same major-version tool
family as the source. Reconcile scoped roles, extensions and ownership deliberately;
never restore over a live database. Run the selected artifact's migration as the
migration role. Verify authentication, retries, history, lexical retrieval,
optional pgvector and retained-key settings through public interfaces.
[Deployment procedures](homelab-deploy.md) include the PostgreSQL-only migration
Job; it must never be used to migrate an ephemeral SQLite file.

## Rollback boundaries

The historical TypeScript executable accepts schema 1 only and intentionally
refuses Go schemas 2/3. Returning to it requires the verified pre-upgrade schema-1
snapshot, retained old artifact, original origin and installation key. Preserve
its previous command/args as well as its image. No down migration exists.

Stop writers, restore into a fresh target and select the matching old runtime.
Never run old and new runtimes against the upgraded database concurrently.
Restoring a pre-upgrade snapshot discards subsequent captures, curation, settings
and revocations; plan that consequence before cutover. Image-only rollback is
sufficient only after proving the previous Go image accepts the current schema.

## Disposable verification

```sh
go -C backend vet ./...
npm run test:backend
npx playwright test --project=go-native
```

Supply `TEST_POSTGRES_URL` only for a disposable administrator database; fixtures
create/drop only their own random databases. PostgreSQL cases skip when absent.
Local PostgreSQL 17 evidence is separate from canonical PostgreSQL 18 acceptance.
The CI shard runner preserves all tests and adds pgvector and actual-image
browser/backup/restore/rollback scenarios. Select an explicit test Docker context,
never an implicit current context. See [container checks](go-container.md).

Existing tests cover fresh/repeated startup, supported upgrades, competing
migrations, failed-DDL rollback/retry, interrupted lock waits, malformed schemas,
restored sessions, passkeys and SSH proof, idempotency/audit preservation, native
portability and exact source-number identity. Full acceptance also requires
frontend/SDK/MCP, container, optional AI/RAG and macOS checks at the reviewed head.
[P0 evidence](p0-consolidation.md) separates these gates from live acceptance.
