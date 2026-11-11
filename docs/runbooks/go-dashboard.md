# Embedded React dashboard

The dashboard is a static single-page app that the Go server embeds and serves
itself. There is no renderer process and no Node runtime in any deployment;
Node 24 is needed only to build and test. Browser API requests use the same Go
origin, including passkey enrollment and GitHub OAuth callbacks.

## Build and start

Use the project's Node 24 version and installed pinned dependencies. Choose a
private data directory and binary destination before running these commands:

```sh
npm run build -w @scratchpad/web
go -C backend build -o /absolute/private-bin/scratchpad-server ./cmd/scratchpad-server
SCRATCHPAD_DATABASE_PATH=/absolute/private-data/scratchpad.sqlite \
  /absolute/private-bin/scratchpad-server setup
```

The web build writes `index.html` and content-hashed assets to
`backend/internal/httpapi/dashboard/dist`, which Git ignores; the committed
`placeholder.txt` beside it keeps the embedded directory present. `go build`
embeds whatever is there, so build the web workspace first. A binary built
without it has no dashboard: `/` returns `404 NOT_FOUND` and the API works
normally. `npm run dev`, the `Dockerfile`, the Nix package and the Playwright
global setup all build the dashboard before the server.

The setup command prints the single-use administrator token; keep that terminal
private. Existing owners use their credentials, or the Go `recover` command when
needed, as described in the [migration runbook](go-backend-migrations.md).

Start the server with the same database configuration as setup:

```sh
SCRATCHPAD_DATABASE_PATH=/absolute/private-data/scratchpad.sqlite \
SCRATCHPAD_PUBLIC_URL=http://localhost:3000 \
SCRATCHPAD_LISTEN_ADDR=127.0.0.1:3000 \
  /absolute/private-bin/scratchpad-server serve
```

Open `http://localhost:3000`. Use a hostname for the public origin so WebAuthn can
use it as a relying party ID. PostgreSQL installations supply
`SCRATCHPAD_DATABASE_URL` to the Go setup/recovery/server processes instead of the
SQLite path. Provider and GitHub configuration also belong to Go.

## What the server serves

Dashboard files are served for GET and HEAD outside `/api/`:

- `/` returns `index.html` with `Cache-Control: no-cache`. Dashboard state lives
  in the query string (`view`, filters, `recordId`, `authError`), which the
  browser reads; the server ignores it.
- `/assets/*` files carry content hashes in their names and are served with
  `Cache-Control: public, max-age=31536000, immutable`.
- Every response has an explicit content type, an `ETag`,
  `X-Content-Type-Options: nosniff` and gzip when the client accepts it.
- Any other path, any other method and unknown `/api/` routes return
  `404 NOT_FOUND`. There is no fallback to `index.html`.

Go serves the API, `/health` and `/ready` directly. The access log covers
dashboard requests like any other: method, path, status and duration, never the
query string.

## Development proxy

`SCRATCHPAD_DASHBOARD_URL` is a development-only setting. When set, Go proxies
dashboard GET/HEAD requests to a running `vite dev` instead of serving the
embedded build, so hot reloading works behind the real origin:

```sh
npm run dev -w @scratchpad/web                                # terminal 1: Vite on 127.0.0.1:4000
SCRATCHPAD_DASHBOARD_URL=http://127.0.0.1:4000 npm run dev    # terminal 2: Go on 127.0.0.1:3000
```

With the variable set, `npm run dev` skips the web build. The value must be a
loopback HTTP(S) origin without credentials, query, fragment or a non-root path.
Go strips authentication cookies, authorization, origin and forwarding headers
from proxied requests and drops cookies set by the target. If Vite is not
running, dashboard requests return `503 DASHBOARD_UNAVAILABLE`; the Go API
remains available. Never set this variable in production.

## Verification

```sh
go -C backend test -race -count=1 -run Dashboard ./...
npx playwright test tests/go-runtime.spec.ts tests/go-dashboard.spec.ts
TEST_POSTGRES_URL=postgres://scratchpad:scratchpad-test@127.0.0.1:5432/scratchpad_test \
  npx playwright test tests/go-dashboard.spec.ts
```

The Playwright global setup (`tests/global-setup.ts`) builds the web workspace
before any test compiles a server; set `SCRATCHPAD_E2E_SKIP_WEB_BUILD=1` to
reuse an existing build. `tests/go-runtime.spec.ts` checks the embedded
document and asset headers, caching, `404` responses and the access log. The
Go tests cover file handling, the build without a dashboard and the
development proxy's credential isolation.

The Chromium workflow in `tests/go-dashboard.spec.ts` verifies actual React
passkey enrollment, logout/login, project creation and settings, capture,
record inspection, owner profile persistence, native export and idempotent
re-import, and new historical JSONL import. It also saves a synthetic encrypted
provider token, tests completion and embeddings against a local provider,
verifies safe provider failure, removes the stored key and checks that
UI/export outputs exclude its value.

Analysis queueing, source-linked suggestion acceptance and source inspection also
use the actual UI and Go worker. The private handoff workflow downloads Markdown
and verifies its server-added draft warning and source citations. These synthetic
provider checks verify application wiring, not real model output quality.
The embedding workflow queues work from Insights, observes a safe failure after
the bounded provider retries, retries through the UI when the provider recovers,
and renders the expected source-backed semantic match using fixed synthetic vectors. This verifies the
existing SQLite/PostgreSQL semantic interface; it does not evaluate PostgreSQL
passage RAG recall or real embedding quality.

It runs against SQLite and optional disposable PostgreSQL. The database
connection needs permission to create and drop the fixture's own
database. PostgreSQL cases explicitly skip when the fixture is not configured.
Fixtures use private temporary databases and synthetic credentials. These checks
do not establish every dashboard workflow, real hardware, canonical CI or
deployed acceptance.
