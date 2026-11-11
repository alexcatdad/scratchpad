# Private production access logs

Issue #20 is independently based on main `ab6f68abaec9884c1a0723a94a1054cbd81db9c4`.

## Contract

The Go server is the only HTTP process: it serves the API and the embedded static dashboard, so one access log covers both. `RequestLog` (`backend/internal/httpapi/access_log.go`) writes one JSON line per request with `method`, `path`, `status` and `durationMs`. `path` is the URL path only. Query strings, origins, headers and bodies are excluded, which matters for the dashboard because its state (search text, filters, record IDs, the GitHub sign-in error) lives in the query string of `/`. A panicking handler is logged as `HTTP_REQUEST_FAILED` without the panic value or stack.

The Node entry point this runbook originally described (`apps/web/server.mjs` with the srvx adapter) no longer exists; there is no second logger to keep private. The development proxy (`SCRATCHPAD_DASHBOARD_URL`) sits behind the same Go log, but Vite prints its own output in the developer's terminal and is not a production path.

## Verification

```sh
fnm exec --using=24.22.1 npm run lint
fnm exec --using=24.22.1 npm run typecheck
fnm exec --using=24.22.1 npm test
go -C backend test ./internal/httpapi/
fnm exec --using=24.22.1 npx playwright test tests/go-runtime.spec.ts tests/workflow.spec.ts
```

`tests/go-runtime.spec.ts` requests the dashboard with a private marker and an `authError` value in the query string and asserts that the server log records `"path":"/"` and neither value.

The original authenticated browser regression reproduced full-query logging on main, including percent-encoded private search markers on HTTP 200 and 401 requests. It covers dashboard navigation, actual dashboard search, API query/filter requests and a synthetic credential header. Corrected logs exclude the unique marker, its raw value and its encoded value while retaining method, path, status and timing. The complete owner enrollment, capture, real stdio MCP and restart workflow passed on the corrected production entry point.

The independent branch aligns existing Go toolchain declarations to 1.27.2 for the baseline standard-library vulnerability gate. No dependencies, database migrations or deployment changes are introduced. Canonical GitHub Actions remains separate from local verification.
