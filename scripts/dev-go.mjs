import { spawn } from "node:child_process";
import { chmodSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const directory = mkdtempSync(join(tmpdir(), "scratchpad-dev-go-"));
chmodSync(directory, 0o700);
const binary = join(directory, "scratchpad-server");
let child;
let stopping = false;

function run(file, args, options = {}) {
  return new Promise((done, reject) => {
    const process = spawn(file, args, { stdio: "inherit", ...options });
    process.once("error", reject);
    process.once("close", (code) => done(code ?? 1));
  });
}

async function cleanup() {
  if (!child) return;
  if (child.exitCode !== null || child.signalCode) return;
  if (child.exitCode === null && !child.signalCode) child.kill("SIGTERM");
  let timer;
  try {
    await Promise.race([
      new Promise((done) => child.once("close", done)),
      new Promise((done) => {
        timer = setTimeout(() => {
          if (child.exitCode === null && !child.signalCode)
            child.kill("SIGKILL");
          done();
        }, 20_000);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}

function stop() {
  if (stopping) return;
  stopping = true;
  void cleanup();
}

process.once("SIGINT", stop);
process.once("SIGTERM", stop);

try {
  const buildCode = await run("npm", ["run", "build", "-w", "@scratchpad/web"]);
  if (buildCode !== 0) process.exitCode = buildCode;
  else {
    const goCode = await run("go", [
      "-C",
      "backend",
      "build",
      "-o",
      binary,
      "./cmd/scratchpad-server",
    ]);
    if (goCode !== 0) process.exitCode = goCode;
    else {
      const env = {
        ...process.env,
        SCRATCHPAD_LISTEN_ADDR:
          process.env.SCRATCHPAD_LISTEN_ADDR ?? "127.0.0.1:3000",
        SCRATCHPAD_PUBLIC_URL:
          process.env.SCRATCHPAD_PUBLIC_URL ?? "http://localhost:3000",
        SCRATCHPAD_DASHBOARD_URL:
          process.env.SCRATCHPAD_DASHBOARD_URL ?? "http://127.0.0.1:4000",
        SCRATCHPAD_UI_PORT: process.env.SCRATCHPAD_UI_PORT ?? "4000",
      };
      child = spawn(
        process.execPath,
        [resolve("scripts/go-dashboard-runtime.mjs"), binary, "serve"],
        { env, stdio: "inherit" },
      );
      const code = await new Promise((done, reject) => {
        child.once("error", reject);
        child.once("close", done);
      });
      process.exitCode = stopping ? 0 : (code ?? 1);
    }
  }
} finally {
  await cleanup();
  rmSync(directory, { recursive: true, force: true });
}
