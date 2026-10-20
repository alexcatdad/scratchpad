# Go compact context and unresolved questions

These additions implement issue [#36](https://github.com/alexcatdad/scratchpad/issues/36) in the Go backend. Go is the default source runtime; published-release and deployed acceptance remain separate. This runbook does not authorize a live upgrade.

## Question capture and answer evidence

Go accepts `qa` captures with a required nonempty `payload.question` and an omitted answer. A supplied answer must be a nonempty string. Existing full question/answer captures remain supported. Question-only content contains no invented empty answer, and normal capture retry identities remain authoritative.

Capture a subsequent answer separately, then create an accepted `answers` relationship through `POST /api/v1/relationships`, from the answer record to the original question. Suggested relationships do not resolve questions until accepted. The original question is immutable. Context derives unresolved status from inline answer text or accepted, fully current, nonempty answer evidence in the selected project. Archived, superseded, partial, unverified, empty or out-of-scope answer sources do not resolve the question. Restoration of applicable evidence can resolve it again; a replacement needs its own answer relationship.

The real stdio `record_qa` schema makes `answer` optional. Full-pair and question-only input are supported by Go. Historical schema-1 TypeScript artifacts required an answer. Link/review operations retain their existing HTTP contract.

## Explicit compact context

The legacy `GET /api/v1/projects/{id}/context` shape remains the default. Explicit `version=1` selects it as well. `version=2` selects compact context; unsupported versions fail instead of silently changing shape.

`GET /api/v1/projects/{id}/context/compact` is the separate version-2 endpoint for generated SDKs. It preserves the legacy Go SDK context method signature. Optional query limits are:

| Parameter      | Default | Supported range                                      |
| -------------- | ------- | ---------------------------------------------------- |
| `limit`        | 20      | 1–100 distinct source previews                       |
| `maxChars`     | 12000   | 4096–64000 serialized UTF-8 bytes, including newline |
| `previewChars` | 320     | 32–2000 UTF-16 units                                 |

The response carries `version: 2`, project identity, distinct `records`, section references, relationship references, counts, limits and `truncation`. Records contain previews, capture authority/confidence, lifecycle applicability and source IDs, without raw payloads, full content or full metadata. Project-state previews retain bounded state/reason/follow-up fields. QA previews report derived `questionStatus`. Fetch the source ID through the existing record-detail API for complete evidence and provenance.

Section arrays reference the preview IDs; repeated references do not duplicate full records. Current state is selected from all fully current state evidence before history limits. Current/applicable material precedes historical material. Counts include omitted records and unresolved questions. `recordsOmitted`, `relationshipsOmitted`, `previewsTruncated` and `budgetLimited` distinguish incomplete context from absent evidence. Relationships and older previews are reduced first to fit the byte budget; Unicode boundaries remain intact and current state has priority.

Both versions load only the selected project's records, their metadata and relevant edges. PostgreSQL uses a repeatable read snapshot; SQLite uses its read transaction. Selection still processes the selected project's history for exact lifecycle/counts. Further database query/index work and concurrent-agent measurements remain required before claiming history-independent query cost.

The MCP `get_project_context` tool accepts optional `version`, `limit`, `maxChars` and `previewChars`. Omitted version preserves legacy behavior. Explicit version 2 verifies that the server actually returned version 2; an older server cannot silently supply an unbounded legacy response. No automatic legacy fallback is claimed.

## Verification

```sh
npm run test:backend
go -C mcp test -race -count=1 ./...
npm run sdk:check
```

Supply disposable `TEST_POSTGRES_URL` for PostgreSQL. Real HTTP tests cover question-only capture, immutable linked answers, accepted versus suggested evidence, archived/restored/superseded sources, malformed historical empty answers, version/limit validation, current state preceding newer unverified history, distinct previews, exact omission counts and Unicode-safe byte budgets. The existing integration command's `questions` phase drives actual authenticated stdio capture, retry, source detail and both context modes on both engines. SQL fixtures are inputs; verification uses the public API.

Local checks do not prove deployed dashboard integration, PostgreSQL operational restore, new passage RAG, measured performance or canonical CI acceptance. Do not switch the supported runtime on this evidence alone.
