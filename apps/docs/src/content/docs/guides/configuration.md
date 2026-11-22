---
title: Configuration
description: Understand which settings belong to the server, the project, and the Mac running your agents.
---

Configuration has three distinct owners. Server and Mac settings are separate: the server exposes an origin, while the capture service on the Mac connects to it.

| Scope   | Responsibility                                                                                                       |
| ------- | -------------------------------------------------------------------------------------------------------------------- |
| Server  | Public URL, persistent database location, authentication state, instance policy, optional AI providers               |
| Project | Project identity, metadata, enabled capture types, repository mirror permission, AI and cross-project participation  |
| Mac     | Server address and SSH key for the capture service, login start, notifications, per-call repository mirroring opt-in |

## Server environment

| Variable                                | Default                  | Purpose                                                                                       |
| --------------------------------------- | ------------------------ | --------------------------------------------------------------------------------------------- |
| `SCRATCHPAD_PUBLIC_URL`                 | `http://localhost:3000`  | Browser origin and passkey relying party; HTTPS, or HTTP on a loopback host                   |
| `SCRATCHPAD_LISTEN_ADDR`                | `127.0.0.1:3000`         | Address the server binds; the container image sets `0.0.0.0:3000`                             |
| `SCRATCHPAD_DATABASE_PATH`              | `data/scratchpad.sqlite` | SQLite file location; use an absolute path outside the container                              |
| `SCRATCHPAD_DATABASE_URL`               | Unset                    | Optional PostgreSQL URL; takes precedence over the SQLite path                                |
| `SCRATCHPAD_TRUSTED_PROXIES`            | Unset                    | Comma-separated reverse-proxy addresses whose forwarded client addresses are trusted          |
| `SCRATCHPAD_ENCRYPTION_KEY_FILE`        | Unset                    | Installation key file that encrypts stored provider secrets; keep it and back it up privately |
| `SCRATCHPAD_GITHUB_CLIENT_ID`           | Unset                    | Optional GitHub owner sign-in, with `SCRATCHPAD_GITHUB_CLIENT_SECRET`                         |
| `SCRATCHPAD_RAG_ENABLED`                | `false`                  | Optional passage retrieval on PostgreSQL with pgvector; `true` or `false`                     |
| `SCRATCHPAD_AGENT_EVENT_RETENTION_DAYS` | `30`                     | Days to keep agent hook events, from 1 to 3,650                                               |

Use the same values for the server and administrator commands (`scratchpad-server migrate`, `setup`, `recover` and `backup`). Changing working directories with the relative database default can create a separate database. An invalid public URL, `SCRATCHPAD_RAG_ENABLED` or retention value stops the server at startup with `CONFIG_INVALID`.

`SCRATCHPAD_DASHBOARD_URL` is for dashboard development only; never set it in production.

## Optional GitHub sign-in

The server enables GitHub owner access when both `SCRATCHPAD_GITHUB_CLIENT_ID` and `SCRATCHPAD_GITHUB_CLIENT_SECRET` are configured. Configure the OAuth callback as the exact `SCRATCHPAD_PUBLIC_URL` origin followed by `/api/v1/auth/github/callback`. Use a dedicated OAuth app per instance and keep its secret in private operator configuration. Machines need their own SSH signing access, not these OAuth values. Without this pair, passkey-only setup and manual credentials remain available. **Settings → About** shows whether GitHub sign-in is configured.

Follow the [OAuth operator runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/github-oauth.md). Servers built from the v0.3.0 release do not include this feature.

## Public URL and storage

Use one stable application origin. Browser passkeys bind to the configured relying party and origin; changing the URL is an authentication and migration concern, not just a cosmetic edit.

SQLite is the default database. The database and all authoritative authentication state must live in persistent storage. Restarting the server or its container must preserve records, challenges, sessions, revocations, and retry state.

The GitHub Pages URL serves public documentation only. It is not the server URL the app connects to.

## The Mac

The app stores the server address and the SSH key reference in the capture service's configuration. Set them in the app's **Connection** page, or from a terminal with `scratchpad connect --server <https-origin>` (add `--public-key <file.pub>` to choose a key). `scratchpad status` shows the service and the saved connection. Your SSH agent signs each challenge, and private keys never leave it; see [Security & recovery](/scratchpad/reference/security/).

`scratchpad connect` looks for keys in the agent given with `--agent-socket`, the one named by `SSH_AUTH_SOCK` and the 1Password agent. The `scratchpad` command reads these Scratchpad environment variables:

| Variable                    | Purpose                                                                     |
| --------------------------- | --------------------------------------------------------------------------- |
| `SCRATCHPAD_MIRROR`         | `true` opts this call into repository mirroring                             |
| `SCRATCHPAD_MIRROR_PATH`    | Mirror file within the repository; defaults to `scratchpad/decisions.jsonl` |
| `SCRATCHPAD_SERVICE_SOCKET` | A different capture service socket, for isolated tests                      |

## Project context

The `scratchpad` command normally discovers the project from its current directory. A project-scoped tool can supply an optional `workingDirectory` for that call. This does not mutate shared state for other calls.

The server receives resolved identity and Git provenance; a local directory path is not an instruction for the server to read that directory.

## Optional features

Repository mirroring requires permission in both project settings and the per-call opt-in on the Mac. Enabling either alone is insufficient.

AI and embeddings are optional. Provider configuration must not become a requirement for capturing or retrieving records. Cross-project automated analysis needs its own explicit policy; it should not silently broaden data exposure.

## Change project settings in the dashboard

Open **Projects → Project settings** to choose enabled capture types, allow repository mirroring, select the record types to mirror, and set AI and cross-project analysis participation. **Edit project** changes display information and classification. These are versioned updates; a stale edit returns a conflict instead of overwriting a newer change.

Disabling a capture type prevents new captures of that type; it does not erase historical records. A successful retry still refers to the original capture. Mirror permission is checked against current project settings, so a local opt-in cannot bypass an owner restriction.

Enable global AI and project AI participation separately before analysis. See [optional AI](/scratchpad/guides/ai/) for provider, schedule, threshold and analysis settings, and [PostgreSQL](/scratchpad/guides/postgresql/) for server database selection.

Agent hook events are stored only for projects whose `agentEvents` setting is `true`. The dashboard has no switch for it yet; see [agent integration](/scratchpad/guides/agents/).

## Defaults for future projects

Open **Settings → Project defaults** to choose enabled capture types, mirror permission/types, AI permission and cross-project participation for future personal/internal projects. Saving defaults does not rewrite existing project settings. External projects start with mirroring, AI and cross-project analysis disabled even when normal-project defaults permit them. The versioned API rejects stale edits.
