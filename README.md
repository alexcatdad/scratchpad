# Scratchpad

Private, self-hosted memory for developers and coding agents. Keep decisions, findings, questions, failures, constraints, and project history—with the reasoning and provenance intact.

[Documentation](https://alexcatdad.github.io/scratchpad/) · [Product requirements](docs/prd.md) · [Architecture](docs/architecture.md) · [API](docs/api.md)

## Status

Scratchpad v0.3.0 includes the TanStack Start dashboard, SQLite API, passkey enrollment, SSH-authenticated Go stdio MCP, immutable captures, deterministic search, audited metadata and relationships, source provenance, JSONL import with diagnostics, native import/export, online SQLite backups, and optional repository mirroring. It adds optional PostgreSQL, persisted AI analysis jobs, source-linked suggestions and patterns, semantic retrieval, five private Markdown formats, and dashboard/MCP controls. SQLite and the capture/retrieval workflow remain useful without AI. This release additionally includes opt-in native pgvector similarity, public GitHub profile enrichment and generated TypeScript/Go HTTP clients; see [optional extension acceptance](docs/optional-readiness.md) for verification evidence. See [release verification](https://alexcatdad.github.io/scratchpad/guides/releases/) for publication and installation evidence; the [v0.1.3 MVP](https://github.com/alexcatdad/scratchpad/releases/tag/v0.1.3) is historical acceptance evidence.

## Install the MCP

```sh
brew install alexcatdad/tap/scratchpad-mcp
scratchpad-mcp --version
```

The tap supports macOS and Linux, including Linux Homebrew on WSL. Versioned archives and checksums are also available from [GitHub Releases](https://github.com/alexcatdad/scratchpad/releases/tag/v0.3.0). macOS binaries are Developer ID signed and notarized. See [MCP configuration and tools](mcp/README.md) to enroll a key and connect your coding client.

## Run with Docker

The versioned image supports Linux ARM64 and AMD64:

```sh
docker volume create scratchpad-data
docker run -d --name scratchpad --restart unless-stopped \
  -p 127.0.0.1:3000:3000 \
  -e SCRATCHPAD_PUBLIC_URL=http://localhost:3000 \
  -v scratchpad-data:/data \
  ghcr.io/alexcatdad/scratchpad:v0.3.0
docker exec scratchpad npm run admin -- setup
```

Open `http://localhost:3000`, enter the single-use setup token, and register a passkey. The named `scratchpad-data` volume stores the database. Additional passkeys and MCP public keys are enrolled in **Settings**. Never send a private key to the server.

For a remote deployment, use your exact HTTPS origin and a TLS reverse proxy; keep the application port private. The [installation guide](https://alexcatdad.github.io/scratchpad/guides/installation/) includes a Compose configuration. The [deployment runbook](docs/runbooks/deployment.md) covers persistent-volume backup/restore, upgrades, and rollback.

Lost-credential recovery is an explicit administrator action:

```sh
docker exec scratchpad npm run admin -- recover
```

For a source checkout, the repository's `docker compose up --build -d` builds the current source instead of pulling the release image.

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

## Release verification

The [release pipeline](docs/runbooks/release.md) publishes native MCP archives, checksums, a versioned multiarchitecture GHCR image, and an update to the existing Homebrew tap. The owner signs and notarizes macOS artifacts locally using the macOS Keychain; Apple signing and notarization secrets are not stored in GitHub. CI independently verifies their signing team, source identity, checksums, and Apple trust before publication.

See the [remaining-product acceptance ledger](docs/post-mvp-readiness.md) for the current source and the [MVP readiness evidence](docs/runbooks/mvp-readiness.md) for prior installation checks. Publication, installation on a particular platform, and acceptance in your deployment remain distinct outcomes.

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
