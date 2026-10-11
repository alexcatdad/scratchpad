# Go backend startup and migrations

This is an incremental implementation of issue [#36](https://github.com/alexcatdad/scratchpad/issues/36). The existing TypeScript application remains the supported runtime. Do not point this development executable at an installation in use, or change container/startup commands until browser, authentication, MCP, write, import/export and optional-provider parity is verified.

## Implemented boundary

The separate `backend` Go module supplies `scratchpad-server migrate` and `scratchpad-server serve`. Both finish ordered transactional migrations before serving requests. The server exposes the existing `GET /health`, `GET /ready` and authenticated `GET /api/v1/records/{id}` shapes. Record details include immutable raw captures, metadata, revisions, evidence, relationships, mirror state and related audit history. These reads accept restored bearer or browser sessions, enforce session transport, expiration and revocation, and check cached GitHub authorization and local key blocking. Enrollment, recovery, GitHub synchronization, capture/configuration writes, other retrieval routes and workers are not implemented in this executable yet.

`POST /api/v1/auth/mcp/challenge` and `POST /api/v1/auth/mcp/verify` implement recipient-bound SSH proof v2 with the existing `scratchpad-auth-v2` namespace. Configure `SCRATCHPAD_PUBLIC_URL` as the externally trusted HTTPS origin, or a loopback HTTP origin for development; it must match the MCP's instance URL. Configure the bind address separately with `SCRATCHPAD_LISTEN_ADDR` (development default `127.0.0.1:3000`). SSH signatures are verified with local `ssh-keygen` outside database transactions. The server accepts public keys and signatures only; private keys remain with the MCP. Signature verification is bounded to five seconds, two anonymous verifications per client and eight globally, with 120 authentication requests per minute per client and at most 4096 client counters. Forwarded client headers are not trusted by this development executable.

Challenges last two minutes, are persisted across restart, and are consumed atomically with session creation. Sessions store only hashes of random bearer tokens and expire after 24 hours. Credential eligibility, GitHub binding generation, cached authorization and local blocking are rechecked when consuming the proof. A failed signature does not consume the challenge. Current complete GitHub snapshots can admit a published key after proof and persist its GitHub provenance; this executable does not refresh those snapshots yet. Cached GitHub SSH authorization fails after 24 hours or on key removal/blocking, while independent local credentials remain independent.

SQLite uses the existing schema-1 FTS5 layout; PostgreSQL uses the existing JSONB and generated lexical-search layout. Migration 2 adds `entities_created(kind, created_at, id)` for deterministic entity ordering. It does not rewrite entity JSON, identifiers, retries, identities, lexical data or optional vector tables. Existing databases must have contiguous supported markers, valid timestamps, required columns, primary keys and indexes. Malformed or newer schemas fail with a fixed safe `DATABASE_NOT_READY` message, without repair or provider-secret output.

SQLite migration inspection and DDL run under `BEGIN IMMEDIATE`. PostgreSQL uses transaction advisory lock `1397772880`, matching the current backend. The migration and its marker commit together. Another startup inspects the committed schema after acquiring the lock. A failed migration rolls back; a startup interrupted while waiting for a lock can be retried. SQLite WAL activation retries transient lock contention for at most five seconds. Startup has a 30-second deadline.

SQLite retains owner-only directories/files, regular files with one hard link, symlink refusal, sidecar checks and macOS ACL refusal. Existing files are never chmodded or opened/closed for permission inspection. An optional sidecar disappearing during inspection is allowed; an unsafe sidecar that remains present is refused. PostgreSQL connection pools are bounded to ten connections.

## Disposable verification

Use the pinned Go toolchain and module dependencies:

```sh
mkdir -p .output
(cd backend && go build -o ../.output/scratchpad-server ./cmd/scratchpad-server)
npm run test:backend
```

Without `TEST_POSTGRES_URL`, PostgreSQL cases explicitly skip. To run them, supply a disposable PostgreSQL administrator connection with database-creation permission. Tests create random databases, then drop only their own databases. Do not use an installation containing owner history. The current Docker context may target another machine; select a local test context explicitly before starting a container fixture.

```sh
TEST_POSTGRES_URL=postgres://scratchpad:scratchpad-test@127.0.0.1:5432/scratchpad_test npm run test:backend
```

Tests execute real race-instrumented server, MCP and integration-runner binaries and exercise CLI startup, HTTP and stdio. Legacy fixture SQL constructs historical input; assertions do not query internal tables. Coverage includes fresh/repeated startup, supported schema-1 upgrade, competing startups, failed-DDL rollback/retry, unsupported and malformed schemas, unsafe SQLite paths, interrupted lock-wait startup, restored sessions and unchanged record details across restart. SSH tests use real local signing, prove successful authenticated record retrieval through stdio on both engines, reject transplanted and reused signatures, verify concurrent single-use proof consumption and preserve challenges across restart. GitHub fixtures cover freshness, removal, future timestamps, binding changes, local blocking and automatic admission from the published snapshot. A closed SQLite schema-1 snapshot is also restored into a separate file before authenticated retrieval. This closed-fixture copy is not a live SQLite backup procedure. PostgreSQL dump/restore, interruption during actual DDL execution, browser enrollment and full stdio capture/retrieval parity remain required before cutover.

GitHub Actions adds a separate Go-backend lane with PostgreSQL 18.6, formatting, vet, race tests, build and vulnerability checking. Local PostgreSQL 17 verification does not establish PostgreSQL 18.6 CI acceptance.

## Backup and rollback before any future cutover

Stop application writers and workers before replacing a runtime. Retain the exact old executable/image and configuration. For the current SQLite application, use its existing online administrative backup command; do not copy only the main database while WAL writers are active. For PostgreSQL, use the existing administrative backup procedure or an operator-controlled `pg_dump`, and restore into a separate disposable database to verify the backup before upgrading.

Keep backups private: they include hashed authentication/session state and may still contain legacy plaintext provider secrets. Schema migrations do not convert reusable secret values. Encryption and independently reported legacy conversion remain later issue-36 slices; missing deployment encryption keys do not enter this migration path.

The current TypeScript executable accepts schema 1 only. Once this Go executable commits schema 2, the TypeScript executable intentionally refuses that database. There is no automatic down migration. To roll back, stop the new runtime and restore the verified pre-upgrade schema-1 backup with the retained old runtime. Never delete migration markers to make an old runtime accept upgraded data. Restoring a pre-upgrade backup discards subsequent writes; plan the maintenance window accordingly. Do not run old and new runtimes against the same upgraded database.

On migration failure, preserve the original database and backup, report the safe startup error, and investigate using operator tools in a controlled environment. Do not modify schema markers or auto-repair incomplete objects. Schema changes, release, deployment and live-user acceptance require separate evidence.
