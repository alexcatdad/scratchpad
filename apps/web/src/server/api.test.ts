import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { createApi } from "./api";
import { normalizeRemote } from "./domain";

const cleanups: (() => void | Promise<void>)[] = [];
afterEach(async () => {
  for (const cleanup of cleanups.splice(0)) await cleanup();
});
async function fixture(persistent = false) {
  const directory = mkdtempSync(join(tmpdir(), "scratchpad-test-"));
  cleanups.push(() => rmSync(directory, { recursive: true, force: true }));
  const databasePath = persistent
    ? join(directory, "memory.sqlite")
    : ":memory:";
  const api = createApi({ databasePath, origin: "http://localhost:3000" });
  cleanups.push(async () => {
    if (api.store.sqlite.open) await api.close();
  });
  // Test-only persisted credentials exercise the production authentication guard.
  await api.store.insert("owner", { id: "owner", displayName: "Test owner" });
  await api.store.insert("credential", {
    id: "test-key",
    kind: "ssh",
    fingerprint: "SHA256:test",
  });
  await api.store.insert("credential", { id: "passkey", kind: "webauthn" });
  await api.store.insert("session", {
    id: createHash("sha256").update("test-token").digest("hex"),
    credentialId: "test-key",
    browser: false,
    expiresAt: new Date(Date.now() + 100000).toISOString(),
  });
  const call = async (
    path: string,
    method = "GET",
    body?: unknown,
    headers: Record<string, string> = {},
  ) => {
    const result = await api.handleRequest(
      new Request(`http://localhost:3000${path}`, {
        method,
        headers: { authorization: "Bearer test-token", ...headers },
        body: body === undefined ? undefined : JSON.stringify(body),
      }),
    );
    return {
      status: result.status,
      data: await result.json(),
      headers: result.headers,
    };
  };
  return { api, call, directory, databasePath };
}
const capture = {
  type: "decision",
  title: "Use SQLite",
  authority: "explicit",
  confidence: "high",
  payload: {
    decision: "Use SQLite",
    rationale: "One container and reliable local persistence.",
  },
};
async function project(call: Awaited<ReturnType<typeof fixture>>["call"]) {
  return (
    await call("/api/v1/projects/resolve", "POST", {
      context: { git: { remote: "git@github.com:alexcatdad/scratchpad.git" } },
    })
  ).data.project;
}

