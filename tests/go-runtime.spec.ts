import { execFileSync, spawn } from "node:child_process";
import { chmodSync, mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
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

test("combined Go runtime forwards CLI commands and stops both HTTP processes", async () => {
  const dir = mkdtempSync(join(tmpdir(), "scratchpad-go-runtime-"));
  chmodSync(dir, 0o700);
  let child: ReturnType<typeof spawn> | undefined;
  let stopped = Promise.resolve<number | null>(null);
  try {
    const binary = join(dir, "server");
    execFileSync(
      "go",
      ["build", "-race", "-o", binary, "./cmd/scratchpad-server"],
      { cwd: "backend" },
    );
    const apiPort = await port(),
      uiPort = await port();
    const env = {
      ...process.env,
      SCRATCHPAD_DATABASE_URL: "",
      SCRATCHPAD_DATABASE_PATH: join(dir, "memory.sqlite"),
      SCRATCHPAD_PUBLIC_URL: `http://localhost:${apiPort}`,
      SCRATCHPAD_LISTEN_ADDR: `127.0.0.1:${apiPort}`,
      SCRATCHPAD_UI_PORT: String(uiPort),
      SCRATCHPAD_GITHUB_CLIENT_ID: "",
      SCRATCHPAD_GITHUB_CLIENT_SECRET: "",
    };
    const launcher = resolve("scripts/go-dashboard-runtime.mjs");
    const report = execFileSync(
      process.execPath,
      [launcher, binary, "migrate"],
      { env, encoding: "utf8" },
    );
    expect(JSON.parse(report)).toMatchObject({
      database: "sqlite",
      schemaVersion: 3,
    });
    child = spawn(process.execPath, [launcher, binary, "serve"], {
      env,
      stdio: "ignore",
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
    await expect
      .poll(() => status(`http://127.0.0.1:${apiPort}/ready`))
      .toBe(200);
    await expect.poll(() => status(`http://127.0.0.1:${uiPort}`)).toBe(200);
    expect(await (await fetch(`http://127.0.0.1:${apiPort}`)).text()).toContain(
      "Scratchpad",
    );
    child.kill("SIGTERM");
    expect(await stopped).toBe(0);
    await expect
      .poll(() => status(`http://127.0.0.1:${apiPort}/ready`))
      .toBe(0);
    await expect.poll(() => status(`http://127.0.0.1:${uiPort}`)).toBe(0);
    const occupied = createServer((socket) => socket.end());
    await new Promise<void>((done) =>
      occupied.listen(uiPort, "127.0.0.1", done),
    );
    try {
      child = spawn(process.execPath, [launcher, binary, "serve"], {
        env,
        stdio: "ignore",
      });
      stopped = new Promise((done, fail) => {
        child?.once("error", fail);
        child?.once("exit", (code) => done(code));
      });
      expect(await stopped).toBe(1);
      await expect
        .poll(() => status(`http://127.0.0.1:${apiPort}/ready`))
        .toBe(0);
    } finally {
      await new Promise<void>((done) => occupied.close(() => done()));
    }
  } finally {
    if (child && child.exitCode === null) child.kill("SIGTERM");
    await stopped;
    rmSync(dir, { recursive: true, force: true });
  }
});
