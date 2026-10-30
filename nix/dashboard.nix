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

  npmDepsHash = "sha256-+3+s1F1JTPLnVQIMHcNHxGKYrllR8dgcbpOA1lHww4c=";

  # Only the web workspace is built; the dev-only dependencies are pruned.
  installPhase = ''
    runHook preInstall
    npm prune --omit=dev --offline --workspace @scratchpad/web
    mkdir -p $out/apps/web
    cp -r node_modules package.json $out/
    # Drop links to workspaces the renderer does not ship or import.
    find $out/node_modules/@scratchpad -maxdepth 1 -type l ! -name web -delete
    cp -r apps/web/dist apps/web/server.mjs apps/web/package.json $out/apps/web/
    runHook postInstall
  '';
  dontNpmInstall = true;

  meta.description = "Scratchpad dashboard renderer";
}
