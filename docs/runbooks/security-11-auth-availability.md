# Authentication admission under anonymous abuse

Issue #11 separates authentication admission by server-provided client address.
Unsupported POST routes and invalid browser origins are rejected before charging
an allowance. Valid setup and recovery tokens have separate allowances;
authenticated management uses the credential identity. Each allowance permits
120 requests per minute. At most 4096 client allowances are retained in memory,
and at most eight proof or OAuth verifications run concurrently.

The TanStack server adapter obtains the address from runtime metadata through
`getRequestIP({ xForwardedFor: false })`. The API never reads forwarding headers
to select a bucket. Srvx defaults to trusting no proxy. If configuring its
`trustProxy` option, specify only actual proxy addresses, prevent direct access
through those addresses, and have the proxy append or replace forwarding data.
Never use unrestricted proxy trust for an internet-facing listener. Clients
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
