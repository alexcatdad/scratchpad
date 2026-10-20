import { spawn } from "node:child_process";
import { chmodSync, mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const directory = mkdtempSync(join(tmpdir(), "scratchpad-dev-go-"));
chmodSync(directory, 0o700);
const binary = join(directory, "scratchpad-server");
let active;
let deadline;
let stopping = false;
const grouped = process.platform !== "win32";

function signal(child, value) {
  try {
    if (grouped) process.kill(-child.pid, value);
    else child.kill(value);
  } catch (error) {
    if (error.code !== "ESRCH") throw error;
  }
}
function stop() {
  stopping = true;
  if (!active) return;
  signal(active, "SIGTERM");
  deadline ??= setTimeout(() => {
    if (active) signal(active, "SIGKILL");
  }, 20_000);
}
process.once("SIGINT", stop);
process.once("SIGTERM", stop);

async function run(file, args, env = process.env) {
  if (stopping) return 0;
  const child = spawn(file, args, { env, stdio: "inherit", detached: grouped });
  active = child;
  try {
    return await new Promise((done, reject) => {
      child.once("error", reject);
      child.once("close", (code) => done(code ?? 1));
    });
  } finally {
    // The build leader can exit before a descendant that ignored SIGTERM.
    if (stopping && grouped && child.pid) signal(child, "SIGKILL");
    clearTimeout(deadline);
    deadline = undefined;
    active = undefined;
  }
}

try {
  let code = await run("npm", ["run", "build", "-w", "@scratchpad/web"]);
  if (code === 0 && !stopping)
    code = await run("go", [
      "-C",
      "backend",
      "build",
      "-o",
      binary,
      "./cmd/scratchpad-server",
    ]);
  if (code === 0 && !stopping)
    code = await run(
      process.execPath,
      [resolve("scripts/go-dashboard-runtime.mjs"), binary, "serve"],
      {
        ...process.env,
        SCRATCHPAD_LISTEN_ADDR:
          process.env.SCRATCHPAD_LISTEN_ADDR ?? "127.0.0.1:3000",
        SCRATCHPAD_PUBLIC_URL:
          process.env.SCRATCHPAD_PUBLIC_URL ?? "http://localhost:3000",
        SCRATCHPAD_UI_PORT: process.env.SCRATCHPAD_UI_PORT ?? "4000",
      },
    );
  process.exitCode = stopping ? 0 : code;
} finally {
  rmSync(directory, { recursive: true, force: true });
}
