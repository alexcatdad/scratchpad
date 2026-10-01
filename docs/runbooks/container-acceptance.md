# Container backup and restore acceptance

This is the production-image acceptance path for owner enrollment, browser operations, real stdio MCP capture, and restoration into a new Docker volume. It uses no production credentials, seeded authentication sessions, host SQLite database, or existing Docker volume.

## Run

Requirements: Docker, Node.js, npm dependencies, Go, Git, OpenSSH, and Playwright Chromium. Port `3100` must be available. From the repository root:

```sh
npm ci
npx playwright install chromium
docker build -t scratchpad:ci .
SCRATCHPAD_E2E_DOCKER=1 npm run test:e2e
```

Linux CI installs browser system libraries with `npx playwright install --with-deps chromium`. An alternative prebuilt image may be selected with `SCRATCHPAD_E2E_IMAGE`; the test never builds or pulls an image implicitly.

The default `npm run test:e2e` still uses the locally built production server with a temporary host SQLite database and a normal restart. Run `npm run build` first for that mode.

## Exact container sequence

`tests/workflow.spec.ts` performs these operations:

1. Generate a unique `scratchpad-e2e-<UUID>` run identity and create an owned named volume.
2. Start the prebuilt image at `127.0.0.1:3100`, with that volume mounted at `/data` and the configured public URL matching the browser origin.
3. Wait for readiness using a 30-second overall deadline and two-second individual request timeouts.
4. Run `docker exec <container> npm run admin -- setup`; consume its token through the browser's real passkey registration flow using a disposable virtual authenticator.
5. Create a project, save mirroring/cross-project/enabled-type settings, reload, and verify persistence plus capture-type filtering.
6. Capture and curate decisions in the browser, add a storage tag, and verify advanced tag filtering for no matches, matching records, and clearing the filter. Add evidence and a relationship, and import a small synthetic historical project-state record. Verify desktop and mobile layouts.
7. Generate an ephemeral SSH key and enroll it through the dashboard's real challenge/proof flow.
8. Launch the built Go MCP executable over stdio. Capture all eight types through the real HTTP API and check retrieval, search, context, history, and relationships.
9. While the container remains running, execute `docker exec <container> npm run admin -- backup /data/backup.sqlite`. The admin command awaits SQLite's consistent online backup and refuses to overwrite an existing destination.
10. Stop and remove the original application container. Create a second, empty named volume. A short-lived helper container mounts the original volume read-only and copies only `backup.sqlite` into the fresh volume as `scratchpad.sqlite`, setting ownership to application UID/GID `1000:1000` and file mode `0600`. No live database or WAL file is copied.
11. Start the same image against the restored volume at the same origin. Start a new MCP process and verify all original IDs, typed content, exact retries, and changed-payload conflicts.
12. Reload the existing browser session and verify curated title, evidence, audit history, imported project state, and project settings. Sign out and authenticate again using the original passkey.
13. Remove only containers and volumes created by this test run and remove its temporary local key/workspace files.

The helper container runs as root only to set the fresh volume's ownership; the application itself runs under UID/GID `1000:1000`. The database and backup stay in Docker-managed volumes throughout the Docker test.

## What a passing run proves

- The built production image supports normal browser setup and key enrollment.
- A central SQLite capture is visible to both browser and a real external MCP process.
- Browser session state, enrolled passkey/SSH credentials, raw records, curated history, evidence, project settings, imports, and persisted retry state survive an actual online backup and fresh-volume restore.
- The restored system accepts both the existing browser session and new passkey/MCP authentication.
- The normal host-server test path remains available and separate from Docker restoration.

This is a local image acceptance test. It does not prove a GHCR publication, signed/notarized release binaries, Homebrew installation, deployment on another host, or restoration of a user's actual historical database. The import fixture is synthetic; real USB Boop import evidence is tracked separately.

## Recorded local verification

The final local run against the frozen application source passed both paths:

- Host production-server workflow: `npm run test:e2e` — **1 passed (10.6 seconds)**, including advanced tag filtering and project-setting persistence.
- Rebuilt Docker image: `scratchpad:ci`, image ID `sha256:5c9e6d970fa029a496b7588ace6f25f39e9e52f7a6c59094c7a196e87d026b62`.
- Fresh-volume backup/restore workflow: `SCRATCHPAD_E2E_DOCKER=1 npm run test:e2e` — **1 passed (53.2 seconds)**, including the same browser checks and real MCP capture/replay.
- Post-run Docker inventory contained no remaining `scratchpad-e2e-*` containers or volumes. Test formatting and static lint passed.

These are local verification results, not evidence of a published container or a completed release.

## Published v0.1.2 verification — 2026-10-01

The [fresh-runner release acceptance](https://github.com/alexcatdad/scratchpad/actions/runs/36853884482) passed the full backup/restore workflow with the public `ghcr.io/alexcatdad/scratchpad:v0.1.2` image and downloaded Linux AMD64 release executable. Both public image platforms passed readiness; Linux ARM64 used QEMU on that runner.

Native ARM64 acceptance on the owner's Mac also passed using the public image and installed Homebrew MCP `0.1.2` (one workflow, 12.0 seconds). The release manifest digest is `sha256:0c463e87093b49cef8d37be49eafa21a789bdbaae3d387c041e4bf9c70745a0e`.

To repeat this check with an installed executable:

```sh
docker pull ghcr.io/alexcatdad/scratchpad:v0.1.2
SCRATCHPAD_E2E_DOCKER=1 \
  SCRATCHPAD_E2E_IMAGE=ghcr.io/alexcatdad/scratchpad:v0.1.2 \
  SCRATCHPAD_E2E_MCP_BINARY="$(brew --prefix scratchpad-mcp)/bin/scratchpad-mcp" \
  npm run test:e2e
```

These runs use disposable credentials and volumes. They establish the published artifacts' workflow behavior, not restoration of a live owner's database or the missing historical scenario facts.
