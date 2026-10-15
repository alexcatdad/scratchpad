import { execFileSync } from "node:child_process";
import { randomUUID } from "node:crypto";
import { readFileSync, rmSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { expect, test } from "@playwright/test";
import postgres from "postgres";
import { sshProof } from "../apps/web/src/lib/ssh-proof";
import { startDurableMcpService } from "./durable-mcp-service";
import { createWorkflowHarness, root } from "./workflow-harness";

test("concurrent agents retain captures and retries through restart and report the workload", async ({
  page,
}, testInfo) => {
  test.setTimeout(240_000);
  const agents = Number(process.env.SCRATCHPAD_BASELINE_AGENTS ?? 3);
  const rounds = Number(process.env.SCRATCHPAD_BASELINE_ROUNDS ?? 2);
  const seedRecords = Number(process.env.SCRATCHPAD_BASELINE_RECORDS ?? 20);
  const uniqueCaptures = agents * rounds;
  const harness = createWorkflowHarness();
  const databaseName = `scratchpad_baseline_${randomUUID().replaceAll("-", "")}`;
  let databaseAdmin: ReturnType<typeof postgres> | undefined;
  let databaseCreated = false;
  let stopDurableMcpService: (() => Promise<void>) | undefined;
  try {
    if (harness.postgresMode) {
      databaseAdmin = postgres(harness.environment.SCRATCHPAD_DATABASE_URL, {
        max: 1,
      });
      await databaseAdmin.unsafe(`CREATE DATABASE "${databaseName}"`);
      databaseCreated = true;
      const url = new URL(harness.environment.SCRATCHPAD_DATABASE_URL);
      url.pathname = `/${databaseName}`;
      harness.environment.SCRATCHPAD_DATABASE_URL = url.toString();
    }
    await harness.start();
    const cdp = await page.context().newCDPSession(page);
    await cdp.send("WebAuthn.enable");
    await cdp.send("WebAuthn.addVirtualAuthenticator", {
      options: {
        protocol: "ctap2",
        transport: "internal",
        hasResidentKey: true,
        hasUserVerification: true,
        isUserVerified: true,
        automaticPresenceSimulation: true,
      },
    });
    await page.goto(harness.origin);
    await page
      .getByLabel("Setup or recovery token")
      .fill(harness.admin("setup").trim().split("\n").at(-1) ?? "");
    await page
      .getByRole("button", { name: "Register passkey", exact: true })
      .click();
    await expect(
      page.getByRole("heading", { name: "Project memory", exact: true }),
    ).toBeVisible();

    const key = resolve(harness.temporary, "identity");
    harness.command("ssh-keygen", ["-q", "-t", "ed25519", "-N", "", "-f", key]);
    const publicKey = readFileSync(`${key}.pub`, "utf8");
    const challenge = await page.evaluate(async (publicKey) => {
      const response = await fetch("/api/v1/auth/credentials/challenge", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ publicKey }),
      });
      if (!response.ok) throw new Error("SSH enrollment challenge failed");
      return response.json();
    }, publicKey);
    const signature = execFileSync(
      "ssh-keygen",
      ["-Y", "sign", "-f", key, "-n", challenge.namespace],
      {
        input: sshProof(challenge, publicKey, harness.origin, "ssh_enroll"),
        encoding: "utf8",
      },
    );
    expect(
      await page.evaluate(
        async ({ publicKey, signature, challengeId }) => {
          const response = await fetch("/api/v1/auth/credentials/verify", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify({ publicKey, signature, challengeId }),
          });
          return response.status;
        },
        { publicKey, signature, challengeId: challenge.challengeId },
      ),
    ).toBe(200);
    const workspace = resolve(harness.temporary, "repo");
    harness.command("git", ["init", workspace]);
    harness.command(
      "git",
      [
        "remote",
        "add",
        "origin",
        "https://github.com/example/synthetic-load.git",
      ],
      workspace,
    );
    const binary = resolve(harness.temporary, "scratchpad-mcp");
    harness.command(
      "go",
      ["build", "-o", binary, "./cmd/scratchpad-mcp"],
      resolve(root, "mcp"),
    );
    const workloadBinary = resolve(harness.temporary, "scratchpad-benchmark");
    harness.command(
      "go",
      ["build", "-o", workloadBinary, "./benchmark"],
      resolve(root, "mcp"),
    );
    const durableService = await startDurableMcpService({
      environment: harness.environment,
      origin: harness.origin,
      publicKey: `${key}.pub`,
      signingKey: key,
      workspace,
      binary,
      statePath: resolve(harness.temporary, "benchmark-state.json"),
      temporary: harness.temporary,
    });
    stopDurableMcpService = durableService.stop;
    let workloadOutput: string;
    let workloadFailure: unknown;
    try {
      workloadOutput = execFileSync(
        workloadBinary,
        [
          "-url",
          harness.origin,
          "-public-key",
          `${key}.pub`,
          "-signing-key",
          key,
          "-workspace",
          workspace,
          "-binary",
          binary,
          "-service-socket",
          durableService.socketPath,
          "-agents",
          String(agents),
          "-rounds",
          String(rounds),
          "-seed-records",
          String(seedRecords),
          "-source-revision",
          harness.command("git", ["rev-parse", "HEAD"]).trim(),
          "-timeout",
          "150s",
        ],
        {
          env: harness.environment,
          encoding: "utf8",
          timeout: 180_000,
        },
      );
    } catch (error) {
      if (
        !error ||
        typeof error !== "object" ||
        !("stdout" in error) ||
        typeof error.stdout !== "string" ||
        !error.stdout.trim()
      )
        throw error;
      workloadOutput = error.stdout;
      workloadFailure = error;
    }
    const report = JSON.parse(workloadOutput);
    const reportPath = testInfo.outputPath("concurrent-agent-baseline.json");
    writeFileSync(reportPath, `${JSON.stringify(report, null, 2)}\n`);
    await testInfo.attach("concurrent-agent-baseline", {
      path: reportPath,
      contentType: "application/json",
    });
    expect(report.errors, JSON.stringify(report.operations)).toBe(0);
    if (workloadFailure) throw workloadFailure;
    expect(report).toMatchObject({
      schemaVersion: 1,
      database: harness.postgresMode ? "postgres" : "sqlite",
      workload: {
        agents,
        rounds,
        seedRecords,
        uniqueCaptures,
        operationsPerRound: ["capture", "retry", "detail", "search", "context"],
      },
      errors: 0,
    });
    for (const operation of report.workload.operationsPerRound) {
      expect(report.operations[operation]).toMatchObject({
        count: uniqueCaptures,
        errors: 0,
      });
      expect(report.operations[operation].responseBytes.total).toBeGreaterThan(
        0,
      );
      expect(report.operations[operation].latencyMs.min).toBeGreaterThan(0);
      expect(report.operations[operation].latencyMs.p50).toBeLessThanOrEqual(
        report.operations[operation].latencyMs.p95,
      );
      expect(report.operations[operation].latencyMs.p95).toBeLessThanOrEqual(
        report.operations[operation].latencyMs.p99,
      );
    }
    const before = await page.evaluate(async () => {
      const records: { id: string }[] = [];
      let cursor: string | null = null;
      do {
        const response: Response = await fetch(
          `/api/v1/records?limit=100${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`,
        );
        if (!response.ok) throw new Error("Capture retrieval failed");
        const data: { records: { id: string }[]; nextCursor: string | null } =
          await response.json();
        records.push(...data.records);
        cursor = data.nextCursor;
      } while (cursor);
      return records;
    });
    expect(before).toHaveLength(seedRecords + uniqueCaptures);
    expect(new Set(before.map((record) => record.id)).size).toBe(
      seedRecords + uniqueCaptures,
    );
    await harness.restartOrRestore();
    const after = await page.evaluate(async () => {
      const records: { id: string }[] = [];
      let cursor: string | null = null;
      do {
        const response: Response = await fetch(
          `/api/v1/records?limit=100${cursor ? `&cursor=${encodeURIComponent(cursor)}` : ""}`,
        );
        if (!response.ok) throw new Error("Retrieval after restart failed");
        const data: { records: { id: string }[]; nextCursor: string | null } =
          await response.json();
        records.push(...data.records);
        cursor = data.nextCursor;
      } while (cursor);
      return records;
    });
    expect(after).toEqual(before);
  } finally {
    try {
      await stopDurableMcpService?.();
      await harness.stop();
      harness.cleanupDocker();
      rmSync(harness.temporary, { recursive: true, force: true });
    } finally {
      try {
        if (databaseCreated)
          await databaseAdmin?.unsafe(
            `DROP DATABASE "${databaseName}" WITH (FORCE)`,
          );
      } finally {
        await databaseAdmin?.end();
      }
    }
  }
});
