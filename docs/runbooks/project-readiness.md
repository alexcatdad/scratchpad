# Project readiness review

> Current authority: user-supplied originals are preserved verbatim in `docs/prd.md`, `docs/architecture.md`, and `docs/api.md`. Earlier reviews are historical. See “Architecture and API baseline reconciliation” at the end for the latest development assessment.

## Purpose

Assess whether Scratchpad has enough product direction to begin, without treating a readiness question as authorization to implement the application.

## Procedure

1. Read the referenced conversation through `read_thread`, using conversation ID `6abae803-c530-83ed-9c2a-4007ef9f109a` and `turnLimit: 10`. Follow any returned cursor. Distinguish explicit user choices from assistant proposals.
2. Inspect the workspace, applicable AGENTS.md instructions, and Git status before editing. Useful commands from the repository root:

   ```sh
   pwd
   rg --files --hidden -g '!.git' -g '!node_modules'
   git status --short --branch
   ```

3. Compare the discussion against any existing PRD, architecture, API contract, and decision records. Earlier discovery reports are evidence and proposals; they do not override later explicit user choices.
4. Separate product direction from implementation details and define a concrete first acceptance scenario.
5. Append material findings or decisions to `decisions.jsonl`, preserving their authority and uncertainty. Do not record assistant recommendations as user-approved requirements.

## Assessment — 2026-09-29

The repository initially contained only Git metadata, with no commits or application files. The conversation reader returned the final five exchanges, with no pagination cursor; earlier discussion was therefore not independently recovered. The supplied preview and returned closing summary identify the intended stack, but do not expose the original user selection of every technology.

There is enough direction to begin a PRD, initial architecture, and first vertical slice. This is an assistant readiness assessment, not approval of a complete specification or authorization to ship.

### Explicit choices visible in the retrieved user messages

- Scratchpad is the working name.
- Record types are distinct; capture rules can be tuned over time.
- Raw data is immutable; other data may change with an adequate audit trail.
- Agents report provenance/confidence and surface ambiguous project identity.
- Automated cleanup produces suggestions.
- The user accepted the proposed default of mirroring decisions/ADRs, with eligible types configurable.
- The user accepted the proposed cross-project restrictions for external/client projects.
- Database portability uses export/import rather than transparent engine migration.
- Optional AI uses an OpenAI-compatible endpoint, including the user's LM Studio setup.

### Direction reported by the conversation's closing assistant summary

Private, self-hosted developer memory; TanStack Start and Node 24; SQLite by default with PostgreSQL optional; a Go stdio MCP client; Git-derived project identity; central API authority with optional repository mirroring; WebAuthn browser authentication and SSH-challenge MCP authentication; GitHub Actions, GHCR, GitHub Releases, and Homebrew distribution.

Preserve these as reported prior direction when drafting the PRD. Original selection messages and current technical feasibility were not verified in this review.

### Details to resolve during initial specification

- Minimum record schema and MCP/HTTP operations, including provenance, confidence, corrections, supersession, retry idempotency, and project scoping.
- First-owner enrollment and credential recovery. Requiring an API endpoint does not by itself specify how a credential is first trusted.
- Deterministic handling of remotes, forks, renames, and non-Git folders, with explicit ambiguity surfaced to the agent/user.
- Initial milestone boundaries and observable acceptance criteria.

### Proposed first acceptance scenario

An agent in a Git repository captures a record through the Go MCP client. The API persists it to SQLite with project identity and provenance. The browser can display it, and a fresh agent session can retrieve it. Repeating the same operation does not duplicate it; correcting it preserves raw history; querying another project does not disclose it. Importing the USB Boop log then tests preservation of real-world source data and provenance.

Defer clustering, embeddings, scheduled cleanup, recommendations, PostgreSQL implementation, and LLM processing until this basic flow works, as proposed in the conversation's closing summary. Whether each belongs in the eventual V1 remains a PRD scope decision.

