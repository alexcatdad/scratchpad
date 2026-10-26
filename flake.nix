{
  description = "Scratchpad central API server";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" "x86_64-darwin" ];
      forAll = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in {
      packages = forAll (pkgs: rec {
        scratchpad-server = pkgs.callPackage ./nix/package.nix { };
        scratchpad-dashboard = pkgs.callPackage ./nix/dashboard.nix { };
        default = scratchpad-server;
      });

      nixosModules.default = import ./nix/module.nix self;
    };
}
