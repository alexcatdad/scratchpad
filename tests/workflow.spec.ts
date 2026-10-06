import { type ChildProcess, execFileSync, spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createServer } from "node:http";
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
  test.setTimeout(240_000);
  if (postgresMode)
    await expect(
      (await fetch(`${origin}/ready`)).json(),
    ).resolves.toMatchObject({
      database: "postgres",
    });
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
  await page.getByRole("dialog").press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "Project settings", exact: true }),
  ).toBeFocused();
  await page.getByRole("button", { name: "Edit project", exact: true }).click();
  await expect(
    page
      .getByRole("dialog")
      .getByLabel("Project display name", { exact: true }),
  ).toHaveValue("Scratchpad");
  await page
    .getByRole("dialog")
    .getByLabel("Project display name", { exact: true })
    .fill("Unsaved name");
  await page
    .getByRole("dialog")
    .getByRole("button", { name: "Close", exact: true })
    .click();
  await page.getByRole("button", { name: "Edit project", exact: true }).click();
  await expect(
    page
      .getByRole("dialog")
      .getByLabel("Project display name", { exact: true }),
  ).toHaveValue("Scratchpad");
  await page.setViewportSize({ width: 390, height: 844 });
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  await page.getByRole("dialog").press("Escape");
  await page.setViewportSize({ width: 1440, height: 1000 });
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
  await page
    .getByRole("button", { name: "Back to results", exact: true })
    .click();
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
    .getByRole("button", { name: "Import & export", exact: true })
    .click();
  await page
    .getByLabel("Import into project", { exact: true })
    .selectOption({ label: "Scratchpad" });
  await page
    .getByLabel("Import JSONL decision log", { exact: true })
    .setInputFiles({
      name: "synthetic-paused-project.jsonl",
      mimeType: "application/x-ndjson",
      buffer: readFileSync(
        resolve(root, "tests/fixtures/synthetic-paused-project.jsonl"),
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
  await expect(
    page
      .locator("section")
      .filter({
        has: page.getByRole("heading", { name: "Reason", exact: true }),
      })
      .getByText("Awaiting a test device", { exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Projects", exact: true }).click();
  await page
    .getByLabel("Project name", { exact: true })
    .fill("UX second project");
  await page
    .getByRole("button", { name: "Create project", exact: true })
    .click();
  const second = page.locator("article").filter({
    has: page.getByRole("heading", {
      name: "UX second project",
      exact: true,
    }),
  });
  await second
    .getByRole("button", { name: "Open memory", exact: true })
    .click();
  await expect(page.getByLabel("Project", { exact: true })).not.toHaveValue("");
  const projectScope = await page
    .getByLabel("Project", { exact: true })
    .inputValue();
  await expect(page).toHaveURL(/projectId=/);
  await page.reload();
  await expect(page.getByLabel("Project", { exact: true })).toHaveValue(
    projectScope,
  );
  await page.getByRole("button", { name: "New record", exact: true }).click();
  await expect(
    page.getByRole("dialog").getByLabel("Project", { exact: true }),
  ).toHaveValue(projectScope);
  await expect
    .poll(() =>
      page.evaluate(() =>
        document.querySelector("dialog")?.contains(document.activeElement),
      ),
    )
    .toBe(true);
  await page.keyboard.press("Shift+Tab");
  await expect
    .poll(() =>
      page.evaluate(() =>
        document.querySelector("dialog")?.contains(document.activeElement),
      ),
    )
    .toBe(true);
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(
    page.getByRole("button", { name: "New record", exact: true }),
  ).toBeFocused();
  const longRecord = await page.evaluate(async (projectId) => {
    for (let index = 0; index < 31; index++) {
      const response = await fetch("/api/v1/records", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          projectId,
          record: {
            type: "finding",
            title: `UX fixture ${index}`,
            authority: "observed",
            payload: {
              finding:
                "## Readable heading\n\n- First point\n- Second point\n\n| Check | Result |\n| --- | --- |\n| Reading | Works |",
              limitations: ["One limitation", "Another limitation"],
            },
          },
        }),
      });
      if (!response.ok) throw new Error("Could not seed UX fixture");
    }
    return true;
  }, projectScope);
  expect(longRecord).toBe(true);
  await page.reload();
  await page.setViewportSize({ width: 390, height: 844 });
  await page.getByRole("button", { name: /UX fixture 30/ }).click();
  await expect(page).toHaveURL(/recordId=/);
  await expect(
    page.getByRole("heading", { name: "UX fixture 30", exact: true }),
  ).toBeInViewport();
  await expect(
    page.getByRole("heading", { name: "Readable heading", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("cell", { name: "Works", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("listitem").filter({ hasText: "One limitation" }),
  ).toBeVisible();
  expect(
    await page.evaluate(
      () => document.documentElement.scrollWidth <= innerWidth,
    ),
  ).toBe(true);
  const recordUrl = page.url();
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "UX fixture 30", exact: true }),
  ).toBeInViewport();
  await page
    .getByRole("button", { name: "Back to results", exact: true })
    .click();
  await expect(page.getByLabel("Project", { exact: true })).toHaveValue(
    projectScope,
  );
  await page.goBack();
  await expect(page).toHaveURL(recordUrl);
  await expect(
    page.getByRole("heading", { name: "UX fixture 30", exact: true }),
  ).toBeInViewport();
  await page.goForward();
  await page
    .getByLabel("Search your memory", { exact: true })
    .fill("zzzz-no-match");
  await page.getByRole("button", { name: "Search", exact: true }).click();
  await expect(
    page.getByText("No matching records.", { exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Clear filters", exact: true })
    .click();
  await expect(page.getByLabel("Project", { exact: true })).toHaveValue("");
  await expect(
    page.getByLabel("Search your memory", { exact: true }),
  ).toHaveValue("");
  await page.setViewportSize({ width: 1440, height: 1000 });

  const key = resolve(temporary, "identity");
  command("ssh-keygen", ["-q", "-t", "ed25519", "-N", "", "-f", key]);
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("button", { name: "Access & MCP", exact: true }).click();
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
  const suppliedBinary = process.env.SCRATCHPAD_E2E_MCP_BINARY;
  const binary = suppliedBinary
    ? resolve(root, suppliedBinary)
    : resolve(temporary, "scratchpad-mcp");
  if (!suppliedBinary) {
    command(
      "go",
      ["build", "-o", binary, "./cmd/scratchpad-mcp"],
      resolve(root, "mcp"),
    );
  }
  const binaryVersion = command(binary, ["--version"]).trim();
  expect(binaryVersion).toMatch(/^scratchpad-mcp \S+$/);
  if (suppliedBinary) {
    console.log(
      `Validating supplied MCP executable: ${binary} (${binaryVersion})`,
    );
  }
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
  const restrictedProject = await page.evaluate(async () => {
    const resolved = await fetch("/api/v1/projects/resolve", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        context: {
          git: {
            remote: "https://github.com/example/scratchpad-e2e-restricted.git",
          },
        },
      }),
    });
    if (!resolved.ok) throw new Error("Could not resolve restricted fixture");
    const { project } = await resolved.json();
    const classified = await fetch(`/api/v1/projects/${project.id}`, {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        kind: "external",
        expectedVersion: project.version,
      }),
    });
    if (!classified.ok)
      throw new Error("Could not classify restricted fixture");
    const data = await classified.json();
    if (
      data.project.kind !== "external" ||
      data.project.settings.repoMirroring.enabled
    )
      throw new Error("Restricted fixture did not disable mirroring");
    return String(project.id);
  });
  command(
    "go",
    [...args, "-phase", "projects", "-restricted-project", restrictedProject],
    resolve(root, "mcp"),
  );
  await restartOrRestore();
  command("go", [...args, "-phase", "verify"], resolve(root, "mcp"));
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Settings", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Memory", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Project memory", exact: true }),
  ).toBeVisible();
  // The restored copy must retain more than raw captures: browser sessions,
  // curated metadata, revisions, evidence, relationships, imports and settings.
  await page.getByRole("button", { name: "Memory", exact: true }).click();
  await page.getByLabel("Search your memory", { exact: true }).fill("SQLite");
  await page.getByRole("button", { name: "Search", exact: true }).click();
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
    .getByRole("dialog")
    .getByRole("button", { name: "Close", exact: true })
    .click();
  await restoredProject
    .getByRole("button", { name: "Show project context", exact: true })
    .click();
  await expect(
    restoredProject.getByRole("button", {
      name: "Paused for hardware validation",
      exact: true,
    }),
  ).toBeVisible();
  await restoredProject
    .getByRole("button", {
      name: "Paused for hardware validation",
      exact: true,
    })
    .click();
  await expect(
    page
      .locator("section")
      .filter({
        has: page.getByRole("heading", { name: "Reason", exact: true }),
      })
      .getByText("Awaiting a test device", { exact: true }),
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
  // A synthetic provider exercises authenticated production MCP routing; real Qwen quality is tested separately.
  const provider = createServer(async (req, res) => {
    const chunks: Buffer[] = [];
    for await (const chunk of req) chunks.push(Buffer.from(chunk));
    const body = JSON.parse(Buffer.concat(chunks).toString());
    res.setHeader("Content-Type", "application/json");
    if (req.url?.endsWith("/embeddings")) {
      res.end(
        JSON.stringify({
          data: (body.input as string[]).map((_, index) => ({
            index,
            embedding: [1, 0.5, 0.25],
          })),
        }),
      );
      return;
    }
    const input = JSON.parse(body.messages.at(-1).content);
    const isExport = String(body.messages[0].content).startsWith("Create a ");
    const output = {
      artifacts: [
        {
          kind: isExport ? "export" : "summary",
          title: "Synthetic MCP derived output",
          content: {
            text: "Synthetic cited output",
            ...(isExport
              ? { markdown: "# Synthetic handoff\n\nPrivate fixture document." }
              : {}),
          },
          sourceRecordIds: input.records.map(
            (record: { id: string }) => record.id,
          ),
        },
      ],
    };
    res.end(
      JSON.stringify({
        choices: [{ message: { content: JSON.stringify(output) } }],
      }),
    );
  });
  await new Promise<void>((done) => provider.listen(0, "0.0.0.0", done));
  try {
    const address = provider.address();
    if (!address || typeof address === "string")
      throw new Error("Fixture provider did not listen");
    const providerUrl = `http://${dockerMode ? "host.docker.internal" : "127.0.0.1"}:${address.port}/v1`;
    const state = JSON.parse(
      readFileSync(resolve(temporary, "state.json"), "utf8"),
    );
    const localProjectId = state.captures[0].projectId as string;
    const otherProjectId = await page.evaluate(
      async ({ baseUrl, localId }) => {
        const api = async (path: string, method = "GET", body?: unknown) => {
          const response = await fetch(`/api/v1${path}`, {
            method,
            headers: { "Content-Type": "application/json" },
            body: body === undefined ? undefined : JSON.stringify(body),
          });
          const data = await response.json();
          if (!response.ok) throw new Error(JSON.stringify(data));
          return data;
        };
        await api("/ai/settings", "PATCH", {
          enabled: true,
          baseUrl,
          model: "synthetic-chat",
          embeddingModel: "synthetic-embedding",
          embeddingDimensions: 3,
          scheduleMinutes: 0,
          expectedVersion: 0,
        });
        const { project: local } = await api(`/projects/${localId}`);
        await api(`/projects/${localId}/settings`, "PATCH", {
          settings: {
            ...local.settings,
            aiProcessing: true,
            crossProjectAnalysis: true,
          },
          expectedVersion: local.version,
        });
        const { project: other } = await api(
          "/projects/resolve-explicit",
          "POST",
          { name: "Synthetic permitted MCP project" },
        );
        await api(`/projects/${other.id}/settings`, "PATCH", {
          settings: {
            ...other.settings,
            aiProcessing: true,
            crossProjectAnalysis: true,
          },
          expectedVersion: other.version,
        });
        await api("/records", "POST", {
          projectId: other.id,
          record: {
            type: "finding",
            title: "Private durable memory fixture",
            authority: "observed",
            confidence: "high",
            payload: {
              finding:
                "Synthetic cross-project source for MCP consent validation",
            },
          },
        });
        return other.id as string;
      },
      { baseUrl: providerUrl, localId: localProjectId },
    );
    await new Promise<void>((done, reject) => {
      const child = spawn(
        "go",
        [
          "run",
          "./ai-integration",
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
          "-other-project",
          otherProjectId,
          "-denied-project",
          restrictedProject,
        ],
        {
          cwd: resolve(root, "mcp"),
          env: environment,
          stdio: ["ignore", "pipe", "pipe"],
        },
      );
      let output = "";
      child.stdout.on("data", (data) => {
        output += data;
      });
      child.stderr.on("data", (data) => {
        output += data;
      });
      const timeout = setTimeout(() => {
        child.kill("SIGKILL");
        reject(new Error(`AI MCP integration timed out: ${output}`));
      }, 120000);
      child.on("error", (error) => {
        clearTimeout(timeout);
        reject(error);
      });
      child.on("exit", (code) => {
        clearTimeout(timeout);
        if (code === 0) done();
        else reject(new Error(`AI MCP integration exited ${code}: ${output}`));
      });
    });
  } finally {
    await new Promise<void>((done, reject) =>
      provider.close((error) => (error ? reject(error) : done())),
    );
  }
});
