import { execFileSync, spawn } from "node:child_process";
import { mkdtempSync, rmSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import { root } from "./workflow-harness";

function runAsync(
  binary: string,
  args: string[],
  options: Parameters<typeof spawn>[2],
) {
  return new Promise<void>((resolveRun, rejectRun) => {
    const child = spawn(binary, args, options);
    child.on("error", rejectRun);
    child.on("exit", (code) => {
      if (code === 0) resolveRun();
      else
        rejectRun(new Error(`${binary} exited with code ${code ?? "unknown"}`));
    });
  });
}

type Options = {
  environment: NodeJS.ProcessEnv;
  origin: string;
  publicKey: string;
  signingKey: string;
  workspace: string;
  binary: string;
  statePath: string;
  temporary: string;
};

export async function startDurableMcpService(options: Options) {
  const mcpDirectory = resolve(root, "mcp");
  const serviceBinary = resolve(options.temporary, "scratchpad-service");
  execFileSync(
    "go",
    ["build", "-o", serviceBinary, "./cmd/scratchpad-service"],
    {
      cwd: mcpDirectory,
      env: options.environment,
      stdio: "pipe",
    },
  );

  const socketDirectory = mkdtempSync(
    resolve(process.platform === "darwin" ? "/private/tmp" : tmpdir(), "sp-"),
  );
  const socketPath = resolve(socketDirectory, "capture.sock");
  const databasePath = resolve(options.temporary, "capture-queue.sqlite");
  const keyHelper = resolve(options.temporary, "synthetic-queue-key");
  writeFileSync(
    keyHelper,
    "#!/bin/sh\nprintf '01234567890123456789012345678901'\n",
    { mode: 0o700 },
  );
  const service = spawn(
    serviceBinary,
    [
      "--database",
      databasePath,
      "--socket",
      socketPath,
      "--key-helper",
      keyHelper,
      "--key-identifier",
      "synthetic-playwright-queue-key",
    ],
    { env: options.environment, stdio: "ignore" },
  );
  try {
    const deadline = Date.now() + 10_000;
    while (Date.now() < deadline) {
      if (service.exitCode !== null)
        throw new Error("durable MCP service exited during startup");
      try {
        if (statSync(socketPath).isSocket()) break;
      } catch {
        await new Promise((resolve) => setTimeout(resolve, 25));
      }
    }
    if (!statSync(socketPath).isSocket())
      throw new Error("durable MCP service did not bind its private socket");

    const fingerprintOutput = execFileSync(
      "ssh-keygen",
      ["-lf", options.publicKey],
      { encoding: "utf8" },
    );
    const fingerprint = fingerprintOutput.match(/\bSHA256:[A-Za-z0-9+/]+/)?.[0];
    if (!fingerprint) throw new Error("synthetic key fingerprint was absent");
    await runAsync(
      "go",
      [
        "run",
        "./integration",
        "-url",
        options.origin,
        "-public-key",
        options.publicKey,
        "-signing-key",
        options.signingKey,
        "-workspace",
        options.workspace,
        "-binary",
        options.binary,
        "-state",
        options.statePath,
        "-service-socket",
        socketPath,
        "-fingerprint",
        fingerprint,
        "-phase",
        "configure-service",
      ],
      { cwd: mcpDirectory, env: options.environment, stdio: "ignore" },
    );
  } catch (error) {
    service.kill("SIGTERM");
    await new Promise<void>((resolve) => service.once("exit", () => resolve()));
    rmSync(socketDirectory, { recursive: true, force: true });
    throw error;
  }

  return {
    socketPath,
    async stop() {
      if (service.exitCode === null) {
        service.kill("SIGTERM");
        await new Promise<void>((resolve) =>
          service.once("exit", () => resolve()),
        );
      }
      rmSync(socketDirectory, { recursive: true, force: true });
    },
  };
}
