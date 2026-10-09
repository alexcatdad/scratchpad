# Authentication admission under anonymous abuse

Issue #11 separates authentication admission by server-provided client address.
Unsupported POST routes and invalid browser origins are rejected before charging
an allowance. Valid setup and recovery tokens have separate allowances;
authenticated management uses the credential identity. Each allowance permits
120 requests per minute. At most 4096 client allowances are retained in memory,
and at most eight protected verifications, eight anonymous cryptographic verifications and four anonymous OAuth callbacks run concurrently. Anonymous MCP and passkey-login verification permits at most two concurrent requests per transport client, preserving capacity for an independent client and keeping setup, recovery and credential enrollment separate.

The TanStack server adapter obtains the address from runtime metadata through
`getRequestIP({ xForwardedFor: false })`. The API ignores forwarding headers
unless the socket peer appears in `SCRATCHPAD_TRUSTED_PROXIES`, an explicit
comma-separated IP allowlist. It then walks `X-Forwarded-For` from right to left
and selects the nearest untrusted hop. The proxy must append or replace incoming
forwarding data. Srvx's own proxy trust stays disabled. See the deployment runbook
for host/Docker address selection; never trust every address. Clients
sharing an address share anonymous allowances. Missing transport metadata uses
a conservative shared unknown-client allowance; authenticated and setup flows
still use their own proven identities. Allowances reset on process restart.

Use Node from `.node-version`, the exact Go patch in `mcp/go.mod`, and synthetic
test data only.

1. Run focused WebAuthn, MCP abuse and GitHub access tests with
   `npm run test -w @scratchpad/web -- src/server/webauthn.test.ts src/server/auth-abuse.test.ts src/server/github-access.test.ts`.
2. Run `npm run typecheck`, `npm run lint`, `npm test` and `npm run build`.
3. Run `npm run test:e2e -- tests/workflow.spec.ts tests/github-workflow.spec.ts`
   to exercise real browser enrollment, recovery, MCP proof and GitHub access.
4. Check canonical GitHub Actions at the current PR head. Local success does not
   establish PostgreSQL, container or deployed acceptance.

The Go 1.27.2 pin is included independently because the previous toolchain fails
canonical vulnerability checks on known standard-library vulnerabilities.

Anonymous OAuth callbacks use a separate four-slot pool, preserving the eight cryptographic proof slots for setup, recovery, passkey and MCP verification, including token/session-authorized OAuth callbacks. Slot selection occurs only after validating the stored OAuth state and browser binding. A pre-consumption `RATE_LIMITED` callback returns HTTP 429 without clearing its browser state cookie, so it can be retried. Failed setup/session proofs and invalid OAuth callbacks consume the anonymous client allowance; a proved token or credential retains its independent allowance.

Unknown or unbound OAuth state is rejected before acquiring the serialized write transaction. Valid state is rechecked atomically before consumption, preserving replay protection.

Token/session-authorized callbacks revalidate the initiating credential before
reserving a protected slot, again before consuming state, and before committing
the resulting sign-in. Consumed setup tokens and revoked sessions cannot use
stale OAuth states to contact GitHub or occupy recovery capacity.

Setup and session proof resolution has a separate 120-per-minute client admission lane checked before hashing or database access. Anonymous login traffic cannot consume this lane. Clients sharing a transport address also share proof lookup capacity; another client retains enrollment and management access when this lane is exhausted. IPv6 peers, allowlist entries and forwarded hops use canonical address forms, including IPv4-mapped IPv6.

Anonymous GitHub login starts charge client admission before reading the binding; successful starts are charged once.

At allowance-map capacity, admission removes expired counters and refuses new keys with HTTP 429 while current-minute counters remain intact. Existing clients keep their remaining allowance. New clients can retry after the minute expires; rotating source addresses cannot reset live limits.
