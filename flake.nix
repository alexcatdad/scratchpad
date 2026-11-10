{
  description = "Scratchpad server (API and embedded dashboard)";

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
        scratchpad-dashboard = pkgs.callPackage ./nix/dashboard.nix { };
        # One Go binary: the API server with the dashboard's static build
        # embedded. No Node runtime is built into or run by any output.
        scratchpad-server = pkgs.callPackage ./nix/package.nix {
          inherit version commit;
          dashboard = scratchpad-dashboard;
        };
        default =
          let
            server = pkgs.writeShellApplication {
              name = "scratchpad-server";
              # SSH sign-in proofs are verified with `ssh-keygen -Y verify`.
              runtimeInputs = [ pkgs.openssh ];
              text = ''exec ${pkgs.lib.getExe scratchpad-server} "$@"'';
            };
            # Compatibility name for hosts that still start the retired
            # two-process launcher: it now runs the server alone (`serve` by
            # default) and drops the settings that pointed at the renderer.
            stack = pkgs.writeShellApplication {
              name = "scratchpad-stack";
              text = ''
                unset SCRATCHPAD_DASHBOARD_URL SCRATCHPAD_UI_PORT
                if [ "$#" -eq 0 ]; then set -- serve; fi
                exec ${pkgs.lib.getExe server} "$@"
              '';
            };
          in
          pkgs.symlinkJoin {
            name = "scratchpad-${version}";
            paths = [ server stack ];
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
