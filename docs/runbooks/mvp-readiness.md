# MVP readiness

## Scope and current conclusion

Readiness covers the complete PRD MVP and acceptance scenarios A–G, including capture/retrieval, project boundaries, authentication, operation, import/export, and distribution. AI processing, embeddings, pattern analysis, and PostgreSQL remain post-MVP. A local development slice does not establish release acceptance.

The core implementation and release automation are present. Full MVP acceptance remains open because signed/notarized publication, both GHCR architectures, Homebrew delivery/installation, and final end-to-end acceptance at the release commit still need evidence. Missing signing and release credentials are concrete external blockers.

## Evidence ledger — 2026-09-29

| Requirement                                                                                 | Authoritative evidence                                                                    | Current assessment                                                                                                                                                                                                             |
| ------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Owner enrollment, browser sign-in/recovery, SSH MCP auth, revocation                        | `apps/web/src/server/webauthn.test.ts`, `api.test.ts`, `tests/workflow.spec.ts`           | Baseline integration and the expanded Docker browser/real-MCP flow passed locally; remote CI at `af9e24b` also passed the expanded workflow.                                                                                   |
| Eight typed captures, retry identity, immutable evidence, concurrent edit conflicts         | API/server tests and real Go MCP transport tests                                          | Implemented and covered by automated tests; record exact release-revision results before acceptance.                                                                                                                           |
| Record provenance, evidence, relationship navigation, revisions, and audit UI               | `apps/web/src/components/record-detail.tsx`                                               | Implemented; expanded Docker browser checks passed locally, including title, evidence, audit, imports, and settings.                                                                                                           |
| Enabled capture types, mirroring policy/types, project classification, cross-project policy | `projects.tsx`, `domain.ts`, `imports.test.ts`                                            | Implemented. Cross-project policy does not imply the deferred AI processor exists.                                                                                                                                             |
| Legacy JSONL arrays, source IDs, original line/object preservation, diagnostics             | `imports.ts`, `imports.test.ts`; dashboard Settings import                                | Live server validation imported all 19 current USB Boop entries and 805 larger work-project entries; 800 source IDs retained in the larger set. Original objects/raw lines preserved; reimport after native restore adds zero. |
| Native knowledge export/import and idempotent historical reimport                           | `imports.test.ts`, API tests                                                              | Knowledge migration preserves source evidence and audit history; excludes authentication.                                                                                                                                      |
| Full operational backup and restore                                                         | `Store.backup`, admin `backup`, `imports.test.ts`, [deployment runbook](deployment.md)    | Passed disposable Docker online backup and fresh-volume restore; browser session, fresh MCP authentication, records, and retry identity survived. See [container acceptance](container-acceptance.md).                         |
| Worktrees, multiple projects, restricted repositories, ambiguity                            | `mcp/internal/scratchpad/acceptance_test.go`, [MCP acceptance runbook](mcp-acceptance.md) | Remote Go CI at `af9e24b` passed vet, race tests, and vulnerability checks; tests use real temporary Git repositories and protocol calls.                                                                                      |
| Four native targets and version metadata                                                    | `scripts/release-build.sh`                                                                | All four compiled locally; binary formats inspected, macOS ARM64 version command verified. These local binaries are not signed release artifacts.                                                                              |
| Signing/notarization, checksums, image/tap publication                                      | `.github/workflows/release.yml`, [release runbook](release.md)                            | Automation and lint/formula tests pass. Actual publication blocked by missing credentials.                                                                                                                                     |
| English docs and Pages                                                                      | `apps/docs`, Documentation workflow                                                       | Documentation workflow at `af9e24b` succeeded; the public release guide was inspected after deployment.                                                                                                                        |
| Final exact-source CI                                                                       | GitHub Actions                                                                            | Inspected green CI and Documentation cover `af9e24b22257651bdc041a181c2029209828fbfa`; later history-tooling and ledger edits require their own runs.                                                                          |

Verified remote baseline: [CI run 36564455760](https://github.com/alexcatdad/scratchpad/actions/runs/36564455760), [Documentation run 36564455764](https://github.com/alexcatdad/scratchpad/actions/runs/36564455764). These links are historical evidence, not a claim about a later HEAD.

## Latest local acceptance

The Docker acceptance command `SCRATCHPAD_E2E_DOCKER=1 npm run test:e2e` passed (one workflow, 51.4 seconds). It used the actual image and disposable named volumes, browser passkey/SSH enrollment, all eight real Go stdio captures, an online backup, fresh-volume restore, new MCP process authentication, stable capture IDs/retries, browser session continuity, detail/audit/evidence, synthetic imported state, project settings, logout, and passkey sign-in. Its own containers and volumes were cleaned. This is local operational evidence, not remote release or public-host acceptance.

The server import validation used private real histories locally: 19 USB Boop entries and 805 larger work-project entries, retaining 800 original IDs in the latter. The original JSON objects and raw lines were preserved. Native export/restore followed by identical legacy reimport added zero captures. The larger native export was approximately 13.3 MB, motivating the implemented 64 MiB import HTTP limit (32 MiB JSONL text); other API requests retain an 8 MiB limit. Historical sources themselves were not copied into public fixtures.

## Historical scenario evidence

The optional [private history acceptance procedure](history-acceptance.md) exercises actual HTTP import, search, detail, audit, and context without publishing source text. Scenario C's request/rationale and later refinement were retrieved and reviewed locally. Scenarios A and B remain unproven: the current USB Boop log and 32 relevant Git revisions do not establish the throughput rationale or explicit paused-state reason required by the examples. Additional authoritative source material is needed; successful keyword retrieval cannot supply missing facts.

## Closed implementation gaps

- Added legacy import support for array-valued decisions and preserved historical IDs/source objects.
- Added record relationship navigation, provenance, evidence, audit/revision visibility, JSONL import with notes, and project controls.
- Added a consistent online SQLite backup command and explicit offline restore/upgrade rollback procedure.
- Added real-worktree and unchanged-contractor-repository tests.
- Added mandatory signed/notarized native-release and multiarchitecture image/tap delivery automation with safe recovery.

Implementation closure and operational acceptance are separate. A source file or test name proves that a check exists, not that its most recent execution passed.

## Remaining verification and external requirements

1. Run all current application, browser/MCP, Go race/vulnerability, documentation, Docker, and infrastructure gates after the final integrated changes.
2. Preserve the passed disposable Docker acceptance evidence and rerun it if operational changes affect backup/restore. Do not use an actual owner's live database for tests.
3. Preserve the completed real-history import/reimport evidence and close any remaining scenario-level retrieval and replacement-chain acceptance gaps. Keep private historical data outside public fixtures/artifacts.
4. Commit the integrated state and require GitHub Actions success for that exact commit. Deploy and check the updated public documentation independently.
5. Provision the credentials named in the release runbook in the existing `action-runners` environment (limited to `main` and `v*` tags). Do not silently substitute unsigned macOS binaries.
6. After the release revision passes CI and credentials are configured, publish a reviewed stable tag and verify every artifact, Apple acceptance, GHCR platform, and clean Homebrew installation.
7. Audit PRD scenarios A–G against current evidence before declaring MVP ready. Report release publication, deployment, and user workflow acceptance separately.

## Repeatable audit procedure

Compare PRD/API contracts with code, meaningful tests, run results, and published artifacts. For every acceptance claim, record the exact revision and the scope the evidence covers. Preserve uncertainty when a check is pending or narrower than the requirement. Update this ledger after authoritative results change; do not carry a green baseline forward to changed source by assumption.
