import { afterEach, describe, expect, it, vi } from "vitest";
import { AiService, OpenAiProvider } from "./ai";
import { defaults, type Entity } from "./domain";
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
  const ai = new AiService(store, fetcher, () => time);
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
      "/records/r1",
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
      "/records/extra44",
    );
    expect(vi.mocked(fetcher).mock.calls.length).toBeGreaterThan(1);
    expect(
      vi
        .mocked(fetcher)
        .mock.calls.every((call) => String(call[1]?.body).length < 20000),
    ).toBe(true);
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
