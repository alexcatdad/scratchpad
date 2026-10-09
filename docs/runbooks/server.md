# Server development and operation

The application server uses Node 24, SQLite, Drizzle, SimpleWebAuthn, and the system `ssh-keygen` verifier. Run commands from the repository root unless stated otherwise.

1. Install the pinned workspace dependencies with `npm ci`.
2. Set `SCRATCHPAD_DATABASE_PATH` to persistent storage and `SCRATCHPAD_PUBLIC_URL` to the browser origin. HTTP is allowed only on localhost; remote deployments require HTTPS.
3. Run the documented root development command. Generate an initial owner token using the administrative setup command, open the dashboard, and enroll a passkey. Setup tokens expire after 15 minutes and are replaced when the administrator generates another.
4. Run the workspace typecheck, lint, and server tests before committing server changes. Tests use isolated temporary databases; never point tests at a live owner's database.
5. To recover browser access, use the separate recovery command. Recovery preserves data, registers a replacement passkey, and invalidates existing sessions. It does not reopen initial setup.
6. Stop the application before an operational SQLite file copy, or use SQLite's online backup API. Native export includes knowledge and audit history but deliberately excludes credentials, challenges, sessions, and setup tokens.

## Protocol decisions

MCP signs the canonical version 2 JSON proof described in [the API contract](../api.md#accepted-clarification-recipient-bound-ssh-proof-2026-10-09), without a newline, using OpenSSH SSHSIG namespace `scratchpad-auth-v2`. The proof binds the locally trusted recipient origin, login/enrollment purpose, challenge ID, nonce, normalized public key and exact returned expiry string. Upgrade server and signing clients together; legacy nonce-only signatures are rejected without fallback. Challenges last two minutes; both browser and MCP sessions last at most 24 hours. Tokens are random and stored only as hashes on the server. Every request checks credential revocation.

Browser writes require the configured Origin and an HttpOnly, SameSite=Strict cookie. HTTPS instances additionally set Secure. MCP bearer credentials cannot enroll or revoke other credentials.

Capture idempotency is scoped to the authenticated credential. Comparison includes the resolved project and normalized record payload, excluding rediscovered Git context so a mirror changing worktree dirtiness cannot break retries. The first capture retains its original Git context. Different idempotency keys deliberately create distinct records.

Mutable metadata, project settings, and relationship decisions require `expectedVersion` or `If-Match`; stale changes fail with `CONFLICT`. Raw captures never change. Search uses FTS5 with literal token matching and stable cursor pagination.

Native imports are transactional and reject conflicting stable IDs. Legacy JSONL import preserves each source object, labels historical authority as unverified, and reports per-line diagnostics. Native exports exclude local operational secrets and authentication data.

## Faithful history import and context

JSONL import accepts `{format: "jsonl", projectId, jsonl, source: {filename}}` (or `sourceName`). String and array-valued decisions, `why`/`reason`, implications, date-only history, and each supported typed record are mapped without modifying the source. Each record preserves the exact input line and the entire parsed source object. Safe historical IDs are retained; unsafe or colliding IDs use deterministic fallback IDs with diagnostics and the original ID in provenance. Repeating an import, including after native export/restore, does not duplicate records; conflicting reuse of a historical ID is reported and never overwrites evidence.

Missing historical authority is represented as `null` and accompanied by `UNVERIFIED_LEGACY_PROVENANCE`; imported source claims are not authenticated human decisions. Date-only values retain `datePrecision: "day"`; their midnight representation is not a claimed exact event time. Unstructured supersession prose is preserved and flagged for review. Explicit source references become suggested relationships, not silently accepted replacements. Sparse valid JSON objects remain available with incomplete-field warnings.

Native imports accept up to 64 MiB HTTP bodies; legacy JSONL text is limited to 32 MiB. Other API request bodies remain limited to 8 MiB. The larger restore limit allows the initial reference datasets, including immutable raw source copies and audit snapshots, to round-trip intact. For larger installations, use full SQLite backup/restore rather than assuming a browser export is an unlimited archive mechanism.

Context returns existing `state`, `recentDecisions`, `constraints`, `openFindings`, and `failures` arrays alongside `currentState`, `stateHistory`, `applicableRecords`, `partiallySuperseded`, `historicalRecords`, and `requiresReview`. Every contextual record has `applicability`, `replacedBy`, and historical-status annotations. Current state is the latest non-imported applicable state by event time. Imported states remain visible with unverified provenance, rather than becoming current policy automatically. Accepted full replacements remove earlier records from current constraints; partial replacements retain and annotate the residual constraint. Accepting a suggested replacement rechecks cycles against accepted relationships.

Project settings now include `enabledRecordTypes`, defaulting to all initial types for older databases. Disabled types prevent new captures, not historical restore. PATCH `/projects/:id` supports name and normal/external kind with `expectedVersion`; changing to external disables mirroring and cross-project analysis until explicitly reenabled. Capture retries recompute mirror permission from current settings.

## Full operational backup and restore

Native exports preserve knowledge, project configuration, provenance, and audit history. Full SQLite backups additionally preserve owner credentials, sessions, challenges, retry identities, and all operational state. Treat these backup files as sensitive.

Create a consistent backup while the service is running:

```sh
docker compose exec scratchpad npm run admin -- backup /data/backup-2026-09-29.sqlite
```

The destination must be an absolute new path distinct from the live database. The command uses SQLite's online backup API, refuses existing destinations, waits until the snapshot completes, and creates mode-0600 output. It does not print credentials or session values.

Restore into a new volume rather than overwriting the running database:

1. Stop Scratchpad. Retain the original data volume intact as a rollback path.
2. Create an empty replacement volume and copy the snapshot into it as `scratchpad.sqlite`, preserving restrictive permissions and assigning ownership to the container's runtime user (UID/GID 1000 in the supplied image). Do not copy live `-wal` or `-shm` files into the new volume: the online backup is a self-contained database.
3. Point the Compose service at the replacement volume, keeping `SCRATCHPAD_PUBLIC_URL` unchanged, and start the same application version.
4. Verify `/ready`, sign in, retrieve a known record and its history, then verify MCP authentication and a safe idempotent retry. Existing unexpired sessions are valid because their hashes and credential state were backed up. Previously revoked credentials remain revoked as of the snapshot.
5. Keep the original stopped volume until the restored instance is verified. Restore intentionally returns all state to the snapshot time, so changes and revocations made afterward are not present.

The automated isolated backup test verifies session validity and retry identity after restore. The Docker acceptance workflow adds service-level enrollment, backup, fresh-volume restore, and retrieval verification.
