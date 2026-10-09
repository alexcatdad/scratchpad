import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import type { ServerRequest } from "srvx";
import { serve } from "srvx/node";
import { expect, it } from "vitest";
import { createApi } from "./api";

it("uses actual srvx socket metadata for configured proxy clients", async () => {
  const api = createApi({
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
    const burst = await Promise.all(
      proofs.map((proof) => call("verify", proof, "192.0.2.1", "192.0.2.2")),
    );
    expect(burst.filter((entry) => entry.status === 200)).toHaveLength(8);
    expect(burst.filter((entry) => entry.status === 429)).toHaveLength(1);
    expect(
      (await call("verify", proofs[8], "192.0.2.1", "192.0.2.2")).status,
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
