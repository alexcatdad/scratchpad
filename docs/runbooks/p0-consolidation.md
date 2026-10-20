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

- [x] SP-01: relevant #41 changes merged, valid schemas accepted, altered FTS/RAG
      projections rejected safely, disposable migration/restore verified.
- [x] SP-02: relevant #42 changes merged, Go default image/dev/admin, launcher
      shutdown and credential isolation verified, release packaging retained.
- [x] SP-03: sole Go backend, retained React/MCP/desktop contracts, migrated
      behavior tests, removed unused server dependencies and permanent runtime modes.
- [x] SP-04: #47 deployment ownership merged, image/manifests/migration/probes,
      preserved installation key and credentials, verified disposable restore/rollback.
- [x] SP-05: authoritative PRD/architecture/API/onboarding accurately reflect
      implementation and future GitHub/team identity with permission-aware RAG.
- [x] Required CI and independent reviews pass at the final PR heads.
- [x] Every implementation PR is merged and final main is verified.

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
- Deployment replacement [#51](https://github.com/alexcatdad/scratchpad/pull/51)
  merged as `96e9648b1ec9b5b0db31935ac33a8d83e8e20147`; all required CI checks
  passed. Its manual publication workflow performs no live rollout.
- Schema replacement [#52](https://github.com/alexcatdad/scratchpad/pull/52)
  merged as `9b124b139ffb560c2f08fb3cff8d2bbf908d8056` with required checks
  passing, including PostgreSQL 18. Superseded #41 is closed; its branch is retained.
- The default Go image passed seven actual-container scenarios: SQLite,
  PostgreSQL, pgvector, encrypted-settings restoration, and historical
  TypeScript rollback with preserved authentication on SQLite and PostgreSQL.
  Accepted local image digest:
  `sha256:768567b8d83f357b8831ebb29e328dd481759f78a7c0cb81b452ae4655ad02ff`.
- Seven native Go browser scenarios passed, including passkeys, GitHub OAuth,
  dashboard integration and restoration. The migrated workflow and private-AI
  browser scenarios also passed. The full GitHub lifecycle passed three
  consecutive normal-build SQLite runs, including live setup/recovery, cached
  access, revocation, passkeys and retained memory. Native GitHub browser tests
  passed on SQLite and disposable PostgreSQL 17 after the fixture correction.
- MCP race tests and 31 Swift tests passed, including real collector IPC.
  Generated-client checks and authenticated Go client calls passed. Focused
  schema, numeric identity and historical PostgreSQL rollback race tests passed.
- React/docs builds, frontend/docs typechecks, lint and entrypoint lifecycle
  tests passed with Node 24.21.0 before final source retirement.
- The full post-retirement SQLite backend race suite passed in 799 seconds with
  Node 24.21.0. A prior combined SQLite/PostgreSQL 17 run reached its 20-minute
  timeout and does not count as full acceptance. Canonical PostgreSQL 18 CI
  shards remain required at the final reviewed head.
- The migrated TLS fixtures now isolate their trusted certificate directory
  from database files. A temporary syscall trace identified Go's certificate
  loader opening and closing the database when `SSL_CERT_DIR` included it,
  releasing process-held SQLite locks. The original live recovery assertions
  and four-connection reader pool remain intact; no driver workaround is used.
- Reviewed CI fixtures prebuild Go executables outside browser startup deadlines,
  wait for API and dashboard readiness, and inspect both container log streams.
  Three affected workflow/harness browser tests, targeted typechecks, lint,
  actionlint, shellcheck and Hadolint passed locally.
- At `9044c9ad4694d6f1f6d29436117624248fdc5e0e`, canonical PostgreSQL 18,
  pgvector, Go backend shards/aggregate, MCP, native Go browser/container,
  macOS and synthetic local-capture checks passed. The retained web/Docker
  lanes exposed two fixture prerequisites: the Docker lane also needs a local
  renderer build, and private-AI acceptance must wait for dashboard readiness.
  Those corrections retain the original behavioral assertions and deadlines.
- Runtime, retirement and documentation replacement
  [#53](https://github.com/alexcatdad/scratchpad/pull/53) merged as
  `73f2ea1e532240e968ee489b25bf268437bc12b8`. Its reviewed head
  `964d9e66f5973a5e5c2fcaa60922e6c3008124ed` passed every required check,
  including the complete retained web/Docker lanes, both PostgreSQL 18 backend
  shards, pgvector, Go browser/container, MCP, macOS and local-capture acceptance.
  Independent reviews reported no unresolved findings. Superseded #42 is closed;
  its pre-existing worktree and branch are retained.
- Final combined local acceptance passed 11 retained browser tests with the
  pinned Go 1.27.2 and Node 24.21.0 toolchains. The explicit real-model opt-in was
  skipped; no real-model quality claim is made.
- The merged main tree was verified byte-for-byte against the tested #53 head,
  with both prerequisite changes present and a clean worktree. Disposable
  PostgreSQL, acceptance images, test servers and agent worktrees were removed.
  Five completed agent branches were removed after their history was preserved
  in a verified private bundle. Pre-existing user branches/worktrees are unchanged.

## Production gate

Use [the project-owned deployment procedure](homelab-deploy.md), preserve the
existing installation key/PVC/database/origin, validate a disposable restoration
and retain the old image plus matching snapshot. Source merge, image publication
and live cutover are separate operations. No live migration or credential change
has been performed by this checklist.
