# Run the server as a NixOS service

The flake exports `packages.<system>.scratchpad-server` (the Go server only) and
`nixosModules.default`. The dashboard renderer runs as a second loopback unit
(`dashboard.enable`, default true) that the API server proxies.

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
copy the reported hash back. The server builds with Go 1.27 (`buildGo127Module`).

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

## Homelab handoff

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
- **Dashboard:** `scratchpad-dashboard` runs on `127.0.0.1:4000` with no credentials; the API server proxies it. No separate exposure is needed. Set `dashboard.enable = false` for API-only.
