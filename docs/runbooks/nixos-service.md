# Run the server as a NixOS service

The flake exports `packages.<system>.scratchpad-server` (the Go server only) and
`nixosModules.default`. The dashboard renderer is not packaged; without
`dashboardUrl` the service serves the API only.

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

`vendorHash` in `nix/package.nix` is `lib.fakeHash`. Run
`nix build .#scratchpad-server`, copy the reported hash into the file and rebuild.

## State

SQLite lives in `/var/lib/scratchpad` (mode 0700, dynamic user). Back it up with
`scratchpad-server backup <destination>` before upgrades.
