# Scratchpad — V1 Data Model and API Contract

**Version:** 0.1  
**Date:** 29 September 2026  
**Status:** Initial implementation contract

---

## 1. Purpose

This document defines the initial logical data model and HTTP API contract between:

- Scratchpad MCP;
- Scratchpad dashboard;
- Scratchpad server;
- future integrations.

The goal is not to freeze every field permanently.

It establishes stable concepts and API boundaries required to begin implementation.

---

## 2. Identifier Strategy

Scratchpad uses globally unique opaque IDs.

Recommended representation:

```text
UUIDv7
```

or an equivalent sortable globally unique identifier.

Prefixing may be used at serialization boundaries for readability:

```text
proj_...
rec_...
rev_...
rel_...
evi_...
job_...
```

Prefixing is optional implementation detail.

IDs must:

- remain stable across edits;
- survive export/import;
- not encode database engine details;
- not depend on Git path;
- not depend on record ordering.

---

## 3. Project

Conceptual model:

```ts
type Project = {
  id: string;
  name: string;
  slug: string;

  kind: "normal" | "external";

  createdAt: string;
  updatedAt: string;

  settings: ProjectSettings;
};
```

---

## 4. Project Source Identity

A project may have one or more source identities.

```ts
type ProjectSource = {
  id: string;
  projectId: string;

  kind: "git_remote" | "manifest" | "folder" | "manual";

  identity: string;
  displayValue?: string;

  createdAt: string;
};
```

Examples:

```text
kind: git_remote
identity: github.com/alexcatdad/scratchpad
```

```text
kind: manifest
identity: package:scratchpad
```

Project sources can change without changing the project ID.

This supports renamed or moved repositories.

---

## 5. Project Settings

```ts
type ProjectSettings = {
  repoMirroring: {
    enabled: boolean;

    recordTypes: RecordType[];
  };

  crossProjectAnalysis: boolean;

  aiProcessing: boolean;
};
```

Recommended defaults:

### Normal project

```text
repoMirroring: configurable
crossProjectAnalysis: true
aiProcessing: global default
```

### External project

```text
repoMirroring: false
crossProjectAnalysis: false
aiProcessing: conservative/global setting
```

---

## 6. Record Types

```ts
type RecordType =
  | "decision"
  | "adr"
  | "business_decision"
  | "finding"
  | "qa"
  | "failure"
  | "constraint"
  | "project_state";
```

---

## 7. Authority Type

```ts
type AuthorityType =
  "explicit" | "observed" | "inferred" | "derived" | "suggested";
```

Meaning:

### explicit

A person/source explicitly stated or made the decision.

### observed

The recording agent directly observed the outcome.

### inferred

The agent interpreted available evidence.

### derived

Scratchpad generated the information from other records.

### suggested

The system believes this may be useful but does not present it as established knowledge.

---

## 8. Confidence

Confidence is deliberately agent-reported rather than centrally calculated.

Initial representation:

```ts
type Confidence = "high" | "medium" | "low" | "unknown";
```

An optional explanatory field may accompany confidence.

```ts
confidenceReason?: string
```

The vocabulary may evolve later.

---

## 9. Raw Record

```ts
type Record = {
  id: string;

  projectId: string;
  type: RecordType;

  title: string;
  content: string;

  authority: AuthorityType;
  confidence: Confidence;
  confidenceReason?: string;

  happenedAt?: string;
  recordedAt: string;

  actor: RecordActor;
  gitContext?: GitContext;

  createdAt: string;
};
```

The original record is immutable.

Fields such as current display title, tags, and curated metadata should not require rewriting the raw record.

---

## 10. Actor

```ts
type RecordActor = {
  kind: "user" | "agent" | "import" | "system";

  displayName?: string;

  client?: string;
  clientVersion?: string;

  credentialFingerprint?: string;
};
```

Examples:

```json
{
  "kind": "agent",
  "client": "codex",
  "credentialFingerprint": "SHA256:..."
}
```

or:

```json
{
  "kind": "import",
  "displayName": "USB Boop decisions.jsonl"
}
```

---