## PRD authoring and maintenance

The user authorized writing the repository PRD on 2026-09-29. Maintain it at `docs/prd.md`.

1. Read this assessment, the existing PRD if present, and `decisions.jsonl` before revising scope.
2. Preserve explicit user decisions. Label choices recovered only from assistant summaries as carried-forward direction; label newly specified behaviors and milestone boundaries as proposals.
3. Describe the problem, users, scope, workflows, requirements, information model, acceptance criteria, open decisions, and operational boundaries. Keep technical contract details in subsequent architecture/API documents.
4. Include Scratchpad's own migration from bootstrap JSONL to service-backed project memory as a dogfooding milestone. Do not make manual JSONL maintenance a permanent requirement.
5. Append material decisions or authorship/scope records to `decisions.jsonl`. Do not silently promote draft requirements to user-approved decisions.
6. Review relative document links and requirement/acceptance references; parse each JSONL line and check unique IDs after an append. Inspect Git status and whitespace changes. Documentation-only work does not require application tests.
7. Link the PRD in the handoff and state any unresolved scope or source limitations that affect implementation.

## Development-start gap review — 2026-09-29

### Evidence and assessment

Reviewed PRD v0.1 and the workspace. The only project files are the PRD, this runbook, and `decisions.jsonl`; there are no application files, contract files, fixtures, CI workflows, README, or checked-in AGENTS.md. Git has no commits. An origin URL is configured, but remote availability/content was not checked; a local missing upstream ref is not proof of a missing remote repository.

The product direction is sufficient for foundation work. Before implementing dependent flows, replace the remaining conceptual boundaries with explicit contracts. This review does not change PRD scope or adopt the recommendations below.

### Prioritized gaps

| Priority / timing | Missing item | Concrete completion condition |
| --- | --- | --- |
| Before owner/project schema | Owner boundary | Settle single owner versus shared users. Multiple agent/device credentials are compatible with single owner. |
| Before enrollment implementation | Initial deployment and authentication flow | Choose the first supported deployment shape and browser origin; specify trusted initial enrollment, passkey registration, MCP SSH-key enrollment/challenge, sessions, revocation, and recovery. |
| Before capture implementation | Record and API/MCP contract | Supply representative payloads, field semantics, enum/unknown handling, raw-versus-editable boundaries, actor/source attribution, operation-key scope and retry semantics, pagination, and errors. Define how future amendments fit without implementing all of them in M1. |
| Before resolver implementation | Active workspace discovery | Define how the MCP client receives a repository path when its process working directory differs from the agent's workspace or one process serves multiple projects. Establish explicit-path/selection precedence, Git remote normalization, and ambiguity handling. |
| During initial scaffold | Architecture and reproducible development | Choose repository layout, package manager, maintained libraries, database access/migrations, contract ownership between TypeScript and Go, and configuration. Verify current compatibility before pinning versions. Provide README, repository agent instructions, ignored local data/secrets, run/test commands, and minimal CI. |
| Alongside first slice | Executable acceptance fixtures | Implement a real MCP protocol-to-API/database test and browser smoke path for the M1 scenarios, including restart persistence, retry conflicts, ambiguous identity, wrong-project access, and server failure. Use small synthetic fixtures before importing sensitive live data. |

### PRD ambiguities to resolve

1. **Analysis policy versus access:** P5 describes cross-project analysis; P6 and A6 apply eligibility to broader retrieval too. Decide whether disabling analysis should also exclude an owner's explicit multi-project search. Document human search, agent credential authorization, and automated analysis separately; one flag should not acquire an unstated meaning.
2. **Mirror coverage:** I4 refers to accepted central records, while the local MCP performs mirroring. Specify whether “accepted” means durably stored or lifecycle-approved, and whether browser-created records are mirrored. If all eligible central changes should reach a mirror, define pull/reconciliation and durable retry ownership before implementing it.
3. **M1 boundaries:** The full browser requirement includes editing/settings, but the first milestone only needs enrollment, project selection, and record listing/detail/search. Map requirements to milestones so the first slice does not accidentally inherit all of V1. Early credential revocation should accompany usable authentication even if broader management/recovery UX lands later.
4. **Repository identity is not checkout identity:** A known fork can be an independent project without prompting forever. Prompt on unresolved identity, preserve explicit mappings, and test multiple checkouts/worktrees independently of process launch location.

