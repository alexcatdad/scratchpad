# CI performance checkpoint

The CI workflow separates backend tests, native Go browsers and Go container
acceptance so independent work can run concurrently. Backend tests run in two
balanced shards; no path-based test skipping is enabled.

## Coverage and merge gate

The existing required `go` check aggregates `go-mcp`, `go-backend`, `go-browser`
and `go-container`. The backend matrix requires both shards to succeed and uses
`fail-fast: false` so a failure does not cancel evidence from the other shard.
The `always()` aggregate job accepts only four successful lane results;
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

Backend and pgvector steps retain `go test -json` output as `go-backend-tests-1`,
`go-backend-tests-2` and `go-pgvector-tests` artifacts, including when tests fail.
Missing backend evidence fails its upload step. Explicit Bash
execution preserves `pipefail`, so piping output through `tee` cannot hide a
failing test command.

## Backend shards

`scripts/backend-shard.sh` assigns a measured group of slow top-level tests to
shard 1 with an anchored `-run` pattern. Shard 2 uses the identical pattern with
`-skip`, running its complete complement across `./...`. New tests, examples and
fuzz seeds therefore run automatically in shard 2; subtests stay with their
parent. Both shards retain `-race`, `-count=1`, the 30-minute timeout, PostgreSQL
and the existing fixture setup and checks. Each runner has its own database.

The initial split balances the sum of per-test median durations from five
unsharded CI runs: approximately 294 seconds per shard. This is a scheduling
estimate, not a runtime guarantee. Rebalance the short slow-test list when
measured timings drift; do not replace the complement with an allowlist.

The regression test executes the actual shard script against a disposable Go
module and checks its union against an unsharded race run, including subtests,
duplicate names across packages, name prefixes, examples, fuzz seeds and skips.
It also checks invalid shard rejection and test failure propagation.

## Verification and measurement

Use the pinned Node version through fnm. From the repository root:

```sh
fnm exec --using "$(cat .node-version)" npm ci
fnm exec --using "$(cat .node-version)" npm run lint
fnm exec --using "$(cat .node-version)" node --test scripts/backend-shard.test.mjs
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
4. Combine both backend artifacts and compare their test identities and outcomes
   against the unsharded reference. Reject missing or overlapping identities.
   Compare pgvector outcomes too. Report flakes, failed attempts and unexpected
   skips alongside timings.
5. Stop and review the measurements before merging or further optimisation.
   If timing and compute constraints conflict, obtain a decision
   before changing either constraint or moving coverage out of the PR gate.

Read-only GitHub evidence can be collected with `gh run view RUN_ID` and
`gh api repos/alexcatdad/scratchpad/actions/runs/RUN_ID/jobs --paginate`.
Download the named test artifacts with `gh run download RUN_ID --name NAME` into
a directory outside the checkout. Run IDs and local diagnostic outputs do not
belong in committed test artifacts.

The deployment workflow still requires successful CI on the exact main push
revision. PR success, merge, deployment and runtime acceptance remain separate.
