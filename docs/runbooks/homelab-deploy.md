# Production deployment on the owner's homelab

The owner instance runs on the owner's homelab Kubernetes. Merging to `main`
deploys it: after **CI** passes on a push to `main`, the **Deploy** workflow
calls the reusable `alexcatdad/ci-actions` rollout workflow (pinned by commit)
on a dedicated homelab runner. Private host details, the database restore
procedure and the operator steps live in the homelab infrastructure
repository, not here.

## What one deployment does

1. Builds the `go-runtime` stage (the default image) of the merged commit's
   `Dockerfile` with the homelab's shared BuildKit and pushes it to the homelab
   registry by digest. Production runs it with the operator-owned Go command;
   the homelab runtime contract refuses rollouts that would leave Go.
2. Waits for a host-side logical dump of the production PostgreSQL database.
   The runner has no database credential; if the dump is refused (for example
   during the nightly backup window) nothing is changed and the run fails.
3. Changes only the container image of the existing Deployment. Command,
   credentials, volumes, probes and resources stay operator-owned, and an
   admission policy rejects anything else from CI.
4. Waits for the rollout (the Deployment uses `Recreate`: Scratchpad and its
   MCP endpoint are unavailable for roughly the startup time, typically under
   a minute), then requires `GET /ready` to answer 200 with
   `"database":"postgres"`.
5. On any failure after the image change, restores the previous image and
   waits for it to become available again.

`deploy/app.json` describes the target. The runner refuses every job except
this repository's `.github/workflows/deploy.yml` on `main`, started by
`workflow_run` or an owner `workflow_dispatch`; pull requests never reach it.

## Redeploy or roll back by hand

Run **Deploy** from `main` with `image` set to a digest-pinned image, for
example the `previous` value in an earlier run's summary. An empty `image`
rebuilds `main`. Only the repository owner can dispatch it.

## Compatibility notes for each release

- Schema changes run at startup (`store.ts` for the TypeScript runtime,
  `backend/internal/persistence` for Go). A rollback runs the previous image
  against the current schema, so keep schema changes additive or ship them
  with an explicit down path; otherwise the database restore in the homelab
  runbook is the recovery.
- Switching the Deployment command from the TypeScript runtime to the Go
  runtime is an operator step, not a CI deploy. It is one-way: Go migrates the
  schema past version 1, which the TypeScript runtime refuses, and converts
  stored provider secrets with the installation key. After the switch, the
  automatic rollback only helps between Go images; returning to TypeScript
  needs the pre-switch database dump. See
  [go-backend-migrations.md](go-backend-migrations.md) and
  [go-secret-configuration.md](go-secret-configuration.md).
- Production connects as an application role that reads and writes data but
  does not own the tables, so the Go server cannot apply schema migrations at
  startup. Before CI deploys a release that adds a Go migration, the homelab
  operator runs `scratchpad-server migrate` from that image on the host as the
  table-owning role. Without that step the new pod fails readiness with
  `DATABASE_NOT_READY (permission, SQLSTATE 42501)`, the migration rolls back
  and CI restores the previous Go image. Startup failures name a class
  (`permission`, `authentication`, `connection`, `schema`, `storage`,
  `database` or `unknown`, with the PostgreSQL SQLSTATE when there is one) and
  never the database URL or credentials. PostgreSQL URL `options` keep their
  spaces, so `options=-c role=<owner>` selects the owning role.
- Releases that change the MCP or SSH proof protocol (for example security
  ticket 13, proof v2) need the matching MCP client installed by the owner at
  the same time.
- `SCRATCHPAD_PUBLIC_URL`, `SCRATCHPAD_TRUSTED_PROXIES` and the optional GitHub
  sign-in settings come from the homelab's private runtime configuration, not
  from this repository.
