# P0 consolidation and acceptance

This is the resumable source checklist for the P0 consolidation begun on
20 October 2026. It records acceptance requirements and actual evidence, not
production authorization. Historical PRs [#41](https://github.com/alexcatdad/scratchpad/pull/41)
and [#42](https://github.com/alexcatdad/scratchpad/pull/42) predate subsequent
client fixes; reconciliation must preserve those fixes.

## Dependency order

1. SP-04 removes the obsolete automatic rollout dependency and provides the
   project-owned, explicitly gated deployment path.
2. SP-01 validates installed projection schemas before runtime default adoption.
3. SP-02 selects Go in source/default images and development/admin entry points.
4. SP-03 proves public behavioral parity, migrates test consumers and retires
   the TypeScript backend without deleting useful acceptance coverage.
5. SP-05 consolidates current contracts and clearly labels enterprise plans.
6. Verify the integrated main revision after all reviewed PRs merge.

SP-01 and deployment implementation are independent; their tests may run before
the listed merge order. Production cutover is a separately authorized operation.

## Acceptance checklist

- [ ] SP-01: relevant #41 changes merged, valid schemas accepted, altered FTS/RAG
      projections rejected safely, disposable migration/restore verified.
- [ ] SP-02: relevant #42 changes merged, Go default image/dev/admin, launcher
      shutdown and credential isolation verified, release packaging retained.
- [ ] SP-03: sole Go backend, retained React/MCP/desktop contracts, migrated
      behavior tests, removed unused server dependencies and permanent runtime modes.
- [ ] SP-04: #47 deployment ownership merged, image/manifests/migration/probes,
      preserved installation key and credentials, verified disposable restore/rollback.
- [ ] SP-05: authoritative PRD/architecture/API/onboarding accurately reflect
      implementation and future GitHub/team identity with permission-aware RAG.
- [ ] Required CI and independent reviews pass at the final PR heads.
- [ ] Every implementation PR is merged and final main is verified.

## Verification procedure

Use the existing pinned Node and Go versions and locked dependencies. Select
an explicit Docker context before any container work. Never use production
credentials or databases for tests. PostgreSQL fixtures create/drop only their
own random databases; container fixtures must remove owned containers/volumes.

```sh
npm ci
npm run lint
npm run typecheck
npm test
npm run build
npm run sdk:check
npm run sdk:build
npm run sdk:test
go -C backend vet ./...
npm run test:backend
go -C mcp vet ./...
go -C mcp test -race ./...
npx playwright test --project=go-native
```

Supply `TEST_POSTGRES_URL` only for a disposable administrator database with
fixture-creation rights. PostgreSQL 17 evidence does not establish PostgreSQL 18
compatibility. Run the PostgreSQL 18 and pgvector CI lanes and the clean-image
browser/restore lane at the reviewed commit. Use existing Swift IPC/package and
release-validator checks for macOS. Compilation does not prove installed
collector/offline-delivery acceptance.

## Evidence recorded during implementation

- Starting main: `0990411697f9eda7c535acf0d2f0697d7ae6f149`.
- Focused projection checks passed on SQLite and disposable PostgreSQL 17.11 with
  pgvector. The PostgreSQL 18-only constraint fixture was skipped locally.
- React production build and frontend/docs TypeScript checks passed locally with
  Node 24.21.0 before retirement changes.
- Full backend, browser, container, review and merged-main gates remain pending.

## Production gate

Use [the project-owned deployment procedure](homelab-deploy.md), preserve the
existing installation key/PVC/database/origin, validate a disposable restoration
and retain the old image plus matching snapshot. Source merge, image publication
and live cutover are separate operations. No live migration or credential change
has been performed by this checklist.
