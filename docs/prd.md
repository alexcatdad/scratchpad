# Scratchpad

**Product Requirements Document**  
**Version:** 0.1  
**Date:** 29 September 2026  
**Status:** Ready for initial implementation  
**Working name:** Scratchpad

---

## 1. Executive Summary

Scratchpad is a private, self-hosted developer memory system designed for software developers who work with coding agents across multiple projects.

It provides durable, structured memory for information that is routinely lost between coding sessions, conversations, branches, repositories, and projects:

- technical decisions and ADRs;
- business and stakeholder decisions;
- questions and answers worth retaining;
- non-obvious findings;
- failures and lessons;
- constraints;
- project-state changes;
- supporting evidence and provenance.

Scratchpad is agent-native. Its primary capture and retrieval interface is a local **stdio MCP**, which automatically discovers the project it is operating within and communicates with a central Scratchpad HTTP API.

The server provides persistent multi-project storage and a small browser dashboard.

Repository-local decision logs remain optionally supported through **dual-write**, but Git is not Scratchpad's storage authority. This allows Scratchpad to support projects where repository changes are undesirable or prohibited, including contractor/client repositories.

Scratchpad is useful without AI.

Optional OpenAI-compatible LLM and embedding support can later provide:

- summaries;
- duplicate detection;
- clustering;
- relationship suggestions;
- recurring-pattern detection;
- cross-project observations;
- cleanup recommendations.

AI-generated information is derived knowledge and must never silently modify or replace raw historical evidence.

---

## 2. Product Thesis

Developers routinely remember **what the code does** through source code and Git history but lose **why it became that way**.

Important reasoning is often distributed across:

- ChatGPT/Codex conversations;
- stakeholder conversations;
- issue trackers;
- pull requests;
- temporary debugging sessions;
- architecture discussions;
- notes;
- personal memory.

Git preserves code history well but does not reliably preserve the reasoning, rejected alternatives, business context, failed attempts, uncertainty, or reasons a project was paused.

Existing ADR and decision-log practices solve part of this problem, but they generally assume deliberate manual capture and repository-local storage.

Scratchpad instead optimizes for:

> **Capture cheaply now; organize intelligently later.**

Agents should be able to record potentially useful context while they work, with the cost of an additional record kept intentionally low.

The system then makes that history searchable and progressively more useful without requiring the developer to maintain a perfect knowledge base manually.

---

## 3. Evidence and Origin

Scratchpad originates from decision-log workflows already used in real projects.

One work-project decision log grew to 800 committed records. Although the records were valuable, the history also demonstrated inconsistent titles and rationale, missing IDs, ambiguous replacement relationships, and difficulty determining which historical guidance still applied.

The value of that history increased when decisions could be understood as chains of refinements, replacements, exceptions, retained constraints, and outcomes rather than as isolated records.

Additional motivating examples include:

### Stakeholder-history recovery

A stakeholder requested a product change.

The change was implemented and deployed.

Months later, the same stakeholder asked why the change had been made.

The implementation existed in Git, but the original business reasoning was difficult to recover.

Scratchpad should make the original request and resulting decision directly retrievable.

### Repeated technical investigation

Development of USB Boop was paused.

When development resumed, prior reasoning about why the application used USB metadata rather than actual file-transfer throughput had not been recorded clearly enough.

The same technical research had to be performed again.

Scratchpad should prevent repeated investigation when useful conclusions were already reached previously.

### Restricted repositories

Some contractor/client repositories cannot contain a personal decision log.

Those decisions currently require an external note system such as Notion.

Scratchpad must provide full project memory without requiring any repository modification.

---

## 4. Primary User

The primary user is:

> **An individual software developer working across multiple projects, frequently with coding agents, who wants durable private memory of why things happened.**

Typical users may include:

- individual developers;
- consultants;
- contractors;
- freelancers;
- homelab users;
- technical leads using the system privately.

Scratchpad is not initially designed as a collaborative company knowledge platform.

One installation represents one owner.

---

## 5. Product Principles

### 5.1 Self-hosted first

Scratchpad should be trivial to operate on a homelab, workstation, NAS, or small server.

No cloud service should be required.

---

### 5.2 Useful without AI

Capture, retrieval, filtering, history inspection, and project management must work without:

- an LLM;
- embeddings;
- a GPU;
- an external API key.

AI enhances Scratchpad but does not make Scratchpad functional.

---

### 5.3 Agent-native

Coding agents should be first-class clients rather than integrations added afterward.

The primary machine interface is MCP.

---

### 5.4 Capture should be cheap

Scratchpad deliberately accepts some noise.

The capture threshold should be approximately:

> Might future me care why this happened?

Agents should not need to perfectly classify or curate history before recording it.

Capture guidance will evolve with real usage.

---

### 5.5 Raw history is immutable

Original captured records are evidence.

They must never be silently rewritten, merged, summarized away, or deleted by automated processing.

---

### 5.6 Derived knowledge is disposable

Summaries, clusters, inferred relationships, embeddings, patterns, recommendations, and other derived data may be regenerated.

They must remain distinguishable from source records.

---

### 5.7 Provenance over false authority

Scratchpad must distinguish:

- what the user explicitly decided;
- what an agent observed;
- what an agent inferred;
- what an LLM summarized;
- what a background process recommended.

An agent-generated interpretation must not silently become an authoritative human decision.

---

### 5.8 Repository-aware, not repository-dependent

Git provides excellent context for project identification and provenance.

Git must not be required as Scratchpad's persistence mechanism.

---

## 6. Goals

Scratchpad should allow a developer or agent to answer:

- Why was this implemented this way?
- Who requested this behavior?
- What technical decision governs this component?
- What changed after the original decision?
- Have I investigated this problem before?
- What did I conclude last time?
- Why was this project paused?
- What failures occurred around this approach?
- Have I made a similar decision in another project?
- Which decisions currently apply to this project?
- What remains uncertain?
- What evidence supports this conclusion?

---

## 7. Non-Goals

Scratchpad V1 is not intended to provide:

- enterprise identity management;
- organizations;
- teams;
- invitations;
- RBAC;
- SAML;
- SCIM;
- billing;
- SaaS hosting;
- Kubernetes-first deployment;
- issue tracking;
- project management;
- runbook replacement;
- Git replacement;
- autonomous policy enforcement;
- automatic human-decision approval;
- destructive AI cleanup;
- mandatory semantic/vector search;
- mandatory cloud LLM processing;
- real-time collaboration.

Team functionality may be contributed or developed later, but the architecture should not be distorted around hypothetical enterprise requirements.

---

## 8. Core Record Types

Record types are intentionally distinct rather than forcing all developer memory into a generic decision object.

Initial supported types:

### 8.1 Decision

A general explicit choice.

Example:

> Use SQLite as the default Scratchpad database.

---

### 8.2 ADR / Technical Decision

An architecture or technical choice with rationale, alternatives, and consequences.

Example:

> Implement the local MCP in Go rather than TypeScript.

---

### 8.3 Business Decision

A product, stakeholder, commercial, or organizational decision.

Example:

> Stakeholder requested replacing workflow X with Y.

---

### 8.4 Finding

A non-obvious technical or operational observation.

Example:

> Moving the application into `~/Applications` allowed notification authorization to succeed.

---

### 8.5 Question & Answer

A question whose answer is expected to remain useful later.

Example:

> Why can't USB Boop display real transfer throughput?

---

### 8.6 Failure / Lesson

An unsuccessful approach, operational incident, unexpected behavior, or lesson worth avoiding later.

Example:

> Cleaning a temporary Homebrew tap also removed the actual installed application.

---

### 8.7 Constraint

A requirement that bounds implementation choices.

Example:

> Semantic processing must use a specific model/provider.

---

### 8.8 Project State

A meaningful project status transition and its reason.

Example:

> Development paused until an Apple Developer Program membership became financially reasonable.

---

## 9. Common Record Envelope

All records should contain a small common envelope.

Conceptually:

```json
{
  "id": "rec_...",
  "type": "finding",
  "projectId": "proj_...",
  "title": "...",
  "content": "...",

  "recordedAt": "...",
  "happenedAt": "...",

  "provenance": {},
  "confidence": "...",

  "gitContext": {},
  "relationships": [],
  "evidence": []
}
```

Exact schema design remains an implementation task.

Required fields should remain small enough that agent capture stays cheap.

---

## 10. Authority and Confidence

The MCP instructions must require the calling agent to report the nature of the record and its confidence appropriately.