## 11. Git Context

```ts
type GitContext = {
  repositoryIdentity?: string;

  remote?: string;

  branch?: string;

  commit?: string;

  rootPathHint?: string;

  worktree?: {
    detected: boolean;
    name?: string;
  };

  dirty?: boolean;
};
```

Local filesystem paths should be treated carefully.

They may be useful operationally but should not become permanent cross-machine identifiers.

---

## 12. Type-Specific Data

Each record may have structured payload data.

Conceptually:

```ts
type RecordPayload =
  | DecisionPayload
  | ADRPayload
  | BusinessDecisionPayload
  | FindingPayload
  | QAPayload
  | FailurePayload
  | ConstraintPayload
  | ProjectStatePayload;
```

---

## 13. Decision Payload

```ts
type DecisionPayload = {
  decision: string;
  rationale?: string;

  alternatives?: string[];
  consequences?: string[];
};
```

---

## 14. ADR Payload

```ts
type ADRPayload = {
  context?: string;

  decision: string;
  rationale?: string;

  alternatives?: Array<{
    name: string;
    reasonRejected?: string;
  }>;

  consequences?: string[];
};
```

---

## 15. Business Decision Payload

```ts
type BusinessDecisionPayload = {
  decision: string;
  rationale?: string;

  requestedBy?: string;

  businessContext?: string;

  expectedOutcome?: string;
};
```

No external stakeholder identity system is required.

`requestedBy` is descriptive provenance.

---

## 16. Finding Payload

```ts
type FindingPayload = {
  finding: string;

  environment?: string;

  limitations?: string[];
};
```

---

## 17. Q&A Payload

```ts
type QAPayload = {
  question: string;
  answer: string;

  limitations?: string[];
};
```

---

## 18. Failure Payload

```ts
type FailurePayload = {
  expected?: string;
  observed: string;

  cause?: string;
  resolution?: string;

  lesson?: string;
};
```

---

## 19. Constraint Payload

```ts
type ConstraintPayload = {
  constraint: string;

  reason?: string;

  scope?: string;
};
```

---

## 20. Project State Payload

```ts
type ProjectStatePayload = {
  state: string;

  reason?: string;

  previousState?: string;

  followUp?: string;
};
```

V1 should not hard-code a global project-state enum.

Projects may legitimately use:

```text
active
paused
blocked
maintenance
research
archived
```

and other domain-specific states.

---

## 21. Curated Metadata

Mutable metadata is separated from raw content.

```ts
type RecordMetadata = {
  recordId: string;

  displayTitle?: string;

  tags: string[];

  archived?: boolean;

  updatedAt: string;
};
```

Changing metadata creates an audit event.

---

## 22. Record Revision

Raw records are immutable.

A curated amendment or correction creates:

```ts
type RecordRevision = {
  id: string;
  recordId: string;

  revisionNumber: number;

  patch: object;

  reason?: string;

  actor: RecordActor;

  createdAt: string;
};
```

The implementation may store full snapshots rather than patches if simpler.

The required behavior is:

> previous visible states remain reconstructable.

---

## 23. Relationships

```ts
type RelationshipType =
  | "related_to"
  | "supports"
  | "contradicts"
  | "refines"
  | "replaces"
  | "partially_replaces"
  | "depends_on"
  | "implements"
  | "caused_by"
  | "answers";
```

Relationship:

```ts
type RecordRelationship = {
  id: string;

  fromRecordId: string;
  toRecordId: string;

  type: RelationshipType;

  note?: string;

  authority: "explicit" | "inferred" | "suggested";

  createdAt: string;
};
```

AI-created relationships default to:

```text
suggested
```

until accepted.

---

## 24. Evidence

```ts
type Evidence = {
  id: string;
  recordId: string;

  kind:
    | "url"
    | "git_commit"
    | "pull_request"
    | "issue"
    | "file"
    | "conversation"
    | "test"
    | "deployment"
    | "other";

  reference: string;

  revision?: string;

  description?: string;

  createdAt: string;
};
```

A linked source does not automatically mean Scratchpad verified its contents.

---

