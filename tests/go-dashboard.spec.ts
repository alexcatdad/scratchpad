import { execFileSync, spawn } from "node:child_process";
import { chmodSync, mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { expect, test } from "@playwright/test";

test("retained React dashboard enrolls and captures through authoritative Go API", async ({
  page,
}) => {
  const dir = mkdtempSync(join(tmpdir(), "scratchpad-go-dashboard-"));
  chmodSync(dir, 0o700);
  const children: ReturnType<typeof spawn>[] = [];
  const exits: Promise<void>[] = [];
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
  const uiPort = await port(),
    apiPort = await port();
  const origin = `http://localhost:${apiPort}`;
  const uiOrigin = `http://127.0.0.1:${uiPort}`;
  const start = (file: string, args: string[], env: NodeJS.ProcessEnv) => {
    const child = spawn(file, args, { env, stdio: "ignore" });
    children.push(child);
    exits.push(
      new Promise<void>((done, fail) => {
        child.once("error", fail);
        child.once("exit", (code) =>
          code === 0
            ? done()
            : fail(new Error("Dashboard fixture exited unsuccessfully")),
        );
      }),
    );
  };
  try {
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
    };
    const token =
      execFileSync(binary, ["setup"], { env, encoding: "utf8" })
        .trim()
        .split("\n")
        .at(-1) ?? "";
    start(process.execPath, [resolve("apps/web/server.mjs")], {
      ...env,
      PORT: String(uiPort),
      HOST: "127.0.0.1",
      SCRATCHPAD_UI_ONLY: "true",
      SCRATCHPAD_DATABASE_PATH: join(dir, "unused-ui.sqlite"),
    });
    start(binary, ["serve"], env);
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
      page.getByRole("heading", { name: "Go dashboard project", exact: true }),
    ).toBeVisible();
    await page.getByRole("button", { name: "Memory", exact: true }).click();
    await page.getByRole("button", { name: "New record", exact: true }).click();
    await page.getByLabel("Type", { exact: true }).selectOption("finding");
    await page
      .getByLabel("Title", { exact: true })
      .fill("Go dashboard evidence");
    await page
      .getByLabel("Finding", { exact: true })
      .fill("The retained dashboard uses the authoritative Go API.");
    await page
      .getByRole("button", { name: "Save record", exact: true })
      .click();
    await page.getByRole("button", { name: /Go dashboard evidence/ }).click();
    await expect(
      page.getByRole("heading", { name: "Go dashboard evidence", exact: true }),
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
    await Promise.all(exits);
    rmSync(dir, { recursive: true, force: true });
  }
});
