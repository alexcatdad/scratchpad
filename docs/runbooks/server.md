# Server development and operation

The application server uses Node 24, SQLite, Drizzle, SimpleWebAuthn, and the system `ssh-keygen` verifier. Run commands from the repository root unless stated otherwise.

1. Install the pinned workspace dependencies with `npm ci`.
2. Set `SCRATCHPAD_DATABASE_PATH` to persistent storage and `SCRATCHPAD_PUBLIC_URL` to the browser origin. HTTP is allowed only on localhost; remote deployments require HTTPS.
3. Run the documented root development command. Generate an initial owner token using the administrative setup command, open the dashboard, and enroll a passkey. Setup tokens expire after 15 minutes and are replaced when the administrator generates another.
4. Run the workspace typecheck, lint, and server tests before committing server changes. Tests use isolated temporary databases; never point tests at a live owner's database.
5. To recover browser access, use the separate recovery command. Recovery preserves data, registers a replacement passkey, and invalidates existing sessions. It does not reopen initial setup.
6. Stop the application before an operational SQLite file copy, or use SQLite's online backup API. Native export includes knowledge and audit history but deliberately excludes credentials, challenges, sessions, and setup tokens.

## Protocol decisions

MCP signs the exact UTF-8 `nonce` returned by the challenge API, without a newline, using OpenSSH SSHSIG namespace `scratchpad-auth`. Challenges last two minutes; both browser and MCP sessions last at most 24 hours. Tokens are random and stored only as hashes on the server. Every request checks credential revocation.

Browser writes require the configured Origin and an HttpOnly, SameSite=Strict cookie. HTTPS instances additionally set Secure. MCP bearer credentials cannot enroll or revoke other credentials.

Capture idempotency is scoped to the authenticated credential. Comparison includes the resolved project and normalized record payload, excluding rediscovered Git context so a mirror changing worktree dirtiness cannot break retries. The first capture retains its original Git context. Different idempotency keys deliberately create distinct records.

Mutable metadata, project settings, and relationship decisions require `expectedVersion` or `If-Match`; stale changes fail with `CONFLICT`. Raw captures never change. Search uses FTS5 with literal token matching and stable cursor pagination.

Native imports are transactional and reject conflicting stable IDs. Legacy JSONL import preserves each source object, labels historical authority as unverified, and reports per-line diagnostics. Native exports exclude local operational secrets and authentication data.