The system should be capable of distinguishing at least:

### Explicit

The user or another source explicitly made the statement or decision.

### Observed

The agent directly observed a technical outcome.

### Inferred

The agent derived a conclusion from evidence but the conclusion was not explicitly stated.

### Derived

Scratchpad's optional AI/background processing created the information from existing records.

### Suggested

The system believes a relationship, consolidation, decision, or pattern may exist but has not promoted it automatically.

Agents are expected to report their own confidence rather than Scratchpad attempting to calculate semantic truth centrally.

---

## 11. Immutability and Audit Model

### 11.1 Raw records

Raw captured records are immutable.

Once created, their original content remains recoverable permanently unless the owner explicitly performs a destructive administrative action.

---

### 11.2 Corrections and amendments

Corrections should create an auditable revision or event.

The previous state remains recoverable.

---

### 11.3 Mutable information

Mutable information may include:

- tags;
- titles;
- display metadata;
- classifications;
- manually curated relationships;
- canonical summaries;
- project metadata.

Every meaningful mutation must produce an audit entry.

---

### 11.4 Derived information

Derived objects can be freely rebuilt.

Examples:

- embeddings;
- summaries;
- duplicate suggestions;
- topic clusters;
- pattern records;
- generated project overviews.

Derived objects must reference the raw records that support them.

---

## 12. Project Model

The Scratchpad API supports an arbitrary number of projects.

The MCP should not normally require a configured project ID.

Project identity is discovered from the working environment.

---

## 13. Project Discovery

### 13.1 Git repository

When operating within a Git repository, the MCP determines:

- repository root;
- configured remote;
- normalized repository identity;
- current branch;
- current commit;
- worktree information where applicable.

Equivalent remotes such as:

```text
git@github.com:owner/project.git
https://github.com/owner/project.git
```

must resolve to the same normalized repository identity.

Worktrees belonging to the same Git repository resolve to the same Scratchpad project.

Branch and worktree information remain record provenance, not project identity.

---

### 13.2 Ambiguous Git identity

If multiple remotes or repository conditions make project identity ambiguous, the MCP must report the ambiguity to the agent.

The agent asks the user which project should be used.

Scratchpad should not silently guess when ambiguity matters.

---

### 13.3 Non-Git directories

If no Git repository exists, the MCP may inspect contextual hints such as:

- `package.json`;
- other project manifests;
- current folder name.

These are discovery hints rather than strong identities.

If a reliable project cannot be resolved, the API returns a structured project-identity error.

The agent then asks the user for the desired project name and retries.

---

## 14. MCP

The Scratchpad MCP is a local **stdio MCP server written in Go**.

Its responsibilities are intentionally narrow:

1. receive MCP tool calls;
2. inspect the local development environment;
3. determine project context;
4. collect Git provenance;
5. authenticate to Scratchpad;
6. call the central HTTP API;
7. optionally write eligible records into the local repository;
8. return results to the agent.

Business logic belongs to the Scratchpad API, not the MCP binary.

---

## 15. MCP Configuration

Local MCP configuration should remain minimal.

Required configuration:

```text
API endpoint
authentication identity/configuration
dual-write enabled/disabled
```

Project IDs are not normally configured.

Other application behavior belongs in the Scratchpad dashboard.

---

## 16. MCP Tools

Initial tools should include capabilities equivalent to:

```text
record_decision
record_adr
record_business_decision
record_finding
record_qa
record_failure
record_constraint
record_project_state

search_memory
get_record
get_project_context
get_decision_history
find_related
```

The exact MCP naming can evolve during implementation.

The first version should prefer explicit tools and schemas over a single overloaded `record_memory` call.

---

## 17. Dual-Write

Scratchpad's central API is the primary memory store.

Repository storage is optional.

When enabled, the MCP may write selected record classes to a repository-local decision log.

Default repository-eligible records should include:

- decisions;
- ADRs;
- business decisions.

General Q&A, findings, failures, and background-derived information should remain central by default.

Eligible record classes should be configurable from the dashboard.

Restricted/client projects can disable repo mirroring entirely.

The central record should maintain enough provenance to indicate whether a repository mirror occurred.

---

## 18. Contractor and External Projects

Scratchpad must fully support projects where repository writes are prohibited.

