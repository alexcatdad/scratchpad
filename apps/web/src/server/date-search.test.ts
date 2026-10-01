import { createHash } from "node:crypto";
import { expect, it } from "vitest";
import { createApi } from "./api";

it("compares inclusive search dates chronologically across fractional precision", async () => {
  const api = createApi({
    databasePath: ":memory:",
    origin: "http://localhost:3000",
  });
  try {
    await api.store.insert("owner", { id: "owner" });
    await api.store.insert("credential", {
      id: "key",
      kind: "ssh",
      fingerprint: "SHA256:test",
    });
    await api.store.insert("session", {
      id: createHash("sha256").update("token").digest("hex"),
      credentialId: "key",
      browser: false,
      expiresAt: new Date(Date.now() + 60_000).toISOString(),
    });
    async function call(path: string, body?: unknown) {
      return api.handleRequest(
        new Request(`http://localhost:3000/api/v1${path}`, {
          method: body === undefined ? "GET" : "POST",
          headers: { authorization: "Bearer token" },
          body: body === undefined ? undefined : JSON.stringify(body),
        }),
      );
    }
    const projectResponse = await call("/projects/resolve-explicit", {
      name: "Date retrieval",
    });
    const { project } = await projectResponse.json();
    const times = [
      "2026-01-01T00:00:00Z",
      "2026-01-01T00:00:00.00001Z",
      "2026-01-01T00:00:00.0001Z",
      "2026-01-01T00:00:00.050Z",
      "2026-01-01T00:00:00.5Z",
      "2026-01-01T00:00:00.500Z",
      "2026-01-01T00:00:01Z",
    ];
    for (const happenedAt of times) {
      const created = await call("/records", {
        projectId: project.id,
        record: {
          type: "finding",
          title: happenedAt,
          authority: "observed",
          confidence: "high",
          happenedAt,
          payload: { finding: "Date boundary evidence" },
        },
      });
      expect(created.status).toBe(201);
    }
    const boundaries = [
      { from: times[0], expected: times },
      { to: times[0], expected: times.slice(0, 1) },
      { from: times[1], expected: times.slice(1) },
      { to: times[1], expected: times.slice(0, 2) },
      { from: times[2], expected: times.slice(2) },
      { to: times[2], expected: times.slice(0, 3) },
      { from: times[3], expected: times.slice(3) },
      { to: times[3], expected: times.slice(0, 4) },
      { from: times[5], expected: times.slice(4) },
      { to: times[5], expected: times.slice(0, 6) },
      { from: times[4], to: times[5], expected: times.slice(4, 6) },
    ];
    for (const { from, to, expected } of boundaries) {
      const params = new URLSearchParams({ projectId: project.id });
      if (from) params.set("from", from);
      if (to) params.set("to", to);
      const response = await call(`/records?${params}`);
      expect(response.status).toBe(200);
      const { records } = await response.json();
      expect(
        records
          .map((record: { happenedAt: string }) => record.happenedAt)
          .sort(),
      ).toEqual([...expected].sort());
    }
    // Captures without happenedAt use the server's recordedAt timestamp.
    const recorded = await call("/records", {
      projectId: project.id,
      record: {
        type: "finding",
        title: "Receipt time",
        authority: "observed",
        payload: { finding: "No historical date" },
      },
    });
    const { record } = await recorded.json();
    const receiptSecond = record.recordedAt.replace(/\.\d+Z$/, "Z");
    const receiptResponse = await call(
      `/records?${new URLSearchParams({ projectId: project.id, from: receiptSecond })}`,
    );
    expect(
      (await receiptResponse.json()).records.map((r: { id: string }) => r.id),
    ).toContain(record.id);
  } finally {
    await api.close();
  }
});