### Work that can wait

- Detailed mirror reconciliation and import formats until their M2 work, while preserving stable IDs/raw input in the initial model.
- Full UI polish and a complete multi-platform release matrix until core capture/retrieval works.
- Numeric performance targets until there is a measured baseline.
- PostgreSQL, embeddings, clustering, scheduled jobs, and optional AI until the core acceptance gate.
- The exact privileged erasure procedure until before sensitive live capture or release, as already required by the PRD; ordinary raw immutability is still an initial invariant.

### Suggested next sequence

1. Resolve owner scope first; then identify the first deployment environment and auth setup expectations.
2. Write a short architecture document plus concrete data/API/MCP examples covering the first slice. Propose remaining engineering defaults together, with current dependency verification when selecting libraries.
3. Clarify the PRD ambiguities and map M1 requirements explicitly.
4. Scaffold the repository, document development commands, add minimal CI, and implement the first slice with its executable acceptance checks.

Do not treat all later-release questions as prerequisites for starting development. No scaffold, dependency installation, network deployment, commit, or push was performed in this review.


## Reconciled development readiness — original PRD supplied 2026-09-29

### Source handling

The user supplied the original conversation's PRD as an attachment at `/Users/alex/.codex/attachments/14b8e359-b5b2-4518-9c1f-29aee05fa2fa/Pasted text.txt`. Its contents now occupy `docs/prd.md` verbatim. The prior reconstruction is preserved at `docs/archive/prd-reconstruction-v0.1.md` and is not current authority. Product requirements in the supplied PRD take precedence over conflicting assistant proposals in that reconstruction.

For future reviews, read the canonical PRD and this reconciliation first. Consult archived material only for historical rationale or explicitly labeled proposals. Do not reopen choices already answered in the supplied source. Current-session intent to use Scratchpad for its own memory remains recorded in decision 004, even though it is not repeated in the supplied PRD.

### Gaps closed and corrections

- **Owner:** one installation represents one owner, with multiple credentials (§§4, 27). The outstanding owner-scope question is closed.
- **Architecture:** TypeScript, Node 24, TanStack Start/React, Drizzle, SQLite default, PostgreSQL optional; Go with the official MCP SDK. TanStack Start owns API/auth routes; no separate HTTP framework initially (§31).
- **Deployment:** OCI container plus persistent SQLite volume; no required Redis, broker, vector database, or external identity service (§32). The specific first hostname/origin and local development configuration still need implementation decisions.
- **Authentication direction:** passkeys for the browser; short-lived key challenge and API authorization for MCP; private keys remain local. HTTPS Git users can enroll a separate/generated local key. Git identity and optional GitHub profile enrichment are not authentication (§§27–28).
- **Tool shape:** prefer distinct typed capture tools; retrieval includes project context, decision history, and related records (§16). Bounded project context is eventually required (§20); an AI implementation is not implied.
- **Mirroring:** default eligible types include general decisions, ADRs, AND business decisions (§17). My reconstruction omitted business decisions. The source describes MCP-local dual-write, not a general browser-to-checkout synchronization service. Do not add reconciliation machinery without a separate need/decision.
- **Configuration:** local MCP settings are endpoint, identity, and dual-write enablement; dashboard owns eligible types and application behavior (§§15, 26). Define the effective interaction of local/project settings without duplicating all dashboard settings locally.
- **Search:** include Git path/context and relationship filters, plus database-native full-text search (§19). My reconstruction omitted some filters.
- **State:** required cross-request auth state and queued work, when those features exist, must persist across process/container restarts (§29). This does not require building deferred AI jobs now.
- **Raw removal:** explicit destructive owner administration is permitted (§11.1). Its procedure remains to design; whether such an exception is allowed is settled.
- **Distribution:** macOS/Linux ARM64 and AMD64, WSL through Linux Homebrew, direct binaries; native Windows is optional. macOS release binaries require Developer ID signing and notarization. GitHub Actions is canonical, with checksums, GHCR publication, and automated tap updates (§§33–35).
- **MVP scope:** import/export, optional dual-write, relationship/history inspection, authentication, container deployment, and distribution are in the supplied MVP (§37). A smaller internal capture loop is a development checkpoint, not permission to call a reduced subset the finished MVP. PostgreSQL support is a product requirement; its delivery timing relative to the SQLite MVP needs explicit sequencing, rather than treating it as speculative.
- **Evidence scenarios:** preserve the source's technical-choice recovery, paused-project recovery, stakeholder rationale, restricted-repository, worktree, non-Git, and restart scenarios (§38).