A project can be marked as external/client work.

Recommended defaults for such projects:

```text
Repository mirroring: disabled
Cross-project analysis: disabled
LLM processing: configurable, conservative default
```

The user may explicitly override these settings.

---

## 19. Retrieval

Basic retrieval must not require AI.

Required deterministic retrieval includes:

- project;
- type;
- topic/tag;
- date;
- lifecycle/status where applicable;
- Git path/context;
- relationship;
- full-text search.

SQLite deployments should use SQLite full-text capabilities.

PostgreSQL deployments should use PostgreSQL-native search capabilities.

---

## 20. Project Context Retrieval

A key product capability should eventually expose a bounded project context suitable for an agent resuming work.

Example result:

```text
Project: USB Boop

State
- Paused

Reason
- Apple Developer Program membership deferred

Relevant decisions
- Metadata-based USB observation
- Notification behavior
- Signing/distribution assumptions

Findings
- ...

Known failures
- ...

Open questions
- ...
```

The summary must link back to underlying records.

---

## 21. Cross-Project Memory

Scratchpad's central storage enables a capability unavailable to repository-local decision logs:

> longitudinal memory across a developer's projects.

Future queries may include:

- Have I encountered this failure before?
- How many projects used this architectural pattern?
- Which approaches have I repeatedly reversed?
- Have I repeatedly made this assumption?
- What previous projects are similar to this situation?

Cross-project analysis is controlled per project.

It must never silently include projects where this capability has been disabled.

---

## 22. Optional AI Layer

AI functionality is optional.

Scratchpad V1 core features must not depend on it.

The initial provider interface should support **OpenAI-compatible APIs**, allowing use with environments such as LM Studio as well as compatible local or remote providers.

Provider-specific integrations are not required initially.

---

## 23. AI Background Processing

When enabled, scheduled processing may suggest:

- duplicate records;
- related records;
- topic clusters;
- decision chains;
- likely contradictions;
- canonical summaries;
- recurring failures;
- cross-project patterns;
- cleanup actions.

The operating principle is:

> **Throw at the wall during capture; curate later.**

However, cleanup remains suggestion-based.

AI may not automatically:

- delete raw records;
- merge raw records destructively;
- supersede decisions;
- promote an inferred decision to an accepted human decision.

---

## 24. Embeddings

Embeddings are an optional secondary index.

They may support:

- similarity search;
- cross-project pattern discovery;
- duplicate candidates;
- related-record suggestions.

Embeddings are derived state and must be rebuildable.

A dedicated vector database is not required.

PostgreSQL installations may later use pgvector.

SQLite vector capabilities may be added where appropriate without changing the authoritative record model.

---

## 25. Browser Dashboard

The dashboard is primarily for:

- browsing memory;
- inspecting project history;
- searching;
- viewing decision chains;
- reviewing AI suggestions;
- adjusting project configuration;
- managing authentication credentials;
- configuring optional AI;
- importing/exporting data;
- inspecting audit history.

It is not intended to become a project-management system.

---

## 26. Dashboard Configuration

The dashboard owns configuration such as:

- enabled record types;
- repository mirroring behavior;
- external/client designation;
- cross-project analysis;
- AI enabled/disabled;
- LLM endpoint;
- LLM model;
- embedding configuration;
- cleanup schedule;
- similarity thresholds;
- automated analysis settings;
- export/import;
- owner profile.

MCP-local configuration should not duplicate these settings.

---

## 27. Authentication

Scratchpad has one owner but supports multiple credentials.

Authentication mechanisms are intentionally different for browser and MCP usage.

---

### 27.1 Browser

The baseline below is expanded by the accepted GitHub owner-access clarification in §45.

Browser authentication uses **WebAuthn/passkeys**.

Passwords are not required.

Possible authenticators include:

- Touch ID;
- Face ID;
- hardware security keys;
- password-manager passkeys;
- platform passkeys.

Browser sessions may use secure server-side session cookies.

---

### 27.2 MCP

MCP authentication uses public-key challenge/response.

The preferred identity is an existing developer SSH/signing key where available.

The API issues a short-lived challenge.

The MCP signs it locally.

The server verifies proof of possession against an enrolled public key and issues short-lived API authorization.

The private key never leaves the developer's machine or hardware token.

---

