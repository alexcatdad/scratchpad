# Scratchpad

Private, self-hosted memory for developers and coding agents. Keep decisions, findings, questions, failures, constraints, and project history—with the reasoning and provenance intact.

[Documentation](https://alexcatdad.github.io/scratchpad/) · [Product requirements](docs/prd.md) · [Architecture](docs/architecture.md) · [API](docs/api.md)

## Status

The first implementation includes a TanStack Start dashboard, SQLite API, passkey enrollment, SSH-authenticated Go stdio MCP, immutable captures, search, audited metadata, navigable record relationships, source provenance, legacy JSONL import with diagnostics, native import/export, online SQLite backups, and optional repository mirroring. This is an early implementation under active validation, not a published signed release. AI processing and PostgreSQL come after the initial SQLite workflow.

## Run with Docker

```sh
docker compose up --build -d
docker compose exec scratchpad npm run admin -- setup
```

Open `http://localhost:3000`, enter the single-use setup token, and register a passkey. The named `scratchpad-data` volume stores the database. For a remote deployment, set `SCRATCHPAD_PUBLIC_URL` to the exact HTTPS browser origin and put the service behind your TLS reverse proxy. Default Compose binding is loopback only. Follow the [deployment runbook](docs/runbooks/deployment.md) for HTTPS, persistent-volume backup/restore, upgrades, and rollback.

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

## Inspect and maintain memory

Open a record to inspect its original payload, capture provenance, evidence, relationship chain, metadata revisions, and audit events. Corrections update curated metadata; original captures remain unchanged. Project settings control allowed capture types, mirror eligibility, and cross-project analysis permission.

**Settings → Import a decision log** accepts a JSONL file and destination project. Inspect its import notes: malformed or incomplete source entries remain visible as diagnostics, and historical authority is not silently promoted to verified fact. Native exports move knowledge and audit history between instances; use the administrator SQLite backup for operational recovery including authentication state.

## Distribution status

The [release pipeline](docs/runbooks/release.md) builds macOS/Linux MCP binaries for ARM64 and AMD64, requires Developer ID signing and Apple notarization for macOS, publishes checksums and a multiarchitecture GHCR image, and updates the existing Homebrew tap. This is implemented automation, **not evidence of a published release**. Apple signing/notarization credentials and tap application credentials still need to be configured in this repository.

See the [MVP readiness evidence](docs/runbooks/mvp-readiness.md) for tested scope and remaining acceptance work. Continue using local builds until signed artifacts are published and verified.

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