## 25. Repository Mirror State

```ts
type MirrorState = {
  recordId: string;

  attempted: boolean;
  succeeded: boolean;

  path?: string;

  error?: string;

  updatedAt: string;
};
```

Central-record success and mirror success are independent.

---

## 26. Audit Event

All important mutations produce audit events.

```ts
type AuditEvent = {
  id: string;

  entityType: string;
  entityId: string;

  action: string;

  actor: RecordActor;

  previous?: object;
  next?: object;

  createdAt: string;
};
```

Raw-record creation itself also produces an audit event.

---

## 27. Derived Artifact

```ts
type DerivedArtifact = {
  id: string;

  projectId?: string;

  kind:
    | "summary"
    | "cluster"
    | "pattern"
    | "duplicate_candidate"
    | "relationship_candidate"
    | "recommendation";

  content: object;

  sourceRecordIds: string[];

  generator?: {
    provider?: string;
    model?: string;
    version?: string;
  };

  createdAt: string;
  expiresAt?: string;
};
```

Derived artifacts are rebuildable.

---

## 28. Embedding

Logical representation:

```ts
type Embedding = {
  recordId: string;

  model: string;
  dimensions: number;

  vector: number[];

  createdAt: string;
};
```

Physical representation may differ between SQLite and PostgreSQL.

Embeddings are not part of the export's authoritative knowledge unless explicitly requested.

They may be regenerated.

---

## 29. Owner Profile

```ts
type OwnerProfile = {
  id: string;

  displayName: string;

  github?: {
    username: string;
    avatarUrl?: string;
    profileUrl?: string;
    linked: boolean;
  };

  createdAt: string;
  updatedAt: string;
};
```

There is one owner per installation in V1.

---

## 30. Credentials

Credential records support multiple enrolled credentials.

```ts
type Credential = {
  id: string;

  kind: "ssh" | "webauthn" | "local_key";

  label?: string;

  publicMaterial: object;

  createdAt: string;

  lastUsedAt?: string;

  revokedAt?: string;
};
```

Private credential material is never stored by Scratchpad.

---

## 31. Authentication Challenge

```ts
type AuthChallenge = {
  id: string;

  kind: "ssh" | "webauthn";

  nonce: string;

  expiresAt: string;
  consumedAt?: string;

  createdAt: string;
};
```

Challenges are single-use.

---

## 32. Browser Session

```ts
type Session = {
  id: string;

  credentialId: string;

  expiresAt: string;

  createdAt: string;
  lastSeenAt: string;

  revokedAt?: string;
};
```

---

## 33. Job

```ts
type Job = {
  id: string;

  type: string;

  status: "queued" | "running" | "completed" | "failed";

  payload: object;

  runAfter: string;

  attempts: number;

  startedAt?: string;
  completedAt?: string;

  lastError?: string;

  createdAt: string;
  updatedAt: string;
};
```

---

## 34. HTTP API Conventions

Base:

```text
/api/v1
```

JSON request/response bodies.

Errors use stable machine-readable codes.

Example:

```json
{
  "error": {
    "code": "PROJECT_IDENTITY_REQUIRED",
    "message": "Project identity could not be resolved safely.",
    "details": {}
  }
}
```

---

## 35. Authentication API

### Request MCP challenge

```http
POST /api/v1/auth/mcp/challenge
```

Response:

```json
{
  "challengeId": "...",
  "nonce": "...",
  "namespace": "scratchpad-auth",
  "expiresAt": "..."
}
```

---

### Verify MCP challenge

```http
POST /api/v1/auth/mcp/verify
```

Request:

```json
{
  "challengeId": "...",
  "publicKey": "...",
  "signature": "..."
}
```

Response:

```json
{
  "accessToken": "...",
  "expiresAt": "..."
}
```

The exact token format may be opaque rather than JWT.

---

## 36. Project Resolution API

```http
POST /api/v1/projects/resolve
```

Request:

```json
{
  "context": {
    "git": {
      "remote": "https://github.com/alexcatdad/scratchpad.git",
      "branch": "main",
      "commit": "abc123"
    }
  }
}
```

