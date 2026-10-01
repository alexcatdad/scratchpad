import { type ChildProcess, execFileSync, spawn } from "node:child_process";
import { createHash, randomUUID } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import Database from "better-sqlite3";

// Explicit opt-in: no external model service is required by ordinary CI.
const enabled = process.env.SCRATCHPAD_REAL_AI === "1";
const providerTimeoutSeconds = Number(
  process.env.SCRATCHPAD_REAL_AI_TIMEOUT_SECONDS ?? 600,
);
if (
  !Number.isInteger(providerTimeoutSeconds) ||
  providerTimeoutSeconds < 5 ||
  providerTimeoutSeconds > 3600
)
  throw new Error("Real AI timeout must be an integer from 5 to 3600 seconds.");
const root = resolve(import.meta.dirname, "..");
const origin = "http://localhost:3102";
const temporary = enabled
  ? mkdtempSync(resolve(tmpdir(), "scratchpad-real-ai-"))
  : "";
const databasePath = resolve(temporary || tmpdir(), "memory.sqlite");
const dockerMode = process.env.SCRATCHPAD_E2E_DOCKER === "1";
const runId = `scratchpad-real-ai-${randomUUID()}`;
const environment = {
  ...process.env,
  PORT: "3102",
  SCRATCHPAD_PUBLIC_URL: origin,
  SCRATCHPAD_DATABASE_PATH: databasePath,
  SCRATCHPAD_DATABASE_URL: "",
};
let application: ChildProcess | undefined;
let containerCreated = false;
let logs = "";
type Entity = { id: string; version: number; [key: string]: unknown };
type Job = Entity & {
  status: string;
  lastError?: string;
  sourceCount?: number;
};
type Artifact = Entity & {
  kind: string;
  private: boolean;
  authority: string;
  sourceRecordIds: string[];
  content: { text: string; markdown?: string };
  generator: { model: string; provider: string };
  generatedAt: string;
};
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
      const data = await response.json();
      if (!response.ok)
        throw new Error(`${response.status}: ${JSON.stringify(data)}`);
      return data;
    },
    { path, method, body },
  ) as Promise<T>;
}
function command(executable: string, args: string[]): string {
  return execFileSync(executable, args, {
    cwd: root,
    env: environment,
    encoding: "utf8",
  });
}
function setupToken(): string {
  const output = dockerMode
    ? command("docker", ["exec", runId, "npm", "run", "admin", "--", "setup"])
    : command(process.execPath, [
        resolve(root, "node_modules/tsx/dist/cli.mjs"),
        resolve(root, "apps/web/src/server/admin.ts"),
        "setup",
      ]);
  return output.trim().split("\n").at(-1) ?? "";
}
async function waitForJob(page: Page, key: string): Promise<Job> {
  let job: Job | undefined;
  await expect
    .poll(
      async () => {
        job = (await request<{ jobs: Job[] }>(page, "/ai/jobs")).jobs.find(
          (item) => item.id === key,
        );
        if (job?.status === "failed")
          throw new Error(`Real provider job failed: ${JSON.stringify(job)}`);
        return job?.status;
      },
      {
        timeout: (providerTimeoutSeconds * 3 + 60) * 1000,
        intervals: [1000, 3000, 5000],
      },
    )
    .toBe("completed");
  return job as Job;
}
function hash(value: unknown): string {
  return createHash("sha256").update(JSON.stringify(value)).digest("hex");
}

