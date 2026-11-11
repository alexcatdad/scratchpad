# go.mod requires Go 1.27; the nixpkgs default buildGoModule may lag behind.
{ lib, buildGo127Module, dashboard, version ? "dev", commit ? "" }:

buildGo127Module {
  pname = "scratchpad-server";
  inherit version;

  src = lib.cleanSource ../backend;
  subPackages = [ "cmd/scratchpad-server" ];

  vendorHash = "sha256-YjSXQVrBtTw0D/HKflMUL/LJhGSS3pi/fRcf9ulMvRY=";

  # The binary embeds the dashboard's static build and serves it itself.
  preBuild = ''
    test -f ${dashboard}/index.html
    cp -r ${dashboard} internal/httpapi/dashboard/dist
  '';
  # Vendoring does not depend on the dashboard.
  overrideModAttrs = _: { preBuild = ""; };

  env.CGO_ENABLED = "0";
  ldflags = [ "-s" "-w" "-X main.version=${version}" "-X main.commit=${commit}" ];

  meta = {
    description = "Scratchpad central API server with the embedded dashboard";
    mainProgram = "scratchpad-server";
  };
}
