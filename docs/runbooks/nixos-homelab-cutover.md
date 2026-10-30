# Cut the owner instance over to the NixOS service

Handoff for the homelab agent. The owner requested this cutover on 30 October
2026: run the owner instance as the native NixOS service from
[nixos-service.md](nixos-service.md) instead of a container, VM or pod. Keep
host inventory, secret values, database URLs and evidence in the private
infrastructure repository, never here.

Last recorded live shape: the published v0.3.0 (TypeScript) image on PostgreSQL
16, behind Tailscale Serve, with the database converted on 2 October 2026
([sqlite-to-postgres.md](sqlite-to-postgres.md)). Confirm it in preflight; if
reality differs, stop and report rather than adapting the plan silently.

This is a runtime switch, not an image update. Go migrates the schema beyond
what v0.3.0 accepts, so after migration the old runtime cannot be pointed back
at the same database ([go-backend-migrations.md](go-backend-migrations.md)).

## Inputs

From the owner:

- GitHub OAuth app client ID and secret. The app's callback must be
  `<publicUrl>/api/v1/auth/github/callback` and its homepage `<publicUrl>`.
  GitHub sign-in is optional for startup; passkeys and enrolled SSH keys keep
  working without it.
- Confirmation of the maintenance window (writes stop during cutover).

From the existing installation, without printing values:

- The exact current HTTPS origin. Keep it as `publicUrl`; a different hostname
  breaks passkeys and needs the `recover` flow.
- The PostgreSQL database, its table-owning migration role and the restricted
  runtime role.
- The existing installation key file, if one exists. Never generate a new key
  when one exists. Only if preflight proves none was ever provisioned, create
  one once per [go-secret-configuration.md](go-secret-configuration.md).

## 1. Read-only preflight

1. Record the current runtime (host, image digest, compose/unit), origin,
   Tailscale Serve config, database engine/version, roles, key reference and
   backup schedule.
2. On the NixOS host: confirm loopback ports `3000` and `4000` are free (or pick
   others via `listenAddress` and `dashboard.port`), PostgreSQL is reachable from
   the host, and the agenix/sops secret mechanism is available.
3. Confirm the scratchpad flake builds on the host:

   ```sh
   nix build github:alexcatdad/scratchpad#scratchpad-server github:alexcatdad/scratchpad#scratchpad-dashboard
   ```

   Record the commit it resolved. Do not make the scratchpad input follow the
   host's nixpkgs unless that nixpkgs provides `buildGo127Module` and the build
   above still passes with it.

## 2. Backup and rehearsal

1. Take a custom-format `pg_dump` of the live database with the existing backup
   identity and store it outside the database host under the existing policy.
2. Restore it into a disposable database. Run the Go `migrate` against that
   copy with the migration role, then start a temporary `serve` on another
   loopback port with the runtime role and the same `publicUrl` and key.
   Require `GET /ready` to report `postgres`. Drop the disposable database after.

Stop if either step fails.

## 3. Configure the host

Add the input and module to the host configuration (see
[nixos-service.md](nixos-service.md#enable)):

```nix
services.scratchpad = {
  enable = true;
  publicUrl = "<existing origin>";
  listenAddress = "127.0.0.1:3000";
  trustedProxies = [ "127.0.0.1" ];
  encryptionKeyFile = "<agenix path to the existing key>";
  github.clientId = "<client id>";
  environmentFile = "<agenix path>";
};
```

`environmentFile` holds `SCRATCHPAD_DATABASE_URL` (runtime role) and
`SCRATCHPAD_GITHUB_CLIENT_SECRET`. Leave `github.clientId` unset and omit the
secret if the OAuth app is not ready yet; set both together later. Do not
`nixos-rebuild switch` until step 4.

## 4. Cutover

1. Stop the old runtime and every other writer. Keep its container/CT and data
   intact for rollback, and disable its autostart.
2. Take the final `pg_dump` and verify it restores.
3. Migrate with the table-owning role before the service starts; the runtime
   role cannot run DDL, so `serve` would fail with `DATABASE_NOT_READY`. Supply
   the URL through a protected file, not the command line. PostgreSQL mode
   stores nothing locally, so this one-off may run before the `scratchpad` user
   exists:

   ```sh
   server=$(nix build --no-link --print-out-paths github:alexcatdad/scratchpad/<recorded commit>#scratchpad-server)
   sudo systemd-run --pty --wait --collect \
     -p EnvironmentFile=<protected file with the migration-role SCRATCHPAD_DATABASE_URL> \
     "$server/bin/scratchpad-server" migrate
   ```

   It prints the engine and schema version. Stop on any error.

4. `nixos-rebuild switch`. Check
   `systemctl status scratchpad scratchpad-dashboard` and
   `journalctl -u scratchpad -b`. The first log line reports the address,
   database engine and schema version.
5. Point Tailscale Serve at `http://127.0.0.1:3000` on the existing hostname
   (Serve, not Funnel). Remove the old Serve target.

## 5. Acceptance

All from the owner's workstation against the existing origin:

1. `GET /ready` returns `{"status":"ready","database":"postgres"}`.
2. Browser sign-in with an existing passkey; existing records are visible.
3. If GitHub is configured: Settings offers GitHub linking and the link
   completes for the owner's account.
4. MCP: authenticated project read, then one clearly labeled synthetic capture
   and an identical retry returning the same record.
5. `systemctl restart scratchpad` and a host reboot keep all of the above.
6. Daily `pg_dump` backups continue against the same database.

Readiness alone is not acceptance.

## Rollback

Never start the old runtime against the migrated database. Stop `scratchpad`,
restore the step 4 final dump into a separate database, point the old runtime
at it, restore its Serve target and verify sign-in before reopening access.
Keep the migrated database and the failed service intact for diagnosis. If the
Go service already accepted writes, take a fresh dump first and report; do not
discard new captures silently.

## Report back

Report separately: preflight findings, backup references (location only),
rehearsal result, migration output, deployed commit, each acceptance item, and
anything left undone. Record evidence in the infrastructure repository.

## Completed: 30 October 2026

Preflight found no running Scratchpad workload and the database on PostgreSQL 18
(not 16). The live database migrated to schema version 3 and the service runs
from the pinned flake commit behind `svc:scratchpad`. A first deployment lacked
`ssh-keygen` on the service PATH, so SSH sign-in failed until the module added
`openssh`. Owner sign-in, GitHub linking, desktop SSH connection, MCP read,
capture and identical retry, and a service restart passed. Host reboot
persistence remains pending.
