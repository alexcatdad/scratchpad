# Single-owner Docker deployment

## Scope

Run Scratchpad as one owner's private instance. The checked-in Compose service builds locally, binds `127.0.0.1:3000`, and persists SQLite at `/data/scratchpad.sqlite` in its named `scratchpad-data` volume. Published images are available from GHCR; the owner’s existing installation uses v0.1.3. A locally built development image is a separate acceptance candidate and must not be described as a published release.

The host needs Docker Compose, storage for the persistent volume, and a TLS reverse proxy for access beyond localhost. GitHub Pages serves public documentation and cannot host the application.

## Local first-owner setup

Run from the repository root:

```sh
docker compose up --build -d
docker compose exec scratchpad npm run admin -- setup
```

Open <http://localhost:3000>, enter the one-use token, and register a passkey. Generate the token against the running service so it uses the same volume and public URL. The token expires after 15 minutes; generating a replacement invalidates the previous token.

```sh
docker compose ps
docker compose logs --tail=100 scratchpad
curl --fail http://localhost:3000/ready
```

Readiness shows whether the service can use its database. It does not prove passkey enrollment, MCP connectivity, or the full product workflow. Verify those through the browser and a real MCP client.

## HTTPS on an existing host reverse proxy

Choose the long-lived hostname before enrolling passkeys. Set `SCRATCHPAD_PUBLIC_URL` to its exact HTTPS origin through the shell or Compose's `.env` file, for example `https://memory.example.com`. Do not include a path prefix.

Keep the application bound to loopback. Configure your existing host reverse proxy to terminate TLS and forward that hostname to `127.0.0.1:3000`, preserving the browser's Host and Origin. A minimal Caddy site block, if Caddy is already your proxy, is:

```caddy
memory.example.com {
  reverse_proxy 127.0.0.1:3000
}
```

The hostname, DNS, firewall, and certificate must be configured for your environment. A proxy running inside another container cannot reach the host service at its own `127.0.0.1`; use an explicitly configured host gateway or a private Docker network instead. Do not expose port 3000 publicly as a substitute for TLS.

Apply the configured origin:

```sh
docker compose up -d
```

Then enroll through the HTTPS URL, or verify an existing credential against the unchanged origin. Remote plain HTTP is rejected. Browser passkey verification and write-origin checks use the configured URL; proxying under an unrelated public origin will fail. MCP uses that same HTTPS server origin as `SCRATCHPAD_URL`.

## Online operational backup

The administrator backup command uses SQLite's online backup API. It can run while the server is active, includes authentication and retry state, creates a new file with restrictive permissions, and refuses to overwrite an existing destination.

From the repository root:

```sh
umask 077
mkdir -p backups
backup_name="scratchpad-$(date -u +%Y%m%dT%H%M%SZ).sqlite"
docker compose exec -T scratchpad npm run admin -- backup "/data/backups/$backup_name"
docker compose cp "scratchpad:/data/backups/$backup_name" "./backups/$backup_name"
chmod 600 "./backups/$backup_name"
```

The copy inside `/data/backups` is still on the same volume. Keep an encrypted off-host copy according to your retention policy. These files contain credentials, sessions, audit history, and private records; native JSON exports deliberately do not contain all of that state.

Do not copy just the live SQLite file while writes continue. A plain file copy can miss WAL contents. Use the online command above or stop the service before copying a complete database state.

## Offline restore

Test restoration on a disposable instance first. Restore a trusted snapshot using the matching application version and public origin. Stop every process that accesses the destination database before replacing it. Do not run two live instances against the same SQLite volume.

The following procedure uses a **fresh named volume** and retains the original volume unchanged for rollback. Replace the example backup filename with the chosen snapshot. Take a fresh online backup first if the current instance is still working. The commands create a separate Compose override; do not overwrite an existing override file.

```sh
test ! -e compose.restore.yaml
image_id=$(docker compose images --quiet scratchpad)
test -n "$image_id"
restore_volume="scratchpad-restored-$(date -u +%Y%m%dT%H%M%SZ)"
docker volume create "$restore_volume"
docker compose stop scratchpad
docker run --rm --user 0:0 --entrypoint sh \
  --mount "type=volume,src=$restore_volume,dst=/restore" \
  --mount "type=bind,src=$PWD/backups/chosen-snapshot.sqlite,dst=/snapshot.sqlite,readonly" \
  "$image_id" -c '
    set -eu
    test -s /snapshot.sqlite
    test ! -e /restore/scratchpad.sqlite
    cp /snapshot.sqlite /restore/scratchpad.sqlite
    chown 1000:1000 /restore /restore/scratchpad.sqlite
    chmod 700 /restore
    chmod 600 /restore/scratchpad.sqlite
  '
cat > compose.restore.yaml <<EOF_OVERRIDE
services:
  scratchpad:
    volumes:
      - restored-data:/data
volumes:
  restored-data:
    external: true
    name: $restore_volume
EOF_OVERRIDE
docker compose -f compose.yaml -f compose.restore.yaml up -d
curl --fail http://localhost:3000/ready
```

Continue using **both Compose files** for all commands against the restored instance, including `exec`, `stop`, backups, and upgrades. Record this selection in the deployment configuration. Running default `docker compose up` without the override would reconnect the original volume; that is a rollback action, not a routine restart. The backup is a self-contained SQLite database; do not copy old `-wal` or `-shm` files into the fresh volume.

Verify browser sign-in, project/record counts, a known decision chain, and real MCP retrieval. A restored backup also restores the authentication state at its snapshot time: review credentials and revoke anything that should no longer be valid. Administrator recovery (`npm run admin -- recover`) can replace a lost passkey and invalidate current sessions without deleting records.

The application must own both the restored database file and its volume directory. SQLite creates WAL and shared-memory files beside the database; assigning ownership only to the database file can cause `SQLITE_READONLY_DIRECTORY` even when the file itself is writable.

Native JSON import/export is a separate knowledge migration feature. Importing an export into a new instance does not restore browser credentials or agent sessions.

## Upgrade and rollback

1. Record the currently deployed source commit and image ID (`docker compose images`). Keep that source revision and image available.
2. Create an online backup and verify it can be opened in a disposable restore environment.
3. Review the target version's release notes and migration requirements. Build the target source with `docker compose build` before stopping the running service.
4. Run `docker compose up -d` to apply the new image, then check readiness, browser sign-in, a known record, MCP capture/retrieval, and restart persistence.
5. If rollback is needed, stop the new service, return to the recorded source/image, and restore the pre-upgrade database snapshot offline. Preserve the failed instance data for diagnosis before replacement.

Do not assume an older server understands a database migrated by a newer server. Restoring the earlier code alone is insufficient; retain the matching pre-upgrade database. Keep the same Compose project name or explicit volume identity so an upgrade does not appear to lose data by attaching a new empty volume. `docker compose down --volumes` destroys the named data volume and is not part of normal upgrade, restart, or rollback.

## Release distribution

The [release runbook](release.md) covers tagged native builds, Apple signing/notarization, GHCR publication, and Homebrew delivery. The published `v0.1.2` native packages, anonymous image pulls, Homebrew installations and disposable backup/restore workflow passed release acceptance. See the [readiness ledger](mvp-readiness.md) for the exact artifacts and run evidence. Verify enrollment, MCP access and backups in your own deployment.
