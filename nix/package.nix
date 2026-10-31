# go.mod requires Go 1.27; the nixpkgs default buildGoModule may lag behind.
{ lib, buildGo127Module, version ? "dev", commit ? "" }:

buildGo127Module {
  pname = "scratchpad-server";
  inherit version;

  src = lib.cleanSource ../backend;
  subPackages = [ "cmd/scratchpad-server" ];

  vendorHash = "sha256-YjSXQVrBtTw0D/HKflMUL/LJhGSS3pi/fRcf9ulMvRY=";

  env.CGO_ENABLED = "0";
  ldflags = [ "-s" "-w" "-X main.version=${version}" "-X main.commit=${commit}" ];

  meta = {
    description = "Scratchpad central API server";
    mainProgram = "scratchpad-server";
  };
}
