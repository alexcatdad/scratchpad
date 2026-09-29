# Scratchpad

Private, self-hosted memory for developers and coding agents. Keep decisions, findings, questions, failures, constraints, and project history—with the reasoning and provenance intact.

[Documentation](https://alexcatdad.github.io/scratchpad/) · [Product requirements](docs/prd.md) · [Architecture](docs/architecture.md) · [API](docs/api.md)

## Status

The first implementation includes a TanStack Start dashboard, SQLite API, passkey enrollment, SSH-authenticated Go stdio MCP, immutable captures, search, audited metadata, import/export, and optional repository mirroring. This is an early implementation under active validation, not a published signed release. AI processing and PostgreSQL come after the initial SQLite workflow.

## Run with Docker

```sh
docker compose up --build -d
docker compose exec scratchpad npm run admin -- setup
```

Open `http://localhost:3000`, enter the single-use setup token, and register a passkey. The named `scratchpad-data` volume stores the database. For a remote deployment, set `SCRATCHPAD_PUBLIC_URL` to the exact HTTPS browser origin and put the service behind your TLS reverse proxy. Default Compose binding is loopback only.

Additional passkeys and MCP public keys are enrolled in **Settings**. Never send a private key to the server. Lost-credential recovery is an explicit administrator action:

```sh
docker compose exec scratchpad npm run admin -- recover
```

## Develop

Use the Node 24 version in `.node-version`, npm, Go 1.27.1, Git and OpenSSH.

```sh
npm ci
export SCRATCHPAD_PUBLIC_URL=http://localhost:3000
export SCRATCHPAD_DATABASE_PATH="$PWD/data/scratchpad.sqlite"
npm run dev
```

In another terminal, use the same absolute database path and public URL for the server and administrator command (see `.env.example`):

```sh
export SCRATCHPAD_PUBLIC_URL=http://localhost:3000
export SCRATCHPAD_DATABASE_PATH="$PWD/data/scratchpad.sqlite"
npm run admin -- setup
```

Build MCP:

```sh
cd mcp
go build -o ../dist/scratchpad-mcp ./cmd/scratchpad-mcp
```

See [MCP configuration and tools](mcp/README.md) and the [development runbook](docs/runbooks/development.md).

## Quality gates

```sh
npm run lint
npm run build
npm run typecheck
npm test
npx playwright install chromium
npm run test:e2e
```

From `mcp/`, also run `go vet ./...` and `go test -race ./...`. The browser integration test uses disposable SQLite storage and a virtual passkey, then drives the real Go MCP through capture and server restart. No production authentication bypass is provided.

GitHub Actions checks the application, documentation, Go code and Docker image. GitHub Pages hosts only the public static documentation, never the private application or its records.

## Principles

- Useful without AI, PostgreSQL, or an external identity provider.
- One owner, multiple credentials; private keys stay local.
- Original captures remain immutable; curated changes are audited.
- Central storage is authoritative. Optional mirrors never commit or push Git.
- Typed capture stays lightweight and evolves through actual usage.
