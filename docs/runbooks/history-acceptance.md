# Private history acceptance

Use this optional procedure to distinguish working retrieval from missing historical evidence. The reference histories are private/local inputs; they must never be copied into public fixtures, Pages output, or CI artifacts. The automated test suite uses anonymized structural fixtures instead.

## Run locally

Install the pinned workspace dependencies first. Supply the two actual JSONL paths and, optionally, the technical project's Git checkout:

```sh
node --import tsx scripts/validate-history.ts \
  --usb-log /absolute/path/to/technical/decisions.jsonl \
  --asource-log /absolute/path/to/stakeholder/decisions.jsonl \
  --usb-repo /absolute/path/to/technical/repository
```

The flag names identify the original reference scenarios; the supplied paths remain operator-controlled. Omit `--usb-repo` to inspect only the current logs. To verify a particular stakeholder record already reviewed locally, add `--stakeholder-record-id` followed by its original identifier. The script never prints that identifier.

The script:

1. Starts a temporary loopback HTTP service backed by an in-memory database.
2. Creates an isolated validation credential inside that disposable database. It does not enroll credentials in, or connect to, a real Scratchpad instance.
3. Imports both logs through HTTP and exercises search, record detail/history, and project context through the actual HTTP API.
4. Checks that retrieved details preserve their original source objects.
5. Reports counts and hashed record tokens, without source text, paths, original IDs, or authentication tokens.
6. Optionally inspects only the technical repository's Git versions of `decisions.jsonl`, `README.md`, and Markdown files under `docs/`.
7. Closes the service and destroys its in-memory state.

A zero exit status means the validation procedure completed without transport or data-preservation errors. It does **not** mean every PRD scenario passed. Read the per-scenario status and inspect the relevant source locally.

## Interpret the evidence

### Scenario A: historical technical rationale

Both sides of the technical choice and its rationale need source support. A search hit containing “metadata” does not establish why actual transfer throughput was rejected. Inspect the returned record and any supporting history; do not substitute keyword counts for that explanation.

### Scenario B: paused project and unresolved work

Recover a recorded state, its reason, unresolved follow-up, and relevant decisions. A resume statement does not establish that the project is currently paused. Imported records remain unverified historical evidence; context must not manufacture a current state from old prose.

### Scenario C: stakeholder request and later changes

Inspect an actual stakeholder request/decision, its rationale, and a later record that refers to it. The script searches for request candidates with rationale and checks whether later historical references can be retrieved through the API. Use `--stakeholder-record-id` to reproduce the specific chain reviewed locally.

Prose references remain source evidence. They are not automatically promoted into accepted relationship edges. A reviewer may subsequently create an explicit, audited relationship, but that is a separate curation action and must not be hidden inside an acceptance test.

## Observed reference validation, 29 September 2026

The current reference logs imported 19 technical-project records and 805 stakeholder-project records through HTTP without skipping records. No source content was added to this repository.

| Scenario | Observed result                                                                                                                                                                                           | Acceptance meaning                                                                                                                                    |
| -------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| A        | Two metadata candidates; no throughput candidates; both record details and import audit events available.                                                                                                 | Retrieval works, but the exact technical rationale is not established by the inspected source.                                                        |
| B        | Two resume/pause-query candidates, zero explicit project-state records, one structured follow-up record, and no asserted current state.                                                                   | The specific paused-state/reason scenario remains unsupported by these logs.                                                                          |
| C        | An older stakeholder-facing boundary decision and rationale, plus a later prose refinement, were locally reviewed. HTTP search retrieved both; record details and audit history preserved both originals. | Historical request/rationale/refinement retrieval was demonstrated. The source references remain prose rather than fabricated accepted relationships. |

The optional Git inspection covered 32 relevant commits and 198 document versions. It found no throughput/transfer-speed or explicit paused-state matches. Membership-related references concerned release requirements or enrollment approval and did not establish a paused project and its reason. There was therefore no justified historical record to synthesize or import for A or B.

These are source-evidence limits, not permission to invent records or claim complete scenario acceptance. If additional authoritative source material is supplied, capture it with its actual provenance and rerun the relevant retrieval scenario.

## Verify changes to this procedure

```sh
node_modules/.bin/biome check scripts/validate-history.ts
node_modules/.bin/tsc --noEmit --target ES2022 --module ESNext \
  --moduleResolution Bundler --lib ES2022,DOM --strict \
  --esModuleInterop --skipLibCheck scripts/validate-history.ts
```

Run the optional procedure again against operator-supplied local histories when changing its retrieval checks. Never make CI depend on personal repository paths or upload its private inputs as workflow artifacts.