Response:

```json
{
  "project": {
    "id": "...",
    "name": "Scratchpad"
  },
  "resolution": {
    "source": "git_remote",
    "confidence": "high"
  }
}
```

Ambiguous:

```json
{
  "error": {
    "code": "PROJECT_IDENTITY_AMBIGUOUS",
    "details": {
      "candidates": []
    }
  }
}
```

---

## 37. Explicit Project Resolution

When the agent has asked the user:

```http
POST /api/v1/projects/resolve-explicit
```

Request:

```json
{
  "name": "USB Boop",
  "context": {
    "folder": "usb-boop"
  }
}
```

The API may:

- match an existing project;
- create a new project;
- request further disambiguation.

---

## 38. Create Record

```http
POST /api/v1/records
```

Request:

```json
{
  "project": {
    "sourceIdentity": "github.com/alexcatdad/scratchpad"
  },

  "record": {
    "type": "finding",
    "title": "USB throughput cannot be obtained from current metadata APIs",
    "content": "...",

    "authority": "observed",
    "confidence": "high",

    "payload": {
      "finding": "..."
    }
  },

  "gitContext": {
    "branch": "main",
    "commit": "abc123"
  }
}
```

Response:

```json
{
  "record": {
    "id": "...",
    "recordedAt": "..."
  },

  "mirror": {
    "eligible": false
  }
}
```

---

## 39. Record Retrieval

```http
GET /api/v1/records/:id
```

Returns:

- raw record;
- current metadata;
- revisions;
- relationships;
- evidence;
- mirror status;
- selected audit information.

---

## 40. Record Search

```http
GET /api/v1/records
```

Possible parameters:

```text
projectId
type
tag
q
from
to
branch
authority
confidence
limit
cursor
```

Example:

```text
GET /api/v1/records?projectId=proj_123&type=decision&q=sqlite
```

Cursor pagination is preferred over offset pagination.

---

## 41. Full-Text Search

Dedicated endpoint may be exposed:

```http
POST /api/v1/search
```

Request:

```json
{
  "query": "why don't we measure transfer speed",
  "projectIds": ["..."],
  "types": ["finding", "qa", "decision"],
  "limit": 20
}
```

Core search remains deterministic.

Semantic search may augment this later.

---

## 42. Project Context

```http
GET /api/v1/projects/:id/context
```

V1 deterministic result may include:

```json
{
  "project": {},
  "state": [],
  "recentDecisions": [],
  "constraints": [],
  "openFindings": [],
  "failures": []
}
```

AI-generated summaries may augment this later but must remain distinguishable.

---

## 43. Relationships

Create:

```http
POST /api/v1/relationships
```

Accept suggested relationship:

```http
POST /api/v1/relationships/:id/accept
```

Reject suggestion:

```http
POST /api/v1/relationships/:id/reject
```

Human/agent-explicit relationships and AI suggestions should not share indistinguishable authority.

---

## 44. Evidence API

```http
POST /api/v1/records/:id/evidence
```

Evidence references may be added after record creation.

---

## 45. Metadata Update

```http
PATCH /api/v1/records/:id/metadata
```

Example:

```json
{
  "displayTitle": "...",
  "tags": ["ci", "performance"]
}
```

Creates an audit event.

Does not alter raw record data.

---

## 46. Amendments

```http
POST /api/v1/records/:id/revisions
```

Request:

```json
{
  "reason": "Clarified affected scope",
  "changes": {
    "displayTitle": "...",
    "curatedSummary": "..."
  }
}
```

Raw capture remains unchanged.

---

## 47. Project Settings

```http
GET /api/v1/projects/:id/settings
PATCH /api/v1/projects/:id/settings
```

Configurable values include:

```text
repo mirroring
eligible mirror types
cross-project analysis
AI processing
```

---

## 48. Global Settings

```http
GET /api/v1/settings
PATCH /api/v1/settings
```

Potential values:

```text
AI enabled
OpenAI-compatible base URL
model
embedding model
cleanup schedule
default project settings
```

Secrets must not be returned in plaintext after storage.

