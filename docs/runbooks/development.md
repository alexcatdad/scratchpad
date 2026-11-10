# Development

Use the versions pinned in `.node-version`, `backend/go.mod` and `mcp/go.mod`.
The Go backend owns application behavior; `apps/web` is the React dashboard, a
static Vite build that the Go binary embeds, `apps/docs` contains static public documentation, `mcp` contains
the Go stdio MCP/capture service, and `macos` contains the native app.

## Authenticated SQLite loop

1. Inspect Git status and read current PRD, architecture and API contracts.
2. Run `npm ci`. When adding/upgrading dependencies, verify stable maintained
   versions and pin them exactly; ordinary work retains the lockfile.
3. Set `SCRATCHPAD_PUBLIC_URL=http://localhost:3000` and an absolute private
   `SCRATCHPAD_DATABASE_PATH`. Run `npm run dev` from the repository root. It
   builds the dashboard to static files, builds the Go executable with them
   embedded and starts that one process.
4. In a second terminal with the same configuration, run `npm run admin -- setup`
   for a fresh installation. Existing owners sign in or explicitly use `recover`.
   The command prints a single-use token; keep its output private.
5. Open the public Go origin and enroll/sign in. Verify actual capture, retrieval
   and restart behavior before claiming the authenticated loop works.

Database/provider/GitHub settings belong to Go. Do not run development against an
installation containing owner history. PostgreSQL tests need a disposable database
with fixture-creation permission, not production credentials.

## Dashboard development

`npm run build -w @scratchpad/web` writes static files to
`backend/internal/httpapi/dashboard/dist` (ignored by Git; the committed
`placeholder.txt` beside it keeps the embed directory present). A Go binary
built without that build has no dashboard: `/` returns `404 NOT_FOUND` and the
API works. Build the web workspace before `go build` when a dashboard is wanted.

For hot reloading behind the Go server:

```sh
npm run dev -w @scratchpad/web                                # terminal 1: Vite on 127.0.0.1:4000
SCRATCHPAD_DASHBOARD_URL=http://127.0.0.1:4000 npm run dev    # terminal 2: Go on 127.0.0.1:3000
```

With `SCRATCHPAD_DASHBOARD_URL` set, `npm run dev` skips the web build and Go
proxies dashboard GET/HEAD requests to Vite. The variable is development-only;
see [the dashboard runbook](go-dashboard.md).

## Verification

Run `npm run lint`, `npm run typecheck`, `npm test`, `npm run build`, generated
SDK checks, `go -C backend vet ./...`, `npm run test:backend`,
`go -C mcp vet ./...` and `go -C mcp test -race ./...`. Use the real browser
workflows with `npx playwright test --project=go-native`; the Playwright global
setup builds the dashboard first so each test server embeds it. Configured PostgreSQL
cases create/drop only their own databases. See [P0 verification](p0-consolidation.md)
for the full acceptance boundary.

Select an explicit Docker test context; the configured context may be remote.
Build the image and use the existing clean-container browser/restore fixtures with
run-owned data. Stop/remove owned processes, containers and test data afterward.
Never delete an existing database or persistent volume as test cleanup.

`bash scripts/lint-infrastructure.sh` uses the pinned versions named in that
script. GitHub Actions is the canonical runner, including PostgreSQL 18, pgvector,
container and macOS lanes. A local passing test is separate from canonical CI,
publication, installed-client acceptance and production rollout.

## Documentation and delivery

Use `npm run docs:dev` for the independent public documentation site. Run
`npm run check -w @scratchpad/docs` and `npm run build -w @scratchpad/docs` after
documentation changes. Pages hosts static public guidance only, never instance
records, credentials or private runtime details.

Make coherent commits/PRs with scoped verification. Preserve current API/MCP,
authentication, provenance and immutable history. Follow [deployment ownership](homelab-deploy.md)
for images/manifests/upgrade/rollback and [Go migrations](go-backend-migrations.md)
before any existing-volume upgrade. Code merge never authorizes live cutover.
