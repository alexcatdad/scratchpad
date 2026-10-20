import { type ChildProcess, execFileSync, spawn } from "node:child_process";
import { existsSync } from "node:fs";
import { resolve } from "node:path";

export function buildGoServer(
  root: string,
  destination: string,
  env: NodeJS.ProcessEnv,
) {
  if (!existsSync(destination))
    execFileSync(
      "go",
      ["build", "-o", destination, "./cmd/scratchpad-server"],
      {
        cwd: resolve(root, "backend"),
        env,
        timeout: 60_000,
      },
    );
  return destination;
}

export function startGoServer(
  root: string,
  binary: string,
  env: NodeJS.ProcessEnv,
  onOutput: (chunk: Buffer) => void = () => {},
) {
  const child = spawn(
    process.execPath,
    [resolve(root, "scripts/go-dashboard-runtime.mjs"), binary, "serve"],
    { env, stdio: ["ignore", "pipe", "pipe"] },
  );
  child.stdout?.on("data", onOutput);
  child.stderr?.on("data", onOutput);
  return child;
}

export async function stopGoServer(child: ChildProcess | undefined) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  const exited = new Promise<void>((done) => child.once("exit", () => done()));
  child.kill("SIGTERM");
  const timer = setTimeout(() => child.kill("SIGKILL"), 20_000);
  try {
    await exited;
  } finally {
    clearTimeout(timer);
  }
}

export function runGoAdmin(
  binary: string,
  args: string[],
  env: NodeJS.ProcessEnv,
  cwd: string,
) {
  return execFileSync(binary, args, {
    cwd,
    env,
    encoding: "utf8",
    timeout: 60_000,
  });
}
