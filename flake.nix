{
  description = "Scratchpad central API server";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" "x86_64-darwin" ];
      forAll = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
      # Server builds are named by commit date (YYYY.MM.DD) and revision; a
      # dirty tree reports "dirty" so it is never mistaken for a pinned commit.
      date = self.lastModifiedDate or "19700101000000";
      version = "${builtins.substring 0 4 date}.${builtins.substring 4 2 date}.${builtins.substring 6 2 date}";
      commit = self.shortRev or "dirty";
    in {
      packages = forAll (pkgs: rec {
        scratchpad-server = pkgs.callPackage ./nix/package.nix { inherit version commit; };
        scratchpad-dashboard = pkgs.callPackage ./nix/dashboard.nix { };
        # The homelab slot runs bin/scratchpad-stack from this one output: the
        # API server and the loopback dashboard renderer as one unit. `nix run`
        # still starts the server alone.
        default =
          let
            server = pkgs.writeShellApplication {
              name = "scratchpad-server";
              # SSH sign-in proofs are verified with `ssh-keygen -Y verify`.
              runtimeInputs = [ pkgs.openssh ];
              text = ''exec ${pkgs.lib.getExe scratchpad-server} "$@"'';
            };
            dashboard = pkgs.writeShellApplication {
              name = "scratchpad-dashboard";
              text = ''exec ${pkgs.lib.getExe pkgs.nodejs_24} ${scratchpad-dashboard}/apps/web/server.mjs "$@"'';
            };
            # The container's launcher: `serve` (the default) runs the server and
            # the UI-only renderer on 127.0.0.1:$SCRATCHPAD_UI_PORT (default 4000)
            # and stops both when either exits; admin commands go to the server.
            stack = pkgs.writeShellApplication {
              name = "scratchpad-stack";
              text = ''exec ${pkgs.lib.getExe pkgs.nodejs_24} ${scratchpad-dashboard}/scripts/go-dashboard-runtime.mjs ${pkgs.lib.getExe server} "$@"'';
            };
          in
          pkgs.symlinkJoin {
            name = "scratchpad-${version}";
            paths = [ server dashboard stack ];
            meta.mainProgram = "scratchpad-server";
          };
      });

      # Lint and tests, run by `nix flake check` (the homelab build gate).
      checks = forAll (pkgs: import ./nix/checks.nix {
        inherit pkgs;
        inherit (pkgs) lib;
        dashboard = self.packages.${pkgs.stdenv.hostPlatform.system}.scratchpad-dashboard;
      });

      nixosModules.default = import ./nix/module.nix self;
    };
}