### 27.3 HTTPS Git users

Git transport and Scratchpad authentication are separate concerns.

A developer using Git over HTTPS may still authenticate through:

- an SSH signing key;
- another enrolled developer key;
- a Scratchpad-generated local key if necessary.

GitHub credentials and PATs must not be reused as Scratchpad authentication secrets.

---

## 28. Git Identity and GitHub Profile

`git config user.name` may be used for friendly presentation such as:

> Hi Alex

It must not be treated as an authenticated identity.

GitHub integration is optional profile enrichment only.

Where a GitHub username can be confidently discovered, Scratchpad may retrieve public information such as:

- username;
- avatar;
- public display name;
- public profile URL.

The dashboard may display a check mark indicating:

> GitHub profile linked

This must not imply that GitHub authenticated the user.

GitHub is not part of Scratchpad's root of trust.

---

## 29. Stateful Application, Restart-Safe API

Scratchpad is a stateful application.

Persistent state includes:

- projects;
- records;
- revisions;
- credentials;
- browser sessions;
- temporary authentication challenges;
- configuration;
- scheduled-job state;
- AI-derived data.

The HTTP API process itself must not depend on required process-local memory.

Restarting or replacing the application container must not lose:

- application state;
- authentication state required across requests;
- queued background work;
- project context.

In-memory caching is permitted only as a disposable optimization.

---

## 30. Database

Scratchpad supports two persistence engines.

### Default

SQLite

Targeted at the normal single-developer self-hosted installation.

Benefits:

- one database file;
- one persistent volume;
- trivial backup;
- trivial restore;
- minimal infrastructure.

---

### Optional

PostgreSQL

Available for users who prefer PostgreSQL, heavier concurrency, or future native vector capabilities.

---

### Portability

Scratchpad does not provide transparent live migration between database engines.

Portability is handled through:

> **Export → Import**

The export format must preserve records, relationships, provenance, and audit history.

---

## 31. Technology Stack

### Server and dashboard

- TypeScript
- Node.js 24 LTS
- TanStack Start
- React
- Drizzle ORM
- SQLite by default
- PostgreSQL optionally
- WebAuthn/passkeys

TanStack Start owns:

- dashboard routes;
- server functions;
- external HTTP API routes;
- authentication endpoints;
- background scheduling integration.

A separate HTTP framework is not required initially.

---

### MCP

- Go
- official MCP Go SDK
- stdio transport
- native executable

The Go MCP handles:

- Git inspection;
- working-directory context;
- SSH/signing integration;
- HTTP calls;
- optional local repository writes.

---

## 32. Deployment

### Scratchpad server

Distributed as an OCI container through:

```text
GitHub Container Registry (GHCR)
```

Minimum deployment:

```text
Scratchpad container
+
persistent SQLite volume
```

Optional deployment:

```text
Scratchpad container
+
PostgreSQL
+
optional local LLM
```

No Redis, message broker, vector database, or external authentication service is required.

---

## 33. MCP Distribution

The Go MCP is distributed as native binaries through GitHub Releases.

Primary installation UX:

```text
Homebrew
```

using the existing project Homebrew tap.

Targets include:

- macOS ARM64;
- macOS AMD64;
- Linux ARM64;
- Linux AMD64;
- WSL through Linux Homebrew.

Direct GitHub Release binaries remain available.

Native Windows distribution may be added independently if required.

No npm registry is required.

---

## 34. macOS Distribution

macOS MCP release binaries must be:

- signed using an Apple Developer ID;
- submitted for Apple notarization before publication.

Homebrew should reference only the signed/notarized release artifacts.

---

## 35. CI and Release

GitHub Actions is the canonical CI/release system for Scratchpad.

CI should cover:

### TypeScript

- formatting/linting;
- type checking;
- tests;
- production build.

### Go

- formatting;
- linting;
- tests;
- race detection where appropriate;
- vulnerability scanning;
- native builds.

### Release

A tagged release should:

1. produce MCP native binaries;
2. sign/notarize macOS artifacts locally, then verify the uploaded artifacts in CI;
3. generate checksums;
4. publish GitHub Release assets;
5. build the Scratchpad application image;
6. publish the image to GHCR;
7. update the Homebrew tap automatically.

The owner performs Apple signing and notarization locally. Apple credentials stay in the local Keychain; CI verifies the signed archives before publication.

