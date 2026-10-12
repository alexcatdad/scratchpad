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

The Go replacement is implemented incrementally; measurements remain separate from CI, release, deployment or live-user acceptance.

## Matched SQLite comparison — 2026-10-12

Measured source revision: `9daf65f85b6239c57f9c9d31d27a4f04ea7e3c1a`. Both backends used the same optimized MCP executable (SHA-256 `f3217987fe5cbdfafe218bb392e23a08cd4eee3700f24969e53f4ab3748e522d`), local Git checkout, synthetic SSH key and workload. Each trial used a fresh private SQLite database initialized in the TypeScript schema-1 format; Go performed its normal schema-2 migration. A synthetic credential fixture authorized actual local SSH possession proof. No live installation or provider was used.

The host was Apple M2 Max, 12 logical CPUs and 32 GiB RAM, macOS arm64, Node v24.21.0 and Go 1.27.2. Go binaries were built without race instrumentation; TypeScript used the production build. SQLite retained each runtime's normal configuration. This compares implemented runtimes, including their libraries and schema handling, rather than isolating programming language.

After successful three-agent smoke trials, each backend ran twice at 1, 4 and 8 agents, with 1,000 seeded findings and 10 rounds per agent. The second repetition reversed backend order. Every measured operation completed without errors; the larger trials verified 2,600 tool operations in total. Setup/authentication/seed time is excluded from measured rounds. Each round uses capture, identical retry, detail, search and legacy project context.

The table reports the range across two trials; each p95 is a per-trial nearest-rank percentile. These are small local samples, not capacity targets or production acceptance.

| Agents | Backend    | Measured rounds (s) | Capture p95 (ms) | Retry p95 (ms) | Detail p95 (ms) | Search p95 (ms) | Context p95 (ms) |
| ------ | ---------- | ------------------- | ---------------- | -------------- | --------------- | --------------- | ---------------- |
| 1      | typescript | 2.7–2.9             | 63.1–130.4       | 62.2–79.1      | 6.0–7.0         | 69.4–73.9       | 75.6–115.4       |
| 1      | go         | 2.9–2.9             | 66.2–67.7        | 63.0–63.6      | 6.1–6.4         | 75.0–77.1       | 88.6–89.2        |
| 4      | typescript | 3.0–3.0             | 71.1–71.9        | 69.9–72.3      | 7.9–10.7        | 74.3–76.2       | 83.4–83.9        |
| 4      | go         | 3.2–3.2             | 68.7–70.3        | 68.5–68.7      | 9.7–10.8        | 82.5–95.2       | 97.6–104.0       |
| 8      | typescript | 3.3–3.3             | 78.7–78.8        | 77.7–78.6      | 20.9–22.9       | 84.7–86.9       | 93.9–94.0        |
| 8      | go         | 4.5–4.7             | 146.6–190.2      | 196.1–294.4    | 20.6–26.9       | 152.3–169.4     | 164.8–188.8      |

Mean measured Go elapsed time was 4.6% higher at 1 agent, 7.8% higher at 4 agents, 38.4% higher at 8 agents. There is no demonstrated overall Go speedup in this workload. Eight-agent capture, retry, search and context tails regressed and require profiling before claiming improvement.

Capture/retry responses were 2,379 encoded MCP bytes and details were 5,461 bytes per call on both runtimes. Legacy context averaged approximately 133 kB (130 KiB) per larger-workload call, with minor concurrency-dependent differences; search result sizes also varied with interleaving. Full latency distributions, error counts and response-byte totals are retained in the local JSON reports. No compact-context, RAG, PostgreSQL, database-wait, CPU or memory attribution is established by these SQLite trials.

The next measurement should isolate HTTP service time and database waiting from MCP Git discovery, then repeat equivalent workloads after changes. The Go SQLite pool currently has one connection, and explicit project-scoped MCP reads still perform checkout discovery; these are inspected candidates for investigation, not proven causes. Preserve capture provenance, retry/audit atomicity and authorization when addressing them. Keep the shared PostgreSQL transaction lock until measured contention and correctness evidence justify changing it.
