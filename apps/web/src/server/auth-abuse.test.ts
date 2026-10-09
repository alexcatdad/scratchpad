import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { ServerRequest } from "srvx";
import { serve } from "srvx/node";
import { expect, it } from "vitest";
import { createApi } from "./api";

it("uses actual srvx socket metadata for configured proxy clients", async () => {
  const api = createApi({
    clock: () => Date.parse("2026-10-09T12:00:00Z"),
    databasePath: ":memory:",
    origin: "http://localhost:3000",
    trustedProxies: ["127.0.0.1"],
  });
  const server = serve({
    hostname: "127.0.0.1",
    port: 0,
    trustProxy: false,
    fetch: (request: ServerRequest) => api.handleRequest(request, request.ip),
  });
  try {
    await api.store.insert("owner", { id: "owner" });
    await server.ready();
    if (!server.url) throw new Error("Missing local test server URL");
    const endpoint = new URL("/api/v1/auth/login/options", server.url);
    const options = (forwarded: string) =>
      fetch(endpoint, {
        method: "POST",
        headers: {
          origin: "http://localhost:3000",
          "x-forwarded-for": forwarded,
        },
        body: "{}",
      });
    for (let attempt = 0; attempt < 125; attempt++) {
      const response = await options(`198.51.100.${attempt}, 192.0.2.2`);
      expect(response.status).toBe(attempt < 120 ? 200 : 429);
      await response.arrayBuffer();
    }
    const independent = await options("192.0.2.1");
    expect(independent.status).toBe(200);
    await independent.arrayBuffer();
  } finally {
    await server.close(true);
    await api.close();
  }
});

it("resolves independent proxy clients from the nearest untrusted hop and ignores forged direct headers", async () => {
  const api = createApi({
    clock: () => Date.parse("2026-10-09T12:00:00Z"),
    databasePath: ":memory:",
    origin: "http://localhost:3000",
    trustedProxies: ["127.0.0.1"],
  });
  try {
    await api.store.insert("owner", { id: "owner" });
    const options = (peer: string, forwarded: string) =>
      api.handleRequest(
        new Request("http://localhost:3000/api/v1/auth/login/options", {
          method: "POST",
          headers: {
            origin: "http://localhost:3000",
            "x-forwarded-for": forwarded,
          },
          body: "{}",
        }),
        peer,
      );
    for (let attempt = 0; attempt < 125; attempt++)
      expect(
        (await options("127.0.0.1", `198.51.100.${attempt}, 192.0.2.2`)).status,
      ).toBe(attempt < 120 ? 200 : 429);
    expect((await options("127.0.0.1", "192.0.2.1")).status).toBe(200);
    expect((await options("192.0.2.2", "192.0.2.1")).status).toBe(429);
    expect(
      (await options("127.0.0.1", "invalid-prefix, 192.0.2.2")).status,
    ).toBe(429);
  } finally {
    await api.close();
  }
});

it("keeps MCP proof available to another client after an anonymous budget is exhausted", async () => {
  const directory = mkdtempSync(join(tmpdir(), "scratchpad-abuse-"));
  const api = createApi({
    clock: () => Date.parse("2026-10-09T12:00:00Z"),
    databasePath: ":memory:",
    origin: "http://localhost:3000",
  });
  try {
    const key = join(directory, "synthetic-key");
    expect(
      spawnSync("ssh-keygen", ["-t", "ed25519", "-N", "", "-f", key]).status,
    ).toBe(0);
    const publicKey = readFileSync(`${key}.pub`, "utf8").trim();
    await api.store.insert("owner", { id: "owner" });
    await api.store.insert("credential", {
      id: "test-key",
      kind: "ssh",
      publicKey: publicKey.split(/\s+/).slice(0, 2).join(" "),
    });
    const call = async (
      path: string,
      body: unknown,
      client: string,
      forwarded: string,
    ) => {
      const response = await api.handleRequest(
        new Request(`http://localhost:3000/api/v1/auth/mcp/${path}`, {
          method: "POST",
          headers: { "x-forwarded-for": forwarded },
          body: JSON.stringify(body),
        }),
        client,
      );
      return { status: response.status, data: await response.json() };
    };
    for (let count = 0; count < 125; count++) {
      const result = await call(
        "challenge",
        { publicKey },
        "192.0.2.2",
        `198.51.100.${count}`,
      );
      expect(result.status).toBe(count < 120 ? 200 : 429);
    }
    const challenge = await call(
      "challenge",
      { publicKey },
      "192.0.2.1",
      "192.0.2.2",
    );
    expect(challenge.status).toBe(200);
    const signed = spawnSync(
      "ssh-keygen",
      ["-Y", "sign", "-f", key, "-n", "scratchpad-auth"],
      { input: challenge.data.nonce, encoding: "utf8" },
    );
    expect(signed.status).toBe(0);
    const result = await call(
      "verify",
      {
        challengeId: challenge.data.challengeId,
        publicKey,
        signature: signed.stdout,
      },
      "192.0.2.1",
      "192.0.2.2",
    );
    expect(result.status).toBe(200);
    expect(result.data.accessToken).toBeTruthy();
    const proofs = [];
    for (let count = 0; count < 9; count++) {
      const next = await call(
        "challenge",
        { publicKey },
        "192.0.2.1",
        "192.0.2.2",
      );
      const signature = spawnSync(
        "ssh-keygen",
        ["-Y", "sign", "-f", key, "-n", "scratchpad-auth"],
        { input: next.data.nonce, encoding: "utf8" },
      );
      expect(signature.status).toBe(0);
      proofs.push({
        challengeId: next.data.challengeId,
        publicKey,
        signature: signature.stdout,
      });
    }
    const burst = await Promise.all([
      ...proofs
        .slice(0, 8)
        .map((proof) => call("verify", proof, "192.0.2.1", "192.0.2.2")),
      call("verify", proofs[8], "192.0.2.3", "192.0.2.2"),
    ]);
    expect(
      burst.slice(0, 8).filter((entry) => entry.status === 200),
    ).toHaveLength(2);
    expect(
      burst.slice(0, 8).filter((entry) => entry.status === 429),
    ).toHaveLength(6);
    expect(burst[8].status).toBe(200);
    expect(
      (await call("verify", proofs[7], "192.0.2.1", "192.0.2.2")).status,
    ).toBe(200);
    const logout = await api.handleRequest(
      new Request("http://localhost:3000/api/v1/auth/logout", {
        method: "POST",
        headers: { authorization: `Bearer ${result.data.accessToken}` },
      }),
      "192.0.2.2",
    );
    expect(logout.status).toBe(200);
    expect(
      (
        await call(
          "verify",
          {
            challengeId: challenge.data.challengeId,
            publicKey,
            signature: signed.stdout,
          },
          "192.0.2.1",
          "192.0.2.2",
        )
      ).status,
    ).toBe(401);
  } finally {
    await api.close();
    rmSync(directory, { recursive: true, force: true });
  }
});

