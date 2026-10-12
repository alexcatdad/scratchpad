# Go backend startup and migrations

This is an incremental implementation of issue [#36](https://github.com/alexcatdad/scratchpad/issues/36). The existing TypeScript application remains the supported runtime. Do not point this development executable at an installation in use, or change container/startup commands until browser, authentication, MCP, write, import/export and optional-provider parity is verified.

## Implemented boundary

The separate `backend` Go module supplies `scratchpad-server migrate` and `scratchpad-server serve`. Both finish ordered transactional migrations before serving requests. The server exposes the existing `GET /health`, `GET /ready` and authenticated `GET /api/v1/records/{id}` shapes. Record details include immutable raw captures, metadata, revisions, evidence, relationships, mirror state and related audit history. These reads accept restored bearer or browser sessions, enforce session transport, expiration and revocation, and check cached GitHub authorization and local key blocking. Passkey enrollment, discoverable login, owner recovery, authentication status, public credential metadata, session logout, GitHub OAuth, key synchronization, blocking and unlinking are implemented. Manual SSH enrollment and credential revocation are also implemented; Project resolution, listing, settings and versioned updates are implemented; Immutable capture and durable retry identities are also implemented; Lexical listing/search is implemented; Deterministic project context is implemented; Metadata/revisions, evidence, mirror reports and relationship curation are implemented; Owner profile/public-GitHub presentation is implemented; Native knowledge export/import, legacy JSONL import and schema-preserving online SQLite backup are implemented; Encrypted AI configuration/provider verification and independently tracked legacy conversion are implemented; global configuration and MCP secret-reference guidance are also implemented; remaining workers are later slices.

`POST /api/v1/auth/mcp/challenge` and `POST /api/v1/auth/mcp/verify` implement recipient-bound SSH proof v2 with the existing `scratchpad-auth-v2` namespace. Configure `SCRATCHPAD_PUBLIC_URL` as the externally trusted HTTPS origin, or a loopback HTTP origin for development; it must match the MCP's instance URL. Configure the bind address separately with `SCRATCHPAD_LISTEN_ADDR` (development default `127.0.0.1:3000`). SSH signatures are verified with local `ssh-keygen` outside database transactions. The server accepts public keys and signatures only; private keys remain with the MCP. Signature verification is bounded to five seconds, two anonymous verifications per client and eight globally, with 120 authentication requests per minute per client and at most 4096 client counters. Forwarded client headers are not trusted by this development executable.

Challenges last two minutes, are persisted across restart, and are consumed atomically with session creation. Sessions store only hashes of random bearer tokens and expire after 24 hours. Credential eligibility, GitHub binding generation, cached authorization and local blocking are rechecked when consuming the proof. A failed signature does not consume the challenge. Complete GitHub snapshots can admit a published key after proof and persist its GitHub provenance. Cached GitHub SSH authorization fails after 24 hours or on key removal/blocking, while independent local credentials remain independent.

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

Tests execute real race-instrumented server, MCP and integration-runner binaries and exercise CLI startup, HTTP and stdio. The backend test command disables Go result caching with `-count=1`: these external-process tests must rebuild and exercise the current executable after backend-only changes. Legacy fixture SQL constructs historical input; assertions do not query internal tables. Coverage includes fresh/repeated startup, supported schema-1 upgrade, competing startups, failed-DDL rollback/retry, unsupported and malformed schemas, unsafe SQLite paths, interrupted lock-wait startup, restored sessions and unchanged record details across restart. SSH tests use real local signing, prove successful authenticated record retrieval through stdio on both engines, reject transplanted and reused signatures, verify concurrent single-use proof consumption and preserve challenges across restart. GitHub fixtures cover freshness, removal, future timestamps, binding changes, local blocking and automatic admission from the published snapshot. A closed SQLite schema-1 snapshot is also restored into a separate file before authenticated retrieval. This closed-fixture copy is not a live SQLite backup procedure. Native PostgreSQL dump/restore is verified separately below. Pre-upgrade PostgreSQL rollback is verified separately below. Interruption during actual DDL execution, full dashboard workflows and full delivery acceptance remain required before cutover.

GitHub Actions adds a separate Go-backend lane with PostgreSQL 18.6, formatting, vet, race tests, build and vulnerability checking. Local PostgreSQL 17 verification does not establish PostgreSQL 18.6 CI acceptance.

## Passkey setup, login and recovery

Use a hostname in `SCRATCHPAD_PUBLIC_URL`: `http://localhost:3000` for local development, or the installation's HTTPS origin. Numeric IP addresses cannot be WebAuthn relying party IDs. An IP-only public origin still supports non-passkey routes; passkey routes return a fixed `CONFIG_INVALID` response. The origin must remain stable to retain access to existing passkeys.

Run `scratchpad-server setup` for an uninitialized owner, or `scratchpad-server recover` for an existing owner, with the same database configuration as the server. The command prints an operator-held token intentionally; handle its terminal output privately. Only its SHA-256 digest is stored. It expires after 15 minutes, and issuing another token invalidates the previous token and any enrollment authorized by it. Setup refuses an initialized installation; recovery refuses an uninitialized installation. Authorization is audited.

The existing `POST /api/v1/auth/register/options` and `/verify` contract accepts the setup/recovery token, or an authenticated browser session for another device. Enrollment requires a discoverable credential and user verification, excludes active passkeys, binds the challenge to its authorization context, and rechecks authorization inside the consuming transaction. Duplicate credential IDs, including revoked credentials, cannot be enrolled again. `POST /api/v1/auth/login/options` and `/verify` use discoverable login with user verification, origin/RP ID validation and counter checks. Failed proofs do not consume a valid challenge. Challenges last two minutes, survive restart, are consumed once and are retired after expiry when issuing another challenge.

Browser session responses contain authentication status and expiry; the token is only in an HttpOnly, SameSite=Strict cookie with Path=/ and a 24-hour lifetime. HTTPS adds Secure. Tokens are persisted only as hashes. Recovery consumes its one-use token, enrolls the new passkey, audits the recovery and revokes every existing session in the same transaction. Existing credentials remain available for fresh login. Logout revokes only the caller's session; browser logout requires the configured Origin, while bearer logout retains the agent API contract. Public credential listing exposes metadata only.

Existing TypeScript owner handles, COSE public keys, credential IDs and counters are retained. TypeScript rows did not store backup flags; the first cryptographically verified login initializes these flags without changing credential provenance. Later logins enforce flag consistency. Cryptographic response verification runs outside the write transaction; credential version/revocation and challenge consumption are checked again before issuing a session.

Run the native Chromium workflows against isolated SQLite and optional disposable PostgreSQL databases:

```sh
npx playwright test tests/go-passkeys.spec.ts
TEST_POSTGRES_URL=postgres://scratchpad:scratchpad-test@127.0.0.1:5432/scratchpad_test npx playwright test tests/go-passkeys.spec.ts
```

The browser uses a minimal test document and native WebAuthn with virtual authenticators; every API request reaches the real Go process. This verifies the browser authentication seam, not full React dashboard parity. Tests cover token rotation, first/additional enrollment, missing enrollment authorization, replay, discoverable login, logout, recovery, retained credentials and challenge/session persistence through restart. Separate HTTP tests use independently signed assertions against historical TypeScript-format fixtures and cover UV, origin, RP ID, challenge, counter, session transport and concurrent consumption. The Go-backend CI lane runs these browser workflows on SQLite and PostgreSQL 18.6.

The PostgreSQL browser workflow also dumps the enrolled installation with native
`pg_dump`, restores it into a new empty database, and restarts Go at the same
public origin. The real Chromium authenticator signs a fresh successful login
after restoration. Administrator recovery then enrolls a new device, invalidates
old sessions and preserves the original passkey for subsequent login. Restarted
challenge consumption and authenticated additional enrollment continue on the
restored database. Client private passkey material remains in the synthetic
browser authenticator, outside the database archive.

The fixture deletes both owned databases and stops its server. CI uses archive
clients from its owned PostgreSQL service; native local clients are required for
configured PostgreSQL browser tests. This covers browser authentication after
restoration, separately from dashboard rendering, real hardware authenticators,
published-container rollback and deployed acceptance.

## GitHub OAuth and published SSH keys

Configure both `SCRATCHPAD_GITHUB_CLIENT_ID` and `SCRATCHPAD_GITHUB_CLIENT_SECRET` from the installation's administrator-managed OAuth app. Partial or blank-valued configuration fails startup with `CONFIG_INVALID`. Register the exact `${SCRATCHPAD_PUBLIC_URL}/api/v1/auth/github/callback` URL with GitHub; only the configured origin is trusted. Public profile information never authorizes an owner. No new database migration is required: GitHub state retains the existing entity layout.

The existing `POST /api/v1/auth/github/options` accepts `setup`, `login`, `link`, `replace` and `recover`. Setup/recovery use the operator-held capability; linking uses an authenticated browser; replacement requires authentication within five minutes. Options persist a ten-minute, browser-bound, one-use state and S256 PKCE verifier. The state cookie is HttpOnly, SameSite=Lax and scoped to `/api/v1/auth/github`; HTTPS adds Secure. The callback atomically consumes state and erases its verifier before exchanging the code. GitHub OAuth access tokens stay in memory and are never persisted. The stable positive numeric account ID authorizes login, including after a username change. Authorization and binding generation are rechecked before issuing the existing browser session cookie. Successful callbacks redirect to `/`; safe failures redirect to `/?authError=<code>` and clear state. Admission limits return JSON without consuming or clearing state.

`GET /api/v1/auth/github` exposes safe binding/cache metadata to authenticated browsers. `POST /api/v1/auth/github/sync` refreshes both authentication and signing keys. `POST /api/v1/auth/github/block` accepts `{publicKey, blocked}`; blocks persist across removal and reappearance and apply to independent SSH keys as well. `DELETE /api/v1/auth/github` requires a fresh independent browser credential. Management mutations require the configured Origin and reject bearer authentication. Replacement/unlinking revoke GitHub-derived credentials and sessions while retaining independent credentials. Operator recovery revokes all existing sessions but retains credentials for fresh login.

Synchronization resolves the stable account ID, follows at most 100 pages per category, validates actual SSH keys with `ssh-keygen`, deduplicates their categories, and confirms username ownership before committing the complete snapshot. Redirects, unsafe or malformed pagination, invalid keys and incomplete fetches fail closed. Each external request has a five-second deadline and a 1 MiB response limit; each complete fetch has a twenty-second deadline. Network requests and key validation occur outside write transactions. Concurrent refreshes apply only the current request and binding generation. Complete removals revoke matching GitHub SSH sessions. Failures record fixed error codes, retain the previous keys and preserve the original successful-sync timestamp. The resulting SSH authorization expires after 24 hours; browser GitHub authorization depends on binding generation rather than SSH-cache freshness.

An advertised `last` page remains a completeness requirement throughout that category's fetch. A missing `next` link before reaching it fails synchronization, including when a later page omits its pagination header. Valid terminal `last` links and combined `next last` relations remain supported. Regression tests verify that incomplete pagination cannot replace keys or advance cache freshness on either database engine.

The Go GitHub client requires `200 OK` from its fixed OAuth and identity/key endpoints. A `206 Partial Content` response is a failed refresh even if its body parses as a valid key array. It cannot replace the complete key set, advance freshness or revoke existing machine sessions. HTTP regression tests verify preserved session access and renewal on SQLite and PostgreSQL.

The worker checks due bindings immediately at startup and every 30 seconds, using persisted `lastAttemptAt` to refresh after five minutes. Previously linked bindings continue public-key refresh if OAuth is subsequently disabled. Shutdown cancels external work and waits for the worker; failure-state persistence is bounded to five seconds. Restart cannot extend complete-cache freshness.

Verify this boundary with real Go processes, synthetic GitHub HTTPS responses and native Chromium redirects:

```sh
npm run test:backend
npx playwright test tests/go-github.spec.ts tests/go-passkeys.spec.ts
```

Supply `TEST_POSTGRES_URL` for the HTTP workflows on disposable PostgreSQL; the GitHub browser redirect test uses isolated SQLite. Fixtures replace only the external GitHub service through a synthetic trusted HTTPS proxy, preserving the fixed production GitHub origins. HTTP coverage includes browser-bound/replayed state, malformed account authority, paginated key categories, partial failure, unsafe pagination, key removal/blocking, stable identity, replacement, independent unlinking, recovery, restart freshness and revocation while OAuth is awaiting GitHub. Chromium verifies the cross-provider redirect, Lax state cookie, Strict session cookie and state-cookie removal. These checks do not establish acceptance against a real administrator-configured GitHub app or a deployed dashboard.

## Local SSH enrollment and credential revocation

An authenticated browser enrolls an independent SSH key through `POST /api/v1/auth/credentials/challenge` and `/verify`. Both require the configured Origin. The persisted two-minute proof uses purpose `ssh_enroll`, binds the public key and initiating credential, and is distinct from agent login. Verification uses the same bounded local `ssh-keygen` boundary as login, outside the write transaction. The consuming transaction rechecks browser authorization, challenge ownership, expiry and replay, then creates the local credential and enrollment audit together. Enrollment returns credential metadata; the agent subsequently proves possession through the existing MCP login routes. GitHub-published or historically managed keys cannot be converted to local credentials. Private keys remain on the signing machine.

`DELETE /api/v1/auth/credentials/{id}` requires an authenticated browser and configured Origin. Credential revocation, associated-session revocation and the audit entry commit atomically; the credential and its provenance remain visible in the public metadata list. GitHub-derived credentials must use GitHub blocking or unlinking instead. The last active passkey cannot be revoked unless another active passkey or a configured linked GitHub account remains. This guard runs inside the serialized transaction, including concurrent revocations. Enrollment and revocation use the shared authentication admission limits.

HTTP tests use real signatures on SQLite and disposable PostgreSQL, including real stdio MCP retrieval with the newly enrolled key, purpose/replay rejection, live authorization checks, retained metadata, GitHub provenance protection and concurrent last-passkey preservation. Run them with `npm run test:backend` and optional `TEST_POSTGRES_URL` as described above.

## Project identity and settings

The Go API implements `POST /api/v1/projects/resolve` for Git context and `/resolve-explicit` for a name/kind or existing project ID. It resolves case-insensitive explicit names under the same serialized transaction as creation. Ambiguous names require explicit project selection. Git resolution normalizes the remote without persisting embedded credentials, creates one source identity and audit with the project, and reuses the unique identity across concurrent agents. Normalization covers SCP and URL remotes, scheme/host case, default HTTP ports, IPv6, HTTP IDNA names, opaque SSH Unicode names, dot segments and legacy IPv4-number forms. The pinned Go networking module supplies maintained IDNA processing.

`GET /api/v1/projects` includes sources. `GET /api/v1/projects/{id}` and `/settings` expose normalized project settings. PATCH on either route requires `expectedVersion` or `If-Match`; stale requests return `CONFLICT` with `details.currentVersion`. Mutation authorization and the version check occur inside the write transaction. Browser mutations require the configured Origin; bearer agent mutations retain their existing transport. Updates preserve stable project ID, source identities and slug. Conversion to an external project disables mirroring and cross-project analysis without changing AI consent or the mirror record-type selection.

Normal projects inherit stored administrator defaults, while external projects use independent conservative defaults. Settings normalize supported record-type arrays and required boolean fields, default legacy missing `enabledRecordTypes` to all eight types, and omit unknown settings fields from public output and audit snapshots. Creation/update audits retain the authenticated actor fingerprint and before/after snapshots. Project context and capture/retrieval workflows remain subsequent parity work.

Run `npm run test:backend`, supplying `TEST_POSTGRES_URL` for disposable PostgreSQL. HTTP tests cover explicit resolution through restart, canonical source identities, inherited/external settings, browser Origin enforcement, stale versions, invalid record types and concurrent resolution/updates on both engines. Historical SQL fixtures construct input only; assertions observe the public HTTP API.

## Immutable captures and retry identities

`POST /api/v1/records` accepts the existing project/record/Git-context contract for all eight record types. The server normalizes titles, confidence defaults and optional declared provenance, validates required payload fields, and derives actor kind/fingerprint from the authenticated session. Declared client/display metadata is retained; supplied identity claims cannot override authenticated provenance. JSON content rendering preserves payload field order, JavaScript integer-key order, nested object order, arrays, null values and string values. Rendering occurs outside the write transaction.

The write transaction rechecks authorization and project settings, enforces enabled record types, and commits the immutable record, initial metadata, lexical index entry and creation audit together. No provider request is part of capture. Browser writes require the configured Origin. Request bodies are bounded to 3 MiB and the operation uses a ten-second deadline. No raw-record update route is introduced.

Optional `Idempotency-Key` values are scoped to the credential ID and persisted with the normalized request and original response in the existing retries table. Comparisons ignore JSON object key order and preserve array order, including restored TypeScript-format retry rows. Reusing a key for a different capture returns `CONFLICT`. Concurrent retries and server restart return the original ID/timestamps and do not add creation audits. Retry identity intentionally excludes Git context, preserving the first capture's provenance. Replays recompute mirror eligibility from current project settings, and remain valid if the record type is subsequently disabled; new captures of that type are refused. Failed validation does not reserve a retry identity.

HTTP tests cover capture/detail, provenance overrides, payload rendering, validation/consent, concurrent retries, restart, historical retry rows, credential scope and current mirror consent on SQLite and disposable PostgreSQL. The real stdio integration runner adds `-phase records`: it exercises all eight capture tools, rereads immutable details, retries each capture and searches the persisted records through the real Go MCP SDK. This phase isolates the records boundary. The original capture/verify phases now also run against Go and verify complete project resumption across restart. Existing integration phases retain their full workflows. Run the complete regression suite with `npm run test:backend` and optional `TEST_POSTGRES_URL` as described above.

## Lexical listing and search

`GET /api/v1/records` and `POST /api/v1/search` implement the existing lexical retrieval shapes with metadata and nullable `nextCursor`. SQLite uses FTS5 and PostgreSQL uses its generated simple-language tsvector. Queries extract at most thirty Unicode letter/number/underscore terms and combine them as literal AND conditions, without executing caller-supplied query operators. POST search retains its query, repeated project/type lists, limit and cursor contract; authenticated browser POST requires the configured Origin.

For nonempty lexical queries, the database joins distinct full-text matches to
immutable record data before Go decodes it. Duplicate SQLite lexical entries do
not duplicate records. Unmatched source bodies are not loaded into the
record selection. Metadata is then selected by candidate record IDs using one
bound JSON-array parameter and each engine's native JSON set function. Unmatched
and orphan metadata is not decoded, while corrupt relevant metadata still fails
safely. This avoids a per-record query loop and parameter-count limits.
A corruption fixture verifies targeted retrieval can succeed
without decoding an unrelated source, while corrupt matching sources still fail
safely. Blank-query record listing, relationship selection, filters, ordering
and cursor behavior remain separate from this optimization; this change does not
claim history-independent cost for the entire read path.

GET filters preserve exact project/type/authority/confidence, repeated project/type lists, tags, Git branch/path, import/client/display provenance, archived state, lifecycle status and relationship endpoints. Only accepted replacement relationships change lifecycle status; rejected links are omitted from relationship filtering. UTC date bounds compare arbitrary fractional-second precision without rounding it to Go nanoseconds or JavaScript milliseconds. Malformed ISO bounds, cursor tuple types and page limits are rejected.

Pages retain descending created-time/identifier ordering, using locale-compatible ordering and the existing base64url `[createdAt,id]` cursor. Limits default to thirty and accept one to one hundred. New opaque entity IDs now use the established prefix plus UUIDv4 format; stored identifiers and retry references remain unchanged. This corrects the earlier development Go generator's base64 token IDs, whose mixed case could disagree with the legacy cursor ordering. The existing pinned UUID and Unicode-text modules are used directly; no module versions were upgraded for this change.

All reads use one consistent database snapshot for records, metadata, relationships and lexical membership. HTTP tests cover new captures, filters, precise time bounds, pagination, malformed input and POST retrieval on SQLite/PostgreSQL. The real stdio records phase proves search can retrieve all eight captures. A controlled late audit-write failure verifies through public listing that record history rolls back and the same retry identity can succeed after fault removal. These are lexical APIs; PostgreSQL RAG remains subsequent work; project-context projection is implemented below.

## Deterministic project context

`GET /api/v1/projects/{id}/context` projects one authenticated, consistent snapshot of project settings, raw captures, metadata and relationships. It preserves the existing current-state/state-history, decision, constraint, finding, failure, question and applicability collections, their limits, full counts and provenance notice. Records remain scoped to the selected project. Sorting compares happened/recorded/created instants with arbitrary fractional precision, then creation time and identifier ordering.

Archival takes precedence over accepted full/partial replacement relationships. Accepted replacements retain the source record ID, relationship ID, type and optional note in `replacedBy`; proposed/rejected links cannot change applicability. Imported knowledge remains unverified, and imported legacy lifecycle claims remain historical or require review rather than becoming current authority. Current-state selection uses the bounded available state collection; state history retains superseded/archived records. The returned relationship slice includes existing statuses for links touching the project, preserving the distinction between recorded links and accepted applicability.

HTTP tests cover current knowledge, every applicability category, replacement provenance, precise state ordering and bounded history on SQLite/PostgreSQL. The original real stdio capture and verify phases exercise all eight capture tools, immutable details, search, decision history, related-record reads and linked resumption context, then repeat across a server restart with stable replay and changed-payload conflict checks. Run these as part of `npm run test:backend`, with `TEST_POSTGRES_URL` for disposable PostgreSQL. This proves the core agent resumption path; full browser-dashboard workflows, remaining configuration and workers, operational restore, encryption and RAG still require parity work before supported-runtime cutover.

## Curation, evidence and relationships

`PATCH /api/v1/records/{id}/metadata` and `POST /api/v1/records/{id}/revisions` amend display title, tags, archival and curated summary only. They require a current metadata version through `expectedVersion` or `If-Match`. Metadata, immutable before/after revision snapshots, review reason/actor and the curation audit commit together. Raw capture title/content/payload/provenance remain unchanged; unsupported patch fields are rejected. Explicit revisions take changes in the existing `changes` object and may retain a review reason.

`POST /api/v1/records/{id}/evidence` retains the existing evidence kinds, reference, optional revision and description with an audit. `POST /api/v1/records/{id}/mirror` records attempts/results only while current project/type mirroring consent permits them. Reports preserve the existing optional-field merge behavior and increment the independent mirror version. Every mutation checks session authorization and browser Origin before processing and again inside the serialized transaction.

`POST /api/v1/relationships` creates explicit/inferred accepted links or suggested links. Suggested relationships cannot change applicability until the versioned `/accept` operation makes their authority explicit; `/reject` preserves recorded authority while rejecting applicability. Both operations preserve audit snapshots. Self-links are refused. Accepted full/partial replacement edges share one cycle graph; dependencies use a separate graph. Cycle checks run inside the write transaction, ignore suggestions/rejections, and exclude the relationship being reconsidered. Concurrent reciprocal writes cannot commit a cycle.

HTTP tests cover amendment immutability, versions, revision/evidence/relationship provenance, mirror consent and accepted/rejected applicability on SQLite/PostgreSQL. A late curation-audit failure verifies rollback of metadata version, revision and audit history through record detail. Concurrent partial replacements verify serialized cycle safety. The original real stdio capture/verify workflows now opt into permitted local mirroring, exercise central mirror reports and verify exactly three local mirror entries after replay/restart. These tests use private synthetic repositories, not the developer checkout. Run them with `npm run test:backend` and optional disposable `TEST_POSTGRES_URL`.

## Owner profile and public GitHub presentation

`GET /api/v1/profile` returns the stored presentation profile or its version-zero owner-name fallback, plus `database.engine` as `sqlite` or `postgresql`. `PATCH /profile` requires a numeric nonnegative `expectedVersion` and a trimmed display name of one to two hundred UTF-16 units. Profile updates and before/after actor audits commit under the serialized write lock. The profile is separate from the owner authentication row.

`POST /profile/github` fetches a validated public username and `DELETE /profile/github` unlinks presentation, both with the same version contract. Public fetching uses a fixed GitHub HTTPS origin, no OAuth credentials, no redirects, a five-second deadline and a 64 KiB response limit. It rejects organizations, mismatched usernames, malformed/oversized responses and safe rate/not-found failures without changing profile state. Network access occurs outside writes; authorization and version are rechecked before saving. A concurrent name update can complete while the public request waits and causes stale fetch rejection.

Presentation includes only validated username, nullable display name/avatar, derived GitHub profile URL and fetched time. Only credential-free HTTPS avatars on `avatars.githubusercontent.com` with the default port are presented; invalid imported avatars are omitted and imported profile URLs/extra fields are not trusted. Public linking never establishes or changes the stable GitHub owner-access binding and cannot authenticate a user.

HTTP tests cover name/version defaults and restart on both engines, public linking/unlinking, authorization separation, bounded external failures, imported URL/field projection, allowed URL normalization and concurrent version rechecks. Synthetic trusted HTTPS fixtures preserve the fixed production origins. Real GitHub-app or deployment acceptance is not established by these tests. Run them within `npm run test:backend`, with optional disposable `TEST_POSTGRES_URL`.

## Native knowledge portability

`POST /api/v1/export` returns the existing version-one Scratchpad archive and attachment filename. It reads all knowledge sections from one database snapshot, preserving captures, metadata, revisions, source identities, relationships, evidence, mirror history, audits, profiles and derived artifacts. Authentication, retries, provider configuration, jobs and embeddings are excluded. Global settings carry only entity metadata and the documented `aiEnabled` and `defaultProjectSettings` policy fields; legacy provider-secret fields never enter a portable archive.

`POST /api/v1/import` currently accepts native `format: "scratchpad"`, version-one archives up to 64 MiB. It validates safe IDs and references, historical versions and UTC timestamps, project/record/metadata/relationship shapes and source identity ownership. Identical entities are skipped; differing existing entities conflict. Restored records regain their lexical index and restored sources regain project resolution identities. Every restored record requires metadata, and accepted replacement/dependency graphs must remain acyclic. Entity writes, indexes, identities and the final actor audit commit in one serialized transaction; late failure leaves no partial knowledge.

Derived artifacts retain source citations, generator metadata and privacy dependencies. Import validates kind-specific content, unique source/project scope and privacy references against shared batched source snapshots. Curated artifacts must match their accepted suggestion, including privacy dependencies. Historical artifacts lacking privacy provenance remain historical; import does not reconstruct it or enable AI processing. These archives may be restored while project AI consent is disabled. The global-settings import section rejects provider configuration fields.

HTTP regression tests exercise literal historical archives, live export/import between SQLite and PostgreSQL in both directions, immutable replay and version preservation, source resolution, lexical retrieval, malformed references, content/identity conflicts, graph cycles, derived/privacy/curated validation and rollback on a late audit failure. They use real processes and synthetic fixtures. Run `npm run test:backend` with disposable `TEST_POSTGRES_URL` to include PostgreSQL. Full operational backup/restore, remaining configuration and workers, secret encryption and PostgreSQL RAG are subsequent issue-36 slices; native knowledge portability does not prove supported-runtime cutover or operational restore.

## Legacy JSONL history

`POST /api/v1/import` also accepts `format: "jsonl"`, an existing `projectId`, `jsonl` text and optional `sourceName` or `source: {filename}`. The default filename is `legacy.jsonl`. Source text follows the existing 32 Mi UTF-16-unit bound inside the 64 MiB request limit. Malformed or non-object lines produce line warnings and skipped results; sparse objects remain importable. Results retain `imported`, `skipped`, `warnings` and per-line `records` status/identity entries.

Safe historical IDs survive. Unsafe IDs and collisions with another project's history receive deterministic fallback IDs; identical anonymous occurrences retain distinct stable identities. Canonical hashing matches the established locale-sorted JSON algorithm, and replay remains idempotent after restart or native export/import. A changed object with an existing identity produces a conflict warning instead of overwriting history. Blank lines do not shift reported source line numbers; CRLF delimiters and a final carriage return preserve the established raw-line behavior.

Each record retains its original object, exact raw line, canonical fingerprint, original ID, source actor/source claims, source date and precision. Arrays of prose, typed payload fields, evidence and unknown source fields survive normalization. Unsupported types, authority/confidence claims and dates do not invent source facts. Strict ISO source dates normalize to UTC milliseconds; import timestamps remain separate. Authenticated importer identity owns the record actor and audits. Import bypasses capture-only enabled-type settings so disabled history is retained, while provenance keeps imported records unverified in project context.

Explicit historical supersession references are resolved after the file's records are known, including mapped IDs and forward references. They create deduplicated suggested replacement relationships with source provenance, not accepted applicability changes. Self-links, missing/foreign references and ambiguous supersession annotations produce warnings. The existing owner-review route remains the acceptance boundary. Records, metadata, lexical indexes, evidence, suggestions and audits share one transaction; a final audit failure rolls everything back.

HTTP tests cover all eight types, raw evidence/provenance, fixed TypeScript hash vectors, duplicate occurrences, unsafe/colliding identities, restart/native replay, sparse/unsupported claims, reviewable links, invalid requests, the source-text limit and late rollback on SQLite/PostgreSQL. Run them within `npm run test:backend`, with disposable `TEST_POSTGRES_URL` for PostgreSQL. Real source-file recovery and deployed acceptance remain separate operational checks.

## Backup and rollback before any future cutover

The development Go binary implements `backup <absolute-destination>` for existing supported SQLite installations. This command bypasses migration/startup and opens the source read-only, so a schema-1 installation remains schema 1. It uses SQLite's online backup API inside a consistent source read transaction, includes committed WAL data and preserves credentials, sessions, retries, settings, indexes and knowledge. A completed snapshot is checked independently as a closed main file, synced and atomically published without replacing an existing destination. Snapshot mode is `0600`, and its parent must meet the existing private-storage profile. Missing/unsafe/unsupported sources, existing target files or journals, and source sidecar aliases are refused. SIGINT/SIGTERM and the bounded backup deadline cancel unfinished work and clean staging files.

For a development binary, an operator can create a pre-upgrade snapshot without starting the Go server:

```sh
go -C backend build -o /private/tmp/scratchpad-server ./cmd/scratchpad-server
SCRATCHPAD_DATABASE_URL='' SCRATCHPAD_DATABASE_PATH='/absolute/source/scratchpad.sqlite' \
  /private/tmp/scratchpad-server backup '/absolute/private-backups/pre-go.sqlite'
```

The successful JSON report includes the source schema version and destination. The PostgreSQL branch returns `BACKUP_EXTERNAL` before connecting or touching SQLite files; use the operator-controlled `pg_dump`/`pg_restore` procedure for that engine. A SQLite backup command does not prove PostgreSQL operational restore.

### PostgreSQL snapshot and fresh-database restoration

Use clients matching the server major version. PostgreSQL documents consistent
single-database snapshots in [pg_dump](https://www.postgresql.org/docs/18/app-pgdump.html)
and direct database restoration in [pg_restore](https://www.postgresql.org/docs/18/app-pgrestore.html).
Retain the existing runtime/configuration and the separately protected installation
key. The following template uses an operator-managed password file and creates a
new destination; substitute the private paths, connection and database names.

```sh
umask 077
export PGPASSFILE='/absolute/private/postgresql.pgpass'
export PGHOST='database.example.internal'
export PGPORT='5432'
export PGUSER='scratchpad_restore_operator'
pg_dump --format=custom --no-owner --no-acl \
  --dbname='scratchpad_source' \
  --file='/absolute/private-backups/pre-upgrade.dump'
createdb 'scratchpad_restore_candidate'
pg_restore --exit-on-error --no-owner --no-acl \
  --dbname='scratchpad_restore_candidate' \
  '/absolute/private-backups/pre-upgrade.dump'
```

Select the destination explicitly with `--dbname`; an archive-to-SQL conversion
is not a database restore. The restoring role owns restored objects and supplies
its deployment grants separately. This is one application database, not a backup
of cluster roles or server configuration. A RAG destination also needs the
matching installed pgvector extension files and permission to restore extensions.
Retain the original public origin/RP ID when validating browser credentials and
mount the retained installation key for encrypted provider settings.

Real-process tests dump a live fixture, restore into an empty database, then verify
fresh SSH proof and authenticated reads, unchanged records, durable retry replay,
native lexical indexes, exclusion of later writes, and retained-key provider
dispatch. Wrong or missing keys deny provider dispatch while preserving memory.
The pgvector case also verifies restored coverage, semantic citations and lexical
fallback with unavailable keys. Uncompressed test archives must not contain the
synthetic reusable provider token. All test material is disposable and synthetic.

```sh
TEST_POSTGRES_URL=postgres://postgres@127.0.0.1:5432/postgres \
  go -C backend test -race -count=1 -run TestPostgresOperationalRestore ./...
TEST_PGVECTOR_URL=postgres://postgres@127.0.0.1:5432/postgres \
  go -C backend test -race -count=1 -run TestRAGPostgresOperationalRestore ./...
```

Configured PostgreSQL fixtures require `pg_dump` and `pg_restore`; missing clients
fail rather than silently omit restore coverage. CI uses the matching clients
inside its owned PostgreSQL service through `TEST_POSTGRES_CONTAINER_ID`. Local
verification uses native clients. These checks do not establish deployed restore
or full release parity. Browser passkey restoration is verified by the separate
Chromium workflow above. The pre-upgrade rollback scenario is verified separately below.

### Pre-upgrade PostgreSQL rollback verification

The retained TypeScript API refuses a Go-upgraded schema-2 database. Rollback
restores the pre-upgrade schema-1 archive into a fresh database instead of editing
migration markers or applying a destructive down migration. Native tests take
that archive before Go starts, upgrade and write through Go, verify TypeScript
refusal and preservation of the upgraded source, then recover authenticated
original record reads through the real TypeScript API process on the restored
snapshot. Post-upgrade writes remain on the upgraded database and are absent
from the rollback snapshot.

This fixture requires project Node 24 and installed pinned dependencies:

```sh
npm ci
TEST_POSTGRES_URL=postgres://postgres@127.0.0.1:5432/postgres \
  npm run test:backend
```

Use the project's Node-version selection before running the command. Missing
Node/dependencies fail configured rollback verification. CI installs them before
the full Go suite. The TypeScript fixture uses actual HTTP/API handlers; it does
not establish dashboard rendering, a published-container rollback or deployed
acceptance. Retain and verify the actual old deployment artifact before cutover.

### Runtime replacement and SQLite restoration

Stop application writers and workers before replacing a runtime. Retain the exact old executable/image and configuration. For the current SQLite application, use its existing online administrative backup command; do not copy only the main database while WAL writers are active. For PostgreSQL, use the existing administrative backup procedure or an operator-controlled `pg_dump`, and restore into a separate disposable database to verify the backup before upgrading.

Restore a completed SQLite snapshot into a fresh private directory or volume while the target service is stopped. Retain the original snapshot unchanged. For a host directory, these operator steps refuse an existing restore directory:

```sh
restore_dir='/absolute/fresh-restore-directory'
mkdir -m 700 "$restore_dir"
install -m 600 '/absolute/private-backups/pre-go.sqlite' "$restore_dir/scratchpad.sqlite"
```

Point the retained runtime and configuration at that new database, then verify readiness, owner authentication, immutable record details, search and retry behavior before accepting the restore. Starting Go intentionally migrates the restored copy; starting the retained TypeScript runtime is the schema-1 rollback path. Do not start Go on the retained snapshot itself. For container volumes, follow the stopped-service/fresh-volume procedure in the deployment runbook.

CLI/HTTP tests verify unchanged schema-1 source bytes, snapshot privacy, authentication and immutable history after fresh-volume restore, committed live WAL data, curation/audits, lexical indexes, retry replay and exclusion of later writes. They also verify unsafe/existing path refusal, sidecar safety, one winner among concurrent backups, cancellation cleanup and safe PostgreSQL delegation. Run them with `npm run test:backend`; no deployed restore or PostgreSQL dump/restore acceptance is implied by these SQLite checks.

Keep backups private: they include hashed authentication/session state and may still contain legacy plaintext provider secrets. Schema migrations do not convert reusable secret values. Go server startup now converts legacy provider tokens separately when a usable retained key is supplied; pending or failed conversion does not block schema migration or ordinary memory. See the secret configuration runbook for encrypted settings, key retention and mounted-file deployment interfaces.

The current TypeScript executable accepts schema 1 only. Once this Go executable commits schema 2, the TypeScript executable intentionally refuses that database. There is no automatic down migration. To roll back, stop the new runtime and restore the verified pre-upgrade schema-1 backup with the retained old runtime. Never delete migration markers to make an old runtime accept upgraded data. Restoring a pre-upgrade backup discards subsequent writes; plan the maintenance window accordingly. Do not run old and new runtimes against the same upgraded database.

On migration failure, preserve the original database and backup, report the safe startup error, and investigate using operator tools in a controlled environment. Do not modify schema markers or auto-repair incomplete objects. Schema changes, release, deployment and live-user acceptance require separate evidence.
