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
        default = scratchpad-server;
      });

      nixosModules.default = import ./nix/module.nix self;
    };
}
