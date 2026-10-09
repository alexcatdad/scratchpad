import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { relative, resolve } from "node:path";
import test from "node:test";
import { createServer } from "vite";

const root = resolve(import.meta.dirname, "..");
const web = resolve(root, "apps/web");

for (const host of [undefined, "127.0.0.1", "0.0.0.0"]) {
  test(`development protects private state with host ${host ?? "default"}`, async () => {
    const directory = mkdtempSync(resolve(web, "security-test-"));
    mkdirSync(resolve(directory, "data"));
    const database = resolve(directory, "custom[state]");
    const before = { ...process.env };
    process.env.SCRATCHPAD_DATABASE_PATH = database;
    delete process.env.SCRATCHPAD_DATABASE_URL;
    delete process.env.SCRATCHPAD_GITHUB_CLIENT_ID;
    delete process.env.SCRATCHPAD_GITHUB_CLIENT_SECRET;
    const files = [
      "custom[state]",
      "custom[state]-wal",
      "custom[state]-shm",
      "memory.sqlite",
      "memory.sqlite-wal",
      "memory.sqlite-shm",
      "memory.db",
      "memory.db-wal",
      "memory.db-shm",
      "data/arbitrary-state",
      ".env",
      ".env.local",
      "private.pem",
      "private.crt",
      "private.key",
      "private.p12",
      ".npmrc",
      ".yarnrc.yml",
    ];
    for (const file of files)
      writeFileSync(resolve(directory, file), "synthetic private state");
    let server;
    try {
      server = await createServer({
      root: web,
      configFile: resolve(web, "vite.config.ts"),
      logLevel: "silent",
      server: { port: 0, ...(host ? { host } : {}) },
      });
      if (!host) assert.equal(server.config.server.host, "127.0.0.1");
      await server.listen();
      const address = server.httpServer.address();
      assert.ok(address && typeof address !== "string");
      if (!host) assert.equal(address.address, "127.0.0.1");
      const origin = `http://127.0.0.1:${address.port}`;
      assert.equal((await fetch(`${origin}/@vite/client`)).status, 200);
      for (const file of files) {
        const path = resolve(directory, file);
        for (const route of [
          `/${relative(web, path).split("\\").join("/")}`,
          `/@fs/${path.split("\\").join("/")}`,
          `/@fs/${path.split("\\").join("/")}?raw`,
        ]) {
          const response = await fetch(`${origin}${route}`);
          assert.equal(response.status, 403, route);
          assert.ok(
            !(await response.text()).includes("synthetic private state"),
          );
        }
      }
    } finally {
      await server?.close();
      for (const key of Object.keys(process.env))
        if (!(key in before)) delete process.env[key];
      Object.assign(process.env, before);
      rmSync(directory, { recursive: true, force: true });
    }
  });
}
