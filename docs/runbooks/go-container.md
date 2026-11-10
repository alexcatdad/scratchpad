# Go runtime image

The final `runtime` target is one Go binary that embeds the React dashboard as
static files; `go-runtime` names the same image explicitly. It contains no Node
runtime. Rebuilding an
existing Compose installation therefore requires the reviewed migration procedure
before replacing its container. Code defaults do not authorize a live cutover.
Rollback verification extracts a pinned historical Git revision into a private
temporary build context. The current Dockerfile contains no TypeScript backend
target, source implementation or backend-only runtime dependencies.
Canonical CI, release artifacts and deployed acceptance remain separate gates.

Use an explicitly selected local disposable Docker context. Inspect its endpoint
first; do not rely on the current context, which may target a live remote host.

```sh
docker context inspect LOCAL_CONTEXT --format '{{.Endpoints.docker.Host}}'
docker --context LOCAL_CONTEXT build --target go-runtime -t scratchpad-go:local .
TEST_DOCKER_CONTEXT=LOCAL_CONTEXT bash scripts/build-rollback-fixture.sh scratchpad-retained:local
TEST_DOCKER_CONTEXT=LOCAL_CONTEXT TEST_GO_IMAGE=scratchpad-go:local \
TEST_RETAINED_IMAGE=scratchpad-retained:local \
  npx playwright test tests/go-container.spec.ts
```

Use the project's Node version and installed dependencies for the browser test.
The build has three stages. A pinned Node 24.21.0 stage builds the dashboard to
static files; a stage with the backend's pinned Go 1.27.3 toolchain, verified
against the [official stable download listing](https://go.dev/dl/), compiles
the server with those files embedded and fails if `index.html` is missing; the
final stage is `debian:bookworm-slim` with the binary. The final stage adds
`openssh-client`, because the server runs `ssh-keygen` to verify SSH sign-in
proofs, and pinned `ca-certificates=20250419~deb12u2` from the
[Debian Bookworm security package](https://packages.debian.org/bookworm/ca-certificates)
so HTTPS clients can use normal system trust. It runs as UID/GID 1000, owns a
private `/data` directory and exposes port 3000. Nothing listens on port 4000.

The entrypoint is `scratchpad-server` and the default command is `serve`, so
`migrate`, `setup`, `recover` and `backup` are passed as container arguments or
through `docker exec <container> scratchpad-server <command>`. Go serves the
API, readiness and the [embedded dashboard](go-dashboard.md). SIGTERM/SIGINT
stop the single process; allow at least 20 seconds for container termination.
The image has no HTTP client, so its health check reads `/ready` through bash's
`/dev/tcp`. `scripts/docker-smoke.sh` checks readiness, the authenticated
boundary, the embedded dashboard and a restart, and fails if the image contains
`node` or the container runs more than the server process.

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

The native runtime test builds the server with the dashboard embedded, runs a
CLI migration, then checks the dashboard document and asset headers, caching,
`404` for every other path, the access log and a clean shutdown:

```sh
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
The PostgreSQL rollback variant takes an uncompressed native schema-1 dump before
Go starts, applies the same upgrade/refusal/source-preservation checks, and
recovers through the retained image after native restore into a fresh database.

The retained npm wrapper can report exit 1 when Docker sends SIGTERM. The fixture
accepts that status only with the wrapper's explicit `signal SIGTERM` log; Go
still requires exit 0. This verifies the selected images' data/authentication
behavior, not a new termination guarantee for the retained wrapper. Missing
explicit image/context configuration skips the corresponding fixture. Local
image verification does not establish published-release compatibility,
or deployed rollback.

The PostgreSQL image scenario creates an unpublished PostgreSQL 18.6 container
on its own network and a private named database volume. Go receives the
connection URL through the process environment; passwords are not client
arguments or printed archive contents. Matching `pg_dump` and `pg_restore`
clients run inside that owned database container. The workflow dumps the live
source, acknowledges a later capture, stops Go, restores the archive into a fresh
database and restarts the actual Go image at the same public origin. Native
browser login, exact original record-response equality and exclusion of the
post-backup capture must pass. The source database and archive are not rewritten
to downgrade a schema. This verifies ordinary PostgreSQL image recovery;
deployed acceptance remains separate.

The pgvector scenario uses the same pinned pgvector 0.8.7/PostgreSQL 18 image as
canonical CI and installs the extension in its owned source database before Go
starts. An unpublished synthetic provider returns one fixed vector per requested
input. The workflow opts the project into AI, enables the Go RAG runtime, waits
for complete passage coverage, and records the cited passage identity before
dumping. External provider counters must show no re-embedding during restoration.
Native restore must retain complete semantic coverage, the same passage
identity and immutable source URL/text. An explicit crash of the provider must report semantic
unavailability while retaining cited lexical evidence. These checks prove actual
image/extension/index/archive wiring, not representative retrieval quality or
real embedding-model interoperability.

The encrypted-provider image scenario uses a private Docker network and a
synthetic provider without published ports. Correct and wrong installation keys
live in separate protected named volumes mounted read-only outside the database
volume. It saves a reusable synthetic token through the authenticated API,
verifies provider connectivity, and checks that the completed SQLite archive
excludes the token's plaintext. Its PostgreSQL variant uses an uncompressed
native archive and the same mounted-key failure/recovery and secretless-provider
checks. After restoration, the retained key permits
provider calls. Wrong and missing keys return `SECRET_UNAVAILABLE` without
dispatching requests; original reads and new captures remain available. Returning
the retained key restores connectivity without replacing settings or history.
The owner can also remove the unusable stored token while the key is missing;
the same provider then operates without a secret, with outbound calls verified
to omit authorization.
Test-owned key volumes, provider containers and network are removed afterward.
This verifies application recovery with synthetic data, not protection against
compromise of the running container or real-provider interoperability.

Before any real upgrade, retain the pre-upgrade operational backup, exact old
image and configuration, and the installation key. Go upgrades schema 1 to 2;
the retained TypeScript image rejects schema 2. Rollback requires a verified
schema-1 snapshot in a fresh stopped-service volume, as described in the
[migration runbook](go-backend-migrations.md). Never delete migration markers or
run both runtimes against the same upgraded database.
