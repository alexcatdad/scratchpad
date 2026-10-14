---
title: Installation & development
description: Install the released MCP and container, enroll the owner, or develop from source.
---

## Install the released MCP

```sh
brew install alexcatdad/tap/scratchpad-mcp
scratchpad-mcp --version
```

Homebrew supports macOS and Linux, including WSL through Linux Homebrew. Exact-version archives and checksums are available from [the v0.3.0 release](https://github.com/alexcatdad/scratchpad/releases/tag/v0.3.0). macOS release binaries are signed and notarized using the owner's local Keychain; they are independently verified before publication.

After setting up the server below, follow [MCP setup](/scratchpad/guides/mcp/) to enroll your public key and point your coding client at the installed `scratchpad-mcp` binary. Homebrew installation alone does not connect it to an instance.

## Run the released server with Compose

Create a `compose.yaml` in a deployment directory:

```yaml
services:
  scratchpad:
    image: ghcr.io/alexcatdad/scratchpad:v0.3.0
    ports:
      - "127.0.0.1:3000:3000"
    environment:
      SCRATCHPAD_PUBLIC_URL: http://localhost:3000
      SCRATCHPAD_DATABASE_PATH: /data/scratchpad.sqlite
    volumes:
      - scratchpad-data:/data
    restart: unless-stopped
volumes:
  scratchpad-data:
```

Then start it and generate a setup token:

```sh
docker compose pull
docker compose up -d
docker compose exec scratchpad npm run admin -- setup
```

Open [localhost:3000](http://localhost:3000), enter the one-use token, and register a passkey. The image supports Linux ARM64 and AMD64. Remote instances require a stable HTTPS origin and a TLS reverse proxy; follow the [deployment runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/deployment.md) before exposing the service beyond localhost.

The named volume stores your database. Preserve it across container upgrades, and take a full SQLite backup before changing application versions. AI services and PostgreSQL are not required.

## Optional GitHub-first setup

Current server source supports GitHub-first owner setup and GitHub browser sign-in; the released v0.3.0 image above predates this capability. Configure the per-instance OAuth app using the [operator runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/github-oauth.md), then generate the same administrator setup token. Choose GitHub on the setup page and authorize your account. The token authorizes initial ownership; a GitHub username or login by itself does not. Passkey-only setup remains available. Existing owners link through authenticated Settings.

After linking, your laptop and VMs can use synchronized published SSH authentication or signing keys by proving possession. Passkeys and independent manual keys remain optional alternatives. See [Security & recovery](/scratchpad/reference/security/) and [Agent integration](/scratchpad/guides/agents/).

## Develop from source

Use Node.js **24.21.0** from `.node-version`, npm, Git, and OpenSSH `ssh-keygen`. Building the MCP also requires Go **1.27.2**. Docker Compose is an alternative for running the server.

Clone [the repository](https://github.com/alexcatdad/scratchpad) and run these commands from its root:

```sh
npm ci
export SCRATCHPAD_PUBLIC_URL=http://localhost:3000
export SCRATCHPAD_DATABASE_PATH="$PWD/data/scratchpad.sqlite"
npm run dev
```

Open [localhost:3000](http://localhost:3000). Keep the terminal running. In a second terminal at the repository root, set the same configuration and generate the owner setup token:

```sh
export SCRATCHPAD_PUBLIC_URL=http://localhost:3000
export SCRATCHPAD_DATABASE_PATH="$PWD/data/scratchpad.sqlite"
npm run admin -- setup
```

Enter the token in the dashboard and register your first passkey. Tokens expire after 15 minutes; generating another replaces the previous token. Setup closes after owner enrollment.

Use the same database path for the administrative command and the running server. An explicit absolute path avoids creating a second database when commands run from different folders.

## Build the source checkout with Docker

```sh
docker compose up --build -d
docker compose exec scratchpad npm run admin -- setup
```

The local Compose configuration serves the dashboard on port 3000 and stores SQLite in a persistent volume. Follow the [deployment runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/deployment.md) before moving beyond localhost: remote instances require HTTPS and a matching `SCRATCHPAD_PUBLIC_URL`.

## Recover access

A server administrator can authorize replacement-passkey registration:

```sh
npm run admin -- recover
```

For Docker:

```sh
docker compose exec scratchpad npm run admin -- recover
```

Use the dashboard recovery flow with the resulting token. Recovery preserves records and invalidates existing sessions; it does not reopen ordinary first-time setup.

## Build and connect the MCP

```sh
cd mcp
go mod download
go build -o ../dist/scratchpad-mcp ./cmd/scratchpad-mcp
```

Follow [MCP setup](/scratchpad/guides/mcp/) to enroll a key and configure your coding client. These are local build instructions, not a signed release installation.

## Verify source changes

From the repository root:

```sh
npm run lint
npm run typecheck
npm test
npm run build
```

The Go module has its own checks, documented in its README. Run those when changing MCP code.

## Work on the docs

```sh
npm run docs:dev
npm run check --workspace @scratchpad/docs
npm run build --workspace @scratchpad/docs
```

Open the address printed by Astro with the `/scratchpad/` base path. Static output is `apps/docs/dist`. GitHub Pages hosts this public site, not the application or your records. A successful local build does not establish a successful Pages deployment.
