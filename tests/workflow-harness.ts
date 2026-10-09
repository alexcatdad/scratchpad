import { type ChildProcess, execFileSync, spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
export const root = resolve(import.meta.dirname, "..");
export function createWorkflowHarness() {
  const temporary = mkdtempSync(resolve(tmpdir(), "scratchpad-e2e-"));
  const origin = "http://localhost:3100";
  const environment = {
    ...process.env,
    PORT: "3100",
    SCRATCHPAD_PUBLIC_URL: origin,
    SCRATCHPAD_DATABASE_PATH: resolve(temporary, "memory.sqlite"),
    SCRATCHPAD_DATABASE_URL: process.env.SCRATCHPAD_E2E_DATABASE_URL ?? "",
  };
  const postgresMode = Boolean(environment.SCRATCHPAD_DATABASE_URL);
  const dockerMode = process.env.SCRATCHPAD_E2E_DOCKER === "1";
  const dockerImage = process.env.SCRATCHPAD_E2E_IMAGE ?? "scratchpad:ci";
  const runId = `scratchpad-e2e-${randomUUID()}`;
  const originalVolume = `${runId}-data`;
  const restoredVolume = `${runId}-restored`;
  const ownedContainers = new Set<string>();
  const ownedVolumes = new Set<string>();
  let activeVolume = originalVolume;
  let activeContainer: string | undefined;
  let server: ChildProcess | undefined;
  let logs = "";
  function createVolume(name: string) {
    command("docker", ["volume", "create", name]);
    ownedVolumes.add(name);
  }
  async function start() {
    if (dockerMode) {
      if (!ownedVolumes.has(activeVolume)) createVolume(activeVolume);
      activeContainer = `${runId}-${activeVolume === originalVolume ? "original" : "restored"}`;
      ownedContainers.add(activeContainer);
      command("docker", [
        "run",
        "--detach",
        "--name",
        activeContainer,
        "--publish",
        "127.0.0.1:3100:3000",
        "--add-host",
        "host.docker.internal:host-gateway",
        "--env",
        `SCRATCHPAD_PUBLIC_URL=${origin}`,
        "--env",
        "SCRATCHPAD_DATABASE_PATH=/data/scratchpad.sqlite",
        ...(postgresMode
          ? [
              "--env",
              `SCRATCHPAD_DATABASE_URL=${environment.SCRATCHPAD_DATABASE_URL}`,
            ]
          : []),
        "--volume",
        `${activeVolume}:/data`,
        dockerImage,
      ]);
    } else {
      server = spawn(
        process.execPath,
        [
          resolve(root, "node_modules/srvx/bin/srvx.mjs"),
          "--prod",
          "--entry",
          "dist/server/server.js",
          "--static",
          "../client",
        ],
        {
          cwd: resolve(root, "apps/web"),
          env: environment,
          stdio: ["ignore", "pipe", "pipe"],
        },
      );
      server.stdout?.on("data", (data) => {
        logs += data;
      });
      server.stderr?.on("data", (data) => {
        logs += data;
      });
    }
    const deadline = Date.now() + 30_000;
    while (Date.now() < deadline) {
      try {
        if (
          (
            await fetch(`${origin}/ready`, {
              signal: AbortSignal.timeout(2_000),
            })
          ).ok
        )
          return;
      } catch {
        /* Server may still be starting. */
      }
      await new Promise((r) => setTimeout(r, 100));
    }
    if (dockerMode && activeContainer)
      logs += command("docker", ["logs", activeContainer]);
    throw new Error(`Server did not start: ${logs}`);
  }
  async function stop() {
    if (dockerMode) {
      if (activeContainer) {
        command("docker", ["stop", "--time", "10", activeContainer]);
        command("docker", ["rm", activeContainer]);
        ownedContainers.delete(activeContainer);
        activeContainer = undefined;
      }
      return;
    }
    const child = server;
    server = undefined;
    if (!child || child.exitCode !== null || child.signalCode !== null) return;
    const exited = new Promise<void>((done) =>
      child.once("exit", () => done()),
    );
    child.kill("SIGTERM");
    const timer = setTimeout(() => child.kill("SIGKILL"), 5_000);
    try {
      await exited;
    } finally {
      clearTimeout(timer);
    }
  }
  function admin(...args: string[]) {
    if (dockerMode) {
      if (!activeContainer) throw new Error("Container is not running");
      return command("docker", [
        "exec",
        activeContainer,
        "npm",
        "run",
        "admin",
        "--",
        ...args,
      ]);
    }
    return command(
      process.execPath,
      [
        resolve(root, "node_modules/tsx/dist/cli.mjs"),
        "src/server/admin.ts",
        ...args,
      ],
      resolve(root, "apps/web"),
    );
  }
  async function restartOrRestore() {
    if (!dockerMode || postgresMode) {
      await stop();
      await start();
      return;
    }
    admin("backup", "/data/backup.sqlite");
    await stop();
    createVolume(restoredVolume);
    command("docker", [
      "run",
      "--rm",
      "--user",
      "0:0",
      "--entrypoint",
      "node",
      "--volume",
      `${originalVolume}:/source:ro`,
      "--volume",
      `${restoredVolume}:/data`,
      dockerImage,
      "-e",
      "const fs=require('node:fs');if(fs.readdirSync('/data').length)throw new Error('Restore target must be empty');fs.copyFileSync('/source/backup.sqlite','/data/scratchpad.sqlite',fs.constants.COPYFILE_EXCL);fs.chownSync('/data/scratchpad.sqlite',1000,1000);fs.chownSync('/data',1000,1000);fs.chmodSync('/data',0o700);fs.chmodSync('/data/scratchpad.sqlite',0o600)",
    ]);
    activeVolume = restoredVolume;
    await start();
  }
  function command(binary: string, args: string[], cwd = root) {
    return execFileSync(binary, args, {
      cwd,
      env: environment,
      encoding: "utf8",
      timeout: 60_000,
    });
  }

  function cleanupDocker() {
    for (const name of ownedContainers) {
      try {
        command("docker", ["rm", "--force", name]);
        ownedContainers.delete(name);
      } catch {}
    }
    for (const name of ownedVolumes) {
      command("docker", ["volume", "rm", name]);
      ownedVolumes.delete(name);
    }
  }

  return {
    temporary,
    origin,
    environment,
    postgresMode,
    dockerMode,
    start,
    stop,
    admin,
    restartOrRestore,
    command,
    cleanupDocker,
  };
}
