import { createHash } from "node:crypto";
import { mkdtempSync, rmSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { createApi } from "./api";
import { defaults, type JsonObject } from "./domain";
import { exportKinds, importLegacy, importNative } from "./imports";

const cleanup: (() => void | Promise<void>)[] = [];
afterEach(async () => {
  for (const action of cleanup.splice(0)) await action();
});
async function fixture(path = ":memory:") {
  const api = createApi({
    databasePath: path,
    origin: "http://localhost:3000",
  });
  cleanup.push(async () => {
    if (api.store.sqlite.open) await api.close();
  });
  await api.store.insert("project", {
    id: "project",
    name: "Example",
    kind: "normal",
    slug: "example",
    settings: defaults("normal"),
  });
  await api.store.insert("credential", { id: "test", kind: "ssh" });
  await api.store.insert("session", {
    id: createHash("sha256").update("token").digest("hex"),
    credentialId: "test",
    browser: false,
    expiresAt: new Date(Date.now() + 60000).toISOString(),
  });
  const call = async (
    path: string,
    method = "GET",
    body?: unknown,
    headers: Record<string, string> = {},
  ) => {
    const response = await api.handleRequest(
      new Request(`http://localhost:3000/api/v1${path}`, {
        method,
        headers: { authorization: "Bearer token", ...headers },
        body: body === undefined ? undefined : JSON.stringify(body),
      }),
    );
    return { status: response.status, data: await response.json() };
  };
  return { ...api, call };
}
const actor = { kind: "import" as const };
// Structural fixtures mirror observed legacy formats without copying private source content.
const source = [
  {
    id: "D001",
    date: "2025-01-02",
    recordType: "decision",
    title: "Retain telemetry boundaries",
    status: "superseded_or_partial",
    decision: [
      "Use public device metadata.",
      "Do not claim measured throughput.",
    ],
    why: ["Metadata is observable without destructive probes."],
    implications: ["Present measured and inferred values separately."],
    extraSections: { Evidence: ["Local test transcript"] },
    supersession: {
      label: "Partial replacement",
      notes: ["Review the later exception."],
    },
  },
  {
    id: "D002",
    date: "2025-02-03",
    recordType: "business_decision",
    title: "Stakeholder requests a smaller status view",
    decision: ["Show only actionable status."],
    why: ["Stakeholder asked for less distraction."],
    requestedBy: "Example stakeholder",
    supersedes: ["D001"],
    status: "accepted",
  },
  {
    date: "2025-03-04",
    decision: "Keep original event evidence.",
    reason: "Allows later verification.",
    evidence: { build: "passed", review: "pending" },
    validation: ["Schema parsed"],
    customField: { retained: true },
  },
  {
    id: "legacy-paused",
    type: "project_state",
    title: "Paused for hardware validation",
    state: "paused",
    reason: "Awaiting a test device",
    date: "2026-01-15",
  },
];

describe("legacy history fidelity", () => {
  it("preserves array decisions, original IDs, precise source evidence, missing authority, and typed project state", async () => {
    const api = await fixture();
    const jsonl = source
      .map((row, index) => `${index === 0 ? "  " : ""}${JSON.stringify(row)}`)
      .join("\n");
    const result = await importLegacy(
      api.store,
      {
        format: "jsonl",
        projectId: "project",
        source: { filename: "decisions.jsonl" },
        jsonl,
      },
      actor,
    );
    expect(result.imported).toBe(4);
    expect(result.skipped).toBe(0);
    const first = await api.store.get("record", "D001");
    if (!first) throw new Error("Import omitted D001");
    expect(first.authority).toBeNull();
    expect(first.happenedAt).toBe("2025-01-02T00:00:00.000Z");
    expect(first.content).toContain("Do not claim measured throughput");
    expect((first.payload as JsonObject).legacyOriginal).toEqual(source[0]);
    expect((first.payload as JsonObject).rationale).toContain(
      "Metadata is observable",
    );
    expect(
      ((first.provenance as JsonObject).import as JsonObject).rawLine,
    ).toBe(jsonl.split("\n")[0]);
    expect(
      ((first.provenance as JsonObject).import as JsonObject).datePrecision,
    ).toBe("day");
    expect((await api.store.get("record", "D002"))?.type).toBe(
      "business_decision",
    );
    expect((await api.store.get("record", "legacy-paused"))?.type).toBe(
      "project_state",
    );
    expect(await api.store.list("relationship")).toHaveLength(1);
    expect((await api.store.list("relationship"))[0].status).toBe("suggested");
    const context = (await api.call("/projects/project/context")).data;
    expect(context.state[0].payload.state).toBe("paused");
    expect(context.currentState).toBeNull();
    expect(
      context.requiresReview.map((record: { id: string }) => record.id),
    ).toContain("legacy-paused");
    expect(
      (await api.call("/records?q=stakeholder&type=business_decision")).data
        .records,
    ).toHaveLength(1);
    expect((await api.call("/records?q=throughput")).data.records).toHaveLength(
      1,
    );
  });
  it("reimports idempotently after native round trip and detects changed original IDs", async () => {
    const api = await fixture();
    const body = {
      format: "jsonl",
      projectId: "project",
      sourceName: "history.jsonl",
      jsonl: source.map((row) => JSON.stringify(row)).join("\n"),
    };
    const first = await importLegacy(api.store, body, actor);
    expect(first.imported).toBe(4);
    expect((await importLegacy(api.store, body, actor)).imported).toBe(0);
    const exported = {
      format: "scratchpad",
      version: 1,
      data: Object.fromEntries(
        await Promise.all(
          exportKinds.map(async (kind) => [kind, await api.store.list(kind)]),
        ),
      ),
    };
    const restored = createApi({
      databasePath: ":memory:",
      origin: "http://localhost:3000",
    });
    cleanup.push(async () => await restored.close());
    await importNative(restored.store, exported, actor);
    expect((await importLegacy(restored.store, body, actor)).imported).toBe(0);
    const changed = await importLegacy(
      restored.store,
      {
        ...body,
        jsonl: JSON.stringify({ ...source[0], decision: "Different" }),
      },
      actor,
    );
    expect(changed.imported).toBe(0);
    expect(changed.warnings).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ code: "IMPORT_ID_CONFLICT" }),
      ]),
    );
    expect(await restored.store.list("record")).toEqual(
      await api.store.list("record"),
    );
  });
  it("keeps duplicate source occurrences and conflicting project identities without silent loss", async () => {
    const api = await fixture();
    const record = { date: "2025-02-03", decision: "Repeated source entry" };
    const result = await importLegacy(
      api.store,
      {
        projectId: "project",
        jsonl: [record, record].map((row) => JSON.stringify(row)).join("\n"),
      },
      actor,
    );
    expect(result.imported).toBe(2);
    expect(
      new Set((await api.store.list("record")).map((r) => r.id)).size,
    ).toBe(2);
    await api.store.insert("project", {
      id: "other",
      name: "Other",
      kind: "normal",
      settings: defaults("normal"),
    });
    const row = JSON.stringify(source[0]);
    await importLegacy(api.store, { projectId: "project", jsonl: row }, actor);
    const collision = await importLegacy(
      api.store,
      { projectId: "other", jsonl: row },
      actor,
    );
    expect(collision.imported).toBe(1);
    expect(collision.warnings).toEqual(
      expect.arrayContaining([
        expect.objectContaining({ code: "ID_COLLISION" }),
      ]),
    );
    const other = (await api.store.list("record")).find(
      (r) => r.projectId === "other",
    );
    if (!other) throw new Error("Import omitted colliding record");
    expect(other.id).not.toBe("D001");
    expect(
      ((other.payload as JsonObject).legacyOriginal as JsonObject).id,
    ).toBe("D001");
  });
});

