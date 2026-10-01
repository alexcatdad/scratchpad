# Optional extension acceptance

The owner requested these additions on 2 October 2026. Published v0.2.0 remains the prior product foundation; implementation, canonical checks, publication and owner deployment are separate outcomes.

| Requirement             | Behavior and evidence                                                                                                                                                                                                                                                                                                                                                                                                                            |
| ----------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Native pgvector         | Explicit PostgreSQL opt-in; full 2,560-dimensional exact cosine; compatible authorized candidates before ranking and consent rechecked afterward; transactional update/delete and startup rebuild; missing extension/schema readiness refusal; original-vector preservation and documented float32/unsupported-dimension fallback. Actual disposable pgvector tests pass, alongside vanilla PostgreSQL backup/restore and SQLite/AI regressions. |
| Public GitHub profile   | Authenticated link/refresh/unlink of public user metadata; fixed upstream API, bounded body/deadline, validated URLs, optimistic conflicts and audit; no credential/session/source changes. Focused API/provider tests, restart persistence, imported-data sanitization, deterministic rendered controls and a real public GitHub fetch pass.                                                                                                    |
| Generated clients       | OpenAPI-derived TypeScript and Go clients cover all 44 paths and 52 operations. Deterministic regeneration, compilation, real SSH challenge/verify, authenticated capture/retrieval, typed failures and packaged TypeScript consumption must pass together. Generators do not replace the MCP HTTP boundary.                                                                                                                                     |
| Operations and delivery | New runbooks and public guides describe configuration and consumption. Canonical CI includes extension-enabled PostgreSQL and generated-client integration. New release publication, installation acceptance and owner upgrade must be proved before calling delivery complete.                                                                                                                                                                  |

Implementation checks use disposable records, keys and databases. The owner's live instance remains on published v0.2.0 while development is verified. No public profile is linked and no AI/project permissions are enabled automatically.

Canonical [CI run 36933787062](https://github.com/alexcatdad/scratchpad/actions/runs/36933787062)
passed all jobs at `f5f1158c11ee9b99f0d4caa02c399135b20184ff`, including native
pgvector, ordinary PostgreSQL, Docker, authenticated generated-client integration,
Go and web/document checks. Local Node 24 Docker browser acceptance passed both
the AI/settings and authenticated memory/MCP/restart workflows. Release workflow
changes after that source require a new exact-source check before tagging.
