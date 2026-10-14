# Production deployment on the owner's homelab

The owner instance runs on the owner's homelab Kubernetes. Merging to `main`
deploys it: after **CI** passes on a push to `main`, the **Deploy** workflow
calls the reusable `alexcatdad/ci-actions` rollout workflow (pinned by commit)
on a dedicated homelab runner. Private host details, the database restore
procedure and the operator steps live in the homelab infrastructure
repository, not here.

## What one deployment does

1. Builds the merged commit from `Dockerfile` with the homelab's shared
   BuildKit and pushes it to the homelab registry by digest.
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

- Schema changes run at startup (`store.ts`). A rollback runs the previous
  image against the current schema, so keep schema changes additive or ship
  them with an explicit down path; otherwise the database restore in the
  homelab runbook is the recovery.
- Releases that change the MCP or SSH proof protocol (for example security
  ticket 13, proof v2) need the matching MCP client installed by the owner at
  the same time.
- `SCRATCHPAD_PUBLIC_URL`, `SCRATCHPAD_TRUSTED_PROXIES` and the optional GitHub
  sign-in settings come from the homelab's private runtime configuration, not
  from this repository.
