# MVP readiness

## Scope

Deliver PRD sections 36–38, including all capture/retrieval/project/authentication/operations/distribution requirements and acceptance scenarios A–G. AI, embeddings, pattern analysis, and PostgreSQL remain post-MVP. A working local development slice is not MVP acceptance.

## Work procedure

1. Compare PRD and API contract with current code, tests, and published artifacts.
2. Implement missing behaviors with coordinated ownership: server/import, MCP acceptance, release automation, dashboard/integration.
3. Run meaningful server and real stdio tests, then browser tests and disposable Docker backup/restart/restore checks.
4. Validate real USB Boop and Asource histories locally, preserving original content and uncertainty. Never publish private historical datasets in test fixtures or release artifacts.
5. Run all lint, types, builds, race tests, and vulnerability checks. Commit and require GitHub Actions success for the release revision.
6. Produce and validate signed/notarized macOS and Linux MCP assets, checksums, GHCR image, and Homebrew installation. Document missing credentials as blockers, never silently publish unsigned macOS assets.
7. Audit every requirement and scenario against current evidence before declaring MVP ready.

## Initial gaps found

- Legacy import skips array-valued decisions and discards historical IDs.
- Dashboard omits relationship navigation, detailed provenance and audit history, legacy JSONL import, and most project controls.
- Full SQLite backup/restore procedure is missing.
- Real-world scenario acceptance and multi-worktree/restricted-repository coverage need explicit evidence.
- Release workflow, published GHCR image, signed/notarized binaries, and Homebrew formula are missing.
- Repository has no release secrets configured; credential provisioning is required for signed publication.

This checklist is evidence tracking, not a claim that unchecked features are complete.
