import { execFileSync, spawn } from "node:child_process";
import { chmodSync, mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "@playwright/test";

async function port(): Promise<number> {
  return await new Promise((done) => {
    const listener = createServer();
    listener.listen(0, "127.0.0.1", () => {
      const address = listener.address();
      if (!address || typeof address === "string")
        throw new Error("Missing port");
      listener.close(() => done(address.port));
    });
  });
}

test("the Go binary alone runs CLI commands and serves the embedded dashboard", async () => {
  const dir = mkdtempSync(join(tmpdir(), "scratchpad-go-runtime-"));
  chmodSync(dir, 0o700);
  let child: ReturnType<typeof spawn> | undefined;
  let stopped = Promise.resolve<number | null>(null);
  let logs = "";
  try {
    const binary = join(dir, "server");
    execFileSync(
      "go",
      ["build", "-race", "-o", binary, "./cmd/scratchpad-server"],
      { cwd: "backend" },
    );
    const apiPort = await port();
    const origin = `http://127.0.0.1:${apiPort}`;
    const env = {
      ...process.env,
      SCRATCHPAD_DATABASE_URL: "",
      SCRATCHPAD_DATABASE_PATH: join(dir, "memory.sqlite"),
      SCRATCHPAD_PUBLIC_URL: `http://localhost:${apiPort}`,
      SCRATCHPAD_LISTEN_ADDR: `127.0.0.1:${apiPort}`,
      SCRATCHPAD_DASHBOARD_URL: "",
      SCRATCHPAD_GITHUB_CLIENT_ID: "",
      SCRATCHPAD_GITHUB_CLIENT_SECRET: "",
    };
    const report = execFileSync(binary, ["migrate"], { env, encoding: "utf8" });
    expect(JSON.parse(report)).toMatchObject({
      database: "sqlite",
      schemaVersion: 3,
    });
    child = spawn(binary, ["serve"], {
      env,
      stdio: ["ignore", "ignore", "pipe"],
    });
    child.stderr?.on("data", (chunk) => {
      logs += chunk.toString();
    });
    stopped = new Promise((done, fail) => {
      child?.once("error", fail);
      child?.once("exit", (code) => done(code));
    });
    const status = async (url: string) => {
      try {
        return (await fetch(url)).status;
      } catch {
        return 0;
      }
    };
    await expect.poll(() => status(`${origin}/ready`)).toBe(200);

    // The query string is dashboard state: it never changes the document and
    // never reaches the access log.
    const index = await fetch(
      `${origin}/?view=Memory&q=synthetic-private-marker&authError=denied`,
    );
    const html = await index.text();
    expect(index.status).toBe(200);
    expect(index.headers.get("content-type")).toBe("text/html; charset=utf-8");
    expect(index.headers.get("cache-control")).toBe("no-cache");
    expect(index.headers.get("x-content-type-options")).toBe("nosniff");
    expect(html).toContain("<title>Scratchpad — Project memory</title>");
    expect(html).toContain('<div id="root"></div>');

    const assets = [...html.matchAll(/(?:src|href)="(\/assets\/[^"]+)"/g)].map(
      (match) => match[1],
    );
    expect(assets.some((path) => path.endsWith(".js"))).toBe(true);
    expect(assets.some((path) => path.endsWith(".css"))).toBe(true);
    for (const path of assets) {
      const asset = await fetch(`${origin}${path}`);
      expect(asset.status).toBe(200);
      expect(asset.headers.get("content-type")).toBe(
        path.endsWith(".js")
          ? "text/javascript; charset=utf-8"
          : "text/css; charset=utf-8",
      );
      expect(asset.headers.get("cache-control")).toBe(
        "public, max-age=31536000, immutable",
      );
      expect(asset.headers.get("x-content-type-options")).toBe("nosniff");
      expect((await asset.arrayBuffer()).byteLength).toBeGreaterThan(0);
      const head = await fetch(`${origin}${path}`, { method: "HEAD" });
      expect(head.status).toBe(200);
      expect(await head.text()).toBe("");
    }

    // Only "/" is the dashboard; nothing else falls back to index.html.
    expect(await status(`${origin}/assets/missing.js`)).toBe(404);
    expect(await status(`${origin}/not-a-page`)).toBe(404);
    expect(await status(`${origin}/api/v1/not-a-real-route`)).toBe(404);
    expect((await fetch(origin, { method: "POST" })).status).toBe(404);
    expect((await (await fetch(`${origin}/health`)).json()).status).toBe("ok");

    child.kill("SIGTERM");
    expect(await stopped).toBe(0);
    await expect.poll(() => status(`${origin}/ready`)).toBe(0);
    expect(logs).toContain('"path":"/"');
    expect(logs).not.toContain("synthetic-private-marker");
    expect(logs).not.toContain("authError");
  } finally {
    if (child && child.exitCode === null) child.kill("SIGTERM");
    await stopped;
    rmSync(dir, { recursive: true, force: true });
  }
});
