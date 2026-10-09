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

Hosted review corrections preserve the established HTTP 404 response for missing imported source records, remove an undocumented source-count ceiling and reject repeated citations rather than treating them as one classification source. A public regression produces a complete 10,001-record export on its first attempt. The canonical browser fixture now supplies the required distinct contradiction endpoints; its earlier missing endpoints correctly failed the new per-kind rule. These corrections address canonical PostgreSQL and browser failures without weakening lifecycle or citation validation.

The second hosted pass tightened portable provenance: participating project IDs must uniquely equal the exact cited source-project set, including on retrieval. All documented ordinary relationship types remain available by deriving the AI enum from the central relationship vocabulary with only lifecycle types removed. Canonical-equal AI and curated rows are fully validated before an idempotent skip, so an invalid historical artifact cannot let other archive rows commit. Five additional red cases reproduced false/duplicate project declarations, skipped invalid originals, and rejected `implements`/`caused_by` relationships; all 46 cases across the changed test files now pass.
