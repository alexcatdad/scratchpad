# CI performance checkpoint

The CI workflow separates backend tests, native Go browsers and Go container
acceptance so independent work can run concurrently. No backend test shards or
path-based test skipping are enabled.

## Coverage and merge gate

The existing required `go` check aggregates `go-mcp`, `go-backend`, `go-browser`
and `go-container`. Its `always()` job accepts only four successful results;
failed, cancelled, skipped or missing results cannot satisfy it. Keep `go` required
in branch protection. Other existing required checks remain required.

Playwright projects partition test files without changing test bodies:

- `retained`: all files except `go-*.spec.ts`, used by the web, PostgreSQL and
  retained-container lanes.
- `go-native`: `go-*.spec.ts` except `go-container.spec.ts`, run once with SQLite
  and PostgreSQL available.
- `go-container`: the actual image, database/secret restore and retained-runtime
  rollback scenarios.

Running Playwright without a project still discovers all three projects. The
native Go tests do not exercise the retained container merely because
`SCRATCHPAD_E2E_DOCKER=1` is set; their old repetitions in that lane are removed.

## Race-runtime exit wait

Only backend and pgvector test steps set `GORACE=atexit_sleep_ms=0`. Both retain
`-race`, uncached execution and the existing test selection. This removes the
[race runtime's default one-second exit sleep](https://go.dev/doc/articles/race_detector#Options),
not race instrumentation or application deadlines. Production builds and the MCP
unit-test lane retain their existing configuration.

Server shutdown cancels and awaits GitHub and AI workers, including RAG
reconciliation, and drains HTTP serving. The MCP SDK waits for session shutdown.
The backend server fixture signals termination, waits for process exit and fails
on an unsuccessful exit or timeout. Recheck these boundaries if process lifecycle
handling changes; an arbitrary exit sleep must not substitute for joining work.

Backend and pgvector steps retain `go test -json` output as `go-backend-tests`
and `go-pgvector-tests` artifacts, including when tests fail. Explicit Bash
execution preserves `pipefail`, so piping output through `tee` cannot hide a
failing test command.

## Verification and measurement

Use the pinned Node version through fnm. From the repository root:

```sh
fnm exec --using "$(cat .node-version)" npm ci
fnm exec --using "$(cat .node-version)" npm run lint
actionlint
fnm exec --using "$(cat .node-version)" npx playwright test --list
fnm exec --using "$(cat .node-version)" npx playwright test --project=retained --list
fnm exec --using "$(cat .node-version)" npx playwright test --project=go-native --list
fnm exec --using "$(cat .node-version)" npx playwright test --project=go-container --list
```

Compare file and test-title inventories before and after the split. Preserve
database variants, race detection, restore and rollback scenarios; investigate
new skips or missing outcomes. Use disposable local fixtures for diagnostics.
Canonical coverage requires GitHub Actions with PostgreSQL 18.6, pgvector and
the explicit runner-local Docker context. Local results do not establish CI or
deployment acceptance.

At the measurement checkpoint:

1. Retain five comparable full baseline runs and five full candidate runs with
   the same test inventory and warm dependency caches. Record source revisions,
   event type, run IDs, cache hits, failures, retries and skipped tests. Keep
   cold-cache runs separate. Do not silently discard failed attempts.
2. Measure push-to-all-required-checks-green, including queue time and the
   separate documentation `build` check. Retain the push timestamp; workflow
   creation time is only a proxy if that timestamp is unavailable. Report the
   median and slowest run. The target median is below ten minutes.
3. Sum job execution durations across the same required workflows for total
   runner minutes, including setup, uploads and the aggregate job. Compare
   candidate and baseline medians; the initial budget is at most 25% additional
   runner minutes. These elapsed runner minutes are not rounded billing units.
4. Compare backend and pgvector JSON test identities and outcomes. Report flakes,
   failed attempts and unexpected skips alongside timings.
5. Stop and review the measurements. Add balanced shards only in a subsequent
   stage if needed. If timing and compute constraints conflict, obtain a decision
   before changing either constraint or moving coverage out of the PR gate.

Read-only GitHub evidence can be collected with `gh run view RUN_ID` and
`gh api repos/alexcatdad/scratchpad/actions/runs/RUN_ID/jobs --paginate`.
Download the named test artifacts with `gh run download RUN_ID --name NAME` into
a directory outside the checkout. Run IDs and local diagnostic outputs do not
belong in committed test artifacts.

The deployment workflow still requires successful CI on the exact main push
revision. PR success, merge, deployment and runtime acceptance remain separate.
