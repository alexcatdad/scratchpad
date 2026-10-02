# Local Docker to a private Proxmox CT

The owner authorized this migration on 2 October 2026. This procedure is not
evidence of completed deployment. Read the owner's current homelab handoff and canonical
infrastructure repository first. Keep private host inventory and evidence outside
this public repository.

## Recommended shape

Use a dedicated unprivileged Debian CT with one shared CPU, 1 GiB RAM, 256 MiB
swap and an 8 GiB persistent root volume. Recheck capacity under representative
load and retain the homelab's host memory reserve. Select healthy storage and
record this application volume as durable; never place the database in a scratch
or download-cache directory.

Run the published Scratchpad image with Docker Compose inside the CT, keeping
SQLite for the initial move. Enable only Docker's required nesting/keyctl features
and TUN access if the private network agent requires it. Do not use a privileged
CT, disable AppArmor or mount the Proxmox/Docker host socket into the application.

Bind the application to guest loopback and expose a stable private HTTPS origin
through the guest's private-network reverse proxy. Tailscale Serve is an existing
homelab pattern. This is a private tailnet service, not a public Funnel endpoint.
Use a verified, explicit release image and digest; pull finished artifacts rather
than compiling them on the guest.

## Read-only preflight

1. Inspect the running local Compose file, image, origin, resource usage, database
   and WAL sizes, provider endpoint and installed MCP configuration. Do not print
   credentials, session values or the contents of private records.
2. Verify SSH access with the smallest relevant command, then inspect live guest
   IDs, resource limits, host available RAM/pressure, storage capacity, pool health
   and device health. Idle snapshots and averaged history are not peak guarantees.
3. Inspect a comparable unprivileged Docker CT's feature/device configuration and
   runtime. Preserve all unrelated guests and infrastructure checkout changes.
4. Agree the new guest ID, resource budget, durable storage, hostname and private
   exposure before provisioning. Record the approved shape in the infrastructure
   repository's inventory, OpenTofu and Ansible baseline.

## Authentication and provider consequences

Scratchpad derives its WebAuthn relying-party ID from the configured public URL's
hostname. A passkey enrolled for `localhost` will not authenticate at a new HTTPS
hostname. Restoring SQLite preserves its credential rows but does not change the
browser's hostname binding. Use the restored instance's administrator `recover`
flow to authorize a new passkey enrollment at the new URL. The owner completes
registration. Successful recovery revokes previous sessions and adds audited
credential events; this is an expected migration change, not exact preservation
of the browser login. Do not replace the database with a fresh setup.

The MCP stays on the developer workstation. Point `SCRATCHPAD_URL` at the new
HTTPS origin and authenticate with an enrolled SSH key. Existing enrolled keys
remain in the operational snapshot; where none exists, enroll one explicitly
through the new authenticated dashboard. Do not copy the developer's private key
onto the server.

`host.docker.internal` on the guest cannot reach the original Mac's LM Studio.
Keep the Qwen completion and embedding models on the Mac. Before enabling AI,
provide a separately configured private authenticated connection to that service,
such as a private HTTPS proxy or a restricted SSH forwarding arrangement, and
test it from the actual application container. If the Mac/provider is offline,
capture and deterministic retrieval still work. Preserve existing processing
permissions. Older downloaded/generated documents may retain the old origin in
their source citations; regenerate derived documents when needed, never rewrite
original captures.

## Migration and acceptance

1. Create a protected operational SQLite backup with the running application's
   online `admin backup` command. Include its Compose/configuration and keep an
   off-machine copy. Native JSON export does not preserve full operational state.
2. Provision only the approved guest and its baseline. Install the maintained
   Docker/Compose and private-network packages from trusted sources, pinning the
   selected versions. Create the private network identity and stable HTTPS origin.
3. Restore a preliminary backup into an isolated target. Set
   `SCRATCHPAD_PUBLIC_URL` to the exact new HTTPS origin and
   `SCRATCHPAD_DATABASE_PATH` to the restored SQLite file. Keep the application
   directory and file owned by its UID 1000, with restrictive permissions.
