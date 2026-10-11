# Concurrent-agent baseline

Issue [#36](https://github.com/alexcatdad/scratchpad/issues/36) requires a measured TypeScript baseline before replacing the central backend with Go. This workload uses the authenticated production HTTP application and independent real stdio MCP processes. It does not bypass authentication or read database tables to verify results.

## Disposable verification

Use the repository's Node version and pinned dependencies. Build the current web application, then run:

```sh
npm run build -w @scratchpad/web
npm run test:baseline
```

The Playwright workflow enrolls a synthetic owner passkey, proves possession of a disposable SSH key, and creates a temporary Git checkout and database. It starts three MCP processes, seeds 20 findings and runs two rounds per agent. Each round captures a unique finding, repeats the exact capture, fetches its details, searches for it and retrieves project context. Independent agents run concurrently; each agent performs its own operations sequentially. Mirroring is disabled.

The runner verifies capture content, exact retry identity, immutable detail equality, search visibility and project scope. The workflow retrieves all records through paginated HTTP, verifies the expected number of distinct captures, and compares the complete result after restart. Container mode instead exercises full operational backup and restoration into a new volume. The harness removes its owned temporary resources after the test.

The report is written to the test's output directory under `test-results/` as `concurrent-agent-baseline.json` and attached to the Playwright result. It contains only workload configuration and measurements, excluding record bodies, keys, sessions and deployment URLs. CI retains these JSON reports for its SQLite, PostgreSQL and container lanes. No report or local database belongs in a commit.

For PostgreSQL, set `SCRATCHPAD_E2E_DATABASE_URL` to a disposable test administrator connection with database-creation permission. The fixture creates and later drops its own database, leaving the original selected database unchanged. Use `SCRATCHPAD_E2E_DOCKER=1` only with an explicitly selected local Docker test context and the built `scratchpad:ci` image.

## Larger measurements

Use a fresh disposable database for each measurement. Increase history and concurrency independently, repeating each configuration to assess noise:

```sh
SCRATCHPAD_BASELINE_AGENTS=1 SCRATCHPAD_BASELINE_ROUNDS=20 SCRATCHPAD_BASELINE_RECORDS=1000 npm run test:baseline
SCRATCHPAD_BASELINE_AGENTS=4 SCRATCHPAD_BASELINE_ROUNDS=20 SCRATCHPAD_BASELINE_RECORDS=1000 npm run test:baseline
SCRATCHPAD_BASELINE_AGENTS=8 SCRATCHPAD_BASELINE_ROUNDS=20 SCRATCHPAD_BASELINE_RECORDS=1000 npm run test:baseline
```

Copy each report to an ignored results directory before the next Playwright run, which can clean its previous output. Keep the same machine, runtime, database engine, history size, agent count, round count and MCP binary when comparing backends. The source revision is recorded by the test; local uncommitted application changes must be recorded separately when interpreting a report. The backend label is supplied by the operator, not detected proof of which implementation is serving requests.

The standalone Go command can run the same workload against another disposable instance after normal owner/key enrollment. Run it from `mcp`; absolute key and checkout paths prevent ambiguity:

```sh
go run ./benchmark -url http://localhost:3000 \
  -public-key /absolute/disposable/identity.pub \
  -signing-key /absolute/disposable/identity \
  -workspace /absolute/disposable/repo \
  -binary /absolute/disposable/scratchpad-mcp \
  -backend-label typescript -source-revision VERIFIED_REVISION \
  -agents 4 -rounds 20 -seed-records 1000 > /absolute/results/baseline.json
```

This command writes synthetic captures to the selected instance and does not remove them. Use disposable installations only. It exits unsuccessfully when a transport, tool or behavioral check fails. Inspect both the exit status and error counts before using measurements. Startup, initial authentication and seed capture are reported separately as setup time. Authentication is warmed sequentially before the concurrent workload, so this measures steady-state agent activity rather than simultaneous login admission. Cold-start authentication saturation requires separate measurement.

## Interpretation and limits

Each operation reports its count, error count, minimum, mean, maximum, and nearest-rank p50/p95/p99 latency in milliseconds. Latency includes the full tool call: local Git discovery where applicable, stdio processing, HTTP, server work and JSON decoding. Response bytes describe the encoded MCP result envelope, including its content representations; they are not HTTP wire bytes. Compare identical boundaries between runs.

The report also records the observed readiness database engine, client Go runtime, OS/architecture, UTC start time, measured elapsed time and workload version. Record backend runtime, host resources and database configuration alongside larger operational comparisons. This first workload does not attribute latency to event-loop stalls, database waits, CPU or memory usage. Add those observations during targeted profiling before claiming the cause of a slowdown or narrowing transaction locks. A small passing CI workload verifies behavior, not a production capacity target or a statistically strong performance result.

The Go backend, migrations, encryption and RAG remain separate subsequent slices. Local measurements do not establish CI, release, deployment or live-user acceptance.
