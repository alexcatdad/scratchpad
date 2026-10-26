{ lib, buildGoModule }:

buildGoModule {
  pname = "scratchpad-server";
  version = "0.0.0-dev";

  src = lib.cleanSource ../backend;
  subPackages = [ "cmd/scratchpad-server" ];

  # Replace after the first build prints the real hash.
  vendorHash = lib.fakeHash;

  env.CGO_ENABLED = "0";
  ldflags = [ "-s" "-w" ];

  meta = {
    description = "Scratchpad central API server";
    mainProgram = "scratchpad-server";
  };
}
