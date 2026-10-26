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

## First build

`vendorHash` in `nix/package.nix` and `npmDepsHash` in `nix/dashboard.nix` are `lib.fakeHash`. Run
`nix build .#scratchpad-server .#scratchpad-dashboard`, copy each reported hash into its file and rebuild.

## State

SQLite lives in `/var/lib/scratchpad` (mode 0700, dynamic user). Back it up with
`scratchpad-server backup <destination>` before upgrades.

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