4. Verify readiness, migrations, knowledge and audit preservation. Compare record
   IDs, original payload/provenance, revisions, relationships, project settings
   and compatible derived data before allowing target writes. Keep provider
   processing inactive during the preliminary restore.
5. Freeze writes to the old instance and stop its service. Take the final snapshot
   with a one-off administrator backup process mounting that same local volume;
   `docker compose exec` cannot run against a stopped service. Replace only the offline target database and
   discard the preliminary target's authentication/write state. Start the target
   on that final snapshot. Do not leave two independently writable authoritative
   copies running.
6. Complete the new-origin passkey recovery and verify browser sign-in, known
   records, settings and private HTTPS/write-origin enforcement. Recovery-related
   session and audit changes are expected; raw captures remain unchanged.
7. Verify the workstation's actual stdio MCP authentication and retrieval against
   the new URL, then a clearly labeled capture with stable retry behavior. Verify
   restart persistence and backup/restore. Test the optional provider independently
   before any project processing, retaining existing consent.
8. Enable autostart and schedule application-consistent backups with an independent
   off-machine copy and documented retention. Guest snapshots complement the
   SQLite backup; they are not its sole consistency or disaster-recovery proof.
9. Update infrastructure recovery state, the handoff and application runbooks;
   record deployed, healthy, browser/MCP accepted, committed and pushed separately.
   Keep the Mac's stopped instance and protected snapshot available for rollback.

## Rollback

Stop the target before reactivating the Mac. Before any target writes, the retained
local state can resume unchanged at its original origin. After target writes,
transfer a fresh operational target backup back to the offline Mac instead of
silently abandoning new records. Use the same application version, restore file
and directory ownership, restore the original origin/provider configuration and
verify access. Keep private network identities and snapshots protected. Origin
recovery may be needed again because the migrated database revokes old sessions;
verify the available localhost passkey rather than promising transparent rollback.

## References

- [Proxmox container documentation](https://github.com/proxmox/pve-docs/blob/master/pct.adoc)
- [Tailscale in unprivileged LXC](https://tailscale.com/docs/features/containers/lxc/lxc-unprivileged)
- [Tailscale Serve](https://tailscale.com/docs/reference/tailscale-cli/serve)
- [SQLite operations and restore](deployment.md)

## Execution checkpoint: 2 October 2026

The dedicated private CT is deployed with the published v0.3.0 image. The online
preliminary snapshot passed integrity and matched every operational entity.
The source service was stopped before the final snapshot replaced the offline
target database. All four projects, four raw records and their four metadata
rows match the final snapshot and a network-isolated operational restore.
Private HTTPS readiness passed from the workstation after application restart;
a wrong-origin registration request returned403. Provider processing remains
disabled in the migrated owner instance.

Daily application-consistent CT snapshots are scheduled, with30-day retention.
A daily Mac copy has completed through verified SSH, passed SQLite integrity,
and keeps90days. Copies depend on workstation connectivity and existing SSH
authentication. The retained stopped local volume and protected final snapshot
provide rollback state.

Owner recovery requires clicking **Recover access** below the sign-in button,
then entering the private administrator token and completing **Register passkey**.
The regular sign-in screen remains the default for an initialized instance.
Owner recovery and browser access are verified at the new HTTPS origin. The
authenticated dashboard shows all four original records and both passkeys.
MCP SSH-key enrollment and acceptance subsequently passed. The installed stdio
client authenticated, retrieved project context, captured one labeled synthetic
finding, retried with the same identity and retrieved the same record. The
complete CT reboot preserved private HTTPS and MCP access; guest autostart is
enabled. A final backup on the Mac passes integrity and retains every original
project/record/metadata row. The local Docker service remains stopped for rollback.

The Git-derived MCP connection resolved the checkout as a new project identity;
this is separate from the existing manually created project. The acceptance
finding remains clearly labeled synthetic. The test did not change original
captures or enable AI processing. The workstation's stable MCP environment and
launcher are stored with its private deployment files; use the new HTTPS origin
and the existing enrolled public key through the unlocked SSH agent.
