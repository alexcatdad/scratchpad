# AI privacy dependencies

Issue #19 is independently based on main `ab6f68abaec9884c1a0723a94a1054cbd81db9c4`. Per-kind derived content validation remains independently scoped to #18.

## Contract

Every completion artifact carries server-owned `privacyDependencies`: version 1, all supplied record IDs, all supplied project metadata IDs and the operation's cross-project consent requirement. This is separate from `sourceRecordIds` and `projectIds`, which remain display citations. Model-supplied dependency claims are ignored. Each batch retains its actual inputs; combined Markdown exports retain the union of every contributing batch's inputs.

Persistence rechecks every dependency and its current consent inside the transaction. Retrieval and review require the full set, including uncited metadata-only projects. A project-scoped view must contain every dependency; A+B output citing only A cannot appear in A-only memory. Revoking either AI or cross-project consent on B hides the output and prevents acceptance. Accepted curated derivatives copy the full dependencies. Cross-project acceptance keeps interpretations in gated curated artifacts rather than copying their text or edges into project-local metadata or relationships. Ordinary single-project review behavior remains available.

Native archives preserve dependencies and reject invalid known dependency schemas or missing references atomically. Unknown legacy provenance remains unchanged historical evidence; access and review fail closed until regenerated. Dependencies are never fabricated from citations. Portable archives are owner-supplied restore data, not cryptographically authenticated provenance statements.

## Verification

```sh
fnm exec --using=24.21.0 npm exec -w @scratchpad/web -- vitest run src/server/ai.test.ts
fnm exec --using=24.21.0 npm run lint
fnm exec --using=24.21.0 npm run typecheck
fnm exec --using=24.21.0 npm test
fnm exec --using=24.21.0 npm run build
```

Synthetic public regressions reproduced A-only retrieval of A+B output and access to artifacts with unknown legacy inputs. Tests cover supplied uncited records, metadata-only inputs, forged model dependencies, both forms of consent revocation, accepted cross-project projections, curated/native round trips, invalid archive rollback and legacy fail-closed access. Real provider connectivity is covered by the existing synthetic local server deadline test; no private sources or provider credentials are used.

The independent branch aligns existing Go toolchain declarations to 1.27.2 for the baseline standard-library vulnerability gate. No dependencies, database migrations or deployment changes are introduced. Local checks remain distinct from canonical GitHub Actions and deployed acceptance.
