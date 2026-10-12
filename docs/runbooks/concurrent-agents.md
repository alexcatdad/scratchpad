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

## Native lexical record selection — 2026-10-12

The Go search path decoded every record before applying native lexical membership. It now joins distinct native matches to record data before decoding. Authenticated HTTP regressions are red-capable on SQLite/PostgreSQL: an unrelated malformed source previously disabled a targeted query, while matching malformed sources must still fail. A separate failing SQLite duplicate-FTS case required distinct selection to preserve unique results. Filters, cursor ordering and authorization remain covered by the existing lexical suite.

The original MCP binary could not be reconstructed byte-for-byte because its VCS stamps changed. A fresh Go comparison therefore froze one new MCP executable for all before/after trials (SHA-256 `f14a45357a2a762d40b0bb477d7342acede347e71ec1f8d6afaede5bd920ad44`). Before used the recorded `9daf65f` backend source; the final candidate was built from `ba3f63cfcba934c727f598c91bcd286b5d55e822` plus the tracked search patch, SHA-256 `3040a1e62a907ef13fd9a8a64c3b9809b2d09385d80a561db47920aeb33c4488`. All other workload, host and runtime settings matched the preceding SQLite study.

Two baseline trials, two first-iteration trials and two final-query trials each used eight agents, 1,000 seeded findings and ten rounds. All 2,400 measured operations completed without errors. Final-query measurements below are compared with the same-client baseline; the first-iteration data remains historical and is excluded from the final figures.

| Query                     | Measured rounds (s) | Capture p95 (ms) | Retry p95 (ms) | Detail p95 (ms) | Search p95 (ms) | Context p95 (ms) |
| ------------------------- | ------------------- | ---------------- | -------------- | --------------- | --------------- | ---------------- |
| Before                    | 4.5–4.8             | 153.9–198.1      | 230.1–239.2    | 25.5–27.6       | 159.4–162.4     | 172.2–175.5      |
| Distinct native selection | 4.2–4.3             | 137.6–137.8      | 105.1–110.7    | 16.0–16.9       | 122.9–133.2     | 194.8–201.1      |

Mean measured elapsed time decreased 8.6%; mean per-trial search p95 decreased 20.4%. Context p95 increased despite the shorter overall workload. This is a local improvement to one read path, not resolution of the original Go concurrency regression. Metadata, relationships, blank-query listing and context still need focused-query/wait analysis. Database waiting, CPU attribution, PostgreSQL and compact/RAG performance remain unmeasured here. The final distinct query passed lexical HTTP regressions on both engines; performance claims apply only to these repeated SQLite trials.

## Bounded pool and context headers — 2026-10-12

Final measured source: `0be9432`. The same frozen MCP executable (SHA-256
`6dfd3ab08a0d6f0bc9a0fbc0a01ca85e697b9a17b81a10f35c6d0932ad0e27d4`)
ran against both backends on an Apple M2 Max with 12 logical CPUs and 32 GiB,
macOS arm64, Node 24.21.0 and Go 1.27.2. Each trial used a fresh private schema-1
SQLite fixture and actual SSH proof. Go applied its normal schema-3 upgrade.
Other owned build, test and agent jobs had finished before timing. The four
trials alternated TypeScript/Go, then Go/TypeScript, with eight agents, 1,000
seeded findings and ten rounds per agent. All 1,600 measured operations passed;
setup, seeding and initial authentication remain outside measured rounds.

| Backend    | Measured rounds (s) | Capture p95 (ms) | Retry p95 (ms) | Detail p95 (ms) | Search p95 (ms) | Context p95 (ms) |
| ---------- | ------------------- | ---------------- | -------------- | --------------- | --------------- | ---------------- |
| TypeScript | 3.64–3.88           | 97.0–116.6       | 85.4–115.2     | 20.7–21.1       | 99.3–117.4      | 105.6–116.8      |
| Go         | 3.78–3.89           | 88.5–90.6        | 83.2–90.4      | 11.3–12.2       | 82.6–110.7      | 149.3–159.6      |

Mean measured elapsed time was 3.832 seconds for Go and 3.757 seconds for
TypeScript: Go took 2.0% longer. This small repeated sample does not establish
a statistically reliable overall speed advantage. Legacy context remains slower
in Go even though other operation tails improved in this sample. Capture/retry
responses averaged 2,379 encoded MCP bytes, details 5,461, search 5,900.6 and
context approximately 133,246 on both backends. Compact context was not requested
by this workload; bounded previews do not explain these legacy response sizes.

The preceding diagnostic source (`72eb8bc`) still had a single SQLite connection.
Its matched MCP comparison measured Go 15.1% longer than TypeScript. A separate
HTTP-only probe reported approximately 14.7 seconds of aggregate connection-pool
waiting; changing only the pool to four connections reduced this to approximately
2.8 seconds and reduced that HTTP replay from 2.30 to 0.82 seconds. Aggregate
waiting sums concurrent goroutines and is not wall-clock elapsed time. These
temporary diagnostic observations explain the bounded-pool change; they are not
final-source CPU/wait measurements or a controlled absolute before/after speed
claim. A timing run overlapping regression tests was excluded.

The authenticated slow-reader regression first timed out an independent capture
after three seconds, then passed three consecutive runs with the bounded pool.
File-backed SQLite opens at most four connections after serialized startup/WAL
activation; `BEGIN IMMEDIATE` still serializes writes. Memory databases retain one
connection. No cache-size or PostgreSQL locking change was made.

Separately, the compact-context history regression constructs 2,000 long findings
(approximately 384 MB of immutable JSON) and one current state. Before selection
from indexed headers, it failed with HTTP 503 at the existing ten-second deadline.
With migration-3 header maintenance and selective body hydration, two race-enabled
runs completed in 433 and 440 ms, preserving exact totals, omission counts and
current-state priority. These are public behavior checks, not the same workload
as the MCP table above. Ordinary selected bodies are hydrated by identifier;
legacy accepted answers with missing rendered content retain their payload
fallback. Counts and lifecycle selection still scan the project's small headers.

Final-source CPU/wait attribution, PostgreSQL concurrency, compact/RAG latency
distributions and production capacity remain separate evaluation work. All seven
rebuilt-image recovery/rollback cases and the combined-runtime lifecycle case
passed with schema 3; local success does not establish canonical CI, release or
deployment acceptance.