---

## 49. AI Suggestions

Example:

```http
GET /api/v1/suggestions
```

Types:

```text
duplicate
relationship
pattern
summary
cleanup
```

Accept:

```http
POST /api/v1/suggestions/:id/accept
```

Reject:

```http
POST /api/v1/suggestions/:id/reject
```

Acceptance may create curated relationships/artifacts.

It never rewrites raw source records.

---

## 50. Export

```http
POST /api/v1/export
```

Export must preserve:

- projects;
- project identities;
- records;
- revisions;
- metadata;
- relationships;
- evidence;
- audit history;
- important settings where appropriate.

Generated embeddings need not be exported by default.

---

## 51. Import

```http
POST /api/v1/import
```

Import should support:

### Scratchpad native export

Round-trip capable.

### Legacy decision logs

Initial supported legacy format:

```text
JSONL
```

Import results include diagnostics:

```json
{
  "imported": 800,
  "skipped": 1,
  "warnings": [
    {
      "record": 42,
      "code": "MISSING_ID"
    }
  ]
}
```

Imports must not silently invent historical authority.

---

## 52. Health Endpoints

```http
GET /health
```

Basic process health.

```http
GET /ready
```

Confirms required persistence is available and migrations are valid.

AI provider availability must not determine core Scratchpad readiness.

---

## 53. API Idempotency

Record-creation APIs should support idempotency.

Suggested header:

```text
Idempotency-Key
```

This prevents an agent retry from creating duplicate raw records after network uncertainty.

Idempotency state must be persisted.

---

## 54. Concurrency

Mutable operations should use optimistic concurrency where needed.

Possible mechanism:

```text
version
```

or:

```text
ETag / If-Match
```

Raw record creation is append-only.

Conflicting curated edits must not silently overwrite one another.

---

## 55. Error Codes

Initial stable error set:

```text
AUTH_REQUIRED
AUTH_INVALID
AUTH_CHALLENGE_EXPIRED
AUTH_CHALLENGE_USED

PROJECT_NOT_FOUND
PROJECT_IDENTITY_REQUIRED
PROJECT_IDENTITY_AMBIGUOUS

RECORD_NOT_FOUND
INVALID_RECORD_TYPE
INVALID_RELATIONSHIP
RELATIONSHIP_CYCLE

MIRROR_NOT_ALLOWED
MIRROR_FAILED

VALIDATION_FAILED
CONFLICT

AI_DISABLED
AI_UNAVAILABLE

IMPORT_INVALID
EXPORT_FAILED
```

Additional codes may be added without changing the overall response envelope.

---

## 56. MCP-to-API Flow

Typical write:

```text
Agent calls record_finding
        ↓
MCP inspects repo
        ↓
MCP resolves project
        ↓
MCP authenticates if token expired
        ↓
POST /records
        ↓
central record persisted
        ↓
if project/settings permit:
MCP mirrors to repo
        ↓
MCP reports result to agent
```

---

## 57. First Implementation Milestone

Implement only the minimum necessary for:

```text
project resolution
+
MCP authentication
+
record creation
+
record retrieval
+
SQLite persistence
+
dashboard listing
```

Initial API subset:

```text
POST /auth/mcp/challenge
POST /auth/mcp/verify

POST /projects/resolve

POST /records
GET  /records
GET  /records/:id

GET /projects
GET /projects/:id
```

Everything else can build on this stable core.

---

## 58. Compatibility Rule

The HTTP API is the contract between the Go MCP and TypeScript application.

Neither implementation may rely on internal types from the other language/runtime.

The contract should eventually be published as OpenAPI.

Generated TypeScript and Go clients may be introduced if useful.

OpenAPI becomes documentation and validation, not a requirement for the first scaffold commit.

## 59. Accepted clarification — enrollment contract requirements

The owner accepted browser-first setup and administrator-controlled recovery on 2026-09-29; see architecture §24 and decision `scratchpad-20260929-010`.