describe("current context and live policy", () => {
  it("follows accepted replacement chains, preserves partial constraints, and orders state by occurrence time", async () => {
    const api = await fixture();
    const capture = async (
      type: string,
      payload: JsonObject,
      happenedAt: string,
    ) =>
      (
        await api.call("/records", "POST", {
          projectId: "project",
          record: {
            type,
            title: type,
            authority: "explicit",
            payload,
            happenedAt,
          },
        })
      ).data.record;
    const old = await capture(
      "constraint",
      { constraint: "Previous constraint" },
      "2025-01-01T00:00:00Z",
    );
    const next = await capture(
      "constraint",
      { constraint: "Replacement constraint" },
      "2025-02-01T00:00:00Z",
    );
    await api.call("/relationships", "POST", {
      fromRecordId: next.id,
      toRecordId: old.id,
      type: "partially_replaces",
    });
    await capture(
      "project_state",
      { state: "paused", reason: "Waiting for test hardware" },
      "2025-04-01T00:00:00Z",
    );
    await capture("project_state", { state: "active" }, "2025-03-01T00:00:00Z");
    let context = (await api.call("/projects/project/context")).data;
    expect(context.currentState.payload.state).toBe("paused");
    expect(context.partiallySuperseded[0].id).toBe(old.id);
    expect(context.constraints).toHaveLength(2);
    const suggestion = (
      await api.call("/relationships", "POST", {
        fromRecordId: old.id,
        toRecordId: next.id,
        type: "replaces",
        authority: "suggested",
      })
    ).data.relationship;
    expect(
      (
        await api.call(`/relationships/${suggestion.id}/accept`, "POST", {
          expectedVersion: 1,
        })
      ).data.error.code,
    ).toBe("RELATIONSHIP_CYCLE");
    await api.call("/relationships", "POST", {
      fromRecordId: next.id,
      toRecordId: old.id,
      type: "replaces",
    });
    context = (await api.call("/projects/project/context")).data;
    expect(context.constraints.map((r: { id: string }) => r.id)).toEqual([
      next.id,
    ]);
    expect(
      context.historicalRecords.map((r: { id: string }) => r.id),
    ).toContain(old.id);
  });
  it("enforces type settings and recomputes mirror permissions for capture replays", async () => {
    const api = await fixture();
    const settings = {
      ...defaults("normal"),
      enabledRecordTypes: ["decision"],
      repoMirroring: { enabled: true, recordTypes: ["decision"] },
    };
    await api.call("/projects/project/settings", "PATCH", {
      expectedVersion: 1,
      settings,
    });
    const body = {
      projectId: "project",
      record: {
        type: "decision",
        title: "A choice",
        authority: "explicit",
        payload: { decision: "A choice" },
      },
    };
    const first = await api.call("/records", "POST", body, {
      "Idempotency-Key": "retry",
    });
    expect(first.data.mirror.eligible).toBe(true);
    const external = await api.call("/projects/project", "PATCH", {
      expectedVersion: 2,
      kind: "external",
    });
    expect(external.data.project.settings.crossProjectAnalysis).toBe(false);
    const replay = await api.call("/records", "POST", body, {
      "Idempotency-Key": "retry",
    });
    expect(replay.data.record.id).toBe(first.data.record.id);
    expect(replay.data.mirror.eligible).toBe(false);
    expect(
      (
        await api.call("/records", "POST", {
          projectId: "project",
          record: {
            type: "finding",
            title: "Observation",
            authority: "observed",
            payload: { finding: "Observation" },
          },
        })
      ).data.error.code,
    ).toBe("RECORD_TYPE_DISABLED");
  });
  it("backs up the live SQLite database including authentication and persistent retry state", async () => {
    const directory = mkdtempSync(join(tmpdir(), "scratchpad-backup-"));
    cleanup.push(() => rmSync(directory, { recursive: true, force: true }));
    const api = await fixture(join(directory, "live.sqlite"));
    const body = {
      projectId: "project",
      record: {
        type: "finding",
        title: "Durable evidence",
        authority: "observed",
        payload: { finding: "Survives restore" },
      },
    };
    const before = await api.call("/records", "POST", body, {
      "Idempotency-Key": "backup-retry",
    });
    const project = await api.store.get("project", "project");
    if (!project) throw new Error("Backup fixture project missing");
    await api.store.update("project", {
      ...project,
      settings: { ...defaults("normal"), aiProcessing: true },
    });
    await api.store.insert("ai_settings", {
      id: "global",
      enabled: true,
      apiKey: "synthetic-backup-provider-secret",
      baseUrl: "http://127.0.0.1:1/v1",
      model: "synthetic-model",
      embeddingModel: "synthetic-embedding",
      embeddingDimensions: 3,
      scheduleMinutes: 0,
    });
    await api.store.insert("ai_job", {
      id: "queued-fixture",
      type: "embed",
      status: "queued",
      scope: { projectIds: ["project"], crossProject: false },
      attempts: 0,
      runAfter: new Date().toISOString(),
    });
    await api.store.insert("ai_job", {
      id: "leased-fixture",
      type: "analyze",
      status: "running",
      scope: { projectIds: ["project"], crossProject: false },
      attempts: 1,
      leaseToken: "synthetic-lease",
      leaseUntil: new Date(Date.now() + 60000).toISOString(),
    });
    await api.store.insert("ai_artifact", {
      id: "artifact-fixture",
      kind: "summary",
      title: "Synthetic backup summary",
      content: { text: "Synthetic derived memory" },
      sourceRecordIds: [before.data.record.id],
      projectIds: ["project"],
      authority: "derived",
      status: "pending",
      private: true,
      generator: { provider: "synthetic", model: "fixture", version: "1" },
    });
    await api.store.insert("ai_embedding", {
      id: "embedding-fixture",
      recordId: before.data.record.id,
      vector: [1, 0.5, 0.25],
      fingerprint: "synthetic-provider-fingerprint",
      contentHash: "synthetic-content-hash",
      dimensions: 3,
    });
    const operationalKinds = [
      "ai_settings",
      "ai_job",
      "ai_artifact",
      "ai_embedding",
    ];
    const operationalBefore = new Map(
      await Promise.all(
        operationalKinds.map(
          async (kind) => [kind, await api.store.list(kind)] as const,
        ),
      ),
    );
    const backup = join(directory, "backup.sqlite");
    await api.store.backup(backup);
    expect(statSync(backup).mode & 0o777).toBe(0o600);
    await expect(api.store.backup(backup)).rejects.toThrow();
    const restored = createApi({
      databasePath: backup,
      origin: "http://localhost:3000",
    });
    cleanup.push(async () => await restored.close());
    const response = await restored.handleRequest(
      new Request("http://localhost:3000/api/v1/records", {
        method: "POST",
        headers: {
          authorization: "Bearer token",
          "Idempotency-Key": "backup-retry",
        },
        body: JSON.stringify(body),
      }),
    );
    expect(response.status).toBe(201);
    expect((await response.json()).record.id).toBe(before.data.record.id);
    expect(await restored.store.list("record")).toHaveLength(1);
    for (const kind of operationalKinds)
      expect(await restored.store.list(kind)).toEqual(
        operationalBefore.get(kind),
      );
    const readRestored = async (path: string) => {
      const result = await restored.handleRequest(
        new Request(`http://localhost:3000/api/v1${path}`, {
          headers: { authorization: "Bearer token" },
        }),
      );
      expect(result.status).toBe(200);
      return result.json();
    };
    const configuration = await readRestored("/ai/settings");
    expect(configuration.apiKeyConfigured).toBe(true);
    expect(JSON.stringify(configuration)).not.toContain(
      "synthetic-backup-provider-secret",
    );
    const jobs = await readRestored("/ai/jobs");
    expect(jobs.jobs).toHaveLength(2);
    expect(
      jobs.jobs.some((job: { status: string }) => job.status === "queued"),
    ).toBe(true);
    expect(
      jobs.jobs.some(
        (job: { status: string; leaseToken?: string }) =>
          job.status === "running" && job.leaseToken === "synthetic-lease",
      ),
    ).toBe(true);
    expect(
      (await readRestored("/suggestions?projectId=project")).suggestions[0]
        .sourceRecordIds,
    ).toEqual([before.data.record.id]);
  });
});

