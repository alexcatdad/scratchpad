---
title: Configuration
description: Understand which settings belong to the server, the project, and the local MCP.
---

Configuration has three distinct owners. Server and MCP settings are separate: the server exposes an origin, while the local MCP connects to it.

| Scope     | Responsibility                                                                                         |
| --------- | ------------------------------------------------------------------------------------------------------ |
| Server    | Public URL, persistent database location, authentication state, instance policy, optional AI providers |
| Project   | Project identity, metadata, repository mirror permission, cross-project analysis permission            |
| Local MCP | Server address, signing-key access, default launch directory, local mirror opt-in                      |

## Server environment

| Variable                   | Default                  | Purpose                                                          |
| -------------------------- | ------------------------ | ---------------------------------------------------------------- |
| `SCRATCHPAD_PUBLIC_URL`    | `http://localhost:3000`  | Browser origin and passkey relying-party configuration           |
| `SCRATCHPAD_DATABASE_PATH` | `data/scratchpad.sqlite` | SQLite file location; use an absolute path for local development |
| `SCRATCHPAD_DATABASE_URL`  | Unset                    | Optional PostgreSQL URL; takes precedence over the SQLite path   |

Use the same values for the server and administrator commands. Changing working directories with the relative database default can create a separate database.

See [MCP setup](/scratchpad/guides/mcp/) for its separate connection and signing variables.

## Optional GitHub sign-in

Current source enables GitHub owner access when both `SCRATCHPAD_GITHUB_CLIENT_ID` and `SCRATCHPAD_GITHUB_CLIENT_SECRET` are configured on the server. Configure the OAuth callback as the exact `SCRATCHPAD_PUBLIC_URL` origin followed by `/api/v1/auth/github/callback`. Use a dedicated OAuth app per instance and keep its secret in private operator configuration. MCP clients need their own SSH signing access, not these OAuth values. Without this pair, passkey-only setup and manual credentials remain available.

Follow the [OAuth operator runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/github-oauth.md). This addition is not present in released v0.3.0 servers.

## Public URL and storage

Use one stable application origin. Browser passkeys bind to the configured relying party and origin; changing the URL is an authentication and migration concern, not just a cosmetic edit.

SQLite is the default database. The database and all authoritative authentication state must live in persistent storage. Restarting the container must preserve records, challenges, sessions, revocations, and retry state.

The GitHub Pages URL serves public documentation only. It is not the server URL used by MCP clients.

## Project context

The MCP normally discovers the project from its process launch directory. A project-scoped tool can supply an optional `workingDirectory` for that call. This does not mutate shared state for other calls.

The server receives resolved identity and Git provenance; a local directory path is not an instruction for the server to read that directory.

## Optional features

Repository mirroring requires permission in both project settings and the local MCP configuration. Enabling either alone is insufficient.

AI and embeddings are optional. Provider configuration must not become a requirement for capturing or retrieving records. Cross-project automated analysis needs its own explicit policy; it should not silently broaden data exposure.

## Change project settings in the dashboard

Open **Projects → Project settings** to choose enabled capture types, enable repository mirroring, select eligible mirror types, and set cross-project analysis permission. **Edit project** changes display information and classification. These are versioned updates; a stale edit returns a conflict instead of overwriting a newer change.

Disabling a capture type prevents new captures of that type; it does not erase historical records. A successful retry still refers to the original capture. Mirror permission is checked against current project settings, so enabling a local flag cannot bypass an owner restriction.

v0.2.0 includes the optional processing engine. Enable global AI and project AI participation separately before analysis. See [optional AI](/scratchpad/guides/ai/) for provider, schedule, threshold and analysis settings, and [PostgreSQL](/scratchpad/guides/postgresql/) for server database selection. Earlier v0.1.3 packages retain their SQLite-only capabilities.

## Defaults for future projects

Open **Settings → New project defaults** to choose enabled capture types, mirror permission/types, AI permission and cross-project participation for future personal/internal projects. Saving defaults does not rewrite existing project settings. External projects start with mirroring, AI and cross-project analysis disabled even when normal-project defaults permit them. The versioned API rejects stale edits.
