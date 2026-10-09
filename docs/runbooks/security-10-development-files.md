# Protect development runtime state

Issue #10 keeps private SQLite state behind the API even when a developer
deliberately exposes Vite beyond loopback.

Use the Node version in `.node-version` and the existing installed dependencies.

1. Run `node --test scripts/development-security.test.mjs` to start disposable
   Vite instances and check loopback defaults, explicit wider binding, ordinary
   assets, and denied synthetic database, companion and secret files.
2. Run `npm run typecheck` and `npm run lint`.
3. Run `npm test` once after the change and `npm run build`.
4. Run `npm run test:e2e -- tests/workflow.spec.ts` to verify normal authenticated
   browser and MCP access. Canonical GitHub Actions additionally verifies
   PostgreSQL and container operation.

The development listener defaults to `127.0.0.1`. Existing Vite secret exclusions
are retained, with additional denials for data directories, SQLite filename
families and the configured database path, including WAL, SHM and journal files.
These denials remain active with an explicit `--host` override.

Use the same absolute database path for development and administrator commands.
Changing it does not relocate or delete existing data. Keep private files outside
served workspaces where practical. Development is not the production ingress;
local regression success does not establish deployed acceptance.
