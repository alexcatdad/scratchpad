# Hermetic lint and test checks for `nix flake check`. The homelab builder
# runs that on every push and PR (no network, GOPROXY=off), so every input
# comes from Nix: Go modules through vendorHash, npm packages through the
# dashboard's npmDepsHash. Swift and the network-bound `task security` stay
# local-only. The commands mirror `task lint` and `task test`.
{ lib, pkgs, dashboard }:

let
  src = lib.cleanSourceWith {
    name = "scratchpad-source";
    src = ../.;
    filter = path: type:
      !(builtins.elem (baseNameOf path) [
        "node_modules" "dist" ".git" "test-results" "coverage" ".tools" ".task" ".build" ".swiftpm"
      ]);
  };

  # Coverage floors live in Taskfile.yml so local and Nix runs never drift.
  floor = name: ''"$(sed -n 's/^  ${name}: //p' "$root/Taskfile.yml")"'';

  goCheck = { name, modRoot, vendorHash, test }:
    pkgs.buildGo127Module {
      pname = "scratchpad-check-${name}";
      version = "0";
      inherit src modRoot vendorHash;

      nativeBuildInputs = [ pkgs.golangci-lint ];
      nativeCheckInputs = with pkgs; [ git openssh nodejs_24 ]
        ++ lib.optionals stdenv.hostPlatform.isLinux [ procps ];

      # Lint in the build phase: gofmt, go vet and golangci-lint.
      buildPhase = ''
        runHook preBuild
        export HOME=$TMPDIR GOLANGCI_LINT_CACHE=$TMPDIR/golangci-lint
        root=$(cd "$NIX_BUILD_TOP/$sourceRoot" && pwd)
        unformatted=$(find . -path ./vendor -prune -o -name '*.go' -print | xargs gofmt -l)
        if [ -n "$unformatted" ]; then
          printf 'gofmt needed:\n%s\n' "$unformatted" >&2
          exit 1
        fi
        go vet ./...
        golangci-lint run --config "$root/.golangci.yml" ./...
        runHook postBuild
      '';

      doCheck = true;
      checkPhase = ''
        runHook preCheck
        ${test}
        runHook postCheck
      '';

      installPhase = "touch $out";
    };

  go-mcp = goCheck {
    name = "go-mcp";
    modRoot = "mcp";
    vendorHash = "sha256-d4swp5PnJZrywidSBbgA2mlWLcwWq0Cp5tMOYtPvsog=";
    test = ''sh "$root/scripts/coverage-go.sh" mcp ${floor "MCP_COVERAGE_FLOOR"} ./internal/...,./cmd/...'';
  };

  go-client = goCheck {
    name = "go-client";
    modRoot = "packages/clients/go";
    vendorHash = "sha256-yP83RYgMzkYQRjtWHCO2Jj/YfL11OiGGO/lfTInWWWI=";
    test = "go test -race -count=1 ./...";
  };
in
{
  inherit go-mcp go-client;

  # Light gate: the backend suite is ~300 serial integration tests (~13 min
  # even without -race), too heavy for the shared builder. The check lints and
  # compiles every test binary; `task test` runs the suite with -race and the
  # coverage floor locally.
  go-backend = goCheck {
    name = "go-backend";
    modRoot = "backend";
    vendorHash = "sha256-YjSXQVrBtTw0D/HKflMUL/LJhGSS3pi/fRcf9ulMvRY=";
    test = "go test -count=1 -exec true ./...";
  };

  # npm lint, typecheck and tests over the whole workspace, reusing the
  # dashboard's pinned npm dependencies.
  npm = pkgs.buildNpmPackage {
    pname = "scratchpad-check-npm";
    version = "0";
    inherit src;
    inherit (dashboard) npmDeps;
    nodejs = pkgs.nodejs_24;

    env.ASTRO_TELEMETRY_DISABLED = "1";
    # scripts/backend-shard.test.mjs runs `go test` on a synthetic module.
    env.GOTOOLCHAIN = "local";

    # npm's prebuilt biome binary expects a standard Linux loader.
    nativeBuildInputs = [ pkgs.go_1_27 ]
      ++ lib.optionals pkgs.stdenv.hostPlatform.isLinux [ pkgs.autoPatchelfHook ];
    buildInputs = lib.optionals pkgs.stdenv.hostPlatform.isLinux [ pkgs.stdenv.cc.cc.lib ];
    # npmConfigHook only patches shebangs in the root node_modules; workspace
    # bins (apps/web's TypeScript 7 tsc) still say /usr/bin/env, which the
    # Linux sandbox lacks.
    preBuild = ''
      find . -path ./node_modules -prune -o -type d -name node_modules -prune -print \
        | while read -r dir; do patchShebangs "$dir"; done
    '' + lib.optionalString pkgs.stdenv.hostPlatform.isLinux "autoPatchelf node_modules/@biomejs";

    buildPhase = ''
      runHook preBuild
      export HOME=$TMPDIR GOCACHE=$TMPDIR/go-cache GOPATH=$TMPDIR/go GOPROXY=off
      npm run lint
      npm run typecheck
      npm test
      runHook postBuild
    '';

    installPhase = "touch $out";
  };
}