describe("native archive integrity", () => {
  it("imports cited ordinary AI relationships, honors renewed consent and round trips curated evidence", async () => {
    const source = await fixture();
    for (const title of ["First", "Second"])
      expect(
        (
          await source.call("/records", "POST", {
            projectId: "project",
            record: {
              type: "decision",
              title,
              authority: "explicit",
              payload: { decision: "Preserve" },
            },
          })
        ).status,
      ).toBe(201);
    const archive = (await source.call("/export", "POST", {})).data;
    archive.data.project = [];
    const sources = archive.data.record.map(
      (record: { id: string }) => record.id,
    );
    archive.data.ai_artifact = [
      {
        id: "ordinary-ai",
        version: 1,
        createdAt: new Date().toISOString(),
        kind: "relationship_candidate",
        title: "Synthetic ordinary edge",
        content: {
          text: "Source-bound refinement",
          fromRecordId: sources[1],
          toRecordId: sources[0],
          relationshipType: "refines",
        },
        sourceRecordIds: sources,
        projectIds: ["project"],
        authority: "derived",
        status: "pending",
        generator: { model: "synthetic" },
      },
    ];
    const destination = await fixture();
    expect((await destination.call("/import", "POST", archive)).status).toBe(
      200,
    );
    await destination.ai.configure(
      { enabled: true, expectedVersion: 0 },
      { kind: "user" },
    );
    await expect(
      destination.ai.review("ordinary-ai", "accepted", { kind: "user" }, 1),
    ).rejects.toMatchObject({ code: "AI_NOT_ALLOWED" });
    const project = await destination.store.get("project", "project");
    if (!project) throw new Error("Synthetic project is missing.");
    await destination.store.update(
      "project",
      { ...project, settings: { ...defaults("normal"), aiProcessing: true } },
      project.version,
    );
    const raw = await destination.store.list("record");
    await destination.ai.review("ordinary-ai", "accepted", { kind: "user" }, 1);
    expect(await destination.store.list("record")).toEqual(raw);
    expect(await destination.store.list("relationship")).toMatchObject([
      { type: "refines", status: "accepted" },
    ]);
    const portable = (await destination.call("/export", "POST", {})).data;
    const restored = createApi({
      databasePath: ":memory:",
      origin: "http://localhost:3000",
    });
    cleanup.push(() => restored.close());
    await importNative(restored.store, portable, actor);
    expect(await restored.store.list("ai_artifact")).toEqual(
      await destination.store.list("ai_artifact"),
    );
    expect(await restored.store.list("curated_artifact")).toEqual(
      await destination.store.list("curated_artifact"),
    );
    expect(await restored.store.list("relationship")).toEqual(
      await destination.store.list("relationship"),
    );
  });
  it("atomically rejects AI lifecycle edges and per-kind content mismatches", async () => {
    const source = await fixture();
    expect(
      (
        await source.call("/records", "POST", {
          projectId: "project",
          record: {
            type: "decision",
            title: "Synthetic",
            content: "Preserve",
            payload: { decision: "Preserve" },
            authority: "explicit",
          },
        })
      ).status,
    ).toBe(201);
    const archive = (await source.call("/export", "POST", {})).data;
    const recordId = archive.data.record[0].id;
    const missing = structuredClone(archive);
    missing.data.project = [];
    missing.data.ai_artifact = [
      {
        id: "missing-citation",
        version: 1,
        createdAt: new Date().toISOString(),
        kind: "summary",
        title: "Synthetic",
        content: { text: "Missing source" },
        sourceRecordIds: ["missing-source"],
        projectIds: ["project"],
        authority: "derived",
        status: "pending",
        generator: { model: "synthetic" },
      },
    ];
    const clean = await fixture();
    expect((await clean.call("/import", "POST", missing)).status).toBe(404);
    expect(await clean.store.list("record")).toHaveLength(0);
    for (const content of [
      {
        text: "Supersede",
        fromRecordId: recordId,
        toRecordId: "uncited",
        relationshipType: "replaces",
      },
      {
        text: "Uncited",
        fromRecordId: recordId,
        toRecordId: "uncited",
        relationshipType: "related_to",
      },
      { text: "Classification", tags: [123] },
      { text: "Repeated classification citation", tags: ["valid"] },
    ]) {
      const invalid = structuredClone(archive);
      invalid.data.project = [];
      invalid.data.ai_artifact = [
        {
          id: "imported-ai",
          version: 1,
          createdAt: new Date().toISOString(),
          kind:
            "relationshipType" in content
              ? "relationship_candidate"
              : "classification",
          title: "Synthetic",
          content,
          sourceRecordIds: Array.isArray(content.tags)
            ? [recordId, recordId]
            : [recordId],
          projectIds: ["project"],
          authority: "derived",
          status: "pending",
          generator: { model: "synthetic" },
        },
      ];
      const destination = await fixture();
      const before = await destination.store.list("record");
      expect((await destination.call("/import", "POST", invalid)).status).toBe(
        400,
      );
      expect(await destination.store.list("record")).toEqual(before);
      expect(await destination.store.list("ai_artifact")).toHaveLength(0);
      expect(await destination.store.list("relationship")).toHaveLength(0);
    }
  });
  it("round trips revisions, source identities, evidence, settings, audit, and rejects unreadable or cyclic archives atomically", async () => {
    const api = await fixture();
    await api.store.insert("source", {
      id: "source",
      projectId: "project",
      kind: "git_remote",
      identity: "example.test/demo/memory",
    });
    api.store.sqlite
      .prepare("INSERT INTO identities VALUES(?,?)")
      .run("example.test/demo/memory", "project");
    const rows = source
      .slice(0, 2)
      .map((row) => JSON.stringify(row))
      .join("\n");
    await importLegacy(api.store, { projectId: "project", jsonl: rows }, actor);
    await api.call("/records/D001/metadata", "PATCH", {
      expectedVersion: 1,
      tags: ["historical"],
      displayTitle: "Curated telemetry constraint",
    });
    await api.call("/records/D001/evidence", "POST", {
      kind: "test",
      reference: "local test run",
      description: "Passed",
    });
    await api.call("/settings", "PATCH", {
      expectedVersion: 0,
      settings: {
        aiEnabled: false,
        defaultProjectSettings: defaults("external"),
      },
    });
    const archive = (await api.call("/export", "POST", {})).data;
    const restored = createApi({
      databasePath: ":memory:",
      origin: "http://localhost:3000",
    });
    cleanup.push(async () => await restored.close());
    await importNative(restored.store, archive, actor);
    for (const kind of exportKinds.filter((kind) => kind !== "audit"))
      expect(await restored.store.list(kind)).toEqual(
        await api.store.list(kind),
      );
    for (const entry of await api.store.list("audit"))
      expect(await restored.store.get("audit", entry.id)).toEqual(entry);
    const detail = (await api.call("/records/D001")).data;
    expect(
      detail.audit.some(
        (entry: { action: string }) => entry.action === "relationship.imported",
      ),
    ).toBe(true);
    for (const mutate of [
      (data: Record<string, JsonObject[]>) => {
        data.metadata = [];
      },
      (data: Record<string, JsonObject[]>) => {
        data.metadata[0].archived = "yes";
      },
      (data: Record<string, JsonObject[]>) => {
        data.relationship[0].type = "invented";
      },
      (data: Record<string, JsonObject[]>) => {
        data.relationship[0].status = "accepted";
        data.relationship.push({
          ...data.relationship[0],
          id: "reverse",
          fromRecordId: "D001",
          toRecordId: "D002",
        });
      },
    ]) {
      const invalid = structuredClone(archive);
      mutate(invalid.data);
      const destination = createApi({
        databasePath: ":memory:",
        origin: "http://localhost:3000",
      });
      cleanup.push(async () => await destination.close());
      await expect(
        importNative(destination.store, invalid, actor),
      ).rejects.toThrow();
      expect(await destination.store.list("project")).toHaveLength(0);
      expect(await destination.store.list("record")).toHaveLength(0);
    }
  });
});
