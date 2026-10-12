# Opt-in Go runtime image

The Dockerfile supplies an experimental `go-runtime` target containing the Go
backend and retained React renderer. The default final `runtime` target continues
to run TypeScript. Existing Compose installations do not switch runtimes.
Full parity, canonical CI, actual release artifacts and rollback verification
remain prerequisites for supported installation cutover.

Use an explicitly selected local disposable Docker context. Inspect its endpoint
first; do not rely on the current context, which may target a live remote host.

```sh
docker context inspect LOCAL_CONTEXT --format '{{.Endpoints.docker.Host}}'
docker --context LOCAL_CONTEXT build --target go-runtime -t scratchpad-go:local .
docker --context LOCAL_CONTEXT build -t scratchpad-retained:local .
TEST_DOCKER_CONTEXT=LOCAL_CONTEXT TEST_GO_IMAGE=scratchpad-go:local \
TEST_RETAINED_IMAGE=scratchpad-retained:local \
  npx playwright test tests/go-container.spec.ts
```

Use the project's Node version and installed dependencies for the browser test.
The image uses the backend's pinned Go 1.27.2 toolchain, verified against the
[official stable download listing](https://go.dev/dl/), and the existing pinned
Node 24.21.0 renderer image. It runs as UID/GID 1000, owns a private `/data`
directory, exposes port 3000 and keeps rendering on loopback port 4000.
The Go target adds pinned `ca-certificates=20250419~deb12u1` from the
[Debian Bookworm security package](https://packages.debian.org/bookworm/ca-certificates)
so HTTPS clients can use normal system trust. Package selection was also checked
against the image's official Debian repository candidate.

The image entrypoint forwards `migrate`, `setup`, `recover` and `backup` arguments
to Go without starting the renderer. Its default `serve` command runs both
processes. Go owns API, readiness, authentication, storage and workers; the
renderer has explicit UI-only mode and no OAuth, encryption-key or PostgreSQL
configuration. `SCRATCHPAD_UI_PORT` selects its private port. In this combined
image, either child exiting causes the companion to stop and the launcher to
fail, allowing the container runtime to restart it. SIGTERM/SIGINT shut down both
children, with a 15-second fallback deadline. Allow at least 20 seconds for
container termination. This differs from the independently managed two-process
[local dashboard setup](go-dashboard.md), where renderer downtime leaves Go up.

For an isolated development installation, create a fresh named volume and keep
the public URL consistent with its published port:

```sh
docker --context LOCAL_CONTEXT volume create scratchpad-go-development
docker --context LOCAL_CONTEXT run --rm \
  -v scratchpad-go-development:/data scratchpad-go:local setup
docker --context LOCAL_CONTEXT run --name scratchpad-go-development \
  --stop-timeout 20 -p 127.0.0.1:3000:3000 \
  -e SCRATCHPAD_PUBLIC_URL=http://localhost:3000 \
  -v scratchpad-go-development:/data scratchpad-go:local
```

Handle the printed setup/recovery capability privately. Reusable provider tokens
require the retained protected file described in the
[secret configuration runbook](go-secret-configuration.md). Database backups do
not replace that key. PostgreSQL and optional pgvector/RAG use Go's existing
configuration and require a database reachable from the container namespace;
host loopback addresses do not refer to the host from inside the container.

## Verification and recovery boundaries

The native launcher test checks real CLI migration forwarding, HTTP startup,
normal shutdown and renderer port-bind failure with companion cleanup:

```sh
npm run build -w @scratchpad/web
npx playwright test tests/go-runtime.spec.ts
```

The image workflow creates only randomly named test containers and volumes in
the explicit context. It enrolls a native Chromium passkey through the actual
dashboard, creates a project and capture, takes a live Go SQLite backup, stops
the source container cleanly, restores the completed snapshot into a fresh
volume, and verifies fresh passkey login, immutable record detail and exclusion
of a post-backup capture at the same
public origin. Go container stops must exit successfully. Test-owned resources
are removed afterward.

With `TEST_RETAINED_IMAGE`, a second browser scenario starts the actual retained
TypeScript image, enrolls and captures, then takes its online schema-1 snapshot
before Go starts. It upgrades the source volume with Go, proves fresh passkey
login and a new Go capture, and verifies TypeScript readiness and API refusal on
the upgraded volume. Restarting Go proves refusal did not damage the source.
Restoring the pre-upgrade snapshot into a fresh volume recovers independent
browser login and the original capture through TypeScript, while excluding later
writes. No migration marker editing or destructive down migration is used.

The retained npm wrapper can report exit 1 when Docker sends SIGTERM. The fixture
accepts that status only with the wrapper's explicit `signal SIGTERM` log; Go
still requires exit 0. This verifies the selected images' data/authentication
behavior, not a new termination guarantee for the retained wrapper. Missing
explicit image/context configuration skips the corresponding fixture. Local
image verification does not establish published-release compatibility,
PostgreSQL image acceptance or deployed rollback.

Before any real upgrade, retain the pre-upgrade operational backup, exact old
image and configuration, and the installation key. Go upgrades schema 1 to 2;
the retained TypeScript image rejects schema 2. Rollback requires a verified
schema-1 snapshot in a fresh stopped-service volume, as described in the
[migration runbook](go-backend-migrations.md). Never delete migration markers or
run both runtimes against the same upgraded database.
