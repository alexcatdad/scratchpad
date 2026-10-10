import { spawnSync } from "node:child_process";
import { gzipSync } from "node:zlib";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AiService, OpenAiProvider } from "./ai";
import { defaults, type Entity } from "./domain";
import { exportKinds, importNative } from "./imports";
import { Store } from "./store";

const stores: Store[] = [];
afterEach(async () => {
  for (const store of stores.splice(0)) await store.close();
});
const actor = { kind: "user" as const, displayName: "Synthetic test owner" };
async function fixture(fetcher: typeof fetch = mockProvider()) {
  const store = new Store(":memory:");
  stores.push(store);
  let time = Date.now();
  const ai = new AiService(
    store,
    fetcher,
    () => time,
    "https://memory.example.test",
  );
  await store.insert("project", {
    id: "p1",
    name: "Synthetic project",
    settings: { ...defaults("normal"), aiProcessing: true },
  });
  await store.insert("record", {
    id: "r1",
    projectId: "p1",
    title: "Use local storage",
    type: "decision",
    content: "Synthetic decision to use SQLite",
    authority: "explicit",
    recordedAt: new Date(time).toISOString(),
    payload: { decision: "Use SQLite" },
  });
  await ai.configure(
    {
      enabled: true,
      embeddingDimensions: 2,
      scheduleMinutes: 0,
      expectedVersion: 0,
    },
    actor,
  );
  return {
    store,
    ai,
    advance: (ms: number) => {
      time += ms;
    },
  };
}
function mockProvider(completion?: unknown): typeof fetch {
  return vi.fn(async (_url: string | URL | Request, options?: RequestInit) => {
    const body = JSON.parse(String(options?.body));
    if (body.input)
      return Response.json({
        data: body.input.map((_: string, index: number) => ({
          index,
          embedding: [1, 0.2],
        })),
      });
    return Response.json({
      choices: [
        {
          message: {
            content: JSON.stringify(
              completion ?? {
                artifacts: [
                  {
                    kind: "summary",
                    title: "Synthetic summary",
                    content: { text: "Local storage was selected." },
                    sourceRecordIds: ["r1"],
                  },
                ],
              },
            ),
          },
        },
      ],
    });
  }) as typeof fetch;
}
describe("optional AI boundary", () => {
  it.each([
    ["p1", "p2"],
    ["p1", "p1"],
  ])(
    "rejects false participating-project provenance: %j",
    async (...projectIds) => {
      const { ai, store } = await fixture();
      await store.insert("project", {
        id: "p2",
        settings: { ...defaults("normal"), aiProcessing: true },
      });
      const artifact = await store.insert("ai_artifact", {
        id: "false-projects",
        kind: "summary",
        title: "Synthetic",
        content: { text: "Only one source project" },
        sourceRecordIds: ["r1"],
        projectIds,
        authority: "derived",
        status: "pending",
        generator: { model: "synthetic" },
      });
      expect(await ai.artifacts()).toHaveLength(0);
      await expect(
        ai.review(artifact.id, "accepted", actor, artifact.version),
      ).rejects.toMatchObject({ code: "AI_ARTIFACT_INVALID" });
      expect(await store.get("ai_artifact", artifact.id)).toEqual(artifact);
      expect(await store.list("curated_artifact")).toHaveLength(0);
    },
  );
  it("exports more than ten thousand source records without a post-processing ceiling", async () => {
    const fetcher: typeof fetch = async (_url, options) => {
      const body = JSON.parse(String(options?.body));
      const input = JSON.parse(body.messages[1].content);
      return Response.json({
        choices: [
          {
            message: {
              content: JSON.stringify({
                artifacts: [
                  {
                    kind: "export",
                    title: "Synthetic full history",
                    content: {
                      text: "Complete synthetic history",
                      markdown: "Synthetic batch",
                    },
                    sourceRecordIds: [input.records[0].id],
                  },
                ],
              }),
            },
          },
        ],
      });
    };
    const { ai, store } = await fixture(fetcher);
    await store.atomic(async () => {
      for (let index = 0; index < 10000; index++)
        await store.insert("record", {
          id: `large-${index}`,
          projectId: "p1",
          title: "Synthetic",
          content: "Synthetic history",
        });
    });
    const job = await ai.enqueue(
      { type: "export", projectId: "p1", format: "handoff" },
      actor,
    );
    await ai.tick();
    expect(await store.get("ai_job", job.id)).toMatchObject({
      status: "completed",
      attempts: 1,
    });
    const artifacts = await ai.artifacts({ projectId: "p1" });
    expect(artifacts).toHaveLength(1);
    expect(artifacts[0]?.sourceRecordIds).toHaveLength(10001);
    const artifact = artifacts[0];
    if (!artifact) throw new Error("Synthetic export is missing.");
    await ai.review(artifact.id, "accepted", actor, artifact.version);
    expect(await store.list("curated_artifact")).toMatchObject([
      { sourceRecordIds: artifact.sourceRecordIds, content: artifact.content },
    ]);
    expect(await store.list("record")).toHaveLength(10001);
  }, 20000);
  it("rejects generated content belonging to a different artifact kind", async () => {
    const { ai, store } = await fixture(
      mockProvider({
        artifacts: [
          {
            kind: "summary",
            title: "Synthetic",
            content: {
              text: "Unrelated fields",
              fromRecordId: "r1",
              toRecordId: "r1",
              relationshipType: "related_to",
            },
            sourceRecordIds: ["r1"],
          },
        ],
      }),
    );
    const job = await ai.enqueue({ type: "analyze", projectId: "p1" }, actor);
    await ai.tick();
    expect(await store.list("ai_artifact")).toHaveLength(0);
    expect(await store.get("ai_job", job.id)).toMatchObject({
      status: "queued",
      attempts: 1,
    });
  });
  it("revalidates stored suggestions before review and keeps invalid evidence inert", async () => {
    const { ai, store } = await fixture();
    await store.insert("record", {
      id: "r2",
      projectId: "p1",
      title: "Second",
      content: "Synthetic",
    });
    await store.insert("project", {
      id: "private",
      settings: defaults("external"),
    });
    await store.insert("record", {
      id: "uncited",
      projectId: "private",
      title: "Private",
      content: "Synthetic",
    });
    const raw = await store.list("record");
    for (const [index, content] of [
      {
        text: "Lifecycle edge",
        fromRecordId: "r2",
        toRecordId: "r1",
        relationshipType: "replaces",
      },
      {
        text: "Partial lifecycle edge",
        fromRecordId: "r2",
        toRecordId: "r1",
        relationshipType: "partially_replaces",
      },
      {
        text: "Uncited endpoint",
        fromRecordId: "uncited",
        toRecordId: "r1",
        relationshipType: "related_to",
      },
      {
        text: "Missing endpoint",
        fromRecordId: "missing",
        toRecordId: "r1",
        relationshipType: "related_to",
      },
      {
        text: "Self edge",
        fromRecordId: "r1",
        toRecordId: "r1",
        relationshipType: "related_to",
      },
    ].entries()) {
      const artifact = await store.insert("ai_artifact", {
        id: `unsafe-${index}`,
        kind: "relationship_candidate",
        title: "Synthetic imported suggestion",
        content,
        sourceRecordIds: ["r1", "r2"],
        projectIds: ["p1"],
        authority: "derived",
        status: "pending",
        generator: { model: "synthetic" },
      });
      await expect(
        ai.review(artifact.id, "accepted", actor, artifact.version),
      ).rejects.toBeDefined();
      expect(await store.get("ai_artifact", artifact.id)).toEqual(artifact);
    }
    expect(await ai.artifacts()).toHaveLength(0);
    expect(await store.list("relationship")).toHaveLength(0);
    expect(await store.list("curated_artifact")).toHaveLength(0);
    expect(await store.list("record")).toEqual(raw);
  });
  it("keeps compatible embeddings usable after a key-only change", async () => {
    const { ai, store } = await fixture();
    await ai.enqueue({ type: "embed", projectId: "p1" }, actor);
    await ai.tick();
    const before = await store.list("ai_embedding");
    expect(before).toHaveLength(1);
    const generation = (await store.get("ai_settings", "global"))
      ?.dispatchGeneration;
    await ai.configure(
      { expectedVersion: 1, apiKey: "synthetic-replacement" },
      actor,
    );
    expect((await store.get("ai_settings", "global"))?.dispatchGeneration).toBe(
      Number(generation) + 1,
    );
    expect(await store.list("ai_embedding")).toEqual(before);
    expect(
      await ai.semanticSearch({ projectId: "p1", query: "Synthetic" }),
    ).toMatchObject({
      results: [
        expect.objectContaining({
          record: expect.objectContaining({ id: "r1" }),
        }),
      ],
    });
  });
  it("never pairs an old endpoint with a replacement credential during snapshot acquisition", async () => {
    const fetcher = mockProvider();
    const { ai, store } = await fixture(fetcher);
    await ai.configure({ apiKey: "synthetic-old", expectedVersion: 1 }, actor);
    const original = store.get.bind(store);
    let changed = false;
    const read = vi
      .spyOn(store, "get")
      .mockImplementation(async (kind, key) => {
        const value = await original(kind, key);
        if (kind === "ai_settings" && !changed) {
          changed = true;
          await ai.configure(
            {
              baseUrl: "https://replacement.example.test/v1",
              apiKey: "synthetic-new",
              expectedVersion: 2,
            },
            actor,
          );
        }
        return value;
      });
    await ai.testProvider().catch(() => {});
    read.mockRestore();
    for (const [url, options] of vi.mocked(fetcher).mock.calls) {
      if (!String(url).startsWith("https://replacement.example.test"))
        expect(new Headers(options?.headers).get("Authorization")).not.toBe(
          "Bearer synthetic-new",
        );
    }
  });
  for (const type of ["embed", "analyze", "export", "connectivity"] as const)
    for (const change of ["endpoint", "remove", "replace"] as const)
      it(`fences later ${type} dispatch after ${change} while the first request is paused`, async () => {
        let started!: () => void;
        let resume!: () => void;
        const firstStarted = new Promise<void>((resolve) => {
          started = resolve;
        });
        const paused = new Promise<void>((resolve) => {
          resume = resolve;
        });
        let calls = 0;
        const respond = mockProvider(
          type === "connectivity"
            ? { ok: true }
            : type === "export"
              ? {
                  artifacts: [
                    {
                      kind: "export",
                      title: "Synthetic export",
                      content: {
                        text: "Synthetic",
                        markdown: "Synthetic document",
                      },
                      sourceRecordIds: ["r1"],
                    },
                  ],
                }
              : undefined,
        );
        const fetcher: typeof fetch = async (url, options) => {
          calls++;
          if (calls === 1) {
            started();
            await paused;
          }
          return respond(url, options);
        };
        const { ai, store } = await fixture(fetcher);
        await ai.configure(
          { apiKey: "synthetic-old", expectedVersion: 1 },
          actor,
        );
        const record = await store.get("record", "r1");
        await store.update("record", {
          ...record,
          content: "Synthetic long record. ".repeat(2000),
        } as Entity);
        const job =
          type === "connectivity"
            ? undefined
            : await ai.enqueue(
                {
                  type,
                  projectId: "p1",
                  ...(type === "export" ? { format: "handoff" } : {}),
                },
                actor,
              );
        const running =
          type === "connectivity"
            ? ai.testProvider().catch((error: unknown) => error)
            : ai.tick();
        await firstStarted;
        await ai.configure(
          {
            expectedVersion: 2,
            ...(change === "endpoint"
              ? { baseUrl: "https://replacement.example.test/v1" }
              : { apiKey: change === "remove" ? "" : "synthetic-new" }),
          },
          actor,
        );
        resume();
        const result = await running;
        expect(calls).toBe(1);
        expect(await store.list("ai_embedding")).toHaveLength(0);
        expect(await store.list("ai_artifact")).toHaveLength(0);
        if (job)
          expect(await store.get("ai_job", String(job.id))).toMatchObject({
            status: "failed",
            lastError: expect.stringContaining("future dispatch stopped"),
          });
        else expect(result).toMatchObject({ code: "AI_CONFIGURATION_CHANGED" });
      });
  it("keeps accepted cross-project interpretations out of project-local metadata and relationships", async () => {
    const { ai, store } = await fixture(
      mockProvider({
        artifacts: [
          {
            kind: "classification",
            title: "Classification",
            content: {
              text: "Influenced by project metadata",
              tags: ["private-derived"],
              classification: "private-derived",
            },
            sourceRecordIds: ["r1"],
          },
          {
            kind: "relationship_candidate",
            title: "Refinement",
            content: {
              text: "Influenced by project metadata",
              fromRecordId: "r2",
              toRecordId: "r1",
              relationshipType: "refines",
            },
            sourceRecordIds: ["r1", "r2"],
          },
        ],
      }),
    );
    await store.insert("record", {
      id: "r2",
      projectId: "p1",
      title: "Second",
      content: "Synthetic",
    });
    await store.insert("metadata", { id: "r1", tags: [], archived: false });
    const metadata = await store.get("metadata", "r1");
    const project = await store.insert("project", {
      id: "p2",
      name: "Uncited metadata",
      settings: { ...defaults("normal"), aiProcessing: true },
    });
    await ai.enqueue(
      { type: "analyze", projectIds: ["p1", "p2"], crossProject: true },
      actor,
    );
    await ai.tick();
    for (const artifact of await ai.artifacts())
      await ai.review(artifact.id, "accepted", actor, artifact.version);
    expect(await store.list("curated_artifact")).toHaveLength(2);
    expect(await store.get("metadata", "r1")).toEqual(metadata);
    expect(await store.list("relationship")).toHaveLength(0);
    await store.update(
      "project",
      { ...project, settings: defaults("external") },
      project.version,
    );
    expect(await ai.artifacts()).toHaveLength(0);
  });
  it.each([false, true])(
    "keeps uncited record and project metadata privacy dependencies (second record: %s)",
    async (withRecord) => {
      for (const revoked of ["aiProcessing", "crossProjectAnalysis"] as const) {
        const fetcher = mockProvider({
          artifacts: [
            {
              kind: "summary",
              title: "Synthetic",
              content: { text: "Cites only A" },
              sourceRecordIds: ["r1"],
              privacyDependencies: {
                version: 1,
                recordIds: ["r1"],
                projectIds: ["p1"],
                crossProject: false,
              },
            },
          ],
        });
        const { ai, store } = await fixture(fetcher);
        const project = await store.insert("project", {
          id: "p2",
          name: "Synthetic uncited input",
          settings: { ...defaults("normal"), aiProcessing: true },
        });
        if (withRecord)
          await store.insert("record", {
            id: "r2",
            projectId: "p2",
            title: "Uncited synthetic record",
            content: "Private input",
          });
        await ai.enqueue(
          { type: "analyze", projectIds: ["p1", "p2"], crossProject: true },
          actor,
        );
        await ai.tick();
        const artifact = (await store.list("ai_artifact"))[0] as Entity;
        expect(artifact.sourceRecordIds).toEqual(["r1"]);
        const input = JSON.parse(
          JSON.parse(String(vi.mocked(fetcher).mock.calls[0]?.[1]?.body))
            .messages[1].content,
        );
        expect(input.projects.map((p: { id: string }) => p.id)).toEqual([
          "p1",
          "p2",
        ]);
        expect(await ai.artifacts({ projectId: "p1" })).toHaveLength(0);
        expect(artifact.privacyDependencies).toEqual({
          version: 1,
          recordIds: withRecord ? ["r1", "r2"] : ["r1"],
          projectIds: ["p1", "p2"],
          crossProject: true,
        });
        expect(
          await ai.artifacts({ projectIds: ["p1", "p2"], crossProject: true }),
        ).toHaveLength(1);
        await store.update(
          "project",
          {
            ...project,
            settings: {
              ...defaults("normal"),
              aiProcessing: true,
              [revoked]: false,
            },
          },
          project.version,
        );
        expect(await ai.artifacts()).toHaveLength(0);
        await expect(
          ai.review(artifact.id, "accepted", actor, artifact.version),
        ).rejects.toMatchObject({ code: "AI_NOT_ALLOWED" });
        expect(await store.list("curated_artifact")).toHaveLength(0);
      }
    },
  );
  it("preserves dependencies in curated portable derivatives and fails closed for unknown legacy provenance", async () => {
    const { ai, store } = await fixture();
    const project = await store.get("project", "p1");
    if (!project) throw new Error("Missing synthetic project.");
    await store.update(
      "project",
      { ...project, kind: "normal" },
      project.version,
    );
    await store.insert("metadata", { id: "r1", recordId: "r1", tags: [] });
    await ai.enqueue({ type: "analyze", projectId: "p1" }, actor);
    await ai.tick();
    const artifact = (await ai.artifacts())[0] as Entity;
    await ai.review(artifact.id, "accepted", actor, artifact.version);
    expect(
      (await store.list("curated_artifact"))[0]?.privacyDependencies,
    ).toEqual(artifact.privacyDependencies);
    const legacy = await store.insert("ai_artifact", {
      ...artifact,
      id: "legacy",
      status: "pending",
      privacyDependencies: undefined,
    });
    expect((await ai.artifacts()).map((a) => a.id)).not.toContain(legacy.id);
    await expect(
      ai.review(legacy.id, "accepted", actor, legacy.version),
    ).rejects.toMatchObject({ code: "AI_NOT_ALLOWED" });
    const archive = {
      version: 1,
      data: Object.fromEntries(
        await Promise.all(
          exportKinds.map(async (kind) => [kind, await store.list(kind)]),
        ),
      ),
    };
    const restored = new Store(":memory:");
    stores.push(restored);
    for (const dependency of [
      { version: 1, recordIds: [], projectIds: ["p1"], crossProject: false },
      {
        version: 1,
        recordIds: ["r1", "missing"],
        projectIds: ["p1"],
        crossProject: false,
      },
      {
        version: 2,
        recordIds: ["r1"],
        projectIds: ["p1"],
        crossProject: false,
      },
    ]) {
      const invalid = structuredClone(archive);
      invalid.data.ai_artifact[0].privacyDependencies = dependency;
      await expect(
        importNative(restored, invalid, actor),
      ).rejects.toMatchObject({ code: "AI_PRIVACY_INVALID" });
      expect(await restored.list("record")).toHaveLength(0);
      expect(await restored.list("project")).toHaveLength(0);
    }
    await importNative(restored, archive, actor);
    expect(await restored.list("ai_artifact")).toEqual(
      await store.list("ai_artifact"),
    );
    expect(await restored.list("curated_artifact")).toEqual(
      await store.list("curated_artifact"),
    );
    const restoredAi = new AiService(restored);
    await restoredAi.configure({ enabled: true, expectedVersion: 0 }, actor);
    expect((await restoredAi.artifacts()).map((a) => a.id)).toEqual([
      artifact.id,
    ]);
  });
  it("defaults disabled, keeps secrets out of settings and audits, and detects stale configuration", async () => {
    const store = new Store(":memory:");
    stores.push(store);
    const ai = new AiService(store);
    expect(await ai.settings()).toMatchObject({
      enabled: false,
      apiKeyConfigured: false,
      model: "qwen/qwen3.8-27b",
      embeddingModel: "text-embedding-qwen3-embedding-4b",
    });
    await ai.configure(
      { apiKey: "private-provider-key", expectedVersion: 0 },
      actor,
    );
    expect(JSON.stringify(await ai.settings())).not.toContain(
      "private-provider-key",
    );
    expect(JSON.stringify(await store.list("audit"))).not.toContain(
      "private-provider-key",
    );
    await expect(
      ai.configure({ enabled: true, expectedVersion: 0 }, actor),
    ).rejects.toMatchObject({ code: "CONFLICT" });
    await expect(
      ai.enqueue({ type: "analyze", projectId: "p1" }, actor),
    ).rejects.toMatchObject({ code: "AI_DISABLED" });
  });
  it("generates provenance, accepts auditable derived knowledge, and preserves raw captures", async () => {
    const { ai, store } = await fixture();
    const raw = await store.get("record", "r1");
    const job = await ai.enqueue({ type: "analyze", projectId: "p1" }, actor);
    await ai.tick();
    expect(await store.get("ai_job", job.id)).toMatchObject({
      status: "completed",
      attempts: 1,
    });
    const artifact = (await ai.artifacts({ projectId: "p1" }))[0] as Entity;
    expect(artifact).toMatchObject({
      authority: "derived",
      status: "pending",
      private: true,
      sourceRecordIds: ["r1"],
      generator: { model: "qwen/qwen3.8-27b" },
    });
    await ai.review(artifact.id, "accepted", actor, artifact.version);
    expect(await store.get("record", "r1")).toEqual(raw);
    expect(await store.list("curated_artifact")).toHaveLength(1);
    expect(
      (await store.list("audit")).some(
        (e) => e.action === "ai.suggestion.accepted",
      ),
    ).toBe(true);
    await expect(
      ai.review(artifact.id, "rejected", actor, artifact.version),
    ).rejects.toMatchObject({ code: "CONFLICT" });
  });
  it("rejects malformed output and invented sources, retries with backoff, and leaves the core healthy", async () => {
    const { ai, store, advance } = await fixture(
      mockProvider({
        artifacts: [
          {
            kind: "summary",
            title: "Invented",
            content: { text: "Unverified" },
            sourceRecordIds: ["secret-record"],
          },
        ],
      }),
    );
    const job = await ai.enqueue({ type: "analyze", projectId: "p1" }, actor);
    await ai.tick();
    expect(await store.get("ai_job", job.id)).toMatchObject({
      status: "queued",
      attempts: 1,
    });
    await ai.tick();
    expect(await store.get("ai_job", job.id)).toMatchObject({ attempts: 1 });
    advance(10000);
    await ai.tick();
    advance(10000);
    await ai.tick();
    expect(await store.get("ai_job", job.id)).toMatchObject({
      status: "failed",
      attempts: 3,
    });
    expect(await store.list("ai_artifact")).toHaveLength(0);
    await expect(store.assertReady()).resolves.toBeUndefined();
  });
  it("recovers a persisted expired lease and serializes two workers", async () => {
    const fetcher = mockProvider();
    const { ai, store, advance } = await fixture(fetcher);
    const job = await ai.enqueue({ type: "analyze", projectId: "p1" }, actor);
    await store.update(
      "ai_job",
      {
        ...job,
        status: "running",
        attempts: 1,
        leaseToken: "crashed-worker",
        leaseUntil: new Date(0).toISOString(),
      },
      job.version,
    );
    advance(1000);
    const otherWorker = new AiService(store, fetcher);
    await Promise.all([ai.tick(), otherWorker.tick()]);
    expect(await store.get("ai_job", job.id)).toMatchObject({
      status: "completed",
      attempts: 2,
    });
    expect(await store.list("ai_artifact")).toHaveLength(1);
  });
  it("filters project consent before model and vector processing, and hides outputs after revocation", async () => {
    const fetcher = mockProvider();
    const { ai, store } = await fixture(fetcher);
    await store.insert("project", {
      id: "denied",
      settings: defaults("external"),
    });
    await store.insert("record", {
      id: "secret",
      projectId: "denied",
      content: "PRIVATE NEVER SEND",
      title: "Private",
    });
    await expect(
      ai.enqueue(
        { type: "analyze", projectIds: ["p1", "denied"], crossProject: true },
        actor,
      ),
    ).rejects.toMatchObject({ code: "AI_NOT_ALLOWED" });
    await ai.enqueue({ type: "analyze", crossProject: true }, actor);
    await ai.tick();
    const calls = vi.mocked(fetcher).mock.calls;
    expect(JSON.stringify(calls)).not.toContain("PRIVATE NEVER SEND");
    expect(await ai.artifacts()).toHaveLength(1);
    const project = (await store.get("project", "p1")) as Entity;
    await store.update(
      "project",
      { ...project, settings: defaults("normal") },
      project.version,
    );
    expect(await ai.artifacts()).toHaveLength(0);
    await expect(
      ai.semanticSearch({ query: "secret", projectId: "p1" }),
    ).rejects.toMatchObject({ code: "AI_NOT_ALLOWED" });
    expect(vi.mocked(fetcher).mock.calls).toHaveLength(calls.length);
  });
  it("single-project suggestions remain visible with cross-project analysis disabled", async () => {
    const { ai, store } = await fixture();
    const project = (await store.get("project", "p1")) as Entity;
    await store.update(
      "project",
      { ...project, settings: { ...defaults("external"), aiProcessing: true } },
      project.version,
    );
    await ai.enqueue({ type: "analyze", projectId: "p1" }, actor);
    await ai.tick();
    expect(await ai.artifacts()).toHaveLength(1);
    expect(await ai.artifacts({ crossProject: true })).toHaveLength(0);
  });
  it("rechecks permission after a provider call and refuses to persist revoked data", async () => {
    let revoke: (() => Promise<void>) | undefined;
    const fetcher = (async (...args: Parameters<typeof fetch>) => {
      const response = await mockProvider()(...args);
      await revoke?.();
      return response;
    }) as typeof fetch;
    const { ai, store } = await fixture(fetcher);
    revoke = async () => {
      const project = (await store.get("project", "p1")) as Entity;
      await store.update(
        "project",
        { ...project, settings: defaults("normal") },
        project.version,
      );
    };
    await ai.enqueue({ type: "analyze", projectId: "p1" }, actor);
    await ai.tick();
    expect(await store.list("ai_artifact")).toHaveLength(0);
    expect((await ai.jobs())[0]).toMatchObject({ status: "failed" });
  });
  it("indexes vectors, searches them, and refuses stale provider/model/dimension indices", async () => {
    const { ai } = await fixture();
    await ai.enqueue({ type: "embed", projectId: "p1" }, actor);
    await ai.tick();
    expect(
      await ai.semanticSearch({ query: "local database", projectId: "p1" }),
    ).toMatchObject({
      results: [{ record: { id: "r1" } }],
      model: "text-embedding-qwen3-embedding-4b",
    });
    await ai.configure(
      {
        embeddingModel: "replacement-model",
        expectedVersion: Number((await ai.settings()).version),
      },
      actor,
    );
    expect(
      await ai.semanticSearch({ query: "local database", projectId: "p1" }),
    ).toMatchObject({ results: [], indexRequired: true });
  });
  it("creates private Markdown exports with resolvable source citations", async () => {
    const { ai } = await fixture(
      mockProvider({
        artifacts: [
          {
            kind: "export",
            title: "Handoff",
            content: {
              text: "Handoff",
              markdown: "# Synthetic handoff\n\nSQLite selected.",
            },
            sourceRecordIds: ["r1"],
          },
        ],
      }),
    );
    await ai.enqueue(
      { type: "export", projectId: "p1", format: "handoff" },
      actor,
    );
    await ai.tick();
    const artifact = (await ai.artifacts())[0] as Entity;
    expect(artifact).toMatchObject({
      kind: "export",
      private: true,
      format: "handoff",
    });
    expect((artifact.content as { markdown: string }).markdown).toContain(
      "/?recordId=r1",
    );
  });
  it("processes every segment of a long source and keeps all records in Markdown exports", async () => {
    const seen: string[] = [];
    const fetcher = vi.fn(
      async (_url: string | URL | Request, options?: RequestInit) => {
        const body = JSON.parse(String(options?.body));
        const input = JSON.parse(body.messages[1].content);
        seen.push(
          ...input.records.map((record: { content: string }) => record.content),
        );
        return Response.json({
          choices: [
            {
              message: {
                content: JSON.stringify({
                  artifacts: [
                    {
                      kind: "export",
                      title: "Synthetic full-history export",
                      content: {
                        text: "Segment summary",
                        markdown: `# Segment\n\n${input.records.map((record: { id: string }) => record.id).join(", ")}`,
                      },
                      sourceRecordIds: [input.records[0].id],
                    },
                  ],
                }),
              },
            },
          ],
        });
      },
    ) as typeof fetch;
    const { ai, store } = await fixture(fetcher);
    const raw = (await store.get("record", "r1")) as Entity;
    const full = `${"x".repeat(20000)}UNIQUE-END-OF-LONG-SOURCE`;
    await store.update("record", { ...raw, content: full }, raw.version);
    for (let index = 0; index < 45; index++)
      await store.insert("record", {
        id: `extra${index}`,
        projectId: "p1",
        title: "Synthetic extra",
        content: "Extra context",
        recordedAt: new Date().toISOString(),
      });
    await ai.enqueue({ type: "export", projectId: "p1", format: "adr" }, actor);
    await ai.tick();
    expect(seen.join(" ")).toContain("UNIQUE-END-OF-LONG-SOURCE");
    expect(seen.reduce((sum, text) => sum + text.length, 0)).toBe(
      full.length + 45 * "Extra context".length,
    );
    const artifact = (await ai.artifacts())[0] as Entity;
    expect(artifact.sourceRecordIds).toHaveLength(46);
    expect((artifact.content as { markdown: string }).markdown).toContain(
      "/?recordId=extra44",
    );
    expect(vi.mocked(fetcher).mock.calls.length).toBeGreaterThan(1);
    expect(
      vi
        .mocked(fetcher)
        .mock.calls.every((call) => String(call[1]?.body).length < 20000),
    ).toBe(true);
  });
  it("rejects unsupported one-source patterns visibly while preserving valid source-linked siblings", async () => {
    const { ai, store } = await fixture(
      mockProvider({
        artifacts: [
          {
            kind: "summary",
            title: "Supported",
            content: { text: "Supported summary" },
            sourceRecordIds: ["r1"],
          },
          {
            kind: "pattern",
            title: "Unsupported recurrence",
            content: {
              text: "Only one source is insufficient for recurrence.",
            },
            sourceRecordIds: ["r1"],
          },
        ],
      }),
    );
    const job = await ai.enqueue({ type: "analyze", projectId: "p1" }, actor);
    await ai.tick();
    expect(await store.get("ai_job", job.id)).toMatchObject({
      status: "completed",
      warnings: [
        { code: "AI_INSUFFICIENT_EVIDENCE", kind: "pattern", count: 1 },
      ],
    });
    expect(await ai.artifacts()).toMatchObject([
      { kind: "summary", sourceRecordIds: ["r1"] },
    ]);
    expect(
      (await store.list("audit")).some(
        (event) => event.action === "ai.output.rejected",
      ),
    ).toBe(true);
    expect(
      (await store.list("ai_artifact")).some(
        (artifact) => artifact.kind === "pattern",
      ),
    ).toBe(false);
  });
  it("supports audited manual retry after terminal provider failures", async () => {
    let available = false;
    const fetcher = (async (...args: Parameters<typeof fetch>) =>
      available
        ? mockProvider()(...args)
        : new Response("Unavailable", { status: 503 })) as typeof fetch;
    const { ai, store, advance } = await fixture(fetcher);
    const job = await ai.enqueue({ type: "analyze", projectId: "p1" }, actor);
    await ai.tick();
    advance(10000);
    await ai.tick();
    advance(10000);
    await ai.tick();
    const failed = (await store.get("ai_job", job.id)) as Entity;
    expect(failed.status).toBe("failed");
    available = true;
    await ai.retry(job.id, actor, failed.version);
    await ai.tick();
    expect(await store.get("ai_job", job.id)).toMatchObject({
      status: "completed",
      attempts: 1,
    });
    expect(
      (await store.list("audit")).some(
        (event) => event.action === "ai.job.retried",
      ),
    ).toBe(true);
  });
  it("blocks outputs when global AI consent is revoked during a provider call", async () => {
    let revoke: (() => Promise<void>) | undefined;
    const fetcher = (async (...args: Parameters<typeof fetch>) => {
      const result = await mockProvider()(...args);
      await revoke?.();
      return result;
    }) as typeof fetch;
    const { ai, store } = await fixture(fetcher);
    revoke = async () => {
      await ai.configure(
        {
          enabled: false,
          expectedVersion: Number((await ai.settings()).version),
        },
        actor,
      );
    };
    await ai.enqueue({ type: "analyze", projectId: "p1" }, actor);
    await ai.tick();
    expect(await store.list("ai_artifact")).toHaveLength(0);
  });
  it("hides cross-project artifacts and forbids review after either source opts out", async () => {
    const { ai, store } = await fixture(
      mockProvider({
        artifacts: [
          {
            kind: "pattern",
            title: "Recurring storage choice",
            content: { text: "Synthetic pattern" },
            sourceRecordIds: ["r1", "r2"],
          },
        ],
      }),
    );
    const project = await store.insert("project", {
      id: "p2",
      settings: { ...defaults("normal"), aiProcessing: true },
    });
    await store.insert("record", {
      id: "r2",
      projectId: "p2",
      title: "Synthetic related decision",
      content: "SQLite again",
    });
    await ai.enqueue({ type: "analyze", crossProject: true }, actor);
    await ai.tick();
    const artifact = (await ai.artifacts())[0] as Entity;
    expect(artifact.kind).toBe("pattern");
    await store.update(
      "project",
      { ...project, settings: { ...defaults("external"), aiProcessing: true } },
      project.version,
    );
    expect(await ai.artifacts()).toHaveLength(0);
    await expect(
      ai.review(artifact.id, "accepted", actor, artifact.version),
    ).rejects.toMatchObject({ code: "AI_NOT_ALLOWED" });
  });
  it("applies accepted classification to metadata and relationship suggestions without changing raw records", async () => {
    const { ai, store } = await fixture(
      mockProvider({
        artifacts: [
          {
            kind: "classification",
            title: "Storage",
            content: {
              text: "Storage classification",
              tags: ["storage"],
              classification: "architecture",
            },
            sourceRecordIds: ["r1"],
          },
          {
            kind: "relationship_candidate",
            title: "Refinement",
            content: {
              text: "Second decision refines first",
              fromRecordId: "r2",
              toRecordId: "r1",
              relationshipType: "refines",
            },
            sourceRecordIds: ["r1", "r2"],
          },
        ],
      }),
    );
    await store.insert("record", {
      id: "r2",
      projectId: "p1",
      title: "Synthetic refinement",
      content: "Additional SQLite detail",
    });
    await store.insert("metadata", { id: "r1", tags: [], archived: false });
    const raw = await store.list("record");
    await ai.enqueue({ type: "analyze", projectId: "p1" }, actor);
    await ai.tick();
    for (const artifact of await ai.artifacts())
      await ai.review(artifact.id, "accepted", actor, artifact.version);
    expect(await store.list("record")).toEqual(raw);
    expect(await store.get("metadata", "r1")).toMatchObject({
      tags: ["storage"],
      classification: "architecture",
    });
    expect(await store.list("relationship")).toMatchObject([
      { type: "refines", authority: "suggested", status: "accepted" },
    ]);
  });
  it("schedules cross-project patterns only for consenting participants and honors disabled analysis modes", async () => {
    const { ai, store } = await fixture();
    await store.insert("project", {
      id: "p2",
      name: "Synthetic second project",
      settings: { ...defaults("normal"), aiProcessing: true },
    });
    await store.insert("project", {
      id: "restricted",
      name: "Synthetic client",
      settings: { ...defaults("external"), aiProcessing: true },
    });
    await ai.configure(
      {
        scheduleMinutes: 60,
        expectedVersion: Number((await ai.settings()).version),
      },
      actor,
    );
    await ai.tick();
    const cross = (await ai.jobs()).find(
      (job) => (job.scope as { crossProject: boolean }).crossProject,
    ) as Entity;
    expect((cross.scope as { projectIds: string[] }).projectIds.sort()).toEqual(
      ["p1", "p2"],
    );
    const fetcher = mockProvider();
    const other = await fixture(fetcher);
    await other.ai.configure(
      {
        analysisModes: [],
        expectedVersion: Number((await other.ai.settings()).version),
      },
      actor,
    );
    await other.ai.enqueue({ type: "analyze", projectId: "p1" }, actor);
    await other.ai.tick();
    expect(vi.mocked(fetcher)).not.toHaveBeenCalled();
  });
  it("rejects self relationships and prevents cyclic accepted dependencies", async () => {
    const { ai, store } = await fixture(
      mockProvider({
        artifacts: [
          {
            kind: "relationship_candidate",
            title: "Dependency",
            content: {
              text: "Reverse dependency",
              fromRecordId: "r2",
              toRecordId: "r1",
              relationshipType: "depends_on",
            },
            sourceRecordIds: ["r1", "r2"],
          },
        ],
      }),
    );
    await store.insert("record", {
      id: "r2",
      projectId: "p1",
      title: "Synthetic other",
      content: "Other",
    });
    await store.insert("relationship", {
      id: "existing",
      fromRecordId: "r1",
      toRecordId: "r2",
      type: "depends_on",
      status: "accepted",
      authority: "explicit",
    });
    await ai.enqueue({ type: "analyze", projectId: "p1" }, actor);
    await ai.tick();
    const artifact = (await ai.artifacts())[0] as Entity;
    await expect(
      ai.review(artifact.id, "accepted", actor, artifact.version),
    ).rejects.toMatchObject({ code: "RELATIONSHIP_CYCLE" });
    expect((await store.get("ai_artifact", artifact.id))?.status).toBe(
      "pending",
    );
    expect(await store.list("curated_artifact")).toHaveLength(0);
  });
  it("schedules only consenting projects, with persisted next-run state", async () => {
    const { ai, store } = await fixture();
    await ai.configure(
      {
        scheduleMinutes: 60,
        expectedVersion: Number((await ai.settings()).version),
      },
      actor,
    );
    await ai.tick();
    await ai.tick();
    expect(await ai.jobs()).toHaveLength(2);
    expect(await store.get("ai_schedule", "global")).toBeDefined();
  });
});

