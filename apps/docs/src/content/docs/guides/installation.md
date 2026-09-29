---
title: Installation & development
description: Run the first Scratchpad implementation locally, enroll the owner, and build the docs.
---

:::caution[Development build]
The first implementation is available from source. There is no published production release yet. Build locally until signed release binaries and versioned container images are published.
:::

## Prerequisites

Use Node.js **24.21.0** from `.node-version`, npm, Git, and OpenSSH `ssh-keygen`. Building the MCP also requires Go **1.27.1**. Docker Compose is an alternative for running the server.

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

## Run with Docker

```sh
docker compose up --build -d
docker compose exec scratchpad npm run admin -- setup
```

The local Compose configuration serves the dashboard on port 3000 and stores SQLite in a persistent volume. Follow the repository deployment runbook before moving beyond localhost: remote instances require HTTPS and a matching `SCRATCHPAD_PUBLIC_URL`.

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
