import { type ChildProcess, execFileSync, spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createServer, type Server } from "node:http";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import postgres from "postgres";

type Entity = { id: string; version: number; [key: string]: unknown };
type Project = Entity & { settings: Record<string, unknown> };
type Artifact = Entity & {
  kind: string;
  status: string;
  sourceRecordIds: string[];
  content: { text: string; markdown?: string };
  format?: string;
};
type Job = Entity & { status: string; lastError?: string };
type CompletionInput = {
  records?: Entity[];
  requestedFormat?: string;
  test?: boolean;
};

const root = resolve(import.meta.dirname, "..");
const temporary = mkdtempSync(resolve(tmpdir(), "scratchpad-post-mvp-"));
const origin = "http://localhost:3101";
const dockerMode = process.env.SCRATCHPAD_E2E_DOCKER === "1";
const postgresMode = process.env.SCRATCHPAD_E2E_POSTGRES === "1";
const postgresDatabase = `scratchpad_ai_browser_${randomUUID().replaceAll("-", "")}`;
let postgresAdmin: ReturnType<typeof postgres> | undefined;
let postgresCreated = false;
const runId = `scratchpad-post-mvp-${randomUUID()}`;
const volume = `${runId}-data`;
const dockerImage = process.env.SCRATCHPAD_E2E_IMAGE ?? "scratchpad:ci";
let containerCreated = false;
let volumeCreated = false;
const environment = {
  ...process.env,
  PORT: "3101",
  SCRATCHPAD_PUBLIC_URL: origin,
  SCRATCHPAD_DATABASE_PATH: resolve(temporary, "memory.sqlite"),
  SCRATCHPAD_DATABASE_URL: "",
};
let application: ChildProcess | undefined;
let provider: Server | undefined;
let providerUrl = "";
let logs = "";
const suppliedContent: string[] = [];
let expectedJobCount = 0;

async function request<T>(
  page: Page,
  path: string,
  method = "GET",
  body?: unknown,
): Promise<T> {
  return page.evaluate(
    async ({ path, method, body }) => {
      const response = await fetch(`/api/v1${path}`, {
        method,
        headers: { "Content-Type": "application/json" },
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
      });
      const value = await response.json();
      if (!response.ok)
        throw new Error(`${response.status}: ${JSON.stringify(value)}`);
      return value;
    },
    { path, method, body },
  ) as Promise<T>;
}

