# Private history acceptance

Use this optional procedure to examine import fidelity and retrieval against operator-supplied histories. PRD §43 permits synthetic acceptance fixtures; other projects are illustrative examples, not sources of truth for Scratchpad or prerequisites for MVP readiness. The reference histories are private/local inputs; they must never be copied into public fixtures, Pages output, or CI artifacts. The automated test suite uses anonymized structural fixtures instead.

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
4. Checks every imported source object and raw line against the supplied file, then checks that retrieved details preserve those originals.
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

These are limits of the supplied real-history dataset. They do not block synthetic product acceptance under PRD §43 and do not justify inventing facts about a real project. If additional authoritative source material is supplied, capture it with its actual provenance and rerun the relevant retrieval scenario.

## Verify changes to this procedure

```sh
node_modules/.bin/biome check scripts/validate-history.ts
node_modules/.bin/tsc --noEmit --target ES2022 --module ESNext \
  --moduleResolution Bundler --lib ES2022,DOM --strict \
  --esModuleInterop --skipLibCheck scripts/validate-history.ts
```

Run the optional procedure again against operator-supplied local histories when changing its retrieval checks. Never make CI depend on personal repository paths or upload its private inputs as workflow artifacts.

## Supplemental conversation evidence — 1 October 2026

The source review found an owner instruction and later owner-approved implementation plan dated 17 September 2026 in the USB Boop resume conversation. They establish the metadata-only access boundary and its rationale: useful USB reporting must survive restricted file access without intrusive transfer benchmarks. The accompanying agent proposal remains distinguishable from the user's instruction.

Three conversation messages were captured into a private supplemental JSONL file with their complete text, source roles, message/turn references, dates and retrospective-import limitations. The supplement and combined input stay under ignored `dist/history-acceptance/`; no conversation text or original message IDs were added to public fixtures or artifacts. No earlier unstated research conclusion or paused state was invented.

The existing HTTP acceptance procedure imported the 19 original technical records plus the three source messages, and all 805 stakeholder records, without skips. Four metadata candidates and two throughput candidates were retrieved; four matching details and their import audit events preserved their original source. The local semantic review inspected both the owner's restriction and the accepted plan, establishing the rationale required by scenario A. This is supplemental historical-source acceptance, not a claim that the original 19-line decision log contained the explanation.

The optional USB Boop history example remains unverified: none of the 45 inspected turns in that resume conversation establishes the original pause and its reason. A later resume or Apple enrollment approval cannot supply those missing facts. The approval plan's release deferral is also distinct from the motivating project pause.

To repeat with operator-provided supplemental sources, preserve the full source text and attribution in JSONL, concatenate the original log and supplement into a private combined file, and pass that file as `--usb-log`. Use the same `--asource-log` and optional `--usb-repo` flags shown above. The validator reports candidates; a reviewer must still inspect the actual rationale. A successful run or search count is not automatic semantic acceptance.
