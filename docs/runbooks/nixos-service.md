# Run the server as a NixOS service

The flake exports `packages.<system>.scratchpad-server` (the Go server with the
dashboard embedded), `packages.<system>.default` (the same server wrapped with
OpenSSH on its path) and `nixosModules.default`, which runs one unit,
`scratchpad.service`. `packages.<system>.scratchpad-dashboard` is the
dashboard's static build; it is a build input of the server and nothing runs it.
No output contains or starts a Node runtime.

## Enable

```nix
{
  inputs.scratchpad.url = "github:alexcatdad/scratchpad";
  # modules = [ scratchpad.nixosModules.default ];

  services.scratchpad = {
    enable = true;
    publicUrl = "https://memory.example.com";
    trustedProxies = [ "127.0.0.1" ];
    encryptionKeyFile = "/run/agenix/scratchpad-installation-key";
    github.clientId = "<oauth client id>";
    environmentFile = "/run/agenix/scratchpad-env";
  };
}
```

`environmentFile` holds `SCRATCHPAD_GITHUB_CLIENT_SECRET=...` (and
`SCRATCHPAD_DATABASE_URL=...` for PostgreSQL). Neither it nor the key may live in
the Nix store. Adopt the existing installation key; do not generate a new one.
GitHub OAuth callback setup is in [github-oauth.md](github-oauth.md).

## Build

`flake.lock` pins nixpkgs. After changing Go or npm dependencies, set the
matching `vendorHash` (`nix/package.nix`) or `npmDepsHash` (`nix/dashboard.nix`) to
`lib.fakeHash`, run `nix build .#scratchpad-server .#scratchpad-dashboard`, and
copy the reported hash back. `npmDepsHash` changes whenever `package-lock.json`
does. The server builds with Go 1.27 (`buildGo127Module`) and copies the
dashboard build into `internal/httpapi/dashboard/dist` before compiling, where
`go:embed` picks it up.

## State and administration

The API server runs as the static `scratchpad` system user. SQLite lives in
`/var/lib/scratchpad` (mode 0700). The server refuses storage owned by another
user or reached through a symlink, so run administrative commands as that user
with the service environment, for example a backup before upgrades:

```sh
sudo systemd-run --pty --wait --collect --uid=scratchpad --gid=scratchpad \
  -p EnvironmentFile=/run/agenix/scratchpad-env \
  -E SCRATCHPAD_DATABASE_PATH=/var/lib/scratchpad/scratchpad.sqlite \
  $(systemctl show -P ExecStart scratchpad | grep -o "/nix/store/[^ ]*/bin/scratchpad-server") \
  backup /var/lib/scratchpad/backup.sqlite
```

Use the same form for `migrate`, `setup` and `recover`.

## Single-unit deployment

Hosts that run Scratchpad as one service outside the NixOS module, such as the
homelab deploy slot, use the flake's `#default` output and start
`bin/scratchpad-server serve`. The wrapper puts OpenSSH on the path for
`ssh-keygen`. Administrative commands use the same binary: `migrate`, `setup`,
`recover` and `backup`.

`bin/scratchpad-stack` remains only as a compatibility name for units written
for the retired two-process launcher. It runs the server alone (`serve` when
given no arguments) and unsets `SCRATCHPAD_DASHBOARD_URL` and
`SCRATCHPAD_UI_PORT`, so an existing unit keeps working unchanged. Switch such
units to `bin/scratchpad-server serve` when convenient.

The unit binds only the API port. It no longer needs
`MemoryDenyWriteExecute=false`, which was there for Node's JIT. Health stays
`GET /health`. Configure it with the same environment as the API server unit
above.

## Upgrading from the two-unit layout

Earlier revisions ran a Node dashboard renderer beside the server. No data
migration is involved; remove what referred to the renderer:

- NixOS module: delete `services.scratchpad.dashboard.enable`,
  `dashboard.package`, `dashboard.port` and `services.scratchpad.dashboardUrl`.
  They were removed, and a configuration that still sets one fails evaluation
  with a message saying so. `scratchpad-dashboard.service` disappears on the
  next `nixos-rebuild switch`.
- Hand-written units: stop and disable any `scratchpad-dashboard` unit, and
  drop `After=`/`Requires=` references to it.
- Environment: remove `SCRATCHPAD_DASHBOARD_URL`, `SCRATCHPAD_UI_PORT`,
  `SCRATCHPAD_UI_ONLY` and the renderer's `HOST`/`PORT`.
  `SCRATCHPAD_DASHBOARD_URL` is now development-only; pointing the server at
  the old renderer port makes the dashboard return `503 DASHBOARD_UNAVAILABLE`.
- Port 4000 is no longer bound. Remove probes or firewall rules for it.
- The flake output `bin/scratchpad-dashboard` is gone. The
  `scratchpad-dashboard` package still exists but is only the static build.

## Homelab handoff

The owner-instance cutover procedure is [nixos-homelab-cutover.md](nixos-homelab-cutover.md).
The installer needs these inputs; none belong in this repository.

- **Origin:** keep the existing tailnet HTTPS hostname as `publicUrl`. The
  WebAuthn relying-party ID derives from its hostname, so a changed hostname
  needs the administrator `recover` flow
  ([homelab-migration.md](homelab-migration.md#authentication-and-provider-consequences)).
  Bind `listenAddress` to loopback, set `trustedProxies` to the Tailscale Serve
  proxy address and expose it with Tailscale Serve (not Funnel).
- **Database:** the owner instance runs on PostgreSQL. Supply
  `SCRATCHPAD_DATABASE_URL` in `environmentFile`; it takes precedence over the
  SQLite path. Do not start against an empty database. Follow
  [sqlite-to-postgres.md](sqlite-to-postgres.md) for backups.
- **Key:** deliver the existing installation key to `encryptionKeyFile` (mode
  0600/0400, no group write, no other access). Never generate a new one.
- **GitHub:** `github.clientId` plus `SCRATCHPAD_GITHUB_CLIENT_SECRET` in
  `environmentFile`. The OAuth callback is `<publicUrl>/api/v1/auth/github/callback`.
- **Dashboard:** embedded in the server and served from the same origin. No
  separate unit, port or exposure is needed.