describe("persistent API domain", () => {
  it("normalizes Git identity without worktree paths or remote credentials", () => {
    expect(normalizeRemote("git@github.com:alexcatdad/scratchpad.git")).toBe(
      normalizeRemote("https://token@github.com/alexcatdad/scratchpad.git"),
    );
  });
  it("requires authenticated access and a project identity", async () => {
    const { api, call } = await fixture();
    expect(
      (
        await api.handleRequest(
          new Request("http://localhost:3000/api/v1/records"),
        )
      ).status,
    ).toBe(401);
    expect(
      (
        await call("/api/v1/projects/resolve", "POST", {
          context: { folder: "example" },
        })
      ).data.error.code,
    ).toBe("PROJECT_IDENTITY_REQUIRED");
    const first = await project(call),
      second = await project(call);
    expect(first.id).toBe(second.id);
  });
  it("persists idempotency across restart, refuses changed content, and retains original Git provenance", async () => {
    const { api, call, databasePath } = await fixture(true),
      p = await project(call);
    const body = {
      projectId: p.id,
      record: capture,
      gitContext: { dirty: false, commit: "original" },
    };
    const first = await call("/api/v1/records", "POST", body, {
      "Idempotency-Key": "capture-1",
    });
    expect(first.status).toBe(201);
    const retry = await call(
      "/api/v1/records",
      "POST",
      { ...body, gitContext: { dirty: true, commit: "later" } },
      { "Idempotency-Key": "capture-1" },
    );
    expect(retry.data.record.id).toBe(first.data.record.id);
    expect(retry.data.record.gitContext.commit).toBe("original");
    expect(
      (
        await call(
          "/api/v1/records",
          "POST",
          { ...body, record: { ...capture, title: "Changed" } },
          { "Idempotency-Key": "capture-1" },
        )
      ).status,
    ).toBe(409);
    await api.close();
    const restarted = createApi({
      databasePath,
      origin: "http://localhost:3000",
    });
    cleanups.push(async () => await restarted.close());
    const response = await restarted.handleRequest(
      new Request("http://localhost:3000/api/v1/records", {
        method: "POST",
        headers: {
          authorization: "Bearer test-token",
          "Idempotency-Key": "capture-1",
        },
        body: JSON.stringify(body),
      }),
    );
    expect((await response.json()).record.id).toBe(first.data.record.id);
  });
  it("preserves raw records through curated corrections and rejects stale edits", async () => {
    const { call } = await fixture(),
      p = await project(call);
    const saved = (
      await call("/api/v1/records", "POST", {
        projectId: p.id,
        record: capture,
      })
    ).data.record;
    const changed = await call(
      `/api/v1/records/${saved.id}/metadata`,
      "PATCH",
      {
        expectedVersion: 1,
        displayTitle: "Use embedded SQLite",
        tags: ["database"],
      },
    );
    expect(changed.status).toBe(200);
    expect(
      (
        await call(`/api/v1/records/${saved.id}/metadata`, "PATCH", {
          expectedVersion: 1,
          tags: ["stale"],
        })
      ).status,
    ).toBe(409);
    const detail = (await call(`/api/v1/records/${saved.id}`)).data;
    expect(detail.record).toEqual(saved);
    expect(detail.revisions).toHaveLength(1);
    expect(detail.revisions[0].previous.tags).toEqual([]);
    expect(detail.metadata.tags).toEqual(["database"]);
    const results = (await call("/api/v1/records?q=SQLite&tag=database")).data
      .records;
    expect(results).toHaveLength(1);
  });
  it("paginates same-time records without duplicates and searches literal FTS input", async () => {
    const { call } = await fixture(),
      p = await project(call);
    for (let n = 0; n < 4; n++)
      await call("/api/v1/records", "POST", {
        projectId: p.id,
        record: { ...capture, title: `Decision ${n}` },
      });
    const first = (await call("/api/v1/records?limit=2")).data;
    const second = (
      await call(`/api/v1/records?limit=2&cursor=${first.nextCursor}`)
    ).data;
    expect(
      new Set([...first.records, ...second.records].map((r) => r.id)).size,
    ).toBe(4);
    expect(second.nextCursor).toBeNull();
    expect((await call("/api/v1/records?q=%22")).status).toBe(200);
  });
  it("round trips knowledge and audit history without exporting authentication", async () => {
    const { call } = await fixture(),
      p = await project(call);
    await call("/api/v1/records", "POST", { projectId: p.id, record: capture });
    const exported = (await call("/api/v1/export", "POST", {})).data;
    expect(exported.data.credential).toBeUndefined();
    expect(exported.data.session).toBeUndefined();
    const destination = await fixture();
    expect(
      (await destination.call("/api/v1/import", "POST", exported)).status,
    ).toBe(200);
    const output = (await destination.call("/api/v1/export", "POST", {})).data;
    expect(output.data.record).toEqual(exported.data.record);
    expect(output.data.project).toEqual(exported.data.project);
    expect(
      (await destination.call("/api/v1/import", "POST", exported)).data
        .imported,
    ).toBe(0);
  });
  it("rejects unroutable native IDs atomically and preserves routable colon IDs", async () => {
    const source = await fixture();
    const p = await project(source.call);
    await source.call("/api/v1/records", "POST", {
      projectId: p.id,
      record: capture,
    });
    const exported = (await source.call("/api/v1/export", "POST", {})).data;
    for (const id of [
      ".",
      "..",
      "victim/accept?",
      "../../auth/logout#",
      "victim#",
      "victim?",
      "victim%2faccept",
      "victim\\accept",
    ]) {
      const destination = await fixture();
      const bad = structuredClone(exported);
      bad.data.record[0].id = id;
      bad.data.metadata[0].id = id;
      bad.data.metadata[0].recordId = id;
      expect(
        (await destination.call("/api/v1/import", "POST", bad)).status,
      ).toBe(400);
      expect(await destination.api.store.list("project")).toHaveLength(0);
      expect(await destination.api.store.list("record")).toHaveLength(0);
    }
    const destination = await fixture();
    const legacy = await destination.call("/api/v1/import", "POST", {
      format: "jsonl",
      projectId: (await project(destination.call)).id,
      jsonl: JSON.stringify({
        id: "stable:decision-1",
        decision: "Synthetic decision",
      }),
    });
    expect(legacy.status).toBe(200);
    // The old record detail route treated %3A as a literal stored ID and returned 404.
    const path = "/api/v1/records/stable%3Adecision-1";
    expect((await destination.call(path)).data.record.id).toBe(
      "stable:decision-1",
    );
    const detail = (await destination.call(path)).data;
    const edited = await destination.call(`${path}/metadata`, "PATCH", {
      expectedVersion: detail.metadata.version,
      displayTitle: "Synthetic edited title",
    });
    expect(edited.status).toBe(200);
    expect(
      (
        await destination.call(`${path}/metadata`, "PATCH", {
          expectedVersion: detail.metadata.version,
          displayTitle: "Stale",
        })
      ).status,
    ).toBe(409);
    const archive = (await destination.call("/api/v1/export", "POST", {})).data;
    const restored = await fixture();
    expect(
      (await restored.call("/api/v1/import", "POST", archive)).status,
    ).toBe(200);
    expect((await restored.call(path)).data.record.id).toBe(
      "stable:decision-1",
    );
  });
  it("rolls back invalid native imports and reports malformed legacy lines", async () => {
    const { call } = await fixture(),
      p = await project(call);
    const legacy = await call("/api/v1/import", "POST", {
      format: "jsonl",
      projectId: p.id,
      jsonl: `{bad}\n${JSON.stringify({ decision: "Keep raw evidence" })}`,
    });
    expect(legacy.data.imported).toBe(1);
    expect(legacy.data.skipped).toBe(1);
    expect(
      legacy.data.warnings.map((warning: { code: string }) => warning.code),
    ).toEqual(
      expect.arrayContaining([
        "INVALID_JSON",
        "MISSING_ID",
        "MISSING_RATIONALE",
        "UNVERIFIED_LEGACY_PROVENANCE",
        "MISSING_DATE",
      ]),
    );
    const exported = (await call("/api/v1/export", "POST", {})).data;
    exported.data.record[0].projectId = "missing";
    const destination = await fixture();
    expect(
      (await destination.call("/api/v1/import", "POST", exported)).status,
    ).toBe(404);
    expect(
      (await destination.call("/api/v1/projects")).data.projects,
    ).toHaveLength(0);
  });
  it("blocks replacement cycles, detects stale settings, and preserves central captures on mirror failure", async () => {
    const { call } = await fixture(),
      p = await project(call);
    const first = (
      await call("/api/v1/records", "POST", {
        projectId: p.id,
        record: capture,
      })
    ).data.record;
    const second = (
      await call("/api/v1/records", "POST", {
        projectId: p.id,
        record: capture,
      })
    ).data.record;
    expect(
      (
        await call("/api/v1/relationships", "POST", {
          fromRecordId: first.id,
          toRecordId: second.id,
          type: "replaces",
        })
      ).status,
    ).toBe(201);
    expect(
      (
        await call("/api/v1/relationships", "POST", {
          fromRecordId: second.id,
          toRecordId: first.id,
          type: "replaces",
        })
      ).data.error.code,
    ).toBe("RELATIONSHIP_CYCLE");
    const settings = {
      ...p.settings,
      repoMirroring: { enabled: true, recordTypes: ["decision"] },
    };
    expect(
      (
        await call(`/api/v1/projects/${p.id}/settings`, "PATCH", {
          settings,
          expectedVersion: 1,
        })
      ).status,
    ).toBe(200);
    expect(
      (
        await call(`/api/v1/projects/${p.id}/settings`, "PATCH", {
          settings,
          expectedVersion: 1,
        })
      ).status,
    ).toBe(409);
    expect(
      (
        await call(`/api/v1/records/${first.id}/mirror`, "POST", {
          attempted: true,
          succeeded: false,
          error: "Read-only checkout",
        })
      ).status,
    ).toBe(200);
    expect((await call(`/api/v1/records/${first.id}`)).data.record.id).toBe(
      first.id,
    );
  });
});

