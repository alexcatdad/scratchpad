# Private production access logs

Issue #20 is independently based on main `ab6f68abaec9884c1a0723a94a1054cbd81db9c4`.

## Contract

The production `npm run start -w @scratchpad/web` command uses `apps/web/server.mjs` with the existing srvx Node adapter and static middleware. Access logs contain method, URL pathname, status and duration in milliseconds. Query strings, origins, headers and bodies are excluded. Unexpected request failures produce a fixed diagnostic and HTTP 500 rather than printing arbitrary exception messages containing private input.

Existing port/host environment configuration, `.env.production` then `.env` loading with environment precedence, static assets and graceful shutdown remain available. The container uses the same package start command. Browser fixtures launch the same entry point rather than the generic srvx CLI, whose default logger prints complete URLs. Use the documented production command to retain this privacy boundary.

## Verification

```sh
fnm exec --using=24.21.0 node --check apps/web/server.mjs
fnm exec --using=24.21.0 npm run lint
fnm exec --using=24.21.0 npm run typecheck
fnm exec --using=24.21.0 npm test
fnm exec --using=24.21.0 npm run build
fnm exec --using=24.21.0 npm run test:e2e -- tests/workflow.spec.ts
```

The authenticated browser regression reproduced full-query logging on main, including percent-encoded private search markers on HTTP 200 and 401 requests. It covers dashboard navigation, actual dashboard search, API query/filter requests and a synthetic credential header. Corrected logs exclude the unique marker, its raw value and its encoded value while retaining method, path, status and timing. The complete owner enrollment, capture, real stdio MCP and restart workflow passed on the corrected production entry point.

The independent branch aligns existing Go toolchain declarations to 1.27.2 for the baseline standard-library vulnerability gate. No dependencies, database migrations or deployment changes are introduced. Canonical GitHub Actions remains separate from local verification.
