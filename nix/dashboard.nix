# The dashboard as static files (index.html and hashed assets). It is a build
# input of the server package, which embeds it; nothing runs it at runtime.
{ lib, buildNpmPackage, nodejs_24 }:

buildNpmPackage {
  pname = "scratchpad-dashboard";
  version = "0.0.0-dev";

  src = lib.cleanSourceWith {
    src = ../.;
    filter = path: type:
      let name = baseNameOf path;
      in !(builtins.elem name [ "node_modules" "dist" "backend" ".git" "test-results" ]);
  };

  nodejs = nodejs_24;
  npmWorkspace = "apps/web";

  npmDepsHash = "sha256-V0wqGBPOOSE6TELsaV/jCMtuk+rpgYy32CX52wIq/+s=";

  # Vite writes the build where the Go module embeds it from.
  installPhase = ''
    runHook preInstall
    test -f backend/internal/httpapi/dashboard/dist/index.html
    cp -r backend/internal/httpapi/dashboard/dist $out
    runHook postInstall
  '';
  dontNpmInstall = true;

  meta.description = "Scratchpad dashboard static build";
}