Existing release patterns from the developer's Paw Proxy, Homebrew tap, Go MCP, and notarized Go application projects should be reused where practical.

---

## 36. Initial Import

Scratchpad should support importing existing decision logs.

Import requirements:

- preserve original content;
- preserve existing IDs when possible;
- generate IDs for records lacking one;
- preserve source information;
- flag unsupported or incomplete historical fields;
- avoid silently inventing missing rationale or authority;
- be idempotent where possible.

The USB Boop and Asource decision histories are appropriate initial real-world validation datasets.

---

## 37. MVP

The first usable vertical slice is:

```text
Agent
  ↓
stdio MCP
  ↓
discover Git/project
  ↓
HTTP API
  ↓
SQLite
  ↓
dashboard
```

The MVP should support:

### Capture

- create each initial record type;
- provenance;
- project context;
- Git context;
- confidence/authority metadata.

### Retrieval

- search;
- project filtering;
- type filtering;
- record inspection;
- relationship/history inspection.

### Projects

- automatic Git-based discovery;
- explicit resolution on ambiguity;
- non-Git project support.

### Dual-write

- configurable;
- decision-oriented records only by default.

### Authentication

- WebAuthn browser owner;
- MCP key challenge authentication.

### Operations

- Docker deployment;
- SQLite persistence;
- backup/export;
- import.

### Distribution

- GHCR application image;
- GitHub Release Go MCP;
- Homebrew formula.

AI features are not required to prove the initial capture/retrieval loop.

---

## 38. MVP Acceptance Scenarios

### Scenario A — recover a historical technical choice

Import USB Boop history.

A new agent asks why USB Boop uses metadata rather than actual file-transfer throughput.

Scratchpad returns the relevant records and supporting history without requiring the developer to repeat the investigation.

---

### Scenario B — resume an interrupted project

USB Boop is paused.

Scratchpad can show:

- that development is paused;
- why it was paused;
- relevant unresolved work;
- recent important decisions.

---

### Scenario C — explain an old stakeholder request

A stakeholder asks why a behavior was changed several months earlier.

Scratchpad retrieves:

- the original business decision/request;
- its rationale;
- related implementation context;
- relevant later changes.

---

### Scenario D — contractor repository

The agent operates inside a repository where decision files must not be committed.

The MCP resolves the Git project normally.

Records are stored centrally.

The repository remains untouched.

---

### Scenario E — worktrees

Several worktrees exist for one repository.

Records from all worktrees resolve to one Scratchpad project while preserving individual branch/commit/worktree provenance.

---

### Scenario F — non-Git project

An agent operates in a non-Git project.

Scratchpad cannot establish a reliable project identity.

The API returns an explicit ambiguity/error response.

The agent asks the user for a project name and retries successfully.

---

### Scenario G — restart resilience

The Scratchpad container is restarted.

Projects, sessions where appropriate, records, credentials, configuration, pending persisted jobs, and history remain valid.

---

## 39. Post-MVP Capabilities

After the core workflow is proven:

### AI processing

- summaries;
- classification assistance;
- duplicate suggestions;
- relationship suggestions;
- contradiction candidates.

### Embeddings

- semantic similarity;
- related-record search;
- cross-project retrieval.

### Pattern memory

Example:

> You encountered substantially the same CI trade-off in four projects.

Patterns are first-class derived objects linking back to supporting records.

---

### Cleanup recommendations

Example:

> These six findings appear to describe the same technical conclusion.

The user may accept or reject suggested consolidation.

Raw evidence remains unchanged.

---

### Shareable summaries

Scratchpad remains private.

When collaboration is necessary, the user can intentionally generate:

- project handoffs;
- architecture summaries;
- decision reports;
- client-facing history;
- ADR exports.

Sharing derived documents is preferred over granting broad shared access to the private Scratchpad instance.

---

## 40. Success Criteria

The project should be considered useful when it demonstrably reduces repeated context reconstruction.

Initial qualitative measures:

- an agent can recover a previously recorded decision without developer explanation;
- a developer can resume an old project and recover why it stopped;
- stakeholder rationale can be recovered months later;
- relevant historical findings can be found across projects;
- recording useful context does not materially interrupt development;
- repository-restricted projects work without compromises;
- raw history remains auditable after later cleanup or summarization.