### Remaining development work

1. **Enrollment and recovery protocol:** Specify trusted first-owner registration, passkey origin/session behavior, SSH public-key enrollment and signing format, challenge persistence/expiry/replay protection, authorization lifetime, revocation, and lost-device recovery. Mechanisms are already selected; wire behavior is not.
2. **First API/MCP/data contract:** Define concrete request/response examples for each capture type, common minimal envelope, source attribution, initial confidence representation, retry and conflict behavior, search pagination, errors, and relationships/history. Keep confidence/subtype evolution cheap as requested in §41; an initial versioned vocabulary does not require a permanent taxonomy.
3. **Workspace handoff:** Specify how each tool call identifies the active checkout if MCP process cwd differs from agent context or multiple projects share a process. Retain normalized Git identity, worktree provenance, and structured ambiguity behavior from §13.
4. **Scaffold and persistence lifecycle:** Pick layout/package manager, verify and pin dependencies, define Drizzle schema migrations and transactional writes, document configuration and development commands, and add repository guidance/minimal CI. Explicitly define how local dual-write enablement interacts with project policy.
5. **Executable acceptance plan:** Turn §38 into fixtures and protocol/browser tests, including restart of auth state, untouched restricted repositories, all initial capture types, worktree identity, and import/export. Add useful retry/conflict checks from the old draft as engineering proposals rather than presumed source requirements.

Cross-project analysis policy is specified, but the source does not explicitly equate it with authorization for all manual cross-project search. Define query behavior when implementing cross-project retrieval; do not silently carry forward that coupling from the reconstructed draft. Publishing prerequisites such as signing credentials and Homebrew automation are release setup work, not blockers to local scaffolding.

### Recommended next step

Write `docs/architecture.md` and a concrete first-slice contract using the supplied product choices. Resolve enrollment/workspace handoff in those documents, then scaffold and test the first internal loop while retaining the full source MVP scope. No additional broad product-discovery round is needed. No implementation, dependency validation, signing setup, release, commit, or deployment was performed during reconciliation.


## Architecture and API baseline reconciliation — 2026-09-29

### Source and maintenance

The user supplied two additional documents from the original conversation:

- `docs/architecture.md`: V1 Architecture, version 0.1, initial implementation baseline.
- `docs/api.md`: V1 Data Model and API Contract, version 0.1, initial implementation contract.

Both files are preserved byte-for-byte from the supplied attachments. Read all three canonical documents before choosing implementation defaults. Their tentative words such as “recommended,” “suggested,” and “may” remain tentative; importing a source is not a reason to harden every option into a mandatory decision. No separate conversational instruction to scaffold or deploy accompanied these documents; this activity records and reconciles the baselines.

Future source intake procedure: inspect the full attachment, check destination files and repository status, preserve supplied text, record source provenance and any supersession, update the current assessment, and verify copied contents and JSONL validity. Clarifications should be explicit amendments rather than silent changes to the supplied examples.