The authentication contract must additionally support initial setup-token redemption with first-passkey registration, authenticated credential enrollment with MCP proof of possession, and a separate audited replacement-passkey recovery flow. Setup/recovery capabilities are short-lived and single-use; initial setup stays disabled after owner enrollment. These flows must preserve project data and restart-safe authentication semantics. Their endpoint names and exact wire formats will be specified during implementation.

## 60. Accepted clarification — MCP session lifetime

Per decision `scratchpad-20260929-011`, successful MCP challenge verification issues a session token valid for up to 24 hours, unless revoked earlier. The client stores it only in MCP process memory and authenticates again after restart or expiry. Credential revocation immediately invalidates associated sessions. No refresh-token API is required initially. Server-side authorization state must survive application restarts; challenges remain random, short-lived, and single-use. Exact signing and token representation remain implementation details.

## 61. Accepted clarification — local workspace selection

Project-scoped MCP tools accept optional `workingDirectory`; otherwise discovery starts from the process launch directory (decision `scratchpad-20260929-012`). Resolve context separately for each call and return the resolved project so the caller can see which project was used.

`workingDirectory` selects local MCP filesystem context, not a filesystem location for the central API to access or a permanent project identity. The MCP sends resolved project/Git context through the API; existing non-Git and ambiguity rules still apply.

## 62. Accepted clarification — capture payload and evolution

Decision `scratchpad-20260929-013` clarifies §§9, 12–20, and 38:

- Include typed `payload` explicitly in persisted and retrieved records. MCP callers provide natural type-specific fields once; readable `content` is generated deterministically from them and is not a second mandatory caller-authored body.
- Use the already resolved `projectId` on creation for Git and non-Git projects. The original source-identity example is illustrative discovery context, not the only supported creation path.
- The server assigns record IDs, receipt timestamps, and authenticated credential attribution. Caller-supplied source/actor descriptions do not override verified credential identity.
- Keep initial required fields small and evolve payload shapes through usage. Optional additions and improved renderers must not require rewriting immutable historical captures. Maintain historical readability and distinguish original payload from generated display/search representations.
- This clarification does not authorize dropping original imported content or unrecognized legacy fields. Legacy source preservation remains required.

The user's approval explicitly requires flexibility. Versioning and client-compatibility mechanics will be specified during implementation; a configurable schema engine is not an initial requirement.

## 63. Accepted clarification — retry and edit-conflict semantics

Decision `scratchpad-20260929-014` fixes the behavior described in §§53–54:

- Same capture request identity with the same content returns the existing record.
- Reuse of that identity with changed content returns `CONFLICT`.
- Different intentional captures remain separate even if their text matches.
- A mutable edit based on an outdated revision returns `CONFLICT` rather than overwriting a concurrent change.
- No automatic merge or content-based deduplication is required.

Persisted idempotency state and an explicit revision precondition must support these behaviors. Concrete key scope, equality rules, retention, and version-versus-ETag syntax will be specified during implementation.

## 64. Accepted clarification — mirror outcome contract

Per decision `scratchpad-20260929-015`, repository mirroring requires both local MCP enablement and project permission, with `decision`, `adr`, and `business_decision` eligible by default. Persist the central record before any mirror append. Return the central record identity even if mirroring fails, with an explicit partial-success outcome. Retrying must not duplicate central or local records.

The MCP reports mirror outcome for its local capture/checkout; the exact reporting endpoint and append coordination remain implementation details. This is not browser-to-repository synchronization, and no automatic Git commit or push is authorized by mirroring.

## 65. MVP implementation extensions

These additions document the current wire behavior while preserving the accepted flexible capture model.

