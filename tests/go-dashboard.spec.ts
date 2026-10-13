import { execFileSync, spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import {
  chmodSync,
  mkdtempSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { createServer as createHTTPServer } from "node:http";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { expect, test } from "@playwright/test";
import postgres from "postgres";
import { sshProof } from "../apps/web/src/lib/ssh-proof";

for (const engine of ["sqlite", "postgres"] as const) {
  test(`retained React dashboard captures and configures through Go on ${engine}`, async ({
    page,
  }) => {
    test.skip(
      engine === "postgres" && !process.env.TEST_POSTGRES_URL,
      "TEST_POSTGRES_URL is not configured",
    );
    const dir = mkdtempSync(join(tmpdir(), "scratchpad-go-dashboard-"));
    chmodSync(dir, 0o700);
    const children: ReturnType<typeof spawn>[] = [];
    const exits: Promise<void>[] = [];
    let completionCalls = 0,
      embeddingCalls = 0;
    let providerUnavailable = false;
    let analysisSourceId = "";
    let generateDocument = false;
    let rejectableAnalysis = false;
    const provider = createHTTPServer((request, response) => {
      request.resume();
      response.setHeader("Content-Type", "application/json");
      if (providerUnavailable) {
        response.statusCode = 503;
        response.end(
          JSON.stringify({
            error: { message: "synthetic-dashboard-provider-token" },
          }),
        );
      } else if (
        request.headers.authorization !==
        "Bearer synthetic-dashboard-provider-token"
      ) {
        response.writeHead(401).end();
      } else if (request.url === "/v1/embeddings") {
        embeddingCalls++;
        response.end(
          JSON.stringify({ data: [{ index: 0, embedding: [1, 0] }] }),
        );
      } else if (request.url === "/v1/chat/completions") {
        completionCalls++;
        response.end(
          JSON.stringify({
            choices: [
              {
                message: {
                  content: analysisSourceId
                    ? JSON.stringify({
                        artifacts: [
                          {
                            kind: generateDocument ? "export" : "summary",
                            title: generateDocument
                              ? "Dashboard private handoff"
                              : rejectableAnalysis
                                ? "Dashboard interpretation to reject"
                                : "Dashboard source-backed interpretation",
                            content: {
                              text: "A provisional summary linked to the saved finding.",
                              ...(generateDocument
                                ? {
                                    markdown:
                                      "## Synthetic handoff\n\nPreserve the cited finding.",
                                  }
                                : {}),
                            },
                            sourceRecordIds: [analysisSourceId],
                          },
                        ],
                      })
                    : '{"ok":true}',
                },
              },
            ],
          }),
        );
      } else {
        response.writeHead(404).end();
      }
    });
    let databaseAdmin: ReturnType<typeof postgres> | undefined;
    let databaseCreated = false;
    const databaseName = `scratchpad_dashboard_${randomUUID().replaceAll("-", "")}`;
    const port = () =>
      new Promise<number>((done) => {
        const listener = createServer();
        listener.listen(0, "127.0.0.1", () => {
          const address = listener.address();
          if (!address || typeof address === "string")
            throw new Error("Missing port");
          listener.close(() => done(address.port));
        });
      });
    const uiPort = await port();
    let apiPort = await port();
    while (apiPort === uiPort) apiPort = await port();
    const origin = `http://localhost:${apiPort}`;
    const uiOrigin = `http://127.0.0.1:${uiPort}`;
    const start = (
      label: string,
      file: string,
      args: string[],
      env: NodeJS.ProcessEnv,
    ) => {
      const child = spawn(file, args, { env, stdio: "ignore" });
      children.push(child);
      exits.push(
        new Promise<void>((done, fail) => {
          child.once("error", fail);
          child.once("exit", (code, signal) =>
            code === 0
              ? done()
              : fail(
                  new Error(
                    `${label} fixture exited: code=${code}, signal=${signal}, UI port=${uiPort}, API port=${apiPort}`,
                  ),
                ),
          );
        }),
      );
    };
    try {
      await new Promise<void>((done) => provider.listen(0, "127.0.0.1", done));
      const providerAddress = provider.address();
      if (!providerAddress || typeof providerAddress === "string")
        throw new Error("Missing provider fixture address");
      const installationKey = join(dir, "installation-key");
      writeFileSync(
        installationKey,
        `${Buffer.alloc(32, 44).toString("base64")}\n`,
        { mode: 0o600 },
      );
      const binary = join(dir, "server");
      execFileSync(
        "go",
        ["build", "-race", "-o", binary, "./cmd/scratchpad-server"],
        { cwd: "backend" },
      );
      const env = {
        ...process.env,
        SCRATCHPAD_DATABASE_URL: "",
        SCRATCHPAD_DATABASE_PATH: join(dir, "memory.sqlite"),
        SCRATCHPAD_PUBLIC_URL: origin,
        SCRATCHPAD_LISTEN_ADDR: `127.0.0.1:${apiPort}`,
        SCRATCHPAD_DASHBOARD_URL: uiOrigin,
        SCRATCHPAD_GITHUB_CLIENT_ID: "",
        SCRATCHPAD_GITHUB_CLIENT_SECRET: "",
        SCRATCHPAD_ENCRYPTION_KEY_FILE: installationKey,
      };
      if (engine === "postgres") {
        databaseAdmin = postgres(process.env.TEST_POSTGRES_URL ?? "", {
          max: 1,
        });
        await databaseAdmin.unsafe(`CREATE DATABASE "${databaseName}"`);
        databaseCreated = true;
        const url = new URL(process.env.TEST_POSTGRES_URL ?? "");
        url.pathname = `/${databaseName}`;
        env.SCRATCHPAD_DATABASE_URL = url.toString();
      }
      const token =
        execFileSync(binary, ["setup"], { env, encoding: "utf8" })
          .trim()
          .split("\n")
          .at(-1) ?? "";
      start("Dashboard", process.execPath, [resolve("apps/web/server.mjs")], {
        ...env,
        PORT: String(uiPort),
        HOST: "127.0.0.1",
        SCRATCHPAD_UI_ONLY: "true",
        SCRATCHPAD_ENCRYPTION_KEY_FILE: "",
        SCRATCHPAD_DATABASE_PATH: join(dir, "unused-ui.sqlite"),
      });
      start("Go API", binary, ["serve"], env);
      await expect
        .poll(async () => {
          try {
            return (await fetch(`${origin}/ready`)).status;
          } catch {
            return 0;
          }
        })
        .toBe(200);
      await expect
        .poll(async () => {
          try {
            return (await fetch(uiOrigin)).status;
          } catch {
            return 0;
          }
        })
        .toBe(200);
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
      await expect(page.getByLabel("Setup or recovery token")).toBeVisible();
      await page.getByLabel("Setup or recovery token").fill(token);
      await page
        .getByRole("button", { name: "Register passkey", exact: true })
        .click();
      await expect(
        page.getByRole("heading", { name: "Project memory", exact: true }),
      ).toBeVisible();
      expect((await fetch(`${uiOrigin}/ready`)).status).toBe(503);
      await page.getByRole("button", { name: "Projects", exact: true }).click();
      await page
        .getByLabel("Project name", { exact: true })
        .fill("Go dashboard project");
      await page
        .getByRole("button", { name: "Create project", exact: true })
        .click();
      await expect(
        page.getByRole("heading", {
          name: "Go dashboard project",
          exact: true,
        }),
      ).toBeVisible();
      await page.getByText("Project settings", { exact: true }).click();
      await page
        .getByLabel("Allow repository mirroring", { exact: true })
        .check();
      await page
        .getByLabel("Allow AI processing for this project", { exact: true })
        .check();
      const savedProject = page.waitForResponse(
        (response) =>
          response.url().endsWith("/settings") &&
          response.request().method() === "PATCH",
      );
      await page
        .getByRole("button", { name: "Save project settings", exact: true })
        .click();
      expect((await savedProject).status()).toBe(200);
      await page.reload();
      await page.getByRole("button", { name: "Projects", exact: true }).click();
      await page.getByText("Project settings", { exact: true }).click();
      await expect(
        page.getByLabel("Allow repository mirroring", { exact: true }),
      ).toBeChecked();
      await page.getByRole("button", { name: "Memory", exact: true }).click();
      await page
        .getByRole("button", { name: "New record", exact: true })
        .click();
      await page.getByLabel("Type", { exact: true }).selectOption("finding");
      await page
        .getByLabel("Title", { exact: true })
        .fill("Go dashboard evidence");
      await page
        .getByLabel("Finding", { exact: true })
        .fill("The retained dashboard uses the authoritative Go API.");
      const savedRecord = page.waitForResponse(
        (response) =>
          response.url().endsWith("/api/v1/records") &&
          response.request().method() === "POST",
      );
      await page
        .getByRole("button", { name: "Save record", exact: true })
        .click();
      const capturedRecord = (await (await savedRecord).json()) as {
        record: { id: string; projectId: string };
      };
      await page.getByRole("button", { name: /Go dashboard evidence/ }).click();
      await expect(
        page.getByRole("heading", {
          name: "Go dashboard evidence",
          exact: true,
        }),
      ).toBeVisible();
      await page.getByRole("button", { name: "Settings", exact: true }).click();
      await expect(
        page.getByText(
          `Database: ${engine === "postgres" ? "PostgreSQL" : "SQLite"}.`,
          { exact: false },
        ),
      ).toBeVisible();
      await page
        .getByLabel("Owner display name", { exact: true })
        .fill("Dashboard test owner");
      await page
        .getByRole("button", { name: "Save profile", exact: true })
        .click();
      await expect(
        page.getByText("Profile saved.", { exact: true }),
      ).toBeVisible();
      const signingKey = join(dir, "dashboard-ssh-key");
      execFileSync("ssh-keygen", [
        "-q",
        "-t",
        "ed25519",
        "-N",
        "",
        "-f",
        signingKey,
      ]);
      const publicKey = readFileSync(`${signingKey}.pub`, "utf8")
        .trim()
        .split(/\s+/)
        .slice(0, 2)
        .join(" ");
      const enrollment = page.locator("section").filter({
        has: page.getByRole("heading", {
          name: "Connect an MCP key",
          exact: true,
        }),
      });
      await enrollment
        .getByLabel("Public key", { exact: true })
        .fill(publicKey);
      await enrollment
        .getByRole("button", {
          name: "Create enrollment challenge",
          exact: true,
        })
        .click();
      const transcript = enrollment.locator("pre");
      await expect(transcript).toBeVisible();
      const enrollmentSignature = execFileSync(
        "ssh-keygen",
        [
          "-Y",
          "sign",
          "-f",
          signingKey,
          "-n",
          await enrollment.locator("code").innerText(),
        ],
        {
          encoding: "utf8",
          input: await transcript.innerText(),
          stdio: ["pipe", "pipe", "ignore"],
        },
      );
      await enrollment
        .getByLabel("Armored SSH signature", { exact: true })
        .fill(enrollmentSignature);
      await enrollment
        .getByRole("button", { name: "Verify and enroll key", exact: true })
        .click();
      await expect(
        page.getByText("MCP key enrolled.", { exact: true }),
      ).toBeVisible();
      const credential = page.locator("li").filter({
        has: page.getByText("MCP key", { exact: true }),
      });
      await expect(
        credential.getByText("ssh · Independent local credential", {
          exact: true,
        }),
      ).toBeVisible();
      const challengeResponse = await fetch(
        `${origin}/api/v1/auth/mcp/challenge`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ publicKey }),
        },
      );
      expect(challengeResponse.status).toBe(200);
      const challenge = (await challengeResponse.json()) as {
        namespace: string;
        challengeId: string;
      };
      const signature = execFileSync(
        "ssh-keygen",
        ["-Y", "sign", "-f", signingKey, "-n", challenge.namespace],
        {
          encoding: "utf8",
          input: sshProof(challenge, publicKey, origin, "ssh_login"),
          stdio: ["pipe", "pipe", "ignore"],
        },
      );
      const verified = await fetch(`${origin}/api/v1/auth/mcp/verify`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          challengeId: challenge.challengeId,
          publicKey,
          signature,
        }),
      });
      expect(verified.status).toBe(200);
      const session = (await verified.json()) as { accessToken: string };
      const machineRecord = () =>
        fetch(`${origin}/api/v1/records/${capturedRecord.record.id}`, {
          headers: { Authorization: `Bearer ${session.accessToken}` },
        });
      const authorizedRecord = await machineRecord();
      expect(authorizedRecord.status).toBe(200);
      expect((await authorizedRecord.json()).record.title).toBe(
        "Go dashboard evidence",
      );
      await credential
        .getByRole("button", { name: "Revoke", exact: true })
        .click();
      await expect(credential.getByText(/Revoked/)).toBeVisible();
      const deniedRecord = await machineRecord();
      expect(deniedRecord.status).toBe(401);
      expect((await deniedRecord.json()).error.code).toBe("AUTH_INVALID");
      await page.reload();
      await page.getByRole("button", { name: "Settings", exact: true }).click();
      await expect(credential.getByText(/Revoked/)).toBeVisible();
      await expect(
        credential.getByRole("button", { name: "Revoke", exact: true }),
      ).toHaveCount(0);
      const ai = page.getByRole("region", {
        name: "AI provider settings",
        exact: true,
      });
      await ai.getByLabel("Enable AI processing", { exact: true }).check();
      await ai
        .getByLabel("OpenAI-compatible base URL", { exact: false })
        .fill(`http://127.0.0.1:${providerAddress.port}/v1`);
      await ai
        .getByLabel("LLM model", { exact: true })
        .fill("synthetic-completion");
      await ai
        .getByLabel("Embedding model", { exact: true })
        .fill("synthetic-embedding");
      await ai.getByLabel("Embedding dimensions", { exact: true }).fill("2");
      await ai
        .getByLabel("Analysis interval (minutes)", { exact: false })
        .fill("0");
      await ai
        .getByLabel("Provider API key", { exact: false })
        .fill("synthetic-dashboard-provider-token");
      await ai
        .getByRole("button", { name: "Save AI settings", exact: true })
        .click();
      await expect(
        ai.getByText(
          "AI settings saved. Project participation is controlled separately.",
          { exact: true },
        ),
      ).toBeVisible();
      await expect(
        ai.getByLabel("Provider API key", { exact: false }),
      ).toHaveValue("");
      await expect(
        ai.getByPlaceholder("Key stored; leave blank to keep", { exact: true }),
      ).toBeVisible();
      await ai
        .getByRole("button", { name: "Test saved provider", exact: true })
        .click();
      await expect(
        ai.getByText(
          "Provider test passed using synthetic text for completion and embeddings.",
          { exact: true },
        ),
      ).toBeVisible();
      expect(completionCalls).toBe(1);
      expect(embeddingCalls).toBe(1);
      analysisSourceId = capturedRecord.record.id;
      await page.getByRole("button", { name: "Insights", exact: true }).click();
      await page
        .getByLabel("Insight project", { exact: false })
        .selectOption({ label: "Go dashboard project" });
      await page
        .getByRole("button", { name: "Analyze memory", exact: true })
        .click();
      const suggestion = page.locator("article").filter({
        has: page.getByRole("heading", {
          name: "Dashboard source-backed interpretation",
          exact: true,
        }),
      });
      await expect(suggestion).toBeVisible({ timeout: 20_000 });
      await suggestion
        .getByRole("button", { name: "Accept suggestion", exact: true })
        .click();
      await expect(
        page.getByText("Suggestion accepted with an audit entry.", {
          exact: true,
        }),
      ).toBeVisible();
      await expect(
        suggestion.getByText("Accepted", { exact: true }),
      ).toBeVisible();
      await suggestion.getByRole("button", { name: /^Source / }).click();
      await expect(
        page.getByRole("heading", {
          name: "Go dashboard evidence",
          exact: true,
        }),
      ).toBeVisible();
      await expect(
        page
          .getByText("The retained dashboard uses the authoritative Go API.", {
            exact: true,
          })
          .first(),
      ).toBeVisible();
      await page.getByRole("button", { name: "Insights", exact: true }).click();
      await page
        .getByLabel("Insight project", { exact: false })
        .selectOption({ label: "Go dashboard project" });
      rejectableAnalysis = true;
      await page
        .getByRole("button", { name: "Analyze memory", exact: true })
        .click();
      const rejectedSuggestion = page.locator("article").filter({
        has: page.getByRole("heading", {
          name: "Dashboard interpretation to reject",
          exact: true,
        }),
      });
      await expect(rejectedSuggestion).toBeVisible({ timeout: 20_000 });
      await rejectedSuggestion
        .getByRole("button", { name: "Reject suggestion", exact: true })
        .click();
      await expect(
        page.getByText("Suggestion rejected.", { exact: true }),
      ).toBeVisible();
      await page.reload();
      await page.getByRole("button", { name: "Insights", exact: true }).click();
      await page
        .getByLabel("Insight project", { exact: false })
        .selectOption({ label: "Go dashboard project" });
      await expect(
        rejectedSuggestion.getByText("Rejected", { exact: true }),
      ).toBeVisible();
      await expect(
        rejectedSuggestion.getByRole("button", { name: "Accept suggestion" }),
      ).toHaveCount(0);
      providerUnavailable = true;
      await page
        .getByRole("button", { name: "Build embeddings", exact: true })
        .click();
      const embeddingJob = page
        .locator("li")
        .filter({ has: page.getByText("Embed", { exact: true }) });
      await expect(
        embeddingJob.getByText("Failed · Attempt 3", { exact: true }),
      ).toBeVisible({ timeout: 30_000 });
      await expect(
        embeddingJob.getByText(
          "AI provider is unavailable or returned invalid output.",
          { exact: true },
        ),
      ).toBeVisible();
      providerUnavailable = false;
      await embeddingJob
        .getByRole("button", { name: "Retry job", exact: true })
        .click();
      await expect(
        page.getByText("Failed job queued for another attempt.", {
          exact: true,
        }),
      ).toBeVisible();
      await expect(
        embeddingJob.getByText("Completed · Attempt 1", { exact: true }),
      ).toBeVisible({ timeout: 20_000 });
      await page
        .getByLabel("Meaning to search for", { exact: true })
        .fill("Which finding preserves authoritative project memory?");
      await page
        .getByRole("button", { name: "Search by meaning", exact: true })
        .click();
      await expect(
        page.getByText("Found 1 semantic matches.", { exact: true }),
      ).toBeVisible();
      const match = page.locator("article").filter({
        has: page.getByRole("button", {
          name: "Go dashboard evidence",
          exact: true,
        }),
      });
      await expect(
        match.getByText(
          "The retained dashboard uses the authoritative Go API.",
          { exact: false },
        ),
      ).toBeVisible();
      await expect(match.getByText(/Similarity 1\.000/)).toBeVisible();
      generateDocument = true;
      await page
        .getByRole("button", { name: "Generate document", exact: true })
        .click();
      const document = page.locator("article").filter({
        has: page.getByRole("heading", {
          name: "Dashboard private handoff",
          exact: true,
        }),
      });
      await expect(document).toBeVisible({ timeout: 20_000 });
      const markdownDownload = page.waitForEvent("download");
      await document
        .getByRole("button", { name: "Download Markdown", exact: true })
        .click();
      const markdownPath = await (await markdownDownload).path();
      if (!markdownPath) throw new Error("Missing generated document");
      const markdown = readFileSync(markdownPath, "utf8");
      expect(markdown).toContain("AI-generated draft.");
      expect(markdown).toContain("## Synthetic handoff");
      expect(markdown).toContain("## Source records");
      expect(markdown).toContain(capturedRecord.record.id);
      expect(markdown).toContain("Go dashboard evidence");
      expect(markdown).not.toContain("synthetic-dashboard-provider-token");
      await page.getByRole("button", { name: "Projects", exact: true }).click();
      await page.getByText("Project settings", { exact: true }).click();
      await page
        .getByLabel("Allow AI processing for this project", { exact: true })
        .uncheck();
      const disabledSettings = page.waitForResponse(
        (response) =>
          response
            .url()
            .endsWith(
              `/projects/${capturedRecord.record.projectId}/settings`,
            ) && response.request().method() === "PATCH",
      );
      const disabledProjects = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname.endsWith("/projects") &&
          response.request().method() === "GET",
      );
      await page
        .getByRole("button", { name: "Save project settings", exact: true })
        .click();
      expect((await disabledSettings).status()).toBe(200);
      await (await disabledProjects).finished();
      await page.getByRole("button", { name: "Insights", exact: true }).click();
      await page
        .getByLabel("Insight project", { exact: false })
        .selectOption({ label: "Go dashboard project" });
      await expect(suggestion).toHaveCount(0);
      await expect(rejectedSuggestion).toHaveCount(0);
      await expect(document).toHaveCount(0);
      await expect(
        page.getByRole("button", { name: "Analyze memory", exact: true }),
      ).toBeDisabled();
      await expect(
        page.getByText(
          "This project must allow AI processing in its project settings.",
          { exact: false },
        ),
      ).toBeVisible();
      await page.getByRole("button", { name: "Projects", exact: true }).click();
      await page.getByText("Project settings", { exact: true }).click();
      await page
        .getByLabel("Allow AI processing for this project", { exact: true })
        .check();
      const enabledSettings = page.waitForResponse(
        (response) =>
          response
            .url()
            .endsWith(
              `/projects/${capturedRecord.record.projectId}/settings`,
            ) && response.request().method() === "PATCH",
      );
      const enabledProjects = page.waitForResponse(
        (response) =>
          new URL(response.url()).pathname.endsWith("/projects") &&
          response.request().method() === "GET",
      );
      await page
        .getByRole("button", { name: "Save project settings", exact: true })
        .click();
      expect((await enabledSettings).status()).toBe(200);
      await (await enabledProjects).finished();
      await page.getByRole("button", { name: "Insights", exact: true }).click();
      await page
        .getByLabel("Insight project", { exact: false })
        .selectOption({ label: "Go dashboard project" });
      await expect(
        page.getByRole("button", { name: "Analyze memory", exact: true }),
      ).toBeEnabled();
      await expect(
        suggestion.getByText("Accepted", { exact: true }),
      ).toBeVisible();
      await expect(
        rejectedSuggestion.getByText("Rejected", { exact: true }),
      ).toBeVisible();
      await expect(document).toBeVisible();
      await page.getByRole("button", { name: "Settings", exact: true }).click();
      providerUnavailable = true;
      await ai
        .getByRole("button", { name: "Test saved provider", exact: true })
        .click();
      await expect(ai.getByRole("alert")).toHaveText(
        "AI provider is unavailable or returned invalid output.",
      );
      const downloaded = page.waitForEvent("download");
      await page
        .getByRole("button", { name: "Download export", exact: true })
        .click();
      const archive = await downloaded;
      const archivePath = await archive.path();
      if (!archivePath) throw new Error("Missing exported archive");
      const contents = readFileSync(archivePath);
      expect(contents.toString()).toContain("Go dashboard evidence");
      expect(contents.toString()).not.toContain(
        "synthetic-dashboard-provider-token",
      );
      expect(await page.locator("body").innerText()).not.toContain(
        "synthetic-dashboard-provider-token",
      );
      await ai
        .getByRole("button", { name: "Remove provider key", exact: true })
        .click();
      await expect(
        ai.getByText("Stored provider key removed.", { exact: true }),
      ).toBeVisible();
      await expect(
        ai.getByPlaceholder("Optional for local providers", { exact: true }),
      ).toBeVisible();
      await page
        .getByLabel("Import Scratchpad export", { exact: true })
        .setInputFiles({
          name: "synthetic-export.json",
          mimeType: "application/json",
          buffer: contents,
        });
      await expect(
        page.getByText(/Imported 0 items; skipped \d+\./),
      ).toBeVisible();
      await page
        .getByLabel("Import JSONL decision log", { exact: true })
        .setInputFiles({
          name: "synthetic-history.jsonl",
          mimeType: "application/x-ndjson",
          buffer: Buffer.from(
            JSON.stringify({
              id: "historical-dashboard-decision",
              date: "2025-01-02",
              title: "Retain the imported history",
              decision: "Keep the historical source unchanged.",
            }),
          ),
        });
      await expect(
        page.getByText("Imported 1 items; skipped 0.", { exact: true }),
      ).toBeVisible();
      await page.getByRole("button", { name: "Memory", exact: true }).click();
      await page
        .getByRole("button", { name: /Retain the imported history/ })
        .click();
      await expect(
        page.getByRole("heading", {
          name: "Retain the imported history",
          exact: true,
        }),
      ).toBeVisible();
      await expect(
        page
          .getByText("Keep the historical source unchanged.", { exact: true })
          .first(),
      ).toBeVisible();
      await page.reload();
      await page.getByRole("button", { name: "Settings", exact: true }).click();
      await expect(
        page.getByLabel("Owner display name", { exact: true }),
      ).toHaveValue("Dashboard test owner");
      await expect(
        ai.getByRole("button", { name: "Remove provider key", exact: true }),
      ).toHaveCount(0);
      await page.getByRole("button", { name: "Sign out", exact: true }).click();
      await expect(
        page.getByRole("heading", { name: "Welcome back.", exact: true }),
      ).toBeVisible();
      await page
        .getByRole("button", { name: "Sign in with passkey", exact: true })
        .click();
      await page.getByRole("button", { name: "Memory", exact: true }).click();
      await expect(
        page.getByRole("heading", { name: "Project memory", exact: true }),
      ).toBeVisible();
      await page.getByRole("button", { name: /Go dashboard evidence/ }).click();
      await expect(
        page.getByRole("heading", {
          name: "Go dashboard evidence",
          exact: true,
        }),
      ).toBeVisible();
      children[0].kill("SIGTERM");
      await exits[0];
      expect(
        await page.evaluate(
          async () =>
            (await (await fetch("/api/v1/auth/status")).json()).authenticated,
        ),
      ).toBe(true);
      expect((await fetch(origin)).status).toBe(503);
    } finally {
      for (const child of children)
        if (child.exitCode === null) child.kill("SIGTERM");
      try {
        await Promise.all(exits);
      } finally {
        try {
          if (databaseAdmin && databaseCreated)
            await databaseAdmin.unsafe(
              `DROP DATABASE "${databaseName}" WITH (FORCE)`,
            );
        } finally {
          await databaseAdmin?.end();
          provider.closeAllConnections();
          await new Promise<void>((done) => provider.close(() => done()));
          rmSync(dir, { recursive: true, force: true });
        }
      }
    }
  });
}
