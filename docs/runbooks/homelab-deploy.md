# Kubernetes deployment lifecycle

Scratchpad owns its image publication workflow, application manifests, migration
instructions and recovery procedure. Publishing an image does not deploy it.
`.github/workflows/deploy.yml` is manually dispatched from `main`, verifies the
selected commit is on `main` and has successful canonical CI, and publishes the
`go-runtime` image to GHCR with provenance and an immutable digest. It has no
cluster credentials and makes no Kubernetes changes.

The manifests under `deploy/kubernetes/` describe a single-replica application,
ClusterIP Service and SQLite PVC. They reference operator-managed ConfigMap and
Secrets; no credential values or Secret objects are committed. The installation
key is mounted read-only from `scratchpad-encryption-key` at the path consumed by
Go. The application runs as UID/GID 1000. A PostgreSQL installation sets
`SCRATCHPAD_DATABASE_URL` in its runtime Secret; SQLite uses the mounted PVC.

## New installation

First provision the namespace, `scratchpad-config` ConfigMap, `scratchpad-runtime`
Secret, `scratchpad-encryption-key` Secret and storage class/PVC policy through
the owner's approved secret and cluster process. The database URL and key must
be supplied through protected files or secret delivery; never put their contents
in this repository or command output. Do not generate an installation key when
adopting an existing installation.

For a new deployment where the existing key is held as a protected local file,
`scripts/adopt-kubernetes-key.sh --check` verifies that the target Secret already
exists and matches the protected file without printing either value. To create a
missing Secret from that existing key file, set `KUBE_CONTEXT`, `KUBE_NAMESPACE`,
`KEY_SECRET` and `KEY_FILE`, then set `SCRATCHPAD_KEY_ADOPTION_APPROVED=yes` and
run `--apply`. It refuses to replace an existing Secret. This is key adoption,
not key generation or rotation.

Select an immutable image reference from a successful Publish application image
run (`ghcr.io/alexcatdad/scratchpad@sha256:...`) and replace the image placeholder
in a reviewed local copy of the Kustomize overlay. Apply only after the owner has
reviewed the target cluster and namespace:

```sh
: "${KUBE_CONTEXT:?Set the reviewed Kubernetes context}"
kubectl --context "$KUBE_CONTEXT" -n app-scratchpad diff -k deploy/kubernetes
kubectl --context "$KUBE_CONTEXT" -n app-scratchpad apply -k deploy/kubernetes
kubectl --context "$KUBE_CONTEXT" -n app-scratchpad rollout status deployment/scratchpad --timeout=10m
kubectl --context "$KUBE_CONTEXT" -n app-scratchpad get service/scratchpad
```

The checked-in image placeholder intentionally cannot start a workload. Do not
apply it unchanged. Keep the configured public URL and trusted proxy list aligned
with the existing private ingress. Readiness is a database connection check, not
proof of authenticated application acceptance.

## Adopt an existing installation

Before applying any workload manifest, record and map the live Deployment's
namespace/name/container, image digest, command/args, Service selector and port,
ConfigMap/Secret references, database endpoint, key Secret name/key, PVC claim,
probes, resources, service account, security context, ingress and rollout
strategy. Use authorized read-only cluster access. The checked-in base is a new
installation shape, not an instruction to replace an existing live spec.

Preserve the current database and role credentials, the existing encryption-key
Secret contents, PVC identity and all unrelated pod settings. Map existing names
to the manifest references in a reviewed local overlay. Never run a Secret
creation command against an existing key name, rotate or regenerate the key, or
recreate/resize/delete a bound PVC as part of adoption. Confirm the mounted key
file still resolves to the retained installation key without printing it. If a
safe mapping cannot be made, stop before applying manifests.

Any TypeScript-to-Go switch requires separate owner approval after Go parity,
release, backup/restore and production acceptance gates pass. Go advances the
schema beyond what the retained TypeScript runtime accepts; do not treat the
first Go image rollout as a routine image update.

For an approved switch, record the existing Deployment revision and its exact
container `command`/`args` and replica count before any change. Enter the approved
maintenance window, fence incoming writes, and stop every existing database
writer as described below. Complete the selected Go image's migration Job against
the existing database using the approved table-owner credential, then run the
guarded rollout helper while the Deployment remains scaled to zero with
`SCRATCHPAD_RUNTIME_SWITCH_APPROVED=yes` and `--switch-to-go`. That operation
sets the digest and removes only the selected container's command/args so the
image's `go-runtime` entrypoint starts. Its preflight requires the existing key
Secret/path and PVC mapping to match. A normal `--apply` preserves command and
args and is for image updates within the already selected runtime. Restore the
recorded replica count only after selecting the Go image and entrypoint, then
wait for readiness and verify authenticated acceptance before reopening access.

To return to the retained TypeScript runtime, first restore the matching
pre-switch database snapshot into a separate database/PVC and prepare a reviewed
runtime configuration pointing at that restored state. Then select the recorded
TypeScript Deployment revision (including its original command/args) and verify
readiness and authenticated reads before routing traffic back. Keep the migrated
database and failed Go workload intact. Never restore the old image against the
Go-migrated schema or replace the installation-key Secret; the same key is
required to read encrypted settings from the restored snapshot.

## Release, migration and rollback

1. Select a published digest from the exact source commit whose CI passed. Review
   the release's schema and runtime notes. Keep the current image digest and a
   verified pre-change database backup. Back up the database using its native
   tool and store the archive outside the database volume under the existing
   encrypted backup policy.
