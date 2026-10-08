import { execFileSync, spawn } from "node:child_process";
import { readFileSync, rmSync } from "node:fs";
import { createServer } from "node:http";
import { resolve } from "node:path";
import { expect, test } from "@playwright/test";

import { createWorkflowHarness, root } from "./workflow-harness";

const {
  admin,
  cleanupDocker,
  command,
  dockerMode,
  environment,
  origin,
  postgresMode,
  restartOrRestore,
  start,
  stop,
  temporary,
} = createWorkflowHarness();

test.beforeAll(start);
test.afterAll(async () => {
  try {
    await stop();
  } finally {
    cleanupDocker();
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
