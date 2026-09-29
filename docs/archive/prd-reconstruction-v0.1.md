> HISTORICAL RECONSTRUCTION — superseded by the user-supplied original PRD on 2026-09-29. This file preserves earlier agent proposals, not current product authority. Use [the canonical PRD](../prd.md) and [the updated readiness review](../runbooks/project-readiness.md). Its M0–M3 labels are not approved release boundaries.

# Scratchpad — Product Requirements Document

**Version:** 0.1  
**Date:** 2026-09-29  
**Status:** Draft for review; not an implementation or release claim  
**Working description:** Private, self-hosted developer memory for decisions, findings, Q&A, failures, and project history, with first-class MCP access and optional AI.

## 1. Purpose and source authority

Scratchpad helps developers and coding agents remember why a project is the way it is, recover useful context across sessions, and distinguish past reasoning from currently applicable guidance.

This document turns discovery into a buildable product proposal. It uses:

1. The retrieved closing exchanges of [Decision Log PRD](chatgpt-conversation://6abae803-c530-83ed-9c2a-4007ef9f109a), including the user's explicit answers about record semantics and the working name.
2. The current repository conversation: write the PRD here, maintain a bootstrap decision log, and aim for Scratchpad eventually to hold its own project memory.
3. Earlier decision-log research, available locally at `/Users/alex/Documents/Codex/decision-log-product-discovery.md`, as evidence of problems rather than approved product scope. Its observations describe a historical source-project snapshot, not current implementation or measured product demand.

The conversation reader returned only the last five exchanges and no older-page cursor. Some architecture choices are visible only in the previous assistant's closing summary. This document preserves that distinction:

| Classification | Meaning |
| --- | --- |
| Confirmed direction | Explicit user choice or acceptance visible in the recovered discussion/current conversation |
| Carried-forward direction | Reported as settled in the previous assistant's summary; original approval is not available here |
| Proposed requirement | Concrete behavior introduced by this PRD to make the product implementable and testable |
| Open decision | An unresolved choice with a stated implementation gate |

Unless identified as confirmed or carried forward, requirements, defaults, milestone boundaries, and acceptance criteria below are proposals for this draft. Authorizing this document does not imply that every new proposal has already been approved.

## 2. Problem

Reasoning disappears into agent conversations, pull requests, notes, and individual memory. Code records what exists; it often omits rejected alternatives, changes in assumptions, failed experiments, and why work stopped.

A repository JSONL log is a useful starting point. As it grows, inconsistent fields, stale status labels, incomplete provenance, and overlapping decisions make retrieval difficult. Reading the newest record is insufficient to establish current applicability. Accepted decisions also do not prove implementation, deployment, or real-world acceptance.

Scratchpad should answer:

- What did we decide or learn, and why?
- Who said it, in which project and context, and with what confidence?
- What changed afterward, and what remains unresolved?
- What does a new session need to continue the work?

The initial opportunity is grounded in the owner's workflows and existing decision logs. Broader demand and time savings remain hypotheses to validate through use.

## 3. Users, jobs, and outcomes

| User | Job | Desired outcome |
| --- | --- | --- |
| Developer / project owner | Resume work after context has faded | Recover relevant reasoning and unfinished work without rereading entire conversations |
| Coding agent | Capture useful context and retrieve it in a later session | Access project-scoped records with provenance, stable references, and visible uncertainty |
| Developer working across personal and client projects | Reuse lessons within appropriate boundaries | Control cross-project analysis, AI processing, and repository mirroring per project |

**Proposed V1 audience boundary:** one owner per self-hosted instance, potentially using several devices and agents. Multiple credentials do not imply multiple independent users. Team roles, shared workspaces, and multi-tenant SaaS are outside this draft's V1 scope.

### Goals

- Make useful capture low friction, without requiring every record to become a formal ADR.
- Make basic capture, search, retrieval, and history work without an LLM.
- Preserve raw inputs and show the provenance of changes and derived material.
- Keep the API authoritative across browser and MCP clients.
- Allow complete export and re-import without dependence on Scratchpad's database engine.
- Use Scratchpad to maintain its own project memory once the reliability gate is met.

### Non-goals for V1

- Replacing Git, issue tracking, runbooks, or project documentation.
- Automatically treating a recorded statement as verified current truth.
- Automatically resolving contradictory decisions or promoting AI suggestions into accepted guidance.
- Recording every terminal command or implementation step already evident from Git.
- Transparent synchronization between independently writable repository files and the server.
- An offline writable MCP database with later conflict reconciliation.
- Transparent SQLite-to-PostgreSQL engine migration.
- Launching every optional analysis capability before the basic memory workflow is useful.

## 4. Product direction and boundaries

| Area | Direction | Authority |
| --- | --- | --- |
| Name | Scratchpad is the working name | Confirmed |
| Capture | Low threshold for useful context; tune the rule through use | Confirmed |
| Types | Distinct record types, including technical and business decisions | Confirmed |
| History | Raw captures remain immutable; other data can change with an adequate audit trail | Confirmed |
| Provenance | Agents report authority/provenance and their confidence | Confirmed |
| Cleanup | Suggestions; no destructive rewriting of raw memory or automatic promotion of canonical findings | Confirmed |
| Repository mirroring | Optional; decisions/ADRs by default, eligible types configurable | Confirmed |
| Cross-project analysis | Configurable; off for external/client projects, on for other classified projects | Confirmed acceptance of prior recommendation |
| Ambiguous identity | Agent surfaces ambiguity to the user | Confirmed |
| Database portability | Export/import rather than transparent engine migration | Confirmed |
| Optional AI | OpenAI-compatible endpoint, including the user's LM Studio setup | Confirmed |
| Architecture | Central API, Git-derived identity, Go stdio MCP, browser dashboard | Carried forward |
| Stack | TanStack Start, Node 24, SQLite default, PostgreSQL optional | Carried forward |
| Authentication | WebAuthn/passkeys for browser; SSH challenge for MCP | Carried forward; enrollment/recovery unresolved |
| Distribution | GitHub Actions, GHCR, GitHub Releases, Homebrew tap | Carried forward |

The stack is an implementation target, not a claim that compatibility or dependency versions have been verified. Architecture work must validate it before scaffolding dependent components.

## 5. Scope and delivery stages

### M0 — Implementation contract

Produce the initial architecture, record schema, MCP/HTTP contracts, authentication enrollment design, and representative acceptance fixtures. Resolve open decisions that block the next stage. Keep bootstrap decisions in the repository.

### M1 — First working vertical slice

Deliver `agent → Go stdio MCP → project resolution → API → SQLite → browser`, plus retrieval from a new agent session. Include the authentication/enrollment necessary to use the slice, immutable capture, project-scoped list/search/get, retry safety, and visible write errors.

Exit when an authenticated agent can capture a record in a known Git project, the browser can display it, and a fresh agent session can retrieve the same record. An uncertain project must require resolution before capture. A repeated operation must not create a second record.

### M2 — Useful and trustworthy V1

Add audited corrections, relationships, portable import/export, project settings, optional repository mirroring, credential management/recovery, backup/restore documentation, and packaged installation. Validate with the USB Boop log first, then an explicitly selected Asource dataset for a larger case.

V1 must support the core record types, filtering and text search, provenance inspection, and explicit unresolved/superseded relationships without AI. It must satisfy the acceptance matrix in section 12.

### M3 — Optional expansion

After core acceptance, consider PostgreSQL support, OpenAI-compatible processing, embeddings, clustering, scheduled cleanup, recommendations, and cross-project pattern analysis. These are intended extension areas, not commitments to ship them all together. The core remains usable with all of them disabled.

## 6. Primary workflows

### 6.1 Install and enroll

The owner starts the service with persistent storage, establishes a trusted first credential, and signs into the browser. The owner then authorizes an MCP credential and configures the MCP client's API endpoint. The client reports configuration or authentication failures with a specific recovery action.

The service does not use an unauthenticated public first-visitor claim to establish ownership. The exact enrollment mechanism is an M0 decision. Configuring an endpoint alone does not establish trust.

### 6.2 Capture from a project

The MCP client derives project candidates from the current Git repository. A known project can be reused; an ambiguous match is presented to the agent to resolve with the user. A non-Git folder requires explicit project selection or creation.

The agent records a decision, rationale, non-obvious finding, useful Q&A, failure, constraint, or meaningful project-state change. It includes source and authority information and receives a stable record ID. It does not need a polished essay or an approval round trip for every ordinary capture.

An agent inference must remain labeled as an inference. Capturing a user decision must identify its source; an agent's confidence does not make it user-authorized.

### 6.3 Resume in a fresh session

The agent resolves the project, searches or lists relevant records, and retrieves full records as needed. Results include provenance, relationship/status indicators, and source references. Conflicting and historical records remain inspectable. The agent can cite record IDs when explaining its understanding.

### 6.4 Correct or evolve knowledge

The owner or an authorized client adds an amendment, changes audited metadata, or links a replacement. Raw captures remain available. Readers can inspect the previous state, actor, time, and reason for changes. Unrelated concurrent changes must not silently overwrite one another.

### 6.5 Import, inspect, and export

The owner previews an import, sees record counts and data-quality issues, then imports with source provenance retained. Legacy gaps remain explicit. Export includes raw captures, normalized records, relationships, revisions, and relevant project settings in a documented portable format. Credentials are excluded.

### 6.6 Use Scratchpad for Scratchpad

Import this project's bootstrap `decisions.jsonl`, verify capture/retrieval/export and recovery, then designate the service as the primary capture destination. The file may remain as an optional mirror or historical artifact. A hand-maintained JSONL file must not remain a permanent product prerequisite.

The switch is an explicit owner choice after validation. Building an importer alone does not satisfy the dogfooding milestone.

## 7. Functional requirements

### 7.1 Projects and settings

| ID | Requirement |
| --- | --- |
| P1 | Give each project a stable server identity independent of display name, local checkout path, or a single remote URL. |
| P2 | Normalize Git remote candidates and support reviewed aliases. Do not silently collapse forks, unrelated repositories, or ambiguous remotes into one project. Worktrees of the same resolved repository reuse the project. |
| P3 | Allow explicit project selection for non-Git folders and ambiguous matches. Treat monorepo subprojects as explicit configuration; do not guess them from directories. |
| P4 | Store independent settings for cross-project analysis, AI processing, and repository mirroring. Disabling one does not implicitly disable or enable another. |
| P5 | Default cross-project analysis off for external/client projects and on for explicitly classified personal/internal projects. Proposed additional default: unclassified projects remain excluded until classified. |
| P6 | Keep ordinary retrieval project-scoped. Cross-project retrieval must be explicit and honor eligible-project settings, including any derived summaries. |

### 7.2 Capture and record types

The initial vocabulary uses distinct types: `decision`, `technical_decision` (ADR), `business_decision`, `finding`, `qa`, `failure` (including lessons), `constraint`, and `project_state`. These identifiers are proposed schema names for the categories discussed in discovery.

| ID | Requirement |
| --- | --- |
| C1 | Require a resolved project, record type, substantive content, and capture provenance. Keep title, rationale, tags, and detailed evidence optional where unavailable; report unknowns rather than inventing them. |
| C2 | Preserve the submitted raw capture and unknown legacy fields. Normalize a separate view for display and retrieval. |
| C3 | Distinguish explicit user statements/decisions, human observations, agent observations, agent inferences, and AI-derived material. Track the authenticated writer separately from the claimed original speaker or author. |
| C4 | Accept agent-reported confidence and its explanation when supplied. Preserve missing confidence as unknown; do not fabricate a score or treat confidence as approval. |
| C5 | Make write retries idempotent using an explicit operation identity. An identical retry returns the original result; reuse with a different payload produces a conflict. Similar text from distinct events is not automatically a duplicate. |
| C6 | Return a durable record ID only after central persistence succeeds. Clearly distinguish stored, failed, and mirror-pending outcomes. |
| C7 | Publish MCP capture guidance: retain reasoning, useful findings, failures, constraints, durable Q&A, and stopping context; skip routine noise. The agent performs capture—Scratchpad does not independently observe every conversation. |

### 7.3 History, relationships, and authority

| ID | Requirement |
| --- | --- |
| H1 | Raw capture payloads cannot be changed through normal edit operations. Corrections retain access to the original. |
| H2 | Audit changes to editable content, tags, display metadata, relationships, and project policies with actor, time, previous/new values or equivalent reconstructable revisions, and reason when applicable. |
| H3 | Support explicit amendment, supersession, related-record, and supporting-evidence links. Partial supersession must describe the affected scope. Reject invalid targets and supersession cycles. |
| H4 | Do not infer that the latest record supersedes all previous records. Preserve unresolved contradictions and show them together when explicitly linked. Automatic contradiction detection is deferred. |
| H5 | Keep proposal/acceptance/supersession separate from evidence of implementation, testing, deployment, or product acceptance. These meanings must not collapse into a single “done” flag. |
| H6 | Use a revision precondition or equivalent conflict mechanism for edits so concurrent clients cannot silently erase each other's changes. |
| H7 | Present stored content as data, including imported text that resembles instructions. Record contents cannot change client permissions or server policy. |

### 7.4 Retrieval and browser experience

| ID | Requirement |
| --- | --- |
| R1 | Provide project-scoped list, text search, and get-by-ID without AI. Support pagination and filters for type, date, tags, provenance, and applicable lifecycle fields. |
| R2 | Return stable IDs, relevant excerpts, source references, and provenance. Clearly expose historical/superseded/derived status and known relationships. |
| R3 | Let the browser list projects, inspect/search records, capture a record, edit audited fields, follow history/relationships, and manage project settings. Browser and MCP use the same server semantics. |
| R4 | Distinguish no results, incomplete/paginated results, unavailable service, and permission failure. An empty result must not be presented as proof that a decision never existed. |
| R5 | Keep sign-out and credential management visible. Provide useful empty states and actionable errors. |
| R6 | Allow keyboard access to core browser workflows and a usable narrow-screen layout. Establish specific accessibility checks during UI implementation. |

### 7.5 Import, export, and repository mirroring

| ID | Requirement |
| --- | --- |
| I1 | Support preview and import of existing JSONL logs. Preserve source identity, original IDs/line references where available, raw content, and parsing/normalization warnings. Missing legacy fields are not silently invented. |
| I2 | Account for every input line as imported, already imported, or rejected/quarantined with a reason. Repeating the same import must not duplicate prior source records. |
| I3 | Export a versioned portable representation that can restore the semantic record graph, history, and provenance on a fresh instance. Preserve IDs or provide an explicit remapping that retains all references. |
| I4 | Keep the central API authoritative. Mirroring is an optional output from accepted central records, with decisions and technical ADRs eligible by default. Other types require configuration. |
| I5 | Report mirror failures separately from successful central writes and support safe retry. Never silently claim both succeeded or create another central record to retry a mirror. |
| I6 | Repository edits do not automatically overwrite server records. Re-ingestion is an explicit import with conflict reporting. Mirroring does not automatically commit or push Git changes. |
| I7 | Mirror format, path, eligible types, and concurrent-writer strategy must be specified before enabling mirroring. |

### 7.6 Optional AI and analysis

AI configuration uses an OpenAI-compatible endpoint and model selection. No AI credentials or endpoint are required for V1 core behavior. Proposed default: AI processing remains off until the owner configures it and enables eligible projects.

When analysis is added:

- Show which endpoint and projects will be used. A compatible endpoint is not necessarily local or private.
- Apply both AI-processing and cross-project permissions before assembling input.
- Keep derived records linked to their source records, processing time, and available model/configuration provenance.
- Treat summaries, clusters, recommendations, and canonical-record candidates as suggestions. Record acceptance or rejection explicitly.
- Never destructively merge/delete raw captures or silently elevate suggestions into user decisions.
- On a policy change, prevent future use of newly excluded sources and prevent derived results from disclosing them in disallowed contexts. Previously sent data cannot be unsent.

## 8. Conceptual information model

This is a product-level model, not a finalized database schema.

| Entity | Minimum meaning |
| --- | --- |
| Owner / credential | Instance owner, enrolled credentials, revocation and audit attribution |
| Project | Stable ID, name, identity aliases, classification, analysis/AI/mirroring settings |
| Raw capture | Immutable original payload, submitting principal, received time, source references, import origin if applicable |
| Record | Stable ID, project, type, content/display fields, source attribution, confidence, lifecycle metadata, current revision |
| Revision / audit event | Actor, time, action, reason, reconstructable change, operation identity |
| Relationship | Source/target record or evidence reference, relationship type, scope, provenance |
| Import/export manifest | Format version, origin, counts, validation findings, identity mappings |
| Derived artifact — later | Output, source IDs, processing provenance, suggestion/review state |

The authenticated submitting principal is verified by the service; claims about the original author remain attributed claims unless separate evidence establishes them. Observed time and server receipt time are distinct. Unknown historical values remain unknown.

## 9. System and interface boundaries

The carried-forward implementation target is a TanStack Start application on Node 24, a central HTTP API with SQLite as the first storage backend, and a Go stdio MCP client. PostgreSQL is an optional later backend reached through portable import/export, not an implicit V1 dependency.

The MCP client resolves local repository context, authenticates to the configured API, exposes memory operations to the agent, and performs configured local mirroring. It must not emit operational logs into the stdio protocol stream. The browser and MCP client share the API's validation, project scoping, audit, and mutation rules.

The initial contract must cover these capabilities without prematurely fixing tool names or URL routes:

| Capability | Required result |
| --- | --- |
| Resolve/select/create project | Stable project ID or structured ambiguity/error |
| Capture | Durable record ID and central persistence outcome |
| List/search/get | Scoped, paginated records with provenance and references |
| Amend/update/link | New revision or explicit conflict |
| Import/export | Manifest, counts, warnings, and usable portable output |
| Manage settings/credentials | Audited changes, clear authorization, visible resulting state |

Before coding, define payload schemas, size limits, pagination, stable error categories, operation-id semantics, revision conflicts, and client/server compatibility. A missing endpoint or unavailable API produces an actionable error; the MCP client must not silently fall back to an independent local source of truth.

## 10. Reliability, privacy, and operation

- **Persistence:** Acknowledged writes survive service restart. Multi-entity changes either commit coherently or expose a recoverable state; no success response for a partially lost central record.
- **Project boundaries:** Normal queries require project scope. Explicit broader queries and later analysis apply project policy. Tests include records and derived artifacts from excluded projects.
- **Credential lifecycle:** Enroll, inspect, revoke, sign out, and recover access through a documented procedure. Challenge authentication must prevent replay; browser authentication must have explicit origin/session rules. Detailed protocol design precedes implementation.
- **Sensitive data:** Do not log credentials or full record bodies in routine diagnostics. Configure endpoint secrets outside project mirrors/portable record exports. Limit diagnostics to identifiers and useful failure context by default.
- **Recovery:** Document backup and restore of persistent storage and configuration. Verify restoration on a fresh instance; portable export is not the only operational recovery mechanism.
- **Failure visibility:** Expose service/storage failures, rejected captures, import quality issues, and pending/failed mirror work. Health responses do not by themselves establish product acceptance.
- **Performance:** Measure capture/search latency and result quality with USB Boop, then the selected larger dataset. Establish numeric budgets from the measured baseline before release; no performance claim is established by this PRD.
- **Release path:** Carried-forward distribution uses GHCR for the service and GitHub Releases/Homebrew for the MCP client, with CI building and testing the corresponding artifacts. Exact supported operating systems and architectures remain open.

## 11. Dogfooding and validation

Validation is progressive:

1. Use small fixtures to prove identity resolution, attribution, capture, retry behavior, history, and project boundaries.
2. Preview/import the USB Boop log. Inspect representative incomplete, superseded, and changing-policy records. Count every source record and preserve original payloads.
3. Prepare a larger Asource case only after explicitly selecting the dataset and its processing permissions. Keep AI disabled unless separately enabled. Do not infer that reference to a dataset authorizes uploading it to an external AI endpoint.
4. Define a small set of owner-authored questions with expected source records. Compare finding those answers in the raw log versus Scratchpad, including wrong-project and superseded-record cases.
5. Record whether a fresh agent session successfully resumes a bounded task using cited records without inheriting obsolete guidance.
6. Import Scratchpad's bootstrap log and complete export/restore validation before switching its primary capture destination.

Product success initially means useful retrieval and trustworthy retention in the owner's real workflow. Track retrieval correctness, provenance coverage, capture friction, import preservation, and resumption outcomes. Record baselines before claiming time savings; capture count alone is not evidence of usefulness.

## 12. V1 acceptance matrix

| ID | Scenario | Required observable outcome |
| --- | --- | --- |
| A1 | Capture in a known Git project, restart service, retrieve in a new agent session | Same record and project IDs, original content, provenance, and timestamps remain available |
| A2 | Repeat a capture operation, then reuse its operation ID with changed content | First retry returns the original record; changed payload produces a conflict |
| A3 | Encounter ambiguous remotes, a fork, or a non-Git folder | No silent assignment; explicit resolution is required and recorded |
| A4 | Correct a record and attempt a stale concurrent edit | Original capture is inspectable, correction has audit history, stale edit conflicts |
| A5 | Supersede part of a decision and retrieve the topic | Relationship and affected scope are visible; original rationale and retained scope remain available |
| A6 | Query project A with matching records in project B | A-scoped results exclude B; explicit broader queries honor eligibility settings |
| A7 | Import a legacy JSONL dataset twice, including malformed/missing-field cases | Every line is accounted for, raw content is preserved, gaps are visible, repeat import creates no duplicates |
| A8 | Export and import into a fresh instance | Records, raw content, relationships, revisions, provenance, and relevant settings retain their meaning and references; credentials are not exported |
| A9 | Server write succeeds but local mirror fails | Central record remains retrievable, mirror failure is visible, safe retry does not duplicate it |
| A10 | AI is unconfigured or disabled | Core capture, search, history, import/export, and browser/MCP workflows function; no AI requests occur |
| A11 | Revoke a credential and exercise the documented recovery procedure | Revoked credential loses access; owner recovers through an explicit trusted path |
| A12 | API is unreachable or persistence fails during capture | Client reports failure/uncertainty accurately; it does not report an unpersisted record as saved |
| A13 | Restore a backup and complete packaged-install smoke checks | Restored records are usable; documented installation supports browser and MCP authentication/capture/retrieval |
| A14 | Import Scratchpad's own log, resume work, then export/recover it | Owner can choose Scratchpad as primary project memory without mandatory manual JSONL maintenance |

M1 requires A1–A3, the basic project isolation in A6, no-AI behavior in A10, and capture failure handling in A12. M2 completes the full V1 matrix. Later AI and PostgreSQL features need their own acceptance additions before release.

## 13. Open decisions and implementation gates

| Question | Proposed starting point / constraint | Must settle before |
| --- | --- | --- |
| Is one owner per instance the correct V1 boundary? | Single owner, multiple credentials; no team permissions model | M0 schema and auth design |
| How is the first owner enrolled, and how is access recovered? | Explicit administrator-controlled enrollment; no public first-visitor takeover | Authentication implementation |
| Which SSH signing/challenge mechanism and credential enrollment flow? | Preserve the SSH-challenge direction; specify trust, expiry, replay protection, and revocation | MCP authentication implementation |
| Exact record schema, confidence representation, and lifecycle vocabulary? | Minimal captures; optional unknowns; separate authority and outcome evidence | API/database contract |
| Git normalization, aliases, forks, renames, and monorepo mapping? | Stable server IDs; explicit resolution for ambiguity | Project resolver implementation |
| Which mutations may an authorized MCP credential perform? | Capture and retrieval required; proposed audited updates must have explicit permissions | Mutation/credential contract |
| How should immutable storage handle accidental secret capture or requested erasure? | No normal edit of raw captures; define an explicit privileged removal/redaction policy, including mirrors/backups, before release | V1 release; sooner if sensitive live capture begins |
| Mirror format, path, and concurrent local writers? | Optional output, decisions/ADRs by default; central persistence remains authoritative | Mirroring implementation |
| Supported package platforms, deployment assumptions, and measured performance budgets? | Validate against the owner's first deployment; document exact supported scope | Packaging and V1 acceptance |
| Which optional capabilities come next after V1? | Decide from dogfooding evidence; do not couple PostgreSQL, AI, and clustering into one mandatory release | M3 planning |

## 14. Risks and mitigations

| Risk | Product response |
| --- | --- |
| Capture becomes noisy enough to discourage retrieval | Start with a simple capture rule, filters, and provenance; tune using real sessions |
| High-confidence agent text is mistaken for user policy | Separate writer, attributed author, authority basis, confidence, and evidence |
| Old decisions look current | Preserve explicit amendment/supersession chains and unresolved qualifications; avoid automatic current-truth claims |
| Multiple stores diverge | Keep API authority explicit; expose mirror failures; require deliberate re-import |
| Cross-project or AI processing leaks client context | Apply independent per-project controls at retrieval and processing boundaries |
| Authentication or project identity is under-specified | Resolve enrollment, credential permissions, and identity rules before implementing dependent flows |
| Platform work overwhelms the useful memory loop | Gate optional features on the first working slice and measured dogfooding results |
| Immutable capture retains accidentally sensitive content | Resolve removal/redaction and recovery policy before sensitive use or V1 release |

## 15. Next deliverables

1. Resolve the M0 questions and record their authority in the bootstrap decision log.
2. Write the initial architecture and versioned record/MCP/HTTP contracts.
3. Establish fixtures and acceptance checks for M1.
4. Scaffold and implement the first vertical slice.
5. Complete V1 with import/export, audited evolution, mirroring, recovery, and packaged installation.

The [readiness runbook](../runbooks/project-readiness.md) describes the discovery and document-maintenance process. The [bootstrap decision log](../../decisions.jsonl) preserves important choices until Scratchpad can reliably serve this role itself.