2. Enter an owner-approved maintenance window and fence public/client write
   admission. Record the current Deployment replica count and stop all writers,
   including other workloads or scheduled jobs using the same database. Suspend
   any reconciler that would restart the old runtime during maintenance. For the
   reviewed application Deployment:

   ```sh
   : "${KUBE_CONTEXT:?}" "${KUBE_NAMESPACE:?}" "${KUBE_DEPLOYMENT:?}"
   replicas=$(kubectl --context "$KUBE_CONTEXT" -n "$KUBE_NAMESPACE" \
     get deployment "$KUBE_DEPLOYMENT" -o jsonpath='{.spec.replicas}')
   kubectl --context "$KUBE_CONTEXT" -n "$KUBE_NAMESPACE" \
     scale "deployment/$KUBE_DEPLOYMENT" --replicas=0
   ```

   Verify that its pods have terminated and no other writer remains before
   taking the final consistent pre-change backup or running migrations. Keep
   the old runtime stopped throughout migration and Go selection. A failed
   migration does not authorize restarting an old binary against changed data.

3. For PostgreSQL, run the selected image's one-off migration Job while writers
   remain stopped. The checked-in migration Job requires a non-empty PostgreSQL
   database URL from `scratchpad-migration`; it refuses to run against SQLite.
   Map `scratchpad-migration` to the already approved table-owner database
   credential through the cluster's secret delivery process. Never put its value
   in the Job, command arguments, workflow inputs, output or repository. Render
   the checked-in Job with the chosen immutable image digest in a protected
   temporary file, then create it and wait for completion:

   ```sh
   case "$SCRATCHPAD_IMAGE" in ghcr.io/alexcatdad/scratchpad@sha256:*) ;; *) exit 2 ;; esac
   digest=${SCRATCHPAD_IMAGE##*@sha256:}
   [ "${#digest}" -eq 64 ] || exit 2
   case "$digest" in *[!0-9a-f]*|'') exit 2 ;; esac
   umask 077
   tmp_job=$(mktemp "${TMPDIR:-/tmp}/scratchpad-migrate.XXXXXX.yaml")
   trap 'rm -f "$tmp_job"' EXIT HUP INT TERM
   sed "s|ghcr.io/alexcatdad/scratchpad@sha256:replace-with-published-digest|$SCRATCHPAD_IMAGE|" \
     deploy/kubernetes/migration-job.yaml > "$tmp_job"
   job=$(kubectl --context "$KUBE_CONTEXT" create -f "$tmp_job" -o name)
   kubectl --context "$KUBE_CONTEXT" -n app-scratchpad wait --for=condition=complete "$job" --timeout=10m
   kubectl --context "$KUBE_CONTEXT" -n app-scratchpad logs "$job"
   ```

   Confirm the Job used the intended image digest and exited successfully. If it
   fails or times out, keep the Deployment stopped and follow the migration
   runbook. Record the protected backup reference and migration outcome for the
   rollout preflight. For SQLite, do not use this Job: stop every writer and run
   `scratchpad-server migrate` against the reviewed existing PVC/database path
   in an isolated one-off pod, then preserve that PVC when the Deployment is
   restarted. See [go-backend-migrations.md](go-backend-migrations.md) for the
   SQLite storage and migration boundaries.

4. Change only the existing Deployment container image to the selected digest
   after the owner has approved the live cutover. Retain the same runtime
   settings, installation-key Secret, PVC, Service and ingress. For an approved
   runtime switch use `--switch-to-go` to clear the old command/args. Keep zero
   replicas until this selection is complete; the helper's rollout check at zero
   replicas is not startup acceptance. Then restore the recorded replica count:

   ```sh
   kubectl --context "$KUBE_CONTEXT" -n "$KUBE_NAMESPACE" \
     scale "deployment/$KUBE_DEPLOYMENT" --replicas="$replicas"
   kubectl --context "$KUBE_CONTEXT" -n "$KUBE_NAMESPACE" \
     rollout status "deployment/$KUBE_DEPLOYMENT" --timeout=10m
   ```

   Require `GET /ready` to report the expected database engine.

5. Verify authenticated browser sign-in, an existing record, controlled MCP
   read/capture, and persistence after pod restart before reopening client access
   and resuming the recorded reconciler state.
6. If rollout or acceptance fails before any new writes, restore the recorded
   image digest and wait for readiness. If the new image migrated schema or
   accepted writes, restore the pre-change database into a separate database or
   isolated PVC using the native database restore procedure, then point the
   previous image at that restored state. Do not point an old binary at a newer
   schema or discard post-backup writes silently. Preserve the failed workload,
   data and backup for diagnosis.

For an existing deployment, run `scripts/rollout-kubernetes.sh --check` after
setting its reviewed context, namespace, deployment/container, expected current
image digest, existing key Secret/item/path and existing PVC. It performs read-only
lookups and refuses a mismatched workload or mutable image. Only after the
backup, selected-image migration and owner approval, set
`SCRATCHPAD_BACKUP_REF`, `SCRATCHPAD_BACKUP_VERIFIED=yes`,
`SCRATCHPAD_MIGRATION_STATE=complete`, and
`SCRATCHPAD_CHANGE_APPROVED=yes`, then run `--apply`. The script changes only
that container image and waits for rollout; it does not rotate Secrets, change
PVCs or automatically roll back a potentially migrated database. If rollout
fails, use the compatibility-aware recovery procedure above.

For PostgreSQL use `pg_dump`/`pg_restore` with the existing approved backup
workflow and owner/runtime role separation; see [postgres.md](postgres.md) and
[go-backend-migrations.md](go-backend-migrations.md). For SQLite, stop all writers
and restore the consistent database snapshot into a fresh PVC; see
[deployment.md](deployment.md). Recover encrypted provider settings only with the
same retained installation key documented in
[go-secret-configuration.md](go-secret-configuration.md). Database snapshots do
not include that key.
