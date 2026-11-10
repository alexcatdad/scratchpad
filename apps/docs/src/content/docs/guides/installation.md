---
title: Install the app
description: Install Scratchpad on a Mac with Homebrew, connect it to your server, and set up a server or a development checkout.
---

## Install the macOS app

The macOS app bundles the background capture service, its encrypted offline
queue, the `scratchpad` command for coding agents and the `scratchpad-mcp`
server for MCP clients. It needs a Mac with Apple silicon running macOS 13 or
later, and a Scratchpad server you can reach. Intel and Linux app builds are not
available.

### Install and connect

```sh
brew install --cask alexcatdad/tap/scratchpad
open -a Scratchpad
```

This fully qualified install trusts only the Scratchpad cask; whole-tap trust is
not needed. See [Homebrew Tap Trust](https://docs.brew.sh/Tap-Trust).

In the app, open **Connection**, enter your server address (for example
`https://scratchpad.example.ts.net`), choose **Find Keys**, pick a key the
server accepts and choose **Connect**. Keep private keys on the computer; the
app signs with your existing SSH key or agent.

Then connect your coding agents. The recommended path is the bundled
`scratchpad` command; see [Agent integration](/scratchpad/guides/agents/). For
clients that need an MCP server, **AI Tools → Set Up…** adds the bundled
`scratchpad-mcp` to the client's configuration and shows you the change first;
see [MCP & project context](/scratchpad/guides/mcp/).

### Upgrades

```sh
brew upgrade --cask scratchpad
```

After an upgrade the capture service keeps running the previous version until it
restarts. The menu bar panel shows **Update installed**; choose **Restart Capture
Service** so it runs the new version. It also switches at your next login.
Installing the app does not deploy a server or transfer another computer's
configuration, signer or queued captures. See the
[distribution runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/macos-app-distribution.md).

## Retired standalone MCP formula

The `alexcatdad/tap/scratchpad-mcp` formula is deprecated and receives no further
releases. It installed only the v0.3.0 MCP binary, without the capture service
the current tools need. Move to the app:

```sh
brew uninstall alexcatdad/tap/scratchpad-mcp
brew install --cask alexcatdad/tap/scratchpad
```

Then switch agents to the bundled `scratchpad` command, or MCP clients to
`/Applications/Scratchpad.app/Contents/Helpers/scratchpad-mcp`.

## Run a server

The app talks to a Scratchpad server that you run yourself, on loopback or
behind HTTPS on a private network. Build and run it from a source checkout with
the steps below, or follow the
[deployment runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/deployment.md)
for a single-host Docker installation. Remote instances need a stable HTTPS
origin set in `SCRATCHPAD_PUBLIC_URL` before you enroll passkeys. The published
`ghcr.io/alexcatdad/scratchpad:v0.3.0` image is historical and predates the Go
server; new releases are not published as container images.

## Optional GitHub-first setup

Current server source supports GitHub-first owner setup and GitHub browser sign-in; the historical v0.3.0 image predates this capability. Configure the per-instance OAuth app using the [operator runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/github-oauth.md), then generate the same administrator setup token. Choose GitHub on the setup page and authorize your account. The token authorizes initial ownership; a GitHub username or login by itself does not. Passkey-only setup remains available. Existing owners link through authenticated Settings.

After linking, your laptop and VMs can use synchronized published SSH authentication or signing keys by proving possession. Passkeys and independent manual keys remain optional alternatives. See [Security & recovery](/scratchpad/reference/security/) and [Agent integration](/scratchpad/guides/agents/).

## Develop from source

Use Node.js **24.21.0** from `.node-version`, npm, Git, OpenSSH `ssh-keygen` and Go **1.27.3**. The source backend and MCP are Go; React is retained for the dashboard. Docker Compose is an alternative for running the server.

Clone [the repository](https://github.com/alexcatdad/scratchpad) and run these commands from its root:

```sh
npm ci
export SCRATCHPAD_PUBLIC_URL=http://localhost:3000
export SCRATCHPAD_DATABASE_PATH="$PWD/data/scratchpad.sqlite"
npm run dev
```

`npm run dev` builds Go and the dashboard, then starts the public Go API on loopback port 3000 and a private UI-only renderer on port 4000. Stopping it stops both processes. Open [localhost:3000](http://localhost:3000). Keep the terminal running. In a second terminal at the repository root, set the same configuration and generate the owner setup token:

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

## Build the CLI and MCP

```sh
cd mcp
go mod download
go build -o ../dist/scratchpad ./cmd/scratchpad
go build -o ../dist/scratchpad-mcp ./cmd/scratchpad-mcp
go build -o ../dist/scratchpad-service ./cmd/scratchpad-service
```

Both clients talk to a running `scratchpad-service`; the app normally starts it.
These are local build instructions, not a signed release installation.

## Verify source changes

From the repository root:

```sh
npm run lint
npm run typecheck
npm test
npm run build
```

Also run `npm run test:backend`, `go -C backend vet ./...`, `go -C mcp test -race ./...` and `go -C mcp vet ./...`. Existing installations must follow the [Go migration procedure](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/go-backend-migrations.md) before switching images; schema-1 rollback requires its matching pre-upgrade snapshot and retained key.

## Work on the docs

```sh
npm run docs:dev
npm run check --workspace @scratchpad/docs
npm run build --workspace @scratchpad/docs
```

Open the address printed by Astro with the `/scratchpad/` base path. Static output is `apps/docs/dist`. GitHub Pages hosts this public site, not the application or your records. A successful local build does not establish a successful Pages deployment.

The app screenshots under `apps/docs/src/assets/screenshots/` are rendered from
fixture data, never captured from a screen. Regenerate them on a Mac with
`task screenshots` after a visible change to the menu bar panel or settings.