- `ProjectSettings.enabledRecordTypes` is an array of initial record type names. Older stored settings default to all types. Disabling a type prevents new captures; it does not discard imported history.
- `PATCH /projects/:id` accepts `name`, `kind`, and optimistic version information (`If-Match` or `expectedVersion`). Switching to external disables mirroring and cross-project analysis; the owner can explicitly reenable either afterward.
- Legacy imported records may have `authority: null` when the source does not establish authority. New captures still require an explicit authority vocabulary value. The importer preserves original source claims without treating them as verified identity or current policy.
- JSONL import accepts `{format: "jsonl", projectId, jsonl, source: {filename}}` or `sourceName`. Results include `imported`, `skipped`, per-line `warnings`, and `records` entries containing `line`, `recordId` where available, and `status`. Safe historical IDs survive; fallback IDs retain their original value in provenance.
- Native import accepts up to 64 MiB request bodies; JSONL text is capped at 32 MiB. Other request bodies remain limited to 8 MiB. Imports are transactional and reject malformed native relationships, missing metadata, and conflicting identities.
- Project context retains its original arrays and adds `currentState`, `stateHistory`, `applicableRecords`, `partiallySuperseded`, `historicalRecords`, and `requiresReview`. Contextual records include `applicability`, `replacedBy`, and historical-status annotations. Imported claims remain visibly unverified.
- Record detail includes audit events for associated relationships and evidence as well as record mutations. Metadata revisions retain before/after snapshots.
- `GET /ready` returns `503 DATABASE_NOT_READY` if the schema-1 marker or required persistence objects are unavailable or unsupported. Startup validates existing databases before applying configuration or DDL; unsupported or incomplete existing schemas are refused rather than silently repaired.
- Date bounds and context ordering compare timestamp instants, preserving user-supplied fractional-second precision. Bounds are inclusive; equivalent representations denote the same instant.

Native export is a portable knowledge archive, excluding authentication state. The administrator's `backup <absolute-destination>` command creates a consistent full SQLite snapshot, including credential/session/retry state. Operational restore requires a stopped service and a fresh destination volume; see the server and deployment runbooks.

## 66. Remaining-product implementation extensions

These extensions describe the source implementation after the SQLite MVP. They do not retroactively add capabilities to the published v0.1.3 artifacts. Verify the installed version and release evidence before using the newer interfaces.

### Persistence and owner presentation

`SCRATCHPAD_DATABASE_URL` selects PostgreSQL when set to a `postgres://` or `postgresql://` URL; otherwise `SCRATCHPAD_DATABASE_PATH` selects SQLite. Both engines implement the same API, persisted authentication, idempotency, optimistic concurrency, audit, and deterministic search semantics. PostgreSQL uses native full-text search. Startup initializes an empty application database and refuses unsupported or incomplete existing schemas. Use a dedicated PostgreSQL database.

`GET /profile` returns `{profile, database: {engine: "sqlite" | "postgresql"}}`. The read-only engine indicator never exposes the connection URL. `PATCH /profile` accepts `{displayName, expectedVersion}` and returns `{profile}`. Display names are presentation data, not authentication.

### Optional AI configuration

`GET /ai/settings` returns the configuration directly, not under a `settings` wrapper. `PATCH /ai/settings` accepts an explicit `expectedVersion` (or `If-Match`) and any configurable fields:

```ts
type AiSettings = {
  enabled: boolean;
  baseUrl: string;
  model: string;
  embeddingModel: string;
  embeddingDimensions: number;
  scheduleMinutes: number;
  requestTimeoutSeconds: number;
  maxOutputTokens: number;
  reasoningEffort: "default" | "none" | "low" | "medium" | "high" | "xhigh";
  similarityThreshold: number;
  analysisModes: string[];
  apiKeyConfigured: boolean;
  version: number;
};
```

`apiKey` is a write-only optional field: omitting it preserves a stored key; an empty string removes it. Responses and audit configuration snapshots expose only whether a key is configured. Provider URLs must be HTTP(S), without embedded credentials, a query, or a fragment. `enabled` defaults to false. A schedule of `0` means manual processing only. The API accepts similarity thresholds from `-1` to `1`; the dashboard presents the usual nonnegative range. Provider request timeouts are configurable from 5 to 600 seconds, default 600. Reasoning effort defaults to `none`; `default` omits the provider-specific override. Choose a supported effort for the configured model. `maxOutputTokens` limits generated output (256–32,768; default 4,096). Size the timeout to the model and hardware: a large local model may require more than 180 seconds.

`GET /settings` and optimistic `PATCH /settings` additionally control `defaultProjectSettings` for new normal/internal projects. Existing projects retain their settings; external projects use conservative defaults regardless of normal-project defaults.