describe("owner authentication", () => {
  it("single-use setup tokens expire and initial setup stays closed after enrollment", async () => {
    const directory = mkdtempSync(join(tmpdir(), "scratchpad-setup-"));
    cleanups.push(() => rmSync(directory, { recursive: true, force: true }));
    const api = createApi({
      databasePath: join(directory, "setup.sqlite"),
      origin: "http://localhost:3000",
    });
    cleanups.push(async () => await api.close());
    const old = await api.auth.createSetupToken(),
      token = await api.auth.createSetupToken();
    expect(old).not.toBe(token);
    const request = (setupToken: string, origin = "http://localhost:3000") =>
      api.handleRequest(
        new Request("http://localhost:3000/api/v1/auth/register/options", {
          method: "POST",
          headers: { origin },
          body: JSON.stringify({ setupToken }),
        }),
      );
    expect((await request(old)).status).toBe(401);
    expect((await request(token, "https://evil.example")).status).toBe(403);
    const options = await request(token);
    expect(options.status).toBe(200);
    expect((await options.json()).options.challenge).toBeTruthy();
    await api.store.insert("owner", { id: "owner" });
    await expect(api.auth.createSetupToken()).rejects.toThrow("disabled");
    expect(await api.auth.createSetupToken(true)).toBeTruthy();
  });
  it("verifies a real OpenSSH signature, prevents replay, survives restart, and enforces revocation", async () => {
    const { api, directory, databasePath } = await fixture(true),
      keyPath = join(directory, "identity");
    const keygen = spawnSync("ssh-keygen", [
      "-q",
      "-t",
      "ed25519",
      "-N",
      "",
      "-f",
      keyPath,
    ]);
    expect(keygen.status).toBe(0);
    const publicKey = readFileSync(`${keyPath}.pub`, "utf8")
      .trim()
      .split(/\s+/)
      .slice(0, 2)
      .join(" ");
    await api.store.insert("credential", {
      id: "real-key",
      kind: "ssh",
      publicKey,
    });
    const challenge = await api.auth.sshChallenge({ publicKey }),
      message = join(directory, "message");
    writeFileSync(message, String(challenge.nonce));
    const sign = spawnSync("ssh-keygen", [
      "-Y",
      "sign",
      "-f",
      keyPath,
      "-n",
      "scratchpad-auth",
      message,
    ]);
    expect(sign.status).toBe(0);
    const body = {
      challengeId: challenge.challengeId,
      publicKey,
      signature: readFileSync(`${message}.sig`, "utf8"),
    };
    const token = await api.auth.sshVerify(body);
    expect(token.accessToken).toBeTruthy();
    await expect(api.auth.sshVerify(body)).rejects.toThrow("consumed");
    await api.close();
    const restarted = createApi({
      databasePath,
      origin: "http://localhost:3000",
    });
    cleanups.push(async () => await restarted.close());
    const request = new Request("http://localhost:3000/api/v1/projects", {
      headers: { authorization: `Bearer ${String(token.accessToken)}` },
    });
    expect((await restarted.handleRequest(request)).status).toBe(200);
    const credential = await restarted.store.get("credential", "real-key");
    if (!credential) throw new Error("Missing test credential");
    await restarted.store.update("credential", {
      ...credential,
      revokedAt: new Date().toISOString(),
    });
    expect((await restarted.handleRequest(request)).status).toBe(401);
  });
  it("does not allow bearer tokens in browser cookies or credential management", async () => {
    const { api, call } = await fixture();
    const cookie = await api.handleRequest(
      new Request("http://localhost:3000/api/v1/projects", {
        headers: { cookie: "scratchpad_session=test-token" },
      }),
    );
    expect(cookie.status).toBe(401);
    expect(
      (
        await call("/api/v1/auth/credentials/challenge", "POST", {
          publicKey: "ssh-ed25519 AAAA",
        })
      ).status,
    ).toBe(403);
  });
});

