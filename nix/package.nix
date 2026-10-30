# go.mod requires Go 1.27; the nixpkgs default buildGoModule may lag behind.
{ lib, buildGo127Module }:

buildGo127Module {
  pname = "scratchpad-server";
  version = "0.0.0-dev";

  src = lib.cleanSource ../backend;
  subPackages = [ "cmd/scratchpad-server" ];

  vendorHash = "sha256-YjSXQVrBtTw0D/HKflMUL/LJhGSS3pi/fRcf9ulMvRY=";

  env.CGO_ENABLED = "0";
  ldflags = [ "-s" "-w" ];

  meta = {
    description = "Scratchpad central API server";
    mainProgram = "scratchpad-server";
  };
}
