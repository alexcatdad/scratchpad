import { type ChildProcess, execFileSync, spawn } from "node:child_process";
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
let server: ChildProcess;
let logs = "";
async function start() {
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
  for (let attempt = 0; attempt < 100; attempt++) {
    try {
      if ((await fetch(`${origin}/ready`)).ok) return;
    } catch {
      /* Server may still be starting. */
    }
    await new Promise((r) => setTimeout(r, 100));
  }
  throw new Error(`Server did not start: ${logs}`);
}
async function stop() {
  if (!server || server.exitCode !== null) return;
  const exited = new Promise<void>((done) => server.once("exit", () => done()));
  server.kill("SIGTERM");
  await exited;
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
  await stop();
  rmSync(temporary, { recursive: true, force: true });
});
test("owner enrollment, memory, MCP and restart preserve the real workflow", async ({
  page,
}) => {
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
  const output = command(
    process.execPath,
    [
      resolve(root, "node_modules/tsx/dist/cli.mjs"),
      "src/server/admin.ts",
      "setup",
    ],
    resolve(root, "apps/web"),
  );
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
  await page.getByRole("button", { name: "Memory", exact: true }).click();
  await page.getByRole("button", { name: "New record", exact: true }).click();
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
  await page.getByRole("button", { name: "Save title", exact: true }).click();
  await expect(
    page.getByRole("heading", {
      name: "SQLite keeps setup simple",
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
  await stop();
  await start();
  command("go", [...args, "-phase", "verify"], resolve(root, "mcp"));
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Project memory", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Welcome back.", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Sign in with passkey", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Project memory", exact: true }),
  ).toBeVisible();
});