`POST /ai/test` uses synthetic completion and embedding input and returns `{ok, model, embeddingModel, dimensions}`. It does not process project history. The completion provider uses OpenAI-compatible chat completions; embedding requests use the compatible embeddings endpoint. Model output is schema-validated and cannot supply arbitrary source IDs.

### Jobs, scope and semantic retrieval

`GET /ai/jobs` returns `{jobs}`. `POST /ai/jobs` accepts `{type: "analyze" | "embed" | "export", projectId?, projectIds?, crossProject?, format?}` and returns HTTP 202 `{job}`. Choose one project for ordinary operations or explicitly request `crossProject: true`. Every selected project must permit the operation; explicitly selecting a denied project returns `AI_NOT_ALLOWED`. Unspecified cross-project scope includes only participating projects.

`POST /ai/jobs/:id/retry` accepts `{expectedVersion}` and returns HTTP 202 `{job}` for a failed job. Jobs retain status, attempts, due time, last error and lease state in the database. The worker recovers expired leases and bounds automatic attempts; retry is a separate audited action. Scheduling and worker execution remain independent of core readiness.

`POST /search/semantic` accepts `{query, projectId?, projectIds?, crossProject?, limit?}` and returns `{results: [{record, score}], model}`. An absent compatible index returns an empty result with `indexRequired: true`. Embeddings are compared only when provider/model/dimension fingerprint and source-content identity match. Changing the embedding configuration requires rebuilding the compatible index. Consent is evaluated before provider calls and before returning results; deterministic `/search` and `/records` remain independent of embeddings.

### Suggestions and documents

`GET /suggestions` returns `{suggestions}` and accepts `projectId`, repeated `projectIds`, and `crossProject=true` filters. Artifact kinds are `summary`, `classification`, `duplicate_candidate`, `relationship_candidate`, `contradiction`, `cluster`, `pattern`, `recommendation`, and `export`. Artifacts retain source IDs, participating project IDs, generator metadata, creation/generation timestamps, derived authority, and review status `pending`, `accepted`, or `rejected`.

`POST /suggestions/:id/accept` and `/reject` require `{expectedVersion}` and return `{suggestion}`. Acceptance creates an audited curated artifact. Classification can update audited curated tags/summary; an explicitly scoped relationship candidate can create a relationship while preserving AI derivation. No review action changes original captures, deletes source records, silently supersedes decisions, or turns AI interpretation into an explicit human decision. Current consent gates access to older artifacts as well as new analysis.

`POST /summaries/export` accepts `{projectId, format}` and returns HTTP 202 `{job}`. Formats are `handoff`, `architecture`, `decisions`, `client_history`, and `adr`. The generated `export` artifact contains private Markdown and server-added source citations. Downloading/sharing a document is a separate owner action; generation does not publish it or grant access to the instance.

Native knowledge exports now also preserve owner presentation profiles, AI artifacts and curated artifacts with their source references. They exclude credentials, sessions, challenges, setup tokens, retry identities, provider secrets/configuration, scheduled jobs and embeddings. Full operational PostgreSQL backup uses `pg_dump`/`pg_restore`; the administrator `backup` command remains SQLite-only and returns `BACKUP_EXTERNAL` for PostgreSQL.

### MCP extensions

The local stdio server adds `semantic_search`, `get_suggestions`, `process_memory`, `get_ai_jobs`, and `generate_document`. Project-scoped AI tools use explicit `projectId` or the existing checkout/working-directory discovery. Cross-project operations require `crossProject: true`; optional `projectIds` constrain that explicit scope. `process_memory.type` is `analyze` or `embed`; `generate_document.format` uses the five formats above. Poll `get_ai_jobs`, then read source-linked artifacts with `get_suggestions`. These tools neither enable AI nor override owner project consent. Existing `search_memory` remains deterministic.

The machine-readable [OpenAPI 3.1 contract](openapi.json) documents the implemented routes and schemas. It can be consumed without generated clients; authentication and optional project-consent boundaries remain server-enforced.
