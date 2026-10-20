# Scratchpad product requirements

Consolidated 20 October 2026. This document defines the current single-owner
product and its planned enterprise direction. [Architecture](architecture.md),
[API contracts](api.md) and [OpenAPI](openapi.json) specify implementation and
wire behavior. [The earlier PRD](archive/p0-baseline/prd.md), existing ADRs and
`decisions.jsonl` retain historical requirements and accepted clarifications.

## Purpose and scope

Scratchpad is private, self-hosted memory for developers and coding agents. It
captures decisions, ADRs, business decisions, findings, questions, failures,
constraints and project-state changes so a later session can recover why work
happened, what evidence supports it and what remains uncertain.

The core product works without AI, PostgreSQL, GitHub or repository writes. One
installation represents one owner with multiple credentials. The dashboard
supports inspection, curation, audit and configuration; agent workflows use the
local Go stdio MCP. This is not an issue tracker or autonomous policy authority.

## Current source product

The Go backend owns authentication, HTTP API/business logic, persistence,
migrations, projects, records, search/context, auditing, relationships,
import/export, optional AI/RAG and background jobs. React/TypeScript remains the
dashboard, with a private Node renderer. The Go MCP and capture service and the
native macOS application remain supported clients. SQLite is the default;
PostgreSQL is an optional engine.

Current source defaults are distinct from published release capabilities and
installed-deployment acceptance. The TypeScript backend has been removed from
the current source tree. Rollback tests extract its pinned historical revision
into disposable fixtures; it is not a supported runtime mode. Track completion in the
[P0 acceptance ledger](runbooks/p0-consolidation.md); never infer parity or a
production cutover from an opt-in build or a passing compilation.

## Required invariants

- Preserve original captures, identifiers, payloads, source claims and provenance.
- Keep raw, curated and derived knowledge distinguishable. Mutable curation is
  audited and old states remain recoverable.
- Distinguish explicit decisions, observed evidence, inference and suggestions;
  retrieval ranking never establishes authority.
- Persist required state across restart, including retries, sessions, challenges,
  queued jobs and revocation. Process-local caches are disposable.
- Keep central persistence authoritative and repository mirrors optional.
- Preserve credential provenance, authentication/recovery and project consent.
- Keep secrets out of logs, public responses, audits and knowledge exports.
- Preserve supported HTTP, generated SDK and MCP contracts through explicit
  transitions; new compact context must not silently replace legacy context.

## Capture and project identity

Support eight typed capture tools: `record_decision`, `record_adr`,
`record_business_decision`, `record_finding`, `record_qa`, `record_failure`,
`record_constraint` and `record_project_state`. Callers provide natural typed
fields once; deterministic rendering creates readable text without AI. Unknown
historical fields and precise immutable JSON numbers remain readable and portable.

Questions may omit an answer. A separately captured applicable current answer
can resolve a question through an accepted `answers` relationship; the original
question is never rewritten. Suggested, archived, superseded, partial, unverified
or out-of-scope evidence must not falsely resolve it.

Resolve normalized Git repository identities centrally. Worktrees share a
project while preserving per-call branch/commit/checkout provenance. Trusted Git
discovery disables executable repository-selected helpers. Non-Git or ambiguous
projects require explicit resolution. `workingDirectory` is a per-call local MCP
context, not a central-server filesystem location or shared current-project state.

All captures enter the collector's encrypted durable queue before acknowledgment.
Pending receipts have no central IDs. Delivered receipts preserve central IDs
across restart. Network absence does not prevent local intake; collector absence
must return an explicit error, with no direct HTTP fallback. Reads and
non-capture mutations remain online-only. Saved work stays bound to its original
server, credential, client save timestamp, request identity and checkout context.

Authenticated verification advertises `clientSavedAt-v1` before deferred delivery.
`recordedAt` remains server-owned; optional `clientSavedAt` records durable local
acceptance. Same identity/content retries return the existing record; changed
content conflicts. Different intentional captures never deduplicate by text alone.

Mirroring requires both local consent and project permission. Eligible types
default to decisions, ADRs and business decisions. Central persistence comes
first; mirror failure remains a separate recoverable result. No automatic commit,
push or browser-to-repository synchronization is required.

## Authentication and access

Preserve independent passkeys, local SSH proof, administrator setup/recovery and
optional verified GitHub owner access. Private keys remain on clients. Setup and
recovery capabilities are short-lived and single-use. Recovery preserves knowledge
and invalidates existing sessions; credential revocation applies immediately.

GitHub owner OAuth and complete synchronized published SSH authentication/signing
key snapshots authenticate the same owner, not a team. Use stable account IDs,
five-minute synchronization, persistent local blocks and a 24-hour deadline since
last complete successful refresh. Failures and restarts never extend freshness.
Unlinking needs fresh independent authentication or recovery; replacement needs
fresh existing authentication or recovery. Both preserve independent credentials
and knowledge and revoke GitHub-derived access. Presentation profile linkage
never grants access. See the [owner-access ADR](adr/0001-github-owner-access.md).

