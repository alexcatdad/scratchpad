import { execFileSync } from "node:child_process";
import { createHash, randomUUID } from "node:crypto";
import postgres from "postgres";
import { afterEach, describe, expect, it } from "vitest";
import { createApi } from "./api";
import { exportKinds } from "./imports";
import { Store } from "./store";

const connection = process.env.TEST_POSTGRES_URL;
const cleanup: (() => Promise<unknown>)[] = [];
afterEach(async () => {
  for (const close of cleanup.splice(0).reverse()) await close();
});
async function database() {
  if (!connection) throw new Error("TEST_POSTGRES_URL is required.");
  const admin = postgres(connection, { max: 1 });
  const name = `scratchpad_test_${randomUUID().replaceAll("-", "")}`;
  await admin.unsafe(`CREATE DATABASE "${name}"`);
  const url = new URL(connection);
  url.pathname = `/${name}`;
  cleanup.push(async () => {
    await admin.unsafe(`DROP DATABASE "${name}" WITH (FORCE)`);
    await admin.end();
  });
  return url.toString();
}
async function fixture(url?: string) {
  const api = createApi({
    databasePath: ":memory:",
    databaseUrl: url,
    origin: "http://localhost:3000",
  });
  cleanup.push(() => api.close());
  await api.store.assertReady();
  if (!(await api.store.get("owner", "owner"))) {
    await api.store.insert("owner", { id: "owner" });
    await api.store.insert("credential", { id: "test", kind: "ssh" });
    await api.store.insert("session", {
      id: createHash("sha256").update("pg-token").digest("hex"),
      credentialId: "test",
      browser: false,
      expiresAt: new Date(Date.now() + 60000).toISOString(),
    });
  }
  const call = async (
    path: string,
    method = "GET",
    body?: unknown,
    headers: Record<string, string> = {},
  ) => {
    const result = await api.handleRequest(
      new Request(
        `http://localhost:3000${path.startsWith("/api/") || path === "/ready" ? path : `/api/v1${path}`}`,
        {
          method,
          headers: { authorization: "Bearer pg-token", ...headers },
          body: body === undefined ? undefined : JSON.stringify(body),
        },
      ),
    );
    return { status: result.status, data: await result.json() };
  };
  return { api, call };
}
describe.skipIf(!connection)("PostgreSQL persistence", () => {
  it("persists captures, native full-text search, idempotency, authentication and export across restart", async () => {
    const url = await database();
    const { api, call } = await fixture(url);
    const project = (
      await call("/projects/resolve", "POST", {
        context: { git: { remote: "git@github.com:demo/postgres.git" } },
      })
    ).data.project;
    const body = {
      projectId: project.id,
      record: {
        type: "finding",
        title: "PostgreSQL persistence",
        authority: "observed",
        confidence: "high",
        payload: { finding: "A durable constellation of memories" },
      },
    };
    const capture = await call("/records", "POST", body, {
      "Idempotency-Key": "pg-first",
    });
    expect(capture.status).toBe(201);
    expect((await call("/records?q=constellation")).data.records).toHaveLength(
      1,
    );
    expect((await call("/records?q=unmatched")).data.records).toHaveLength(0);
    const archive = (await call("/export", "POST", {})).data;
    expect(archive.data.record[0].id).toBe(capture.data.record.id);
    await api.close();
    const restarted = await fixture(url);
    expect((await restarted.call("/ready")).data.database).toBe("postgres");
    expect(
      (
        await restarted.call("/records", "POST", body, {
          "Idempotency-Key": "pg-first",
        })
      ).data.record.id,
    ).toBe(capture.data.record.id);
    expect(
      (
        await restarted.call(
          "/records",
          "POST",
          { ...body, record: { ...body.record, title: "Changed" } },
          { "Idempotency-Key": "pg-first" },
        )
      ).status,
    ).toBe(409);
    const restored = await fixture(await database());
    expect((await restored.call("/import", "POST", archive)).status).toBe(200);
    expect(
      (await restored.call("/records?q=constellation")).data.records,
    ).toHaveLength(1);
  });
  it("serializes retries across application connections and rejects stale edits", async () => {
    const url = await database();
    const first = await fixture(url);
    const second = await fixture(url);
    const project = (
      await first.call("/projects/resolve-explicit", "POST", {
        name: "Concurrent",
      })
    ).data.project;
    const body = {
      projectId: project.id,
      record: {
        type: "decision",
        title: "Retry safely",
        authority: "explicit",
        confidence: "high",
        payload: { decision: "Preserve one capture" },
      },
    };
    const outcomes = await Promise.all([
      first.call("/records", "POST", body, { "Idempotency-Key": "race" }),
      second.call("/records", "POST", body, { "Idempotency-Key": "race" }),
    ]);
    expect(outcomes.every((r) => r.status === 201)).toBe(true);
    expect(new Set(outcomes.map((r) => r.data.record.id)).size).toBe(1);
    expect(await first.api.store.list("record")).toHaveLength(1);
    const metadata = await first.api.store.get(
      "metadata",
      outcomes[0].data.record.id,
    );
    if (!metadata) throw new Error("Capture metadata missing.");
    const edits = await Promise.allSettled([
      first.api.store.update("metadata", { ...metadata, tags: ["one"] }, 1),
      second.api.store.update("metadata", { ...metadata, tags: ["two"] }, 1),
    ]);
    expect(edits.filter((r) => r.status === "fulfilled")).toHaveLength(1);
    expect(edits.filter((r) => r.status === "rejected")).toHaveLength(1);
  });
  it("round-trips complete SQLite knowledge through PostgreSQL and back", async () => {
    const source = await fixture();
    const project = (
      await source.call("/projects/resolve", "POST", {
        context: { git: { remote: "https://github.com/demo/portable.git" } },
      })
    ).data.project;
    const capture = async (title: string) => {
      const result = await source.call("/records", "POST", {
        projectId: project.id,
        record: {
          type: "finding",
          title,
          authority: "observed",
          confidence: "high",
          payload: { finding: `Portable ${title}` },
        },
      });
      expect(result.status).toBe(201);
      return result.data.record;
    };
    const first = await capture("First finding");
    const second = await capture("Second finding");
    const edit = await source.call(`/records/${first.id}/metadata`, "PATCH", {
      displayTitle: "Curated title",
      tags: ["portable"],
      expectedVersion: 1,
    });
    expect(edit.status).toBe(200);
    expect(
      (
        await source.call(`/records/${first.id}/evidence`, "POST", {
          kind: "url",
          reference: "https://example.com/fixture",
          label: "Synthetic evidence",
        })
      ).status,
    ).toBe(201);
    expect(
      (
        await source.call("/relationships", "POST", {
          fromRecordId: first.id,
          toRecordId: second.id,
          type: "supports",
          authority: "explicit",
          status: "accepted",
        })
      ).status,
    ).toBe(201);
    expect(
      (
        await source.call("/profile", "PATCH", {
          displayName: "Portable owner",
          expectedVersion: 0,
        })
      ).status,
    ).toBe(200);
    expect(
      (
        await source.call("/settings", "PATCH", {
          settings: { aiEnabled: false },
          expectedVersion: 0,
        })
      ).status,
    ).toBe(200);
    const artifact = await source.api.store.insert("ai_artifact", {
      id: "artifact_fixture",
      kind: "summary",
      title: "Portable derived interpretation",
      content: { text: "A cited synthetic summary" },
      sourceRecordIds: [first.id, second.id],
      projectIds: [project.id],
      status: "accepted",
      authority: "derived",
      private: true,
      generator: { provider: "synthetic", model: "fixture", version: "1" },
    });
    await source.api.store.insert("curated_artifact", {
      id: "curated_fixture",
      artifactId: artifact.id,
      kind: artifact.kind,
      content: artifact.content,
      sourceRecordIds: artifact.sourceRecordIds,
      projectIds: artifact.projectIds,
      authority: "derived",
      generator: artifact.generator,
      private: true,
    });
    const archive = (await source.call("/export", "POST", {})).data;
    const pg = await fixture(await database());
    expect((await pg.call("/import", "POST", archive)).status).toBe(200);
    const pgArchive = (await pg.call("/export", "POST", {})).data;
    const destination = await fixture();
    expect((await destination.call("/import", "POST", pgArchive)).status).toBe(
      200,
    );
    for (const kind of exportKinds)
      for (const entity of archive.data[kind]) {
        expect(await pg.api.store.get(kind, entity.id)).toEqual(entity);
        expect(await destination.api.store.get(kind, entity.id)).toEqual(
          entity,
        );
      }
    expect(
      (await destination.call("/records?q=Portable")).data.records,
    ).toHaveLength(2);
    expect((await pg.call("/records?q=Portable")).data.records).toHaveLength(2);
    const corrupted = structuredClone(archive);
    corrupted.data.ai_artifact[0].sourceRecordIds = ["missing-source"];
    const clean = await fixture();
    expect((await clean.call("/import", "POST", corrupted)).status).toBe(404);
    expect(await clean.api.store.list("record")).toHaveLength(0);
  });
  it("rolls back a failed transaction and reports broken persistence readiness", async () => {
    const url = await database();
    const store = new Store(":memory:", url);
    cleanup.push(() => store.close());
    await store.assertReady();
    await expect(
      store.atomic(async () => {
        await store.insert("project", { id: "rolled-back" });
        throw new Error("rollback");
      }),
    ).rejects.toThrow("rollback");
    expect(await store.get("project", "rolled-back")).toBeUndefined();
    const sql = postgres(url);
    cleanup.push(() => sql.end());
    await sql`DROP TABLE retries`;
    await expect(store.assertReady()).rejects.toMatchObject({
      code: "DATABASE_NOT_READY",
    });
  });
  it.skipIf(!process.env.TEST_POSTGRES_CONTAINER)(
    "restores pg_dump including credentials, retries, AI jobs and provider configuration",
    async () => {
      const sourceUrl = await database();
      const source = await fixture(sourceUrl);
      const project = (
        await source.call("/projects/resolve-explicit", "POST", {
          name: "Backup fixture",
        })
      ).data.project;
      const body = {
        projectId: project.id,
        record: {
          type: "finding",
          title: "Restorable source",
          authority: "observed",
          confidence: "high",
          payload: { finding: "Synthetic PostgreSQL full backup" },
        },
      };
      const record = (
        await source.call("/records", "POST", body, {
          "Idempotency-Key": "full-backup-retry",
        })
      ).data.record;
      await source.api.store.insert("challenge", {
        id: "backup-challenge",
        kind: "ssh",
        nonce: "synthetic-nonce",
        expiresAt: new Date(Date.now() + 60000).toISOString(),
      });
      await source.api.store.insert("ai_settings", {
        id: "global",
        enabled: false,
        apiKey: "synthetic-provider-secret",
        model: "synthetic-model",
      });
      await source.api.store.insert("ai_job", {
        id: "queued-backup-job",
        type: "analyze",
        status: "queued",
        scope: { projectIds: [project.id] },
        attempts: 0,
      });
      const kinds = [
        ...exportKinds,
        "owner",
        "credential",
        "session",
        "challenge",
        "ai_settings",
        "ai_job",
      ];
      const original = new Map(
        await Promise.all(
          kinds.map(
            async (kind) => [kind, await source.api.store.list(kind)] as const,
          ),
        ),
      );
      // Stop connections before backup for a deterministic snapshot fixture. pg_dump itself provides a consistent snapshot.
      await source.api.close();
      const targetUrl = await database();
      const container = process.env.TEST_POSTGRES_CONTAINER ?? "";
      const path = `/tmp/scratchpad-backup-${randomUUID()}.dump`;
      const parsed = new URL(sourceUrl);
      const target = new URL(targetUrl);
      const username = decodeURIComponent(parsed.username);
      try {
        execFileSync(
          "docker",
          [
            "exec",
            container,
            "pg_dump",
            "-U",
            username,
            "-d",
            parsed.pathname.slice(1),
            "--format=custom",
            "--file",
            path,
          ],
          { stdio: "pipe", timeout: 60000 },
        );
        execFileSync(
          "docker",
          [
            "exec",
            container,
            "pg_restore",
            "-U",
            username,
            "-d",
            target.pathname.slice(1),
            "--exit-on-error",
            path,
          ],
          { stdio: "pipe", timeout: 60000 },
        );
        const restored = await fixture(targetUrl);
        for (const kind of kinds)
          expect(await restored.api.store.list(kind)).toEqual(
            original.get(kind),
          );
        expect(
          (await restored.call("/records?q=Restorable")).data.records[0].id,
        ).toBe(record.id);
        expect(
          (
            await restored.call("/records", "POST", body, {
              "Idempotency-Key": "full-backup-retry",
            })
          ).data.record.id,
        ).toBe(record.id);
        expect((await restored.call("/ai/jobs")).data.jobs[0].status).toBe(
          "queued",
        );
        expect(
          (await restored.call("/ai/settings")).data.apiKeyConfigured,
        ).toBe(true);
        expect(
          JSON.stringify((await restored.call("/ai/settings")).data),
        ).not.toContain("synthetic-provider-secret");
      } finally {
        execFileSync("docker", ["exec", container, "rm", "-f", path], {
          stdio: "pipe",
          timeout: 10000,
        });
      }
    },
  );
  it("refuses unrelated databases without modifying their tables", async () => {
    const url = await database();
    const sql = postgres(url);
    cleanup.push(() => sql.end());
    await sql`CREATE TABLE unrelated(value TEXT)`;
    const store = new Store(":memory:", url);
    cleanup.push(() => store.close());
    await expect(store.assertReady()).rejects.toMatchObject({
      code: "DATABASE_NOT_READY",
    });
    expect(
      (
        await sql`SELECT tablename FROM pg_tables WHERE schemaname=current_schema()`
      ).map((r) => r.tablename),
    ).toEqual(["unrelated"]);
  });
});
