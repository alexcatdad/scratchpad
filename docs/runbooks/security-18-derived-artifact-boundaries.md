# Derived artifact boundaries

Issue #18 is independently based on main `ab6f68abaec9884c1a0723a94a1054cbd81db9c4`.

## Contract

Generation, native import, listing and review use shared per-kind content validation. Relationship candidates and contradictions require distinct cited endpoints; lifecycle relationship types are excluded. Classification binds one source record and uses bounded string tags and classification. Exports require Markdown; other prose artifacts cannot carry relationship or classification fields. Duplicate candidates, contradictions and patterns require independent source records.

Review independently validates stored content, source existence and source project membership before any writes, then checks current project consent, including cross-project consent for multi-project artifacts. Invalid preexisting derived payloads remain unchanged in historical storage and native exports, are hidden from suggestion access and cannot be accepted. Import rejects invalid derived content atomically. Import does not require enabling AI: historical evidence can be restored while processing remains disabled. Accepting restored suggestions requires current consent.

Valid ordinary explicit relationships retain their existing native archive behavior. This change does not reinterpret historical raw captures or promote AI interpretation into lifecycle authority. Provider input privacy dependency tracking remains the independently scoped issue #19.

## Verification

```sh
fnm exec --using=24.21.0 npm exec -w @scratchpad/web -- vitest run src/server/ai.test.ts src/server/imports.test.ts
fnm exec --using=24.21.0 npm run lint
fnm exec --using=24.21.0 npm run typecheck
fnm exec --using=24.21.0 npm test
fnm exec --using=24.21.0 npm run build
```

Synthetic public regressions reproduced acceptance of a stored `replaces` suggestion and native import of invalid derived content on main. Corrected cases cover both lifecycle types, uncited and missing endpoints, self edges, classification type errors and model content belonging to a different kind. Valid source-bound refinement imports, requires consent before review, preserves raw captures, and round trips accepted AI, curated evidence and ordinary relationships. Both changed test files pass all 40 cases.

The independent branch also aligns the existing Go toolchain declarations to 1.27.2, required to pass the baseline standard-library vulnerability gate. No dependencies, database migrations or deployment changes are introduced. Canonical GitHub Actions and current-head reviews remain release evidence, separate from local checks.
