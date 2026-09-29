# Development

## Scope and ownership

Implement the accepted PRD and addenda using npm workspaces: `apps/web` for TanStack Start and API, `apps/docs` for Astro/Starlight, and `mcp` for the Go stdio server. Initial parallel ownership: server agent owns `apps/web/src/server`; MCP agent owns `mcp`; docs agent owns `apps/docs`; primary agent owns integration, UI, root tooling and CI.

## Procedure

1. Inspect Git status, read canonical specs and contributor instructions.
2. Verify latest stable dependency versions from npm/Go registries and official documentation; pin selected versions. Use Node 24's latest LTS patch as specified by the architecture.
3. Install with `npm ci`; work on the web app with `npm run dev`, docs with `npm run docs:dev`.
4. Run `npm run lint`, `npm run typecheck`, `npm test`, `npm run build`; run `go test -race ./...` and `go vet ./...` inside `mcp`.
5. Build and exercise the local container with `docker compose up --build`. Use disposable persistent volumes for integration tests, never a real user's data.
6. Verify browser and real stdio MCP flows, including auth, capture/retrieval, failure behavior and persistence across restart.
7. Commit coherent checked changes often; run GitHub Actions against the committed revision. Pages is a static public presentation/docs target, never the private application or its data.

## Completion evidence

Track verified commands and outstanding work in handoffs and decision records. The initial repository had only six documentation files and two local commits; GitHub repository was public and empty when implementation began. No release artifact or deployment exists merely because a workflow is present.

## Dashboard integration checkpoint — 2026-09-29

The production web build and disposable Playwright workflow passed locally. Run `npm run build -w @scratchpad/web` followed by `npm run test:e2e` to exercise virtual passkey enrollment, browser capture and editing, SSH key enrollment, the real stdio MCP binary, and persistence across a server restart. The test creates and removes its own database and credentials; screenshots remain ignored under `test-results/`.

Docker smoke verification of the final startup command, remote GitHub Actions runs, and live Pages deployment are still pending. This checkpoint does not establish full MVP or deployment acceptance.