it("audits portable owner profiles and AI configuration without exposing credentials", async () => {
  const { call } = await fixture();
  const profile = await call("/api/v1/profile");
  expect(profile.data.profile.version).toBe(0);
  expect(profile.data.database.engine).toBe("sqlite");
  const changed = await call("/api/v1/profile", "PATCH", {
    displayName: "Local owner",
    expectedVersion: 0,
  });
  expect(changed.data.profile.displayName).toBe("Local owner");
  expect(
    (
      await call("/api/v1/profile", "PATCH", {
        displayName: "Stale",
        expectedVersion: 0,
      })
    ).status,
  ).toBe(409);
  const configured = await call("/api/v1/ai/settings", "PATCH", {
    enabled: true,
    apiKey: "synthetic-test-secret",
    expectedVersion: 0,
  });
  expect(configured.data.apiKeyConfigured).toBe(true);
  expect(JSON.stringify(configured.data)).not.toContain(
    "synthetic-test-secret",
  );
  expect((await call("/api/v1/settings")).data.settings.aiEnabled).toBe(true);
  const archive = (await call("/api/v1/export", "POST", {})).data;
  expect(archive.data.profile[0].displayName).toBe("Local owner");
  expect(archive.data.ai_settings).toBeUndefined();
  expect(JSON.stringify(archive)).not.toContain("synthetic-test-secret");
});