it.each([
  "::1",
  "0:0:0:0:0:0:0:1",
  "::ffff:127.0.0.1",
  "0:0:0:0:0:ffff:7f00:1",
])("canonicalizes IPv6 proxy peer %s before admission", async (peer) => {
  const api = createApi({
    clock: () => Date.parse("2026-10-09T12:00:00Z"),
    databasePath: ":memory:",
    origin: "http://localhost:3000",
    trustedProxies: ["::1", "127.0.0.1"],
  });
  try {
    await api.store.insert("owner", { id: "owner" });
    const call = (forwarded: string) =>
      api.handleRequest(
        new Request("http://localhost:3000/api/v1/auth/login/options", {
          method: "POST",
          headers: {
            origin: "http://localhost:3000",
            "x-forwarded-for": forwarded,
          },
          body: "{}",
        }),
        peer,
      );
    for (let n = 0; n < 121; n++)
      expect(
        (await call(n % 2 ? "2001:0db8:0:0:0:0:0:1" : "2001:db8::1")).status,
      ).toBe(n < 120 ? 200 : 429);
    expect((await call("2001:db8::2")).status).toBe(200);
  } finally {
    await api.close();
  }
});

it("rejects invalid browser management origins before proof lookup admission", async () => {
  const api = createApi({
    clock: () => Date.parse("2026-10-09T12:00:00Z"),
    databasePath: ":memory:",
    origin: "http://localhost:3000",
  });
  try {
    await api.store.insert("owner", { id: "owner" });
    await api.store.insert("credential", { id: "browser", kind: "webauthn" });
    await api.store.insert("session", {
      id: createHash("sha256").update("synthetic").digest("hex"),
      credentialId: "browser",
      browser: true,
      expiresAt: new Date(Date.now() + 60000).toISOString(),
    });
    const token = await api.auth.createSetupToken(true);
    for (let n = 0; n < 121; n++) {
      const rejected = await api.handleRequest(
        new Request("http://localhost:3000/api/v1/auth/logout", {
          method: "POST",
          headers: {
            origin: "https://evil.example",
            cookie: "scratchpad_session=synthetic",
          },
        }),
        "192.0.2.1",
      );
      expect(rejected.status).toBe(403);
    }
    const accepted = await api.handleRequest(
      new Request("http://localhost:3000/api/v1/auth/register/options", {
        method: "POST",
        headers: { origin: "http://localhost:3000" },
        body: JSON.stringify({ setupToken: token }),
      }),
      "192.0.2.1",
    );
    expect(accepted.status).toBe(200);
    const logout = await api.handleRequest(
      new Request("http://localhost:3000/api/v1/auth/logout", {
        method: "POST",
        headers: {
          origin: "http://localhost:3000",
          cookie: "scratchpad_session=synthetic",
        },
      }),
      "192.0.2.1",
    );
    expect(logout.status).toBe(200);
  } finally {
    await api.close();
  }
});

it("preserves active budgets at allowance capacity and admits clients after expiry", async () => {
  let time = Date.now();
  const api = createApi({
    databasePath: ":memory:",
    origin: "http://localhost:3000",
    clock: () => time,
  });
  try {
    await api.store.insert("owner", { id: "owner" });
    const call = (peer: string) =>
      api.handleRequest(
        new Request("http://localhost:3000/api/v1/auth/login/verify", {
          method: "POST",
          headers: { origin: "http://localhost:3000" },
          body: JSON.stringify({ challengeId: "synthetic-missing" }),
        }),
        peer,
      );
    for (let n = 0; n < 119; n++)
      expect((await call("192.0.2.1")).status).toBe(401);
    for (let n = 0; n < 4095; n++)
      expect(
        (await call(`198.18.${Math.floor(n / 256)}.${n % 256}`)).status,
      ).toBe(401);
    expect((await call("192.0.2.1")).status).toBe(401);
    expect((await call("192.0.2.2")).status).toBe(429);
    expect((await call("192.0.2.1")).status).toBe(429);
    time += 60000;
    expect((await call("192.0.2.2")).status).toBe(401);
    expect((await call("192.0.2.1")).status).toBe(401);
  } finally {
    await api.close();
  }
});