async function startProvider() {
  provider = createServer(async (incoming, outgoing) => {
    const chunks: Buffer[] = [];
    for await (const chunk of incoming) chunks.push(Buffer.from(chunk));
    const raw = Buffer.concat(chunks).toString("utf8");
    suppliedContent.push(raw);
    const body = JSON.parse(raw) as {
      input?: string[];
      messages?: { content: string }[];
    };
    outgoing.setHeader("Content-Type", "application/json");
    if (incoming.url === "/v1/embeddings") {
      outgoing.end(
        JSON.stringify({
          data: (body.input ?? []).map((text, index) => ({
            index,
            embedding: /unrelated|documentation/i.test(text)
              ? [0, 1, 0]
              : [1, 0, 0],
          })),
        }),
      );
      return;
    }
    if (incoming.url !== "/v1/chat/completions") {
      outgoing.statusCode = 404;
      outgoing.end(JSON.stringify({ error: "Unsupported fixture endpoint" }));
      return;
    }
    const input = JSON.parse(
      body.messages?.[1]?.content ?? "{}",
    ) as CompletionInput;
    if (input.test) {
      outgoing.end(
        JSON.stringify({
          choices: [{ message: { content: JSON.stringify({ ok: true }) } }],
        }),
      );
      return;
    }
    const records = input.records ?? [];
    const ids = records.map((record) => record.id);
    const system = body.messages?.[0]?.content ?? "";
    const artifact = (
      kind: string,
      title: string,
      sourceRecordIds = ids.slice(0, 2),
    ) => ({
      kind,
      title,
      content: {
        text: `Synthetic ${title}; review the linked evidence before acting.`,
        ...(kind === "classification"
          ? { tags: ["synthetic", "ci"], classification: "CI trade-off" }
          : {}),
        ...(kind === "relationship_candidate"
          ? {
              fromRecordId: ids[0],
              toRecordId: ids[1],
              relationshipType: "related_to",
            }
          : {}),
      },
      sourceRecordIds,
    });
    const artifacts = system.startsWith("Create a ")
      ? [
          {
            ...artifact(
              "export",
              `Synthetic ${input.requestedFormat} document`,
              ids.slice(0, 1),
            ),
            content: {
              text: "Synthetic private document",
              markdown: `# Synthetic ${input.requestedFormat}\n\nObserved: preserve CI investigations.\n\nUncertain: proposed next steps require review.\n`,
            },
          },
        ]
      : [
          artifact("summary", "Synthetic evidence summary"),
          artifact(
            "classification",
            "Synthetic CI classification",
            ids.slice(0, 1),
          ),
          artifact("duplicate_candidate", "Synthetic duplicate investigation"),
          artifact("relationship_candidate", "Synthetic related decisions"),
          artifact("contradiction", "Synthetic conflicting trade-offs"),
          artifact("cluster", "Synthetic CI topic cluster"),
          artifact("pattern", "Synthetic recurring CI pattern"),
          artifact("recommendation", "Synthetic consolidation recommendation"),
        ];
    outgoing.end(
      JSON.stringify({
        choices: [{ message: { content: JSON.stringify({ artifacts }) } }],
      }),
    );
  });
  const localProvider = provider;
  await new Promise<void>((done) => localProvider.listen(0, "0.0.0.0", done));
  const address = provider.address();
  if (!address || typeof address === "string")
    throw new Error("Provider fixture did not bind TCP");
  providerUrl = `http://${dockerMode ? "host.docker.internal" : "127.0.0.1"}:${address.port}/v1`;
}
async function stopProvider() {
  if (!provider) return;
  const current = provider;
  provider = undefined;
  current.closeAllConnections();
  await new Promise<void>((done, reject) =>
    current.close((error) => (error ? reject(error) : done())),
  );
}
function command(binary: string, args: string[]) {
  return execFileSync(binary, args, {
    cwd: root,
    env: environment,
    encoding: "utf8",
    timeout: 60000,
  });
}
function setupToken() {
  return dockerMode
    ? command("docker", ["exec", runId, "npm", "run", "admin", "--", "setup"])
    : execFileSync(
        process.execPath,
        [
          resolve(root, "node_modules/tsx/dist/cli.mjs"),
          "src/server/admin.ts",
          "setup",
        ],
        { cwd: resolve(root, "apps/web"), env: environment, encoding: "utf8" },
      );
}
async function createDatabase() {
  if (!postgresMode) return;
  const connection = process.env.TEST_POSTGRES_URL;
  if (!connection)
    throw new Error(
      "PostgreSQL browser acceptance requires TEST_POSTGRES_URL for a disposable test server.",
    );
  postgresAdmin = postgres(connection, { max: 1 });
  await postgresAdmin.unsafe(`CREATE DATABASE "${postgresDatabase}"`);
  postgresCreated = true;
  const url = new URL(connection);
  url.pathname = `/${postgresDatabase}`;
  if (dockerMode && ["localhost", "127.0.0.1", "::1"].includes(url.hostname))
    url.hostname = "host.docker.internal";
  environment.SCRATCHPAD_DATABASE_URL = url.toString();
}
async function dropDatabase() {
  if (!postgresAdmin) return;
  try {
    if (postgresCreated)
      await postgresAdmin.unsafe(
        `DROP DATABASE "${postgresDatabase}" WITH (FORCE)`,
      );
  } finally {
    await postgresAdmin.end();
    postgresAdmin = undefined;
    postgresCreated = false;
  }
}
async function startApplication() {
  if (dockerMode) {
    command("docker", ["volume", "create", volume]);
    volumeCreated = true;
    command("docker", [
      "run",
      "--detach",
      "--name",
      runId,
      "--publish",
      "127.0.0.1:3101:3000",
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
      `${volume}:/data`,
      dockerImage,
    ]);
    containerCreated = true;
  } else
    application = spawn(
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
  application?.stdout?.on("data", (value) => {
    logs += value;
  });
  application?.stderr?.on("data", (value) => {
    logs += value;
  });
  await expect
    .poll(
      async () => {
        try {
          const ready = await fetch(`${origin}/ready`, {
            signal: AbortSignal.timeout(2000),
          });
          if (ready.status !== 200) logs += await ready.text();
          return ready.status;
        } catch {
          return 0;
        }
      },
      { timeout: 30000, message: "Disposable production server must be ready" },
    )
    .toBe(200)
    .catch((error) => {
      throw new Error(`${error.message}\n${logs.slice(-4000)}`);
    });
}
async function stopApplication() {
  if (containerCreated) {
    logs += command("docker", ["logs", runId]);
    command("docker", ["rm", "--force", runId]);
    containerCreated = false;
  }
  if (volumeCreated) {
    command("docker", ["volume", "rm", volume]);
    volumeCreated = false;
  }
  if (!application || application.exitCode !== null) return;
  const child = application;
  const exited = new Promise<void>((done) => child.once("exit", () => done()));
  child.kill("SIGTERM");
  const timer = setTimeout(() => child.kill("SIGKILL"), 5000);
  try {
    await exited;
  } finally {
    clearTimeout(timer);
  }
}
async function awaitJobs(page: Page) {
  expectedJobCount++;
  await expect
    .poll(
      async () => {
        const { jobs } = await request<{ jobs: Job[] }>(page, "/ai/jobs");
        const failed = jobs.find((job) => job.status === "failed");
        if (failed)
          throw new Error(`Background job failed: ${JSON.stringify(failed)}`);
        return (
          jobs.length >= expectedJobCount &&
          jobs.every((job) => job.status === "completed")
        );
      },
      { timeout: 45000, intervals: [250, 500, 1000] },
    )
    .toBe(true);
  await page
    .getByRole("button", { name: "Refresh insights", exact: true })
    .click();
}

test.beforeAll(async () => {
  await createDatabase();
  await startProvider();
  await startApplication();
});
test.afterAll(async () => {
  try {
    await stopApplication();
    await stopProvider();
  } finally {
    await dropDatabase();
    rmSync(temporary, { recursive: true, force: true });
  }
});

test("private AI settings, evidence review, semantic search and document downloads", async ({
  page,
}) => {
  test.setTimeout(180000);
  const exceptions: string[] = [];
  page.on("pageerror", (error) => exceptions.push(error.message));
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
  const token = setupToken().trim().split("\n").at(-1) ?? "";
  await page.goto(origin);
  await page.getByLabel("Setup or recovery token").fill(token);
  await page
    .getByRole("button", { name: "Register passkey", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Project memory", exact: true }),
  ).toBeVisible();

  const profile = await request<{ database: { engine: string } }>(
    page,
    "/profile",
  );
  expect(profile.database.engine).toBe(postgresMode ? "postgresql" : "sqlite");

  const projects: Project[] = [];
  for (const name of [
    "Synthetic CI Alpha",
    "Synthetic CI Beta",
    "Synthetic excluded client",
  ]) {
    const { project } = await request<{ project: Project }>(
      page,
      "/projects/resolve-explicit",
      "POST",
      { name, kind: name.includes("excluded") ? "external" : "normal" },
    );
    if (!name.includes("excluded")) {
      await request(page, `/projects/${project.id}/settings`, "PATCH", {
        settings: {
          ...project.settings,
          aiProcessing: true,
          crossProjectAnalysis: true,
        },
        expectedVersion: project.version,
      });
    } else {
      expect(project.settings.aiProcessing).toBe(false);
      expect(project.settings.crossProjectAnalysis).toBe(false);
    }
    projects.push(project);
  }
  const snapshots = new Map<string, Entity>();
  for (let index = 0; index < projects.length; index++) {
    const title =
      index === 2
        ? "EXCLUDED_CLIENT_SECRET"
        : `Synthetic CI investigation ${index}`;
    const { record } = await request<{ record: Entity }>(
      page,
      "/records",
      "POST",
      {
        projectId: projects[index]?.id,
        record: {
          type: "finding",
          title,
          authority: "observed",
          confidence: "high",
          payload: {
            finding:
              index === 2
                ? "EXCLUDED_CLIENT_SECRET must never enter provider requests"
                : "Synthetic finding: preserve the CI investigation so it is not repeated",
            synthetic: true,
          },
        },
      },
    );
    snapshots.set(
      record.id,
      (await request<{ record: Entity }>(page, `/records/${record.id}`)).record,
    );
  }
  await page.reload();
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page
    .getByLabel("Owner display name", { exact: true })
    .fill("Synthetic owner");
  await page.getByRole("button", { name: "Save profile", exact: true }).click();
  await expect(page.getByText("Profile saved.", { exact: true })).toBeVisible();
  const defaults = page.getByRole("region", { name: "New project defaults" });
  await defaults
    .getByLabel("Allow repository mirroring by default", { exact: true })
    .check();
  await defaults
    .getByRole("group", { name: "Default enabled capture types", exact: true })
    .getByLabel("Question & answer", { exact: true })
    .uncheck();
  await defaults
    .getByRole("button", { name: "Save project defaults", exact: true })
    .click();
  await expect(
    defaults.getByText("New project defaults saved.", { exact: true }),
  ).toBeVisible();
  const previousDefaults = await request<{ settings: { version: number } }>(
    page,
    "/settings",
  );
  await defaults
    .getByRole("button", { name: "Save project defaults", exact: true })
    .click();
  await expect
    .poll(
      async () =>
        (await request<{ settings: { version: number } }>(page, "/settings"))
          .settings.version,
    )
    .toBe(previousDefaults.settings.version + 1);
  const inherited = (
    await request<{ project: Project }>(
      page,
      "/projects/resolve-explicit",
      "POST",
      { name: "Synthetic inherited defaults" },
    )
  ).project;
  expect(
    (inherited.settings.repoMirroring as { enabled: boolean }).enabled,
  ).toBe(true);
  expect(inherited.settings.enabledRecordTypes).not.toContain("qa");
  expect(inherited.settings.aiProcessing).toBe(false);
  const conservative = (
    await request<{ project: Project }>(
      page,
      "/projects/resolve-explicit",
      "POST",
      { name: "Synthetic conservative defaults", kind: "external" },
    )
  ).project;
  expect(
    (conservative.settings.repoMirroring as { enabled: boolean }).enabled,
  ).toBe(false);
  expect(conservative.settings.aiProcessing).toBe(false);
  expect(conservative.settings.crossProjectAnalysis).toBe(false);
  const staleDefaults = await page.evaluate(async (expectedVersion) => {
    const response = await fetch("/api/v1/settings", {
      method: "PATCH",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ expectedVersion, settings: {} }),
    });
    return { status: response.status, body: await response.json() };
  }, previousDefaults.settings.version);
  expect(staleDefaults.status).toBe(409);
  expect(staleDefaults.body.error.code).toBe("CONFLICT");
  await page.reload();
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await expect(
    defaults.getByLabel("Allow repository mirroring by default", {
      exact: true,
    }),
  ).toBeChecked();
  await expect(
    defaults
      .getByRole("group", {
        name: "Default enabled capture types",
        exact: true,
      })
      .getByLabel("Question & answer", { exact: true }),
  ).not.toBeChecked();

  const aiSettings = page.getByRole("region", { name: "AI provider settings" });
  await aiSettings.getByLabel("Enable AI processing", { exact: true }).check();
  await aiSettings
    .getByLabel("OpenAI-compatible base URL", { exact: false })
    .fill(providerUrl);
  await aiSettings
    .getByLabel("LLM model", { exact: true })
    .fill("synthetic-completion");
  await aiSettings
    .getByLabel("Embedding model", { exact: true })
    .fill("synthetic-embedding");
  await aiSettings
    .getByLabel("Embedding dimensions", { exact: true })
    .fill("3");
  await aiSettings
    .getByLabel("Analysis interval (minutes)", { exact: false })
    .fill("0");
  await aiSettings
    .getByLabel("Provider API key", { exact: false })
    .fill("synthetic-secret-key");
  const saved = page.waitForResponse(
    (response) =>
      response.request().method() === "PATCH" &&
      response.url().endsWith("/ai/settings"),
  );
  await aiSettings
    .getByRole("button", { name: "Save AI settings", exact: true })
    .click();
  const savedResponse = await saved;
  expect(savedResponse.ok(), await savedResponse.text()).toBe(true);
  await expect(
    aiSettings.getByText(
      "AI settings saved. Project participation is controlled separately.",
    ),
  ).toBeVisible();
  await aiSettings
    .getByRole("button", { name: "Test saved provider", exact: true })
    .click();
  await expect(
    aiSettings.getByText(
      "Provider test passed using synthetic text for completion and embeddings.",
    ),
  ).toBeVisible();
  expect(JSON.stringify(await request(page, "/ai/settings"))).not.toContain(
    "synthetic-secret-key",
  );
  expect(
    JSON.stringify(await request(page, "/export", "POST", {})),
  ).not.toContain("synthetic-secret-key");

  await page.getByRole("button", { name: "Insights", exact: true }).click();
  await expect(
    page.getByText("2 participating projects.", { exact: false }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Analyze memory", exact: true })
    .click();
  await awaitJobs(page);
  const { suggestions } = await request<{ suggestions: Artifact[] }>(
    page,
    "/suggestions?crossProject=true",
  );
  expect(suggestions.map((item) => item.kind).sort()).toEqual(
    [
      "summary",
      "classification",
      "duplicate_candidate",
      "relationship_candidate",
      "contradiction",
      "cluster",
      "pattern",
      "recommendation",
    ].sort(),
  );
  const suggestionCards = page.getByRole("region", { name: "AI suggestions" });
  const summaryCard = suggestionCards.locator("article").filter({
    has: page.getByRole("heading", {
      name: "Synthetic evidence summary",
      exact: true,
    }),
  });
  await summaryCard
    .getByRole("button", { name: "Accept suggestion", exact: true })
    .click();
  await expect(
    page.getByText("Suggestion accepted with an audit entry.", { exact: true }),
  ).toBeVisible();
  const duplicateCard = suggestionCards.locator("article").filter({
    has: page.getByRole("heading", {
      name: "Synthetic duplicate investigation",
      exact: true,
    }),
  });
  await duplicateCard
    .getByRole("button", { name: "Reject suggestion", exact: true })
    .click();
  await expect(
    page.getByText("Suggestion rejected.", { exact: true }),
  ).toBeVisible();
  const reviewed = (
    await request<{ suggestions: Artifact[] }>(
      page,
      "/suggestions?crossProject=true",
    )
  ).suggestions;
  expect(reviewed.find((item) => item.kind === "summary")?.status).toBe(
    "accepted",
  );
  expect(
    reviewed.find((item) => item.kind === "duplicate_candidate")?.status,
  ).toBe("rejected");

  await page
    .getByRole("button", { name: "Build embeddings", exact: true })
    .click();
  await awaitJobs(page);
  await page
    .getByLabel("Meaning to search for", { exact: true })
    .fill("avoid repeating an earlier CI investigation");
  await page
    .getByRole("button", { name: "Search by meaning", exact: true })
    .click();
  await expect(
    page.getByRole("button", {
      name: "Synthetic CI investigation 0",
      exact: true,
    }),
  ).toBeVisible();
  await expect(
    page.getByText("EXCLUDED_CLIENT_SECRET", { exact: false }),
  ).toHaveCount(0);
  await page
    .getByRole("button", { name: "Synthetic CI investigation 0", exact: true })
    .click();
  await expect(
    page.getByRole("heading", {
      name: "Synthetic CI investigation 0",
      exact: true,
    }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Insights", exact: true }).click();
  await page
    .getByLabel("Insight project", { exact: false })
    .selectOption(projects[0]?.id ?? "");
  for (const format of [
    "handoff",
    "architecture",
    "decisions",
    "client_history",
    "adr",
  ]) {
    await page
      .getByLabel("Document kind", { exact: false })
      .selectOption(format);
    await page
      .getByRole("button", { name: "Generate document", exact: true })
      .click();
    await awaitJobs(page);
    const documentCard = page
      .getByRole("region", { name: "AI suggestions" })
      .locator("article")
      .filter({
        has: page.getByRole("heading", {
          name: `Synthetic ${format} document`,
          exact: true,
        }),
      });
    const downloadPromise = page.waitForEvent("download");
    await documentCard
      .getByRole("button", { name: "Download Markdown", exact: true })
      .click();
    const download = await downloadPromise;
    expect(download.suggestedFilename()).toMatch(/\.md$/);
    const path = await download.path();
    expect(path).not.toBeNull();
    const markdown = readFileSync(path ?? "", "utf8");
    expect(markdown).toMatch(/^> AI-generated draft\./);
    expect(markdown).toContain(`# Synthetic ${format}`);
    expect(markdown).toContain("## Source records");
    expect(markdown).toContain([...snapshots.keys()][0] ?? "");
    expect(markdown).toContain(
      `(/?recordId=${encodeURIComponent([...snapshots.keys()][0] ?? "")})`,
    );
    expect(markdown).not.toContain("EXCLUDED_CLIENT_SECRET");
    expect(markdown).not.toContain("synthetic-secret-key");
  }

  const citedRecord = [...snapshots.values()][0];
  expect(citedRecord).toBeDefined();
  await page.goto(
    `${origin}/?recordId=${encodeURIComponent(String(citedRecord?.id))}`,
  );
  await expect(
    page.getByRole("heading", {
      name: String(citedRecord?.title),
      exact: true,
    }),
  ).toBeVisible();

  for (const [id, original] of snapshots)
    expect(
      (await request<{ record: Entity }>(page, `/records/${id}`)).record,
    ).toEqual(original);
  expect(suppliedContent.join("\n")).not.toContain("EXCLUDED_CLIENT_SECRET");
  // Existing cross-project artifacts must disappear when source participation is withdrawn.
  const current = await request<{
    settings: Record<string, unknown>;
    version: number;
  }>(page, `/projects/${projects[1]?.id ?? ""}/settings`);
  await request(page, `/projects/${projects[1]?.id ?? ""}/settings`, "PATCH", {
    settings: { ...current.settings, crossProjectAnalysis: false },
    expectedVersion: current.version,
  });
  const remainingArtifacts = (
    await request<{ suggestions: Artifact[] }>(
      page,
      "/suggestions?crossProject=true",
    )
  ).suggestions;
  const withdrawnRecordId = [...snapshots.keys()][1] ?? "";
  expect(
    remainingArtifacts.some((artifact) =>
      artifact.sourceRecordIds.includes(withdrawnRecordId),
    ),
  ).toBe(false);
  expect(
    remainingArtifacts.some((artifact) => artifact.kind === "pattern"),
  ).toBe(false);

  await stopProvider();
  expect((await fetch(`${origin}/ready`)).status).toBe(200);
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page
    .getByRole("button", { name: "Test saved provider", exact: true })
    .click();
  await expect(
    page.getByRole("alert").filter({ hasText: /unavailable/i }),
  ).toBeVisible();
  const captured = await request<{ record: Entity }>(page, "/records", "POST", {
    projectId: projects[0]?.id ?? "",
    record: {
      type: "finding",
      title: "Synthetic provider-down capture",
      authority: "observed",
      payload: {
        finding: "Capture remains available when optional provider is offline",
        synthetic: true,
      },
    },
  });
  expect(captured.record.id).toBeTruthy();
  const results = await request<{ records: Entity[] }>(
    page,
    "/records?q=provider-down",
  );
  expect(
    results.records.some((record) => record.id === captured.record.id),
  ).toBe(true);
  await page.getByRole("button", { name: "Insights", exact: true }).click();
  await page
    .getByLabel("Insight project", { exact: false })
    .selectOption(projects[0]?.id ?? "");
  const enqueue = page.waitForResponse(
    (response) =>
      response.request().method() === "POST" &&
      response.url().endsWith("/ai/jobs"),
  );
  await page
    .getByRole("button", { name: "Analyze memory", exact: true })
    .click();
  const failedJob = (await (await enqueue).json()).job as Job;
  const jobStatus = async () =>
    (await request<{ jobs: Job[] }>(page, "/ai/jobs")).jobs.find(
      (job) => job.id === failedJob.id,
    )?.status;
  await expect.poll(jobStatus, { timeout: 30000 }).toBe("failed");
  const retry = page.getByRole("button", { name: "Retry job", exact: true });
  await expect(retry).toBeVisible();
  await startProvider();
  const providerSettings = await request<{ version: number }>(
    page,
    "/ai/settings",
  );
  await request(page, "/ai/settings", "PATCH", {
    baseUrl: providerUrl,
    expectedVersion: providerSettings.version,
  });
  await retry.click();
  await expect(
    page.getByText("Failed job queued for another attempt.", { exact: true }),
  ).toBeVisible();
  await expect.poll(jobStatus, { timeout: 30000 }).toBe("completed");
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.setViewportSize({ width: 390, height: 844 });
  await expect(
    page.getByRole("heading", { name: "Settings", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Save AI settings", exact: true }),
  ).toBeVisible();
  const mobileFits = async () => {
    const measured = await page.evaluate(() => ({
      fits: document.documentElement.scrollWidth <= window.innerWidth + 1,
      viewport: window.innerWidth,
      width: document.documentElement.scrollWidth,
      overflow: [...document.querySelectorAll<HTMLElement>("body *")]
        .filter(
          (element) =>
            element.getBoundingClientRect().right > window.innerWidth + 1,
        )
        .map((element) => ({
          tag: element.tagName,
          className: element.className,
          right: element.getBoundingClientRect().right,
        }))
        .slice(0, 20),
    }));
    if (!measured.fits) console.log("Mobile overflow:", measured);
    return measured.fits;
  };
  expect(await mobileFits()).toBe(true);
  await page.getByRole("button", { name: "Insights", exact: true }).click();
  await expect(
    page.getByLabel("Insight project", { exact: false }),
  ).toBeVisible();
  await expect(
    page.getByRole("heading", { name: "Background jobs", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByLabel("Document kind", { exact: false }),
  ).toBeVisible();
  expect(await mobileFits()).toBe(true);
  await page
    .getByRole("region", { name: "AI suggestions" })
    .getByRole("button", { name: /^Source / })
    .first()
    .click();
  await expect(
    page.getByRole("heading", {
      name: "Synthetic CI investigation 0",
      exact: true,
    }),
  ).toBeVisible();
  expect(await mobileFits()).toBe(true);
  await page.setViewportSize({ width: 1440, height: 1000 });
  expect(exceptions).toEqual([]);
  if (dockerMode) logs += command("docker", ["logs", runId]);
  expect(logs).not.toContain("synthetic-secret-key");
});