## Retrieval, curation and portability

Always provide deterministic listing/full-text search, project/type/date/tag and
provenance filters, complete record detail and relationship/audit inspection.
Current context must reflect replacements, contradictions, applicability and
unverified imports. Explicit version-2 context offers bounded previews, distinct
source IDs, current-state priority and omission counts; legacy context remains
available. Follow [the context contract](runbooks/go-context.md).

Mutable project/record settings use optimistic versions and reject stale writes.
Curation never silently overwrites raw evidence or promotes AI interpretation.
Native knowledge export/import preserves supported knowledge, audit and source
references, while excluding credentials, sessions, provider secrets and jobs.
JSONL import preserves original lines/IDs or their provenance, reports diagnostics
and does not invent missing authority. Operational database backup is separate
from portable knowledge export.

## Optional processing and existing RAG

Optional OpenAI-compatible completion/embedding providers support source-linked
analysis, suggestions, classification, relationships, patterns, semantic search
and private Markdown documents. Global enablement never overrides per-project
consent. External projects default to AI and cross-project participation disabled.
Recheck source/privacy dependencies before dispatch, persistence, retrieval and
review; incomplete historical dependency provenance fails closed.

Jobs, schedules, leases, retry state and restart recovery live in the application
database. No external queue is required. Provider failures do not disable core
capture/search/readiness. Generated documents remain derived drafts with source
citations; sharing is an explicit owner action.

SQLite retains existing semantic search. Optional PostgreSQL passage RAG uses
operator-installed pgvector, durable asynchronous history/capture indexing,
source-faithful passages, compatible generations and exact vector ranking.
Hybrid retrieval fuses bounded lexical and semantic candidates, deduplicates
passages, applies live lifecycle/consent, and returns bounded cited evidence.
Incomplete indexing is explicit; provider/index failure permits lexical fallback.
Explicit cross-project requests still obey every selected project's consent.
P0 preserves and verifies existing RAG; it does not add new retrieval features.

## Upgrade, credentials and operations

Recognize supported schema-1 installations and apply ordered serialized,
transactional migrations before serving/working. Reject malformed/newer schemas
without automatic repair. Preserve raw captures and source history. Verify
fresh startup, supported upgrade, concurrency, failure/retry, restart and restore
on both engines.

Reusable provider secrets use versioned authenticated encryption and a separately
retained protected installation-key file. Never replace an existing key silently.
Track legacy secret conversion separately from schema migration. Missing/wrong
keys block affected provider operations without plaintext fallback; ordinary
memory and token-free providers remain usable. Keep installation keys and encrypted
settings together in recovery planning. See [secret recovery](runbooks/go-secret-configuration.md).

Scratchpad owns image publication, app manifests/configuration, migration
execution, deployment workflows, probes, validation, backups, restoration,
rollback and application credential integration. Generic platform infrastructure
stays outside the repository. Adoption preserves existing database/PVC, origin,
credentials and key. No destructive migration or live production cutover follows
from a code merge. The operator reviews the exact cutover and restore procedure,
validates a disposable restore and explicitly authorizes production action.

The schema-1 TypeScript artifact cannot read Go schema 2/3. Returning to it
requires the old artifact and verified pre-upgrade database snapshot with the
retained key. Never delete markers to force compatibility. Snapshot restoration
loses later writes; define the maintenance boundary before migration.

## Acceptance and delivery

Use synthetic data to demonstrate authenticated browser and actual stdio flows,
all capture types, immutable details, stable replay/conflicts, curation/audit,
Git/non-Git identity, paused-project resumption and persistence after restore.
Use public behavior seams rather than private implementation assertions.

Validate Go tests/race/vet, frontend type/test/build, lint/format, generated SDKs,
MCP, SQLite migrations/restore, PostgreSQL 18/pgvector, existing RAG, clean
containers, dashboard-to-Go integration and macOS client/package compatibility.
Do not remove failing checks or legacy tests until equivalent Go behavior is
proven. Compare representative concurrent-agent behavior and report measured
limitations. Local tests, canonical GitHub CI, publication and deployed acceptance
remain separate evidence. Follow [P0 verification](runbooks/p0-consolidation.md).

## Planned enterprise direction — P1

Future work includes GitHub App user/installation identity, multiple users and
teams, organization/project membership, per-project authorization, user-bound
agent credentials and permission-aware retrieval/RAG. These are planned rather
than implemented. Existing owner OAuth, immutable history and project consent
are foundations, not multi-user authorization.

Enterprise design must enforce permissions across records, search, context,
relationships, exports and derived inputs, with explicit treatment of historical
personal data. P0 does not implement enterprise RBAC, migrate to GitHub Apps or
add new RAG features. New ingestion corpora, model reranking, approximate indexes,
external vector services and distributed workers remain outside this scope.
