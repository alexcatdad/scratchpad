# Retained React dashboard with the Go API

This optional local integration keeps Go as the public HTTP listener and sole
backend. The existing Node process renders the React dashboard on a private
loopback port. Browser API requests use the public Go origin, including passkey
enrollment and GitHub OAuth callbacks. This does not establish full dashboard
parity, container packaging or a supported installation cutover.

## Build and start

Use the project's Node 24 version and installed pinned dependencies. Choose a
private data directory and binary destination before running these commands:

```sh
npm run build -w @scratchpad/web
go -C backend build -o /absolute/private-bin/scratchpad-server ./cmd/scratchpad-server
SCRATCHPAD_DATABASE_PATH=/absolute/private-data/scratchpad.sqlite \
  /absolute/private-bin/scratchpad-server setup
```

The setup command prints the single-use administrator token; keep that terminal
private. Existing owners use their credentials, or the Go `recover` command when
needed, as described in the [migration runbook](go-backend-migrations.md).

Start the renderer in one terminal:

```sh
SCRATCHPAD_UI_ONLY=true HOST=127.0.0.1 PORT=4000 node apps/web/server.mjs
```

Start Go in another terminal, using the same database configuration as setup:

```sh
SCRATCHPAD_DATABASE_PATH=/absolute/private-data/scratchpad.sqlite \
SCRATCHPAD_PUBLIC_URL=http://localhost:3000 \
SCRATCHPAD_LISTEN_ADDR=127.0.0.1:3000 \
SCRATCHPAD_DASHBOARD_URL=http://127.0.0.1:4000 \
  /absolute/private-bin/scratchpad-server serve
```

Open `http://localhost:3000`. Use a hostname for the public origin so WebAuthn can
use it as a relying party ID. PostgreSQL installations supply
`SCRATCHPAD_DATABASE_URL` to the Go setup/recovery/server processes instead of the
SQLite path. Provider and GitHub configuration also belong to Go.

The renderer's `SCRATCHPAD_UI_ONLY=true` mode refuses backend initialization and
returns `503 DASHBOARD_ONLY` for its API and readiness routes. Keep that listener
private. The existing TypeScript runtime remains available when this mode is
unset or false; its schema restrictions still apply.

## Boundaries and verification

`SCRATCHPAD_DASHBOARD_URL` must be a loopback HTTP(S) origin without credentials,
query, fragment or a non-root path. Both processes must share the same host or
network namespace. Go serves API, health and readiness routes directly. Only
dashboard GET/HEAD requests reach the renderer; unknown `/api/` routes remain
Go errors. Go strips authentication cookies, authorization and forwarding headers
from renderer requests and prevents renderer responses from setting cookies.

Stopping the renderer makes dashboard rendering return
`503 DASHBOARD_UNAVAILABLE`; the Go API remains available. Stop both owned
processes with their normal termination signal after development.

```sh
go -C backend test -race -count=1 -run TestDashboard ./...
npx playwright test tests/go-dashboard.spec.ts
```

Build the web workspace before the browser test. The Chromium workflow verifies
actual React passkey enrollment, project creation, capture, record inspection and
API availability after renderer shutdown. The HTTP test verifies credential
isolation, session-cookie ownership and API route ownership. Fixtures use private
temporary databases and synthetic credentials. These checks do not establish
every dashboard workflow, real hardware, canonical CI or deployed acceptance.
