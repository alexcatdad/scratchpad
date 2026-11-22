---
title: Optional PostgreSQL
description: Select PostgreSQL persistence, move knowledge through export/import and preserve operational state with database backups.
---

SQLite remains the simplest default. The server also supports PostgreSQL with the same authenticated capture, search, history, audit and AI interfaces. The v0.1.3 MVP packages are SQLite-only.

## Select the database

Set the server's `SCRATCHPAD_DATABASE_URL` to a `postgres://` or `postgresql://` connection URL for a dedicated application database. Use exactly the same configuration for administrator commands. Keep the URL/password in your deployment's secret configuration; it is not a dashboard field.

With this variable unset, `SCRATCHPAD_DATABASE_PATH` selects SQLite. **Settings → Profile** and **Settings → About** show the active engine without exposing its connection URL.

The application initializes an empty database. It refuses unsupported or incomplete existing schemas rather than silently modifying an unrelated database. If the application role does not own the tables, run `scratchpad-server migrate` with a migration role before starting the server with the ordinary role. PostgreSQL provides native full-text search; no separate search service or vector database is required. pgvector is needed only for optional [passage retrieval](/scratchpad/guides/optional-extensions/).

The [PostgreSQL operations runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/postgres.md) provides the deployment, database setup, backup and restore procedure. If Scratchpad runs in Docker, the connection hostname must be reachable from that container; `localhost` inside it refers to the application container.

## Move existing knowledge

Use **Settings → Import & export** to export your knowledge, then import it into an authenticated destination instance configured for the other database engine. Projects, identities, original records, revisions, evidence, relationships, audit, owner presentation and derived/curated artifacts retain their source references.

This is a knowledge migration. It does not move authentication credentials, browser and machine sessions, capture retry identities, provider keys, jobs or embeddings. Enroll the destination owner and machine SSH credentials separately, configure its optional provider, and rebuild embeddings when needed. Scratchpad does not perform transparent live migration between engines.

## Back up operational state

Use PostgreSQL's `pg_dump` and `pg_restore` for full operational backup/restore. The `scratchpad-server backup` command is SQLite-only and refuses a PostgreSQL deployment.

Restore into a fresh database with the target application stopped, then verify readiness, browser sign-in, retrieval through the `scratchpad` command and queued work. Preserve the original database for rollback. A full database snapshot contains private records and authentication state; protect it accordingly.

See [installation](/scratchpad/guides/installation/), [configuration](/scratchpad/guides/configuration/) and [optional AI](/scratchpad/guides/ai/) for the surrounding setup.