### Gaps now closed

The architecture establishes server/MCP responsibilities, a proposed repository layout, Git/worktree identity rules, database-native full-text search, optional local mirroring, persistent job direction, CI/release deliverables, and the first vertical slice. It explicitly sequences PostgreSQL and AI after the first SQLite slice.

The API document defines record types (`adr`, not the reconstruction's `technical_decision`), authority and initial confidence vocabularies, type-specific payloads, curated metadata/revisions, relationships/evidence, owner/credential/session models, endpoint names, error envelope, persisted idempotency direction, concurrency direction, and a first endpoint subset. OpenAPI is explicitly not required for the first scaffold commit.

There is no need to write another architecture or conceptual API document before starting foundation work. The next design work consists of bounded amendments and runnable implementation.

### First-slice clarifications

| Item | Existing baseline | Remaining implementation detail |
| --- | --- | --- |
| Trusted enrollment and browser access | Single owner, passkeys, enrolled public-key challenge auth | First credential enrollment, recovery route, browser enrollment/login endpoints, configured origin, and how the first dashboard is authenticated. API §57's small endpoint subset does not enumerate all of this; it does not authorize bypassing browser auth. |
| Challenge proof and authorization | Single-use persisted challenges; namespace; short-lived token | Exact signed byte representation/algorithm, binding to purpose/credential/instance, atomic consume behavior, token storage/expiry/revocation. Authenticated project resolution must follow obtaining authorization; API §56 lists resolution before authentication and needs ordering clarification. |
| Workspace context | MCP owns cwd/Git inspection | Per-call workspace source/precedence when process cwd differs from the agent's checkout, and behavior for multiple workspaces. Suggested origin priority must not override the stated competing-remote ambiguity rule. |
| Complete record shape | Raw Record in API §9; typed payloads in §§12–20; payload in create example §38 | Put payload explicitly in persisted/returned record shape and define type/payload validation. Specify which actor/credential/timestamp fields the server derives versus client-supplied attribution. Define content/payload consistency rather than silently discarding one. |
| Resolved project on create | Resolution returns stable ID; create example uses sourceIdentity | Define ID-based creation for manual/non-Git projects and how conflicting supplied identifiers are rejected. Define when resolve creates versus only matches projects. Weak folder/manifest hints must not silently merge unrelated projects. |
| Mutation and list behavior | Persisted Idempotency-Key; version or ETag; cursor direction | Choose concrete key scope, payload comparison, expiry/replay behavior, conflict response, initial concurrency mechanism, list response envelopes, ordering, cursor semantics, and validation limits. |

These are implementation details to settle alongside the first scaffold; they do not justify another broad product discovery round. An initial confidence vocabulary is already provided, so do not reopen it as missing.

### Later-feature completion details

- **Mirroring:** The state model exists, but no report-result endpoint is specified. Define how MCP records success/failure, local/project enablement precedence, append/retry behavior, and whether status is per checkout. Two-way sync remains out of scope; do not invent browser-to-checkout replication.
- **Curated state:** The amendment example uses `curatedSummary`, which is absent from `RecordMetadata`; define the projected current state, allowed fields, and reconstructable revision rules when implementing edits.
- **Relationships:** Accept/reject routes require a review state and reviewer provenance separate from original authority. Define direction, partial-replacement scope, and which relationship kinds forbid cycles; a blanket ban would be ambiguous for symmetric related/contradicting links.
- **Import/export:** Define a versioned manifest, source preservation and duplicate/collision semantics, unsupported legacy fields, and settings/secret exclusions before native round-trip acceptance.
- **Jobs:** Architecture calls for leases, but the conceptual job shape omits lease ownership/expiry and crash-recovery details. Specify these when adding jobs, not as a prerequisite to initial record capture.
- **Cross-project queries:** Preserve the distinction between explicit owner retrieval and automated analysis; the analysis flag's meaning must not silently expand.

### Ready-to-start conclusion

The product, architecture, and logical API baseline are now present. Foundation implementation can begin after choosing routine package/runtime versions and recording first-slice clarifications. Next execution sequence: validate current dependencies, scaffold the chosen layout and database migrations, establish authenticated enrollment/project/capture/retrieval, add browser listing and acceptance checks, then expand toward the full PRD MVP. Use the supplied §57 subset as an internal milestone, not the completed product scope.

This reconciliation did not verify dependency compatibility, execute application tests, scaffold code, create a commit, or publish anything.


## Initial documentation checkpoint

The user requested a local commit of the initial documents before discussing implementation recommendations. For this checkpoint: inspect Git status and the complete file list; validate decision-log JSONL and supplied-document copies; stage only `docs/` and `decisions.jsonl`; inspect the staged summary; commit with `docs: add initial Scratchpad specifications`; verify the resulting commit and clean working tree. Do not push unless requested. Subsequent recommendations remain proposals until resolved with the user.


## Recording accepted implementation clarifications

For each user-approved recommendation, inspect current files, append a clearly dated clarification to the relevant canonical documents, append an accepted decision with the user's source statement, and validate JSONL uniqueness plus whitespace changes. Preserve the baseline and distinguish accepted behavior from unapproved protocol details. Implementation and commit/push are separate actions.

On 2026-09-29 the user accepted browser-first setup with an administrator-generated single-use token, passkey registration, dashboard-managed credentials, SimpleWebAuthn, and separate administrator-authorized recovery. Architecture §24 and API §59 close that gap. Detailed MCP challenge/token behavior is the next discussion.


On 2026-09-29 the user accepted the simplified MCP authentication flow with sessions lasting up to 24 hours, client-memory token storage, reauthentication after MCP restart, immediate credential revocation, and no refresh tokens. Architecture §25 and API §60 record it. The earlier 15-minute recommendation was not adopted. Workspace handoff is the next pending recommendation.


On 2026-09-29 the user accepted MCP process launch-directory discovery by default, optional per-call `workingDirectory`, and resolved-project visibility in results. Architecture §26 and API §61 close the workspace-handoff decision. Requiring a directory on every call was not adopted. The remaining record-shape clarification is next; no record-format proposal has been approved by this entry.


On 2026-09-29 the user accepted typed payloads with deterministically generated content, server-assigned identity/timestamps, and resolved-project-ID creation, explicitly requiring that schemas remain flexible and adapt through use. Architecture §27 and API §62 record the clarification. Keep optional fields optional, preserve historical raw captures/readability, and avoid premature schema machinery. Retry/concurrency defaults remain a separate pending recommendation.


On 2026-09-29 the user accepted repeat-capture replay, conflicts for changed content under the same request identity, conflicts for stale edits, and preservation of separate intentional captures even when text matches. Architecture §28 and API §63 record these semantics. Exact wire representations remain implementation details; repository mirroring enablement and failure behavior are the next proposed clarification.


## Decision-review completion — 2026-09-29

The user accepted simple local mirroring: local AND project enablement, central-first persistence, visible partial success, duplicate-safe retry, MCP-local capture scope, and no automatic Git commit/push. Architecture §29 and API §64 record it.

The main product choices for starting implementation are resolved across decisions 010–015: enrollment/recovery, MCP sessions, workspace discovery, flexible capture shape, retry/concurrency semantics, and mirroring. Earlier “next question” notes above are historical. Remaining protocol, dependency, pagination, storage, and migration details can be specified during implementation within these constraints. Keep the supplied full MVP scope while first building the smaller SQLite capture/retrieval loop.

The initial documentation checkpoint is `be48339`. The user subsequently requested a local commit of accepted clarifications 010–015. Follow the checkpoint procedure above, staging only the four changed documentation/decision files, and use `docs: record accepted implementation decisions`. Verify the resulting commit and clean working tree; do not push.
