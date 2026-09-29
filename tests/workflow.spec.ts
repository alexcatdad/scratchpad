import { type ChildProcess, execFileSync, spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import { expect, test } from "@playwright/test";

const root = resolve(import.meta.dirname, "..");
const temporary = mkdtempSync(resolve(tmpdir(), "scratchpad-e2e-"));
const origin = "http://localhost:3100";
const environment = {
  ...process.env,
  PORT: "3100",
  SCRATCHPAD_PUBLIC_URL: origin,
  SCRATCHPAD_DATABASE_PATH: resolve(temporary, "memory.sqlite"),
};
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
      "--env",
      `SCRATCHPAD_PUBLIC_URL=${origin}`,
      "--env",
      "SCRATCHPAD_DATABASE_PATH=/data/scratchpad.sqlite",
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
        (await fetch(`${origin}/ready`, { signal: AbortSignal.timeout(2_000) }))
          .ok
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
  if (!server || server.exitCode !== null) return;
  const child = server;
  const exited = new Promise<void>((done) => child.once("exit", () => done()));
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
  if (!dockerMode) {
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
    "const fs=require('node:fs');if(fs.readdirSync('/data').length)throw new Error('Restore target must be empty');fs.copyFileSync('/source/backup.sqlite','/data/scratchpad.sqlite',fs.constants.COPYFILE_EXCL);fs.chownSync('/data/scratchpad.sqlite',1000,1000);fs.chownSync('/data',1000,1000);fs.chmodSync('/data/scratchpad.sqlite',0o600)",
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
test.beforeAll(start);
test.afterAll(async () => {
  try {
    await stop();
  } finally {
    for (const name of ownedContainers) {
      try {
        command("docker", ["rm", "--force", name]);
      } catch {
        /* Already removed after a failed start. */
      }
    }
    for (const name of ownedVolumes) {
      command("docker", ["volume", "rm", name]);
    }
    rmSync(temporary, { recursive: true, force: true });
  }
});
test("owner enrollment, memory, MCP and restart preserve the real workflow", async ({
  page,
}) => {
  test.setTimeout(dockerMode ? 180_000 : 120_000);
  page.on("pageerror", (error) =>
    console.error("Browser exception:", error.message),
  );
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
  const output = admin("setup");
  const token = output.trim().split("\n").at(-1) ?? "";
  await page.goto(origin);
  await page.getByLabel("Setup or recovery token").fill(token);
  await page
    .getByRole("button", { name: "Register passkey", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Project memory", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Projects", exact: true }).click();
  await page.getByLabel("Project name", { exact: true }).fill("Scratchpad");
  await page
    .getByRole("button", { name: "Create project", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Scratchpad", exact: true }),
  ).toBeVisible();
  await page.getByText("Project settings", { exact: true }).click();
  await page.getByLabel("Allow repository mirroring", { exact: true }).check();
  await page
    .getByLabel("Include in cross-project analysis", { exact: true })
    .uncheck();
  await page
    .getByRole("group", { name: "Record types to mirror", exact: true })
    .getByLabel("Finding", { exact: true })
    .check();
  await page
    .getByRole("group", { name: "Enabled capture types", exact: true })
    .getByLabel("Question & answer", { exact: true })
    .uncheck();
  const settingsSaved = page.waitForResponse(
    (response) =>
      response.request().method() === "PATCH" &&
      response.url().endsWith("/settings"),
  );
  await page
    .getByRole("button", { name: "Save project settings", exact: true })
    .click();
  expect((await settingsSaved).ok()).toBe(true);
  await page.reload();
  await page.getByRole("button", { name: "Projects", exact: true }).click();
  await page.getByText("Project settings", { exact: true }).click();
  await expect(
    page.getByLabel("Allow repository mirroring", { exact: true }),
  ).toBeChecked();
  await expect(
    page.getByLabel("Include in cross-project analysis", { exact: true }),
  ).not.toBeChecked();
  await expect(
    page
      .getByRole("group", { name: "Record types to mirror", exact: true })
      .getByLabel("Finding", { exact: true }),
  ).toBeChecked();
  await expect(
    page
      .getByRole("group", { name: "Enabled capture types", exact: true })
      .getByLabel("Question & answer", { exact: true }),
  ).not.toBeChecked();
  await page.getByRole("button", { name: "Memory", exact: true }).click();
  await page.getByRole("button", { name: "New record", exact: true }).click();
  await expect(
    page.getByLabel("Type", { exact: true }).locator('option[value="qa"]'),
  ).toHaveCount(0);
  await expect(page.getByRole("dialog")).toBeVisible();
  await page.getByLabel("Type", { exact: true }).selectOption("decision");
  await page.getByLabel("Title", { exact: true }).fill("Start with SQLite");
  await page
    .getByLabel("Decision", { exact: true })
    .fill("Use SQLite for the first self-hosted installation.");
  await page
    .getByLabel("Rationale")
    .fill("Keep installation and backup simple.");
  await page.getByRole("button", { name: "Save record", exact: true }).click();
  await page.getByRole("button", { name: /Start with SQLite/ }).click();
  await expect(
    page.getByRole("heading", { name: "Start with SQLite", exact: true }),
  ).toBeVisible();
  await page.screenshot({
    path: "test-results/dashboard-desktop.png",
    fullPage: true,
  });
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(
    page.getByRole("heading", { name: "Start with SQLite", exact: true }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.screenshot({
    path: "test-results/dashboard-mobile.png",
    fullPage: true,
  });
  await page.setViewportSize({ width: 1440, height: 1000 });
  await page.getByText("Edit display title", { exact: true }).click();
  await page
    .getByLabel("Display title", { exact: true })
    .fill("SQLite keeps setup simple");
  await page
    .getByLabel("Tags, separated by commas", { exact: true })
    .fill("storage");
  await page.getByRole("button", { name: "Save title", exact: true }).click();
  await expect(
    page.getByRole("heading", {
      name: "SQLite keeps setup simple",
      exact: true,
    }),
  ).toBeVisible();
  await page.getByText("More filters", { exact: true }).click();
  await page.getByLabel("Tag", { exact: true }).fill("nonexistent-tag");
  await page
    .getByRole("button", { name: "Apply filters", exact: true })
    .click();
  await expect(
    page.getByText("No matching records.", { exact: true }),
  ).toBeVisible();
  await page.getByLabel("Tag", { exact: true }).fill("storage");
  await page
    .getByRole("button", { name: "Apply filters", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: /SQLite keeps setup simple/ }),
  ).toBeVisible();
  await page.getByLabel("Tag", { exact: true }).fill("");
  await page
    .getByRole("button", { name: "Apply filters", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: /SQLite keeps setup simple/ }),
  ).toBeVisible();
  await page.getByRole("button", { name: /SQLite keeps setup simple/ }).click();
  await page.getByText("Add evidence", { exact: true }).click();
  await page
    .getByLabel("Reference", { exact: true })
    .fill("https://example.com/decision-evidence");
  await page
    .getByLabel("Description", { exact: true })
    .fill("Reviewed storage tradeoff");
  await page
    .getByRole("button", { name: "Save evidence", exact: true })
    .click();
  await expect(
    page.getByText("https://example.com/decision-evidence", { exact: true }),
  ).toBeVisible();
  await page.getByText(/Audit trail \(/).click();
  await expect(page.getByText(/Record.curated/)).toBeVisible();
  await page.getByRole("button", { name: "New record", exact: true }).click();
  await page.getByLabel("Type", { exact: true }).selectOption("decision");
  await page
    .getByLabel("Title", { exact: true })
    .fill("Keep SQLite after review");
  await page
    .getByLabel("Decision", { exact: true })
    .fill("The initial storage choice remains appropriate.");
  await page.getByRole("button", { name: "Save record", exact: true }).click();
  await page.getByRole("button", { name: /Keep SQLite after review/ }).click();
  await page.getByText("Link another record", { exact: true }).click();
  await page.getByLabel("Find related record", { exact: true }).fill("SQLite");
  await page.getByRole("button", { name: "Find records", exact: true }).click();
  await page
    .getByLabel("Related record", { exact: true })
    .selectOption({ label: "Start with SQLite" });
  await page
    .getByLabel("Relationship type", { exact: true })
    .selectOption("refines");
  await page
    .getByLabel("Relationship note", { exact: true })
    .fill("Review confirmed the original rationale.");
  await page
    .getByRole("button", { name: "Add relationship", exact: true })
    .click();
  await expect(
    page.getByText("Review confirmed the original rationale.", { exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Open related record", exact: true })
    .click();
  await expect(
    page.getByRole("heading", {
      name: "SQLite keeps setup simple",
      exact: true,
    }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page
    .getByLabel("Import into project", { exact: true })
    .selectOption({ label: "Scratchpad" });
  await page
    .getByLabel("Import JSONL decision log", { exact: true })
    .setInputFiles({
      name: "history.jsonl",
      mimeType: "application/x-ndjson",
      buffer: Buffer.from(
        `${JSON.stringify({
          id: "legacy-paused",
          type: "project_state",
          title: "Paused for hardware validation",
          state: "paused",
          reason: "Awaiting a test device",
          date: "2026-01-15",
        })}\n`,
      ),
    });
  await expect(page.getByRole("status")).toContainText("Imported 1");
  await page.getByRole("button", { name: "Projects", exact: true }).click();
  await page
    .getByRole("button", { name: "Show project context", exact: true })
    .click();
  await expect(
    page.getByRole("button", {
      name: "Paused for hardware validation",
      exact: true,
    }),
  ).toBeVisible();
  await page
    .getByRole("button", {
      name: "Paused for hardware validation",
      exact: true,
    })
    .click();
  await expect(
    page.getByRole("heading", {
      name: "Paused for hardware validation",
      exact: true,
    }),
  ).toBeVisible();
  const key = resolve(temporary, "identity");
  command("ssh-keygen", ["-q", "-t", "ed25519", "-N", "", "-f", key]);
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page
    .getByLabel("Public key", { exact: true })
    .fill(readFileSync(`${key}.pub`, "utf8"));
  const challengeResponse = page.waitForResponse((r) =>
    r.url().endsWith("/auth/credentials/challenge"),
  );
  await page
    .getByRole("button", { name: "Create enrollment challenge", exact: true })
    .click();
  const challenge = await (await challengeResponse).json();
  const signature = execFileSync(
    "ssh-keygen",
    ["-Y", "sign", "-f", key, "-n", challenge.namespace],
    { input: challenge.nonce, encoding: "utf8" },
  );
  await page
    .getByLabel("Armored SSH signature", { exact: true })
    .fill(signature);
  await page
    .getByRole("button", { name: "Verify and enroll key", exact: true })
    .click();
  await expect(
    page.getByText("MCP key enrolled.", { exact: true }),
  ).toBeVisible();
  const projectDirectory = resolve(temporary, "repo");
  command("git", ["init", projectDirectory]);
  command(
    "git",
    [
      "remote",
      "add",
      "origin",
      "https://github.com/example/scratchpad-e2e.git",
    ],
    projectDirectory,
  );
  const binary = resolve(temporary, "scratchpad-mcp");
  command(
    "go",
    ["build", "-o", binary, "./cmd/scratchpad-mcp"],
    resolve(root, "mcp"),
  );
  const args = [
    "run",
    "./integration",
    "-url",
    origin,
    "-public-key",
    `${key}.pub`,
    "-signing-key",
    key,
    "-workspace",
    projectDirectory,
    "-binary",
    binary,
    "-state",
    resolve(temporary, "state.json"),
  ];
  command("go", [...args, "-phase", "capture"], resolve(root, "mcp"));
  await restartOrRestore();
  command("go", [...args, "-phase", "verify"], resolve(root, "mcp"));
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Project memory", exact: true }),
  ).toBeVisible();
  // The restored copy must retain more than raw captures: browser sessions,
  // curated metadata, revisions, evidence, relationships, imports and settings.
  await page.getByRole("button", { name: "Memory", exact: true }).click();
  await page.getByRole("button", { name: /SQLite keeps setup simple/ }).click();
  await expect(
    page.getByRole("heading", {
      name: "SQLite keeps setup simple",
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    page.getByText("https://example.com/decision-evidence", { exact: true }),
  ).toBeVisible();
  await page.getByText(/Audit trail \(/).click();
  await expect(page.getByText(/Record.curated/)).toBeVisible();
  await page.getByRole("button", { name: "Projects", exact: true }).click();
  const restoredProject = page.locator("article").filter({
    has: page.getByRole("heading", { name: "Scratchpad", exact: true }),
  });
  await restoredProject.getByText("Project settings", { exact: true }).click();
  await expect(
    restoredProject.getByLabel("Allow repository mirroring", { exact: true }),
  ).toBeChecked();
  await expect(
    restoredProject.getByLabel("Include in cross-project analysis", {
      exact: true,
    }),
  ).not.toBeChecked();
  await expect(
    restoredProject
      .getByRole("group", { name: "Record types to mirror", exact: true })
      .getByLabel("Finding", { exact: true }),
  ).toBeChecked();
  await restoredProject
    .getByRole("button", { name: "Show project context", exact: true })
    .click();
  await expect(
    restoredProject.getByRole("button", {
      name: "Paused for hardware validation",
      exact: true,
    }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Welcome back.", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Sign in with passkey", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: "Sign out", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Memory", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Project memory", exact: true }),
  ).toBeVisible();
});