test.describe("real local Qwen processing", () => {
  test.skip(
    !enabled,
    "Requires explicit SCRATCHPAD_REAL_AI=1 and the owner's local LM Studio models.",
  );
  test.beforeAll(async () => {
    if (dockerMode) {
      command("docker", [
        "run",
        "--detach",
        "--name",
        runId,
        "--publish",
        "127.0.0.1:3102:3000",
        "--add-host",
        "host.docker.internal:host-gateway",
        "--env",
        `SCRATCHPAD_PUBLIC_URL=${origin}`,
        "--env",
        "SCRATCHPAD_DATABASE_PATH=/data/scratchpad.sqlite",
        process.env.SCRATCHPAD_E2E_IMAGE ?? "scratchpad:ci",
      ]);
      containerCreated = true;
    } else {
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
      application.stdout?.on("data", (value) => {
        logs += value;
      });
      application.stderr?.on("data", (value) => {
        logs += value;
      });
    }
    await expect
      .poll(
        async () => {
          try {
            return (
              await fetch(`${origin}/ready`, {
                signal: AbortSignal.timeout(2000),
              })
            ).status;
          } catch {
            return 0;
          }
        },
        { timeout: 30000 },
      )
      .toBe(200);
  });
  test.afterAll(async () => {
    if (containerCreated) {
      command("docker", ["rm", "--force", "--volumes", runId]);
      containerCreated = false;
    }
    if (application && application.exitCode === null) {
      const child = application;
      const exited = new Promise<void>((done) =>
        child.once("exit", () => done()),
      );
      child.kill("SIGTERM");
      const timer = setTimeout(() => child.kill("SIGKILL"), 5000);
      try {
        await exited;
      } finally {
        clearTimeout(timer);
      }
    }
    if (temporary) rmSync(temporary, { recursive: true, force: true });
  });
  test("authenticated application generates cited insights, 2560-dimensional semantic matches and a private project handoff", async ({
    page,
  }) => {
    test.setTimeout((providerTimeoutSeconds * 6 + 400) * 1000);
    const errors: string[] = [];
    page.on("pageerror", (error) => errors.push(error.message));
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
    await page.goto(origin);
    await page.getByLabel("Setup or recovery token").fill(setupToken());
    await page
      .getByRole("button", { name: "Register passkey", exact: true })
      .click();
    await expect(
      page.getByRole("heading", { name: "Project memory", exact: true }),
    ).toBeVisible();
    const { project } = await request<{
      project: Entity & { settings: Record<string, unknown> };
    }>(page, "/projects/resolve-explicit", "POST", {
      name: "Synthetic Lantern Cache (real model validation)",
      kind: "normal",
    });
    await request(page, `/projects/${project.id}/settings`, "PATCH", {
      expectedVersion: project.version,
      settings: {
        ...project.settings,
        aiProcessing: true,
        crossProjectAnalysis: false,
      },
    });
    const captures = [
      {
        type: "decision",
        title: "Synthetic: start with a local SQLite cache",
        authority: "explicit",
        payload: {
          decision: "Use SQLite for the single-owner offline cache",
          rationale:
            "Avoid a separate database service; workload is small and only one developer writes.",
          synthetic: true,
        },
      },
      {
        type: "decision",
        title: "Synthetic: consider PostgreSQL for concurrent writers",
        authority: "inferred",
        payload: {
          decision:
            "Propose PostgreSQL if several independent workers need to write concurrently",
          rationale:
            "This is an unapproved proposal and conflicts with the current SQLite-only deployment preference.",
          synthetic: true,
        },
      },
      {
        type: "finding",
        title: "Synthetic: SQLite WAL improves reader concurrency",
        authority: "observed",
        payload: {
          finding:
            "A disposable concurrency experiment found SQLite WAL allows readers during short writes, but writes remain serialized.",
          synthetic: true,
        },
      },
      {
        type: "failure",
        title: "Synthetic: long transactions blocked cache writes",
        authority: "observed",
        payload: {
          observed:
            "A background worker held a transaction while waiting for the model and blocked interactive writes",
          lesson:
            "Call the model outside the database transaction and lease jobs durably.",
          synthetic: true,
        },
      },
      {
        type: "project_state",
        title: "Synthetic: ready to implement the cache",
        authority: "explicit",
        payload: {
          state: "active",
          reason:
            "Start with SQLite; reassess after measuring actual write contention.",
          followUp:
            "Implement short atomic writes and durable jobs before adding PostgreSQL.",
          synthetic: true,
        },
      },
    ];
    const originals = new Map<string, string>();
    for (const capture of captures) {
      const { record } = await request<{ record: Entity }>(
        page,
        "/records",
        "POST",
        { projectId: project.id, record: { ...capture, confidence: "high" } },
      );
      originals.set(record.id, hash(record));
    }
    const settings = await request<{ version: number }>(page, "/ai/settings");
    const providerUrl =
      process.env.SCRATCHPAD_REAL_AI_URL ??
      (dockerMode
        ? "http://host.docker.internal:1234/v1"
        : "http://localhost:1234/v1");
    await request(page, "/ai/settings", "PATCH", {
      enabled: true,
      baseUrl: providerUrl,
      model: "qwen/qwen3.8-27b",
      embeddingModel: "text-embedding-qwen3-embedding-4b",
      embeddingDimensions: 2560,
      scheduleMinutes: 0,
      similarityThreshold: 0.1,
      requestTimeoutSeconds: providerTimeoutSeconds,
      reasoningEffort: "none",
      maxOutputTokens: 4096,
      expectedVersion: settings.version,
    });
    await page.reload();
    await page.getByRole("button", { name: "Insights", exact: true }).click();
    await page
      .getByLabel("Insight project", { exact: false })
      .selectOption(project.id);
    const { job: embedding } = await request<{ job: Job }>(
      page,
      "/ai/jobs",
      "POST",
      { type: "embed", projectId: project.id },
    );
    expect((await waitForJob(page, embedding.id)).sourceCount).toBe(
      captures.length,
    );
    if (dockerMode) {
      const vectors = JSON.parse(
        command("docker", [
          "exec",
          runId,
          "node",
          "--input-type=module",
          "-e",
          "import Database from 'better-sqlite3'; const db=new Database('/data/scratchpad.sqlite',{readonly:true}); const rows=db.prepare(\"SELECT data FROM entities WHERE kind='ai_embedding'\").all(); console.log(JSON.stringify(rows.map(row=>{const e=JSON.parse(row.data); return {dimensions:e.dimensions,length:e.vector.length,finite:e.vector.every(Number.isFinite),nonzero:e.vector.some(n=>n!==0)}}))); db.close();",
        ]),
      ) as {
        dimensions: number;
        length: number;
        finite: boolean;
        nonzero: boolean;
      }[];
      expect(vectors).toHaveLength(captures.length);
      for (const vector of vectors)
        expect(vector).toEqual({
          dimensions: 2560,
          length: 2560,
          finite: true,
          nonzero: true,
        });
    } else {
      const sqlite = new Database(databasePath, { readonly: true });
      try {
        const rows = sqlite
          .prepare("SELECT data FROM entities WHERE kind='ai_embedding'")
          .all() as { data: string }[];
        expect(rows.length).toBe(captures.length);
        for (const row of rows) {
          const value = JSON.parse(row.data);
          expect(value.dimensions).toBe(2560);
          expect(value.vector.length).toBe(2560);
          expect(value.vector.every(Number.isFinite)).toBe(true);
        }
      } finally {
        sqlite.close();
      }
    }
    const matches = await request<{
      results: { record: Entity; score: number }[];
      model: string;
    }>(page, "/search/semantic", "POST", {
      projectId: project.id,
      query:
        "Why choose SQLite WAL for the local cache instead of operating a PostgreSQL service?",
      limit: 5,
    });
    expect(matches.model).toBe("text-embedding-qwen3-embedding-4b");
    expect(matches.results.length).toBeGreaterThan(0);
    expect(
      matches.results.every(
        (item) => originals.has(item.record.id) && Number.isFinite(item.score),
      ),
    ).toBe(true);
    expect(
      matches.results
        .slice(0, 3)
        .some((item) => /SQLite/.test(String(item.record.title))),
    ).toBe(true);
    console.log(
      `Real embedding acceptance passed: ${captures.length} source records, 2560-dimensional Qwen vectors and ${matches.results.length} semantic matches through the authenticated application.`,
    );
    let acceptedInsights = 0;
    if (process.env.SCRATCHPAD_REAL_AI_EXPORT_ONLY !== "1") {
      const { job: analysis } = await request<{ job: Job }>(
        page,
        "/ai/jobs",
        "POST",
        { type: "analyze", projectId: project.id },
      );
      expect((await waitForJob(page, analysis.id)).sourceCount).toBe(
        captures.length,
      );
      await page
        .getByRole("button", { name: "Refresh insights", exact: true })
        .click();
      const { suggestions } = await request<{ suggestions: Artifact[] }>(
        page,
        `/suggestions?projectId=${project.id}`,
      );
      expect(suggestions.length).toBeGreaterThan(0);
      acceptedInsights = suggestions.length;
      for (const suggestion of suggestions) {
        expect(suggestion.authority).toBe("derived");
        expect(suggestion.private).toBe(true);
        expect(suggestion.generator).toMatchObject({
          model: "qwen/qwen3.8-27b",
          provider: providerUrl,
        });
        expect(Number.isFinite(Date.parse(suggestion.generatedAt))).toBe(true);
        expect(suggestion.sourceRecordIds.length).toBeGreaterThan(0);
        expect(
          suggestion.sourceRecordIds.every((key) => originals.has(key)),
        ).toBe(true);
      }
      await expect(
        page.getByRole("heading", {
          name: suggestions[0]?.title as string,
          exact: true,
        }),
      ).toBeVisible();
    }
    const { job: exported } = await request<{ job: Job }>(
      page,
      "/summaries/export",
      "POST",
      { projectId: project.id, format: "handoff" },
    );
    await waitForJob(page, exported.id);
    await page
      .getByRole("button", { name: "Refresh insights", exact: true })
      .click();
    const document = (
      await request<{ suggestions: Artifact[] }>(
        page,
        `/suggestions?projectId=${project.id}`,
      )
    ).suggestions.find((item) => item.kind === "export") as Artifact;
    expect(document.private).toBe(true);
    expect(document.sourceRecordIds.length).toBe(captures.length);
    expect(document.content.markdown).toMatch(/^> AI-generated draft\./);
    expect(document.content.markdown).toMatch(/SQLite/i);
    expect(document.content.markdown).toMatch(
      /handoff|next|follow|implementation/i,
    );
    for (const key of originals.keys())
      expect(document.content.markdown).toContain(`${origin}/?recordId=${key}`);
    const card = page.locator("article").filter({
      has: page.getByRole("heading", {
        name: document.title as string,
        exact: true,
      }),
    });
    const download = page.waitForEvent("download");
    await card
      .getByRole("button", { name: "Download Markdown", exact: true })
      .click();
    const file = await download;
    const path = await file.path();
    expect(path).not.toBeNull();
    expect(readFileSync(path as string, "utf8")).toBe(
      document.content.markdown,
    );
    for (const [key, original] of originals)
      expect(
        hash(
          (await request<{ record: Entity }>(page, `/records/${key}`)).record,
        ),
      ).toBe(original);
    const firstId = [...originals.keys()][0] as string;
    const expectedSourceUrl = `${origin}/?recordId=${encodeURIComponent(firstId)}`;
    const downloadedSourceUrl = [
      ...(document.content.markdown ?? "").matchAll(
        /\]\((https?:\/\/[^)]+)\)/g,
      ),
    ]
      .map((match) => match[1])
      .find((url) => url === expectedSourceUrl);
    expect(downloadedSourceUrl).toBe(expectedSourceUrl);
    const linked = await page.goto(downloadedSourceUrl as string);
    expect(linked?.status()).toBe(200);
    await expect(
      page.getByRole("complementary", { name: "Record detail" }),
    ).toBeVisible();
    await expect(
      page
        .getByRole("complementary", { name: "Record detail" })
        .getByRole("heading", {
          name: "Synthetic: start with a local SQLite cache",
          exact: true,
        }),
    ).toBeVisible();
    expect(errors).toEqual([]);
    expect((await fetch(`${origin}/ready`)).status, logs.slice(-2000)).toBe(
      200,
    );
    console.log(
      `Real Qwen acceptance: ${acceptedInsights} cited insights, ${captures.length} validated 2560-dimensional embeddings, ${matches.results.length} semantic results, private ${document.content.markdown?.length}-character handoff; immutable source hashes preserved.`,
    );
  });
});