Record volume itself is not a success metric.

---

## 41. Deferred Decisions

The following should be tuned through actual usage rather than over-designed now:

- exact capture threshold;
- confidence vocabulary;
- record subtype schemas;
- similarity thresholds;
- cron frequency;
- AI prompts;
- pattern-scoring rules;
- relationship inference;
- dashboard information architecture;
- retention of low-value captures.

The architecture should allow these behaviors to evolve without changing the immutable raw record layer.

---

## 42. Working Product Description

> **Scratchpad is a private, self-hosted memory system for developers and coding agents. It captures decisions, findings, questions, failures, constraints, and project history through MCP and makes that context searchable across projects. Scratchpad works without AI and can optionally use local or OpenAI-compatible models to summarize, connect, and surface patterns in a developer's history.**

Short form:

> **Scratchpad — remember why the code is this way.**

## 43. Accepted clarification — illustrative projects and synthetic acceptance

On 1 October 2026, the owner clarified that other projects are examples, not sources of truth for Scratchpad's requirements. USB Boop and stakeholder-history references illustrate the intended workflows; their exact past conversations or decisions are not prerequisites for MVP acceptance.

Acceptance may use clearly labeled synthetic data to demonstrate the complete behavior in §§37–38, including a paused project, its reason, unresolved follow-up and relevant decisions surviving restart or restore. Synthetic examples must not be presented as facts about a real project. Real-history imports remain useful optional validation of import fidelity and retrieval, with their original provenance and uncertainty preserved.

## 44. Accepted clarification — optional extensions

On 2 October 2026, the owner requested the three previously optional additions: pgvector, public GitHub profile enrichment and generated API clients.

PostgreSQL may opt into native vector similarity while SQLite keeps its existing semantic retrieval. Vector storage remains derived and rebuildable, preserves the original embedding dimensions, and applies existing project consent and compatibility checks. This request does not require approximate indexing or dimension reduction.

Public GitHub profile linkage is an owner-controlled presentation feature with link, refresh and unlink behavior. It never enrolls an authentication credential, establishes an authenticated identity, or changes the passkey/SSH root of trust.

Generate consumable TypeScript and Go clients from the HTTP contract. Verify deterministic regeneration, compilation, authenticated requests and typed failure handling. Generated clients do not introduce direct cross-runtime dependencies between the central API and MCP implementations.

## 45. Accepted clarification — GitHub owner access

Accepted on 8 October 2026; see the accepted [GitHub owner-access ADR](adr/0001-github-owner-access.md) and issue #3. This supersedes the presentation-only authentication restriction in §§27–28 and §44 without automatically promoting existing descriptive profile linkage into verified identity.

One owner may choose GitHub browser sign-in and automatic machine authentication through synchronized published SSH authentication and signing keys. First-owner setup still requires the administrator's single-use token; existing owners link through authenticated Settings. The binding uses GitHub's stable account ID. Every machine proves possession locally using the existing SSH challenge; no separate dashboard approval is required for an eligible GitHub-managed key. GPG support remains deferred.

Synchronize both complete key sets every five minutes. Failed, malformed or incomplete synchronization retains the previous set and its original freshness timestamp. GitHub-managed keys can renew machine sessions and authorize requests for up to 24 hours after the last successful synchronization. Beyond that limit, deny GitHub-managed machine access until synchronization succeeds. Detected removal invalidates associated sessions; persistent local blocking overrides GitHub even after removal/re-addition. This is an explicit bounded revocation-delay tradeoff during outages. A valid browser session continues locally; an expired GitHub browser session requires GitHub availability or independent authentication.

Passkey-only setup, optional passkeys, independent manual SSH enrollment and administrator recovery remain available. Unlinking requires fresh independent local authentication or recovery. Account replacement requires fresh existing authentication or recovery. Both preserve knowledge and independent local credentials while invalidating GitHub-derived access; neither converts synchronized keys into local credentials.

A portable onboarding skill discovers a usable existing key through the agent's preferred Git/SSH tooling, configures the existing MCP connection and verifies a scoped authenticated read. It does not introduce a setup command, publish keys or copy private material. The operator configures one OAuth app per instance; client machines share that server configuration. Source implementation, release publication and live acceptance remain separate.