describe("OpenAI-compatible response validation", () => {
  it("consumes many one-byte chunks within a constrained heap", async () => {
    const { ai } = await fixture();
    const config = { ...(await ai.settings()), embeddingDimensions: 2 };
    const result = spawnSync(
      process.execPath,
      [
        "--max-old-space-size=96",
        "--import",
        "tsx",
        "--input-type=module",
        "-e",
        `
          import { OpenAiProvider } from ${JSON.stringify(new URL("./ai.ts", import.meta.url).href)};
          const prefix = Buffer.from('{"data":[{"index":0,"embedding":[1,2]}],"padding":"');
          const padding = 1024 * 1024;
          let offset = 0;
          const stream = new ReadableStream({
            pull(controller) {
              if (offset < prefix.length) controller.enqueue(new Uint8Array([prefix[offset]]));
              else if (offset < prefix.length + padding) controller.enqueue(new Uint8Array([32]));
              else if (offset === prefix.length + padding) controller.enqueue(new Uint8Array([34]));
              else if (offset === prefix.length + padding + 1) controller.enqueue(new Uint8Array([125]));
              else { controller.close(); return; }
              offset++;
            },
          });
          const provider = new OpenAiProvider(${JSON.stringify(config)}, "synthetic-key", async () => new Response(stream));
          console.log(JSON.stringify(await provider.embed(["Synthetic"])));
        `,
      ],
      { encoding: "utf8", timeout: 20000, maxBuffer: 1024 * 1024 },
    );
    expect(result.error).toBeUndefined();
    expect(result.status, result.stderr).toBe(0);
    expect(JSON.parse(result.stdout.trim())).toEqual([[1, 2]]);
  });

  it.each(["complete", "embed"] as const)(
    "cancels oversized chunked %s bodies before consuming the tail",
    async (operation) => {
      const { ai } = await fixture();
      const config = {
        ...(await ai.settings()),
        embeddingDimensions: 2,
      } as ConstructorParameters<typeof OpenAiProvider>[0];
      let pulled = 0,
        cancelled = false;
      const stream = new ReadableStream<Uint8Array>({
        pull(controller) {
          if (pulled++ < 20)
            controller.enqueue(new Uint8Array(1024 * 1024).fill(32));
          else controller.close();
        },
        cancel() {
          cancelled = true;
        },
      });
      const provider = new OpenAiProvider(
        config,
        "synthetic-key",
        (async () => new Response(stream)) as typeof fetch,
      );
      await expect(
        operation === "embed"
          ? provider.embed(["Synthetic"])
          : provider.complete("Synthetic", {}),
      ).rejects.toMatchObject({
        code: "AI_UNAVAILABLE",
        message: "AI response exceeds the limit.",
      });
      expect(cancelled).toBe(true);
      expect(pulled).toBeLessThan(20);
    },
  );
  it.each([0, 1])(
    "measures the exact 8 MiB boundary in UTF-8 bytes with %s excess bytes",
    async (excess) => {
      const { ai } = await fixture();
      const config = {
        ...(await ai.settings()),
        embeddingDimensions: 2,
      } as ConstructorParameters<typeof OpenAiProvider>[0];
      const prefix = '{"data":[{"index":0,"embedding":[1,2]}],"padding":"';
      const suffix = '"}';
      const paddingBytes =
        8 * 1024 * 1024 + excess - Buffer.byteLength(prefix + suffix);
      const padding =
        "é".repeat(Math.floor(paddingBytes / 2)) +
        (paddingBytes % 2 ? " " : "");
      const payload = Buffer.from(prefix + padding + suffix);
      const provider = new OpenAiProvider(
        config,
        "",
        (async () =>
          new Response(
            new ReadableStream({
              start(controller) {
                for (let offset = 0; offset < payload.length; offset += 65535)
                  controller.enqueue(payload.subarray(offset, offset + 65535));
                controller.close();
              },
            }),
          )) as typeof fetch,
      );
      if (excess)
        await expect(provider.embed(["Synthetic"])).rejects.toMatchObject({
          code: "AI_UNAVAILABLE",
          message: "AI response exceeds the limit.",
        });
      else
        await expect(provider.embed(["Synthetic"])).resolves.toEqual([[1, 2]]);
    },
  );
  it("bounds decompressed gzip bytes rather than the compressed Content-Length", async () => {
    const compressed = gzipSync(
      Buffer.from(
        JSON.stringify({
          data: [{ index: 0, embedding: [1, 2] }],
          padding: " ".repeat(9 * 1024 * 1024),
        }),
      ),
    );
    expect(compressed.byteLength).toBeLessThan(8 * 1024 * 1024);
    const server = createServer((_request, response) => {
      response.writeHead(200, {
        "Content-Type": "application/json",
        "Content-Encoding": "gzip",
        "Content-Length": compressed.byteLength,
      });
      response.end(compressed);
    });
    await new Promise<void>((resolve) =>
      server.listen(0, "127.0.0.1", resolve),
    );
    const address = server.address();
    if (!address || typeof address === "string")
      throw new Error("Missing test server address");
    const { ai } = await fixture();
    const config = {
      ...(await ai.settings()),
      baseUrl: `http://127.0.0.1:${address.port}/v1`,
      embeddingDimensions: 2,
    } as ConstructorParameters<typeof OpenAiProvider>[0];
    try {
      await expect(
        new OpenAiProvider(config).embed(["Synthetic"]),
      ).rejects.toMatchObject({
        code: "AI_UNAVAILABLE",
        message: "AI response exceeds the limit.",
      });
    } finally {
      server.closeAllConnections();
      await new Promise<void>((resolve, reject) =>
        server.close((error) => (error ? reject(error) : resolve())),
      );
    }
  });
  it("uses the bound for semantic search and connectivity while core storage stays ready", async () => {
    let oversized = false;
    const normal = mockProvider();
    const fetcher = (async (...args: Parameters<typeof fetch>) =>
      oversized
        ? new Response(new Uint8Array(8 * 1024 * 1024 + 1))
        : normal(...args)) as typeof fetch;
    const { ai, store } = await fixture(fetcher);
    await ai.enqueue({ type: "embed", projectId: "p1" }, actor);
    await ai.tick();
    oversized = true;
    await expect(
      ai.semanticSearch({ projectId: "p1", query: "Synthetic query" }),
    ).rejects.toMatchObject({
      code: "AI_UNAVAILABLE",
      message: "AI response exceeds the limit.",
    });
    await expect(ai.testProvider()).rejects.toMatchObject({
      code: "AI_UNAVAILABLE",
      message: "AI response exceeds the limit.",
    });
    await expect(store.assertReady()).resolves.toBeUndefined();
    expect((await store.get("record", "r1"))?.title).toBe("Use local storage");
    await expect(
      store.insert("record", {
        id: "r2",
        projectId: "p1",
        title: "Core capture remains available",
      }),
    ).resolves.toMatchObject({ id: "r2" });
  });
  it("uses the configured transport deadline for delayed model headers and aborts at the overall deadline", async () => {
    const server = createServer((_request, response) => {
      const timer = setTimeout(() => {
        response.setHeader("Content-Type", "application/json");
        response.end(
          JSON.stringify({ data: [{ index: 0, embedding: [1, 2] }] }),
        );
      }, 150);
      response.on("close", () => clearTimeout(timer));
    });
    await new Promise<void>((resolve) =>
      server.listen(0, "127.0.0.1", resolve),
    );
    const address = server.address();
    if (!address || typeof address === "string")
      throw new Error("Missing test server address");
    const { ai } = await fixture();
    const config = {
      ...(await ai.settings()),
      baseUrl: `http://127.0.0.1:${address.port}/v1`,
      embeddingDimensions: 2,
      requestTimeoutSeconds: 0.5,
    } as ConstructorParameters<typeof OpenAiProvider>[0];
    try {
      await expect(
        new OpenAiProvider(config).embed(["Synthetic"]),
      ).resolves.toEqual([[1, 2]]);
      await expect(
        new OpenAiProvider({ ...config, requestTimeoutSeconds: 0.05 }).embed([
          "Synthetic",
        ]),
      ).rejects.toMatchObject({
        code: "AI_UNAVAILABLE",
        message: expect.stringContaining("timed out"),
      });
    } finally {
      server.closeAllConnections();
      await new Promise<void>((resolve, reject) =>
        server.close((error) => (error ? reject(error) : resolve())),
      );
    }
  });
  it.each([
    { data: [{ index: 0, embedding: [0, 0] }] },
    { data: [{ index: 0, embedding: [1] }] },
    { data: [{ index: 1, embedding: [1, 2] }] },
    { data: [] },
  ])("rejects malformed or invalid embeddings", async (data) => {
    const { ai } = await fixture();
    const config = await ai.settings();
    const provider = new OpenAiProvider(
      config as ConstructorParameters<typeof OpenAiProvider>[0],
      "",
      (async () => Response.json(data)) as typeof fetch,
    );
    await expect(provider.embed(["Synthetic"])).rejects.toMatchObject({
      code: "AI_UNAVAILABLE",
    });
  });
  it("requests strict JSON Schema and configurable reasoning without exposing provider internals", async () => {
    const fetcher = mockProvider();
    const { ai } = await fixture(fetcher);
    await ai.enqueue({ type: "analyze", projectId: "p1" }, actor);
    await ai.tick();
    const body = JSON.parse(
      String(vi.mocked(fetcher).mock.calls[0]?.[1]?.body),
    );
    expect(body.reasoning_effort).toBe("none");
    expect(body.response_format).toMatchObject({
      type: "json_schema",
      json_schema: {
        strict: true,
        schema: {
          type: "object",
          properties: { artifacts: { type: "array" } },
        },
      },
    });
    expect(await ai.settings()).toMatchObject({
      requestTimeoutSeconds: 600,
      reasoningEffort: "none",
    });
    await ai.configure(
      {
        requestTimeoutSeconds: 180,
        expectedVersion: (await ai.settings()).version,
      },
      actor,
    );
    expect((await ai.settings()).requestTimeoutSeconds).toBe(180);
  });
  it("requests a single Markdown export without unrelated analysis fields", async () => {
    const fetcher = mockProvider({
      artifacts: [
        {
          kind: "export",
          title: "Synthetic handoff",
          content: {
            text: "SQLite cache handoff",
            markdown: "# Handoff\nUse SQLite.",
          },
          sourceRecordIds: ["r1"],
        },
      ],
    });
    const { ai } = await fixture(fetcher);
    await ai.enqueue(
      { type: "export", projectId: "p1", format: "handoff" },
      actor,
    );
    await ai.tick();
    const body = JSON.parse(
      String(vi.mocked(fetcher).mock.calls[0]?.[1]?.body),
    );
    const artifacts =
      body.response_format.json_schema.schema.properties.artifacts;
    expect(artifacts).toMatchObject({ minItems: 1, maxItems: 1 });
    expect(artifacts.items.properties.content.required).toEqual([
      "text",
      "markdown",
    ]);
    expect(Object.keys(artifacts.items.properties.content.properties)).toEqual([
      "text",
      "markdown",
    ]);
    expect((await ai.jobs())[0]?.status).toBe("completed");
    const artifact = (await ai.artifacts())[0] as Entity;
    const markdown = String(
      (artifact.content as { markdown: string }).markdown,
    );
    expect(markdown).toMatch(
      /^> AI-generated draft\. Source records retain their original authority;/,
    );
    expect(markdown).toContain("(https://memory.example.test/?recordId=r1)");
  });
  it("meets OpenAI strict required-properties rules and removes only optional null placeholders", async () => {
    const fetcher = mockProvider({
      artifacts: [
        {
          kind: "summary",
          title: "Optional nulls",
          content: {
            text: "Source-backed summary",
            markdown: null,
            fromRecordId: null,
            toRecordId: null,
            relationshipType: null,
            tags: null,
            classification: null,
          },
          sourceRecordIds: ["r1"],
        },
      ],
    });
    const { ai } = await fixture(fetcher);
    await ai.enqueue({ type: "analyze", projectId: "p1" }, actor);
    await ai.tick();
    const artifact = (await ai.artifacts())[0] as Entity;
    expect(artifact.content).toEqual({ text: "Source-backed summary" });
    const body = JSON.parse(
      String(vi.mocked(fetcher).mock.calls[0]?.[1]?.body),
    );
    const check = (schema: unknown): void => {
      if (Array.isArray(schema)) {
        for (const item of schema) check(item);
        return;
      }
      if (!schema || typeof schema !== "object") return;
      const definition = schema as {
        type?: string;
        properties?: Record<string, unknown>;
        required?: string[];
        additionalProperties?: boolean;
      };
      if (definition.type === "object") {
        expect(definition.required?.sort()).toEqual(
          Object.keys(definition.properties ?? {}).sort(),
        );
        expect(definition.additionalProperties).toBe(false);
      }
      for (const value of Object.values(schema)) check(value);
    };
    check(body.response_format.json_schema.schema);
    const content =
      body.response_format.json_schema.schema.properties.artifacts.items
        .properties.content;
    expect(content.properties.markdown).toMatchObject({
      anyOf: [{ type: "string" }, { type: "null" }],
    });
  });
  it("continues rejecting null mandatory fields even when the provider violates its schema", async () => {
    const { ai, store } = await fixture(
      mockProvider({
        artifacts: [
          {
            kind: "summary",
            title: "Null mandatory text",
            content: { text: null, markdown: null },
            sourceRecordIds: ["r1"],
          },
        ],
      }),
    );
    const job = await ai.enqueue({ type: "analyze", projectId: "p1" }, actor);
    await ai.tick();
    expect(await store.get("ai_job", job.id)).toMatchObject({
      status: "queued",
      lastError: "AI completion failed artifact schema validation.",
    });
    expect(await store.list("ai_artifact")).toHaveLength(0);
  });
  it("keeps an hour-long provider timeout inside a longer renewable worker lease", async () => {
    let leaseUntil = 0;
    let readLease: (() => Promise<number>) | undefined;
    const fetcher = (async (...args: Parameters<typeof fetch>) => {
      leaseUntil = (await readLease?.()) ?? 0;
      return mockProvider()(...args);
    }) as typeof fetch;
    const { ai, store } = await fixture(fetcher);
    await ai.configure(
      {
        requestTimeoutSeconds: 3600,
        expectedVersion: Number((await ai.settings()).version),
      },
      actor,
    );
    const job = await ai.enqueue({ type: "analyze", projectId: "p1" }, actor);
    readLease = async () =>
      Date.parse(String((await store.get("ai_job", job.id))?.leaseUntil));
    const before = Date.now();
    await ai.tick();
    expect(leaseUntil - before).toBeGreaterThan(3600000);
  });
  it("returns a safe provider error without leaking response bodies or API keys", async () => {
    const fetcher = (async () =>
      new Response("private-provider-key and sensitive data", {
        status: 503,
      })) as typeof fetch;
    const { ai, store } = await fixture(fetcher);
    await ai.configure(
      {
        apiKey: "private-provider-key",
        expectedVersion: Number((await ai.settings()).version),
      },
      actor,
    );
    await ai.enqueue({ type: "analyze", projectId: "p1" }, actor);
    await ai.tick();
    expect(JSON.stringify(await ai.jobs())).not.toContain(
      "private-provider-key",
    );
    expect(JSON.stringify(await store.list("audit"))).not.toContain(
      "private-provider-key",
    );
  });
});

import { createServer } from "node:http";
