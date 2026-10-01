import { randomUUID } from "node:crypto";
import postgres from "postgres";
import { afterEach, describe, expect, it, vi } from "vitest";
import { AiService } from "./ai";
import { defaults } from "./domain";
import { Store } from "./store";

const connection = process.env.TEST_PGVECTOR_URL;
const cleanup: (() => Promise<unknown>)[] = [];
afterEach(async () => {
  for (const close of cleanup.splice(0).reverse()) await close();
});
async function fixture(extension = true) {
  const admin = postgres(connection as string, { max: 1 });
  const name = `scratchpad_vector_${randomUUID().replaceAll("-", "")}`;
  await admin.unsafe(`CREATE DATABASE "${name}"`);
  cleanup.push(async () => {
    await admin.unsafe(`DROP DATABASE "${name}" WITH (FORCE)`);
    await admin.end();
  });
  const url = new URL(connection as string);
  url.pathname = `/${name}`;
  const sql = postgres(url.toString());
  cleanup.push(() => sql.end());
  if (extension) await sql`CREATE EXTENSION vector`;
  return { url: url.toString(), sql };
}
function embedding(id: string, vector: number[], fingerprint = "model-a") {
  return {
    id,
    recordId: `record-${id}`,
    vector,
    dimensions: vector.length,
    fingerprint,
    model: "synthetic-model",
    contentHash: `hash-${id}`,
  };
}
describe.skipIf(!connection)("opt-in native pgvector", () => {
  it("ranks all 2560 dimensions after candidate compatibility filtering and survives restart/rebuild", async () => {
    const { url, sql } = await fixture();
    let store = new Store(":memory:", url);
    await store.assertReady();
    const query = Array.from({ length: 2560 }, (_, i) => (i === 2559 ? 1 : 0));
    const best = await store.insert("ai_embedding", embedding("best", query));
    await store.close();
    store = new Store(":memory:", url, { pgvector: true });
    cleanup.push(() => store.close());
    await store.assertReady();
    const other = await store.insert(
      "ai_embedding",
      embedding(
        "other",
        query.map((n, i) => (i === 0 ? 1 : n)),
      ),
    );
    const incompatible = await store.insert(
      "ai_embedding",
      embedding("incompatible", query, "different-model"),
    );
    expect(await store.vectorScores([best, other], query)).toEqual([
      { recordId: "record-best", score: 1 },
      { recordId: "record-other", score: expect.closeTo(Math.SQRT1_2, 6) },
    ]);
    expect(
      await store.vectorScores(
        [{ ...incompatible, fingerprint: "model-a" }],
        query,
      ),
    ).toBeUndefined();
    expect(
      await store.vectorScores([{ ...best, contentHash: "stale" }], query),
    ).toBeUndefined();
    expect(
      (
        await sql`SELECT vector_dims(embedding) AS dimensions FROM pgvector_embeddings WHERE id='best'`
      )[0]?.dimensions,
    ).toBe(2560);
    const updated = await store.update(
      "ai_embedding",
      { ...best, vector: other.vector },
      best.version,
    );
    expect(
      (await store.vectorScores([updated], query))?.[0]?.score,
    ).toBeCloseTo(Math.SQRT1_2, 6);
    await store.remove("ai_embedding", "other");
    expect(
      await sql`SELECT id FROM pgvector_embeddings WHERE id='other'`,
    ).toHaveLength(0);
    await expect(
      store.atomic(async () => {
        await store.insert("ai_embedding", embedding("rollback", query));
        throw new Error("rollback");
      }),
    ).rejects.toThrow("rollback");
    expect(
      await sql`SELECT id FROM pgvector_embeddings WHERE id='rollback'`,
    ).toHaveLength(0);
    await store.close();
    const disabled = new Store(":memory:", url);
    await disabled.remove("ai_embedding", "best");
    await disabled.close();
    store = new Store(":memory:", url, { pgvector: true });
    await store.assertReady();
    expect(
      await sql`SELECT id FROM pgvector_embeddings WHERE id='best'`,
    ).toHaveLength(0);
    await sql`DROP TABLE pgvector_embeddings`;
    await expect(store.assertReady()).rejects.toMatchObject({
      code: "DATABASE_NOT_READY",
    });
  });
  it("runs native ranking only over consent-authorized compatible source records", async () => {
    const { url } = await fixture();
    const store = new Store(":memory:", url, { pgvector: true });
    cleanup.push(() => store.close());
    const fetcher = vi.fn(async (_url: unknown, options?: RequestInit) => {
      const body = JSON.parse(String(options?.body));
      return Response.json({
        data: body.input.map((_: string, index: number) => ({
          index,
          embedding: [1, 0],
        })),
      });
    }) as typeof fetch;
    const ai = new AiService(store, fetcher);
    await store.insert("project", {
      id: "allowed",
      settings: { ...defaults("normal"), aiProcessing: true },
    });
    await store.insert("project", {
      id: "denied",
      settings: defaults("normal"),
    });
    for (const projectId of ["allowed", "denied"])
      await store.insert("record", {
        id: `r-${projectId}`,
        projectId,
        title: "Synthetic source",
        type: "finding",
        content: "Synthetic content",
        payload: { finding: "Synthetic content" },
      });
    await ai.configure(
      {
        enabled: true,
        embeddingDimensions: 2,
        scheduleMinutes: 0,
        expectedVersion: 0,
      },
      { kind: "user", displayName: "Test owner" },
    );
    await ai.enqueue(
      { type: "embed", projectId: "allowed" },
      { kind: "user", displayName: "Test owner" },
    );
    await ai.tick();
    const vectorScores = store.vectorScores.bind(store);
    const native = vi.spyOn(store, "vectorScores");
    expect(
      await ai.semanticSearch({ query: "source", projectId: "allowed" }),
    ).toMatchObject({ results: [{ record: { id: "r-allowed" }, score: 1 }] });
    expect(native.mock.calls[0]?.[0].map((e) => e.recordId)).toEqual([
      "r-allowed",
    ]);
    const calls = vi.mocked(fetcher).mock.calls.length;
    await expect(
      ai.semanticSearch({ query: "secret", projectId: "denied" }),
    ).rejects.toMatchObject({ code: "AI_NOT_ALLOWED" });
    expect(vi.mocked(fetcher).mock.calls).toHaveLength(calls);
    const allowedProject = await store.get("project", "allowed");
    if (!allowedProject) throw new Error("Missing test project");
    native.mockImplementationOnce(async (eligible, query) => {
      const scores = await vectorScores(eligible, query);
      await store.update(
        "project",
        { ...allowedProject, settings: defaults("normal") },
        allowedProject.version,
      );
      return scores;
    });
    await expect(
      ai.semanticSearch({ query: "source", projectId: "allowed" }),
    ).rejects.toMatchObject({ code: "AI_NOT_ALLOWED" });
    const revoked = await store.get("project", "allowed");
    if (!revoked) throw new Error("Missing test project");
    await store.update(
      "project",
      { ...revoked, settings: { ...defaults("normal"), aiProcessing: true } },
      revoked.version,
    );
    await ai.configure(
      { embeddingModel: "changed-model", expectedVersion: 1 },
      { kind: "user", displayName: "Test owner" },
    );
    expect(
      await ai.semanticSearch({ query: "source", projectId: "allowed" }),
    ).toMatchObject({ indexRequired: true, results: [] });
  });
  it("fails readiness without a preinstalled extension but ordinary PostgreSQL remains independent", async () => {
    const { url, sql } = await fixture(false);
    const enabled = new Store(":memory:", url, { pgvector: true });
    cleanup.push(() => enabled.close());
    await expect(enabled.assertReady()).rejects.toMatchObject({
      code: "DATABASE_NOT_READY",
    });
    expect(
      await sql`SELECT extname FROM pg_extension WHERE extname='vector'`,
    ).toHaveLength(0);
    const normal = new Store(":memory:", url);
    cleanup.push(() => normal.close());
    await normal.assertReady();
    expect(await normal.vectorScores([], [1, 0])).toBeUndefined();
  });
  it("retains configurations above pgvector's 16000 dimension storage limit without truncation", async () => {
    const { url, sql } = await fixture();
    const store = new Store(":memory:", url, { pgvector: true });
    cleanup.push(() => store.close());
    const vector = Array.from({ length: 16384 }, (_, i) =>
      i === 16383 ? 1 : 0,
    );
    const entity = await store.insert(
      "ai_embedding",
      embedding("wide", vector),
    );
    expect((await store.get("ai_embedding", "wide"))?.vector).toEqual(vector);
    expect(await store.vectorScores([entity], vector)).toBeUndefined();
    expect(await sql`SELECT id FROM pgvector_embeddings`).toHaveLength(0);
  });
});
it("rejects pgvector for SQLite and leaves default SQLite behavior unchanged", async () => {
  expect(() => new Store(":memory:", undefined, { pgvector: true })).toThrow(
    "requires",
  );
  const store = new Store(":memory:");
  await store.assertReady();
  expect(await store.vectorScores([], [1, 0])).toBeUndefined();
  await store.close();
});
