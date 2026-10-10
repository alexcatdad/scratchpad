import { execFileSync, spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer, type Server } from "node:http";
import { resolve } from "node:path";
import { expect, test } from "@playwright/test";
import postgres from "postgres";
import { sshProof } from "../apps/web/src/lib/ssh-proof";
import { createApi } from "../apps/web/src/server/api";
import { createWorkflowHarness, root } from "./workflow-harness";

const { cleanupDocker, command, start, stop, temporary } =
  createWorkflowHarness();

// Reuse the production frontend driver. Only the external GitHub service and
// clock are synthetic; browser HTTP and subprocess MCP use the real API.
const origin = "http://localhost:3102";
let now = Date.now();
let available = true;
let published = true;
let api: ReturnType<typeof createApi>;
let proxy: Server;
const key = resolve(temporary, "github-identity");
let publicKey: string;
let extraPublicKey: string | undefined;
let account = { id: 987654321, login: "synthetic-owner", type: "User" };
let databaseUrl: string | undefined;
let databaseAdmin: ReturnType<typeof postgres> | undefined;
const databaseName = `scratchpad_github_e2e_${randomUUID().replaceAll("-", "")}`;
function openApi() {
  api = createApi({
    origin,
    databasePath: resolve(temporary, "github.sqlite"),
    databaseUrl,
    clock: () => now,
    githubOAuth: {
      clientId: "synthetic-client",
      clientSecret: "synthetic-secret",
    },
    githubAuthFetch: async (input) => {
      if (!available) return new Response("unavailable", { status: 503 });
      const url = String(input);
      if (url.endsWith("/access_token"))
        return Response.json({
          access_token: "synthetic-token",
          token_type: "bearer",
          scope: "",
        });
      if (url.includes("/keys?"))
        return Response.json(
          published
            ? [
                { id: 1, key: publicKey },
                ...(extraPublicKey ? [{ id: 3, key: extraPublicKey }] : []),
              ]
            : [],
        );
      if (url.includes("/ssh_signing_keys?"))
        return Response.json(published ? [{ id: 2, key: publicKey }] : []);
      return Response.json(account);
    },
  });
}
test.beforeAll(async () => {
  command("ssh-keygen", ["-q", "-t", "ed25519", "-N", "", "-f", key]);
  publicKey = readFileSync(`${key}.pub`, "utf8").trim();
  await start();
  if (process.env.SCRATCHPAD_E2E_DATABASE_URL) {
    databaseAdmin = postgres(process.env.SCRATCHPAD_E2E_DATABASE_URL, {
      max: 1,
    });
    await databaseAdmin.unsafe(`CREATE DATABASE "${databaseName}"`);
    const url = new URL(process.env.SCRATCHPAD_E2E_DATABASE_URL);
    url.pathname = `/${databaseName}`;
    databaseUrl = url.toString();
  }
  openApi();
  proxy = createServer(async (req, res) => {
    try {
      const chunks: Buffer[] = [];
      for await (const chunk of req) chunks.push(Buffer.from(chunk));
      const body = Buffer.concat(chunks);
      const headers = new Headers();
      for (const [name, value] of Object.entries(req.headers))
        if (value)
          headers.set(name, Array.isArray(value) ? value.join(", ") : value);
      const response =
        req.url?.startsWith("/api/") || req.url === "/ready"
          ? await api.handleRequest(
              new Request(`${origin}${req.url}`, {
                method: req.method,
                headers,
                ...(body.length ? { body } : {}),
              }),
            )
          : await fetch(`http://localhost:3100${req.url}`, { headers });
      res.statusCode = response.status;
      response.headers.forEach((value, name) => {
        if (
          name !== "set-cookie" &&
          name !== "content-encoding" &&
          name !== "content-length"
        )
          res.setHeader(name, value);
      });
      const cookies = response.headers.getSetCookie();
      if (cookies.length) res.setHeader("set-cookie", cookies);
      res.end(Buffer.from(await response.arrayBuffer()));
    } catch (error) {
      res.statusCode = 500;
      res.end(String(error));
    }
  });
  await new Promise<void>((done) => proxy.listen(3102, done));
});
test.afterAll(async () => {
  if (proxy) await new Promise<void>((done) => proxy.close(() => done()));
  await api?.close();
  if (databaseAdmin) {
    await databaseAdmin.unsafe(`DROP DATABASE "${databaseName}" WITH (FORCE)`);
    await databaseAdmin.end();
  }
  await stop();
  cleanupDocker();
  rmSync(temporary, { recursive: true, force: true });
});

async function machineSession() {
  const response = await fetch(`${origin}/api/v1/auth/mcp/challenge`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ publicKey }),
  });
  if (!response.ok) return { status: response.status, token: "" };
  const challenge = await response.json();
  const signature = execFileSync(
    "ssh-keygen",
    ["-Y", "sign", "-f", key, "-n", challenge.namespace],
    {
      input: sshProof(challenge, publicKey, origin, "ssh_login"),
      encoding: "utf8",
    },
  );
  const verified = await fetch(`${origin}/api/v1/auth/mcp/verify`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({
      publicKey,
      challengeId: challenge.challengeId,
      signature,
    }),
  });
  const body = await verified.json();
  return { status: verified.status, token: String(body.accessToken ?? "") };
}
async function readProjects(token: string) {
  return await fetch(`${origin}/api/v1/projects`, {
    headers: { Authorization: `Bearer ${token}` },
  });
}
async function realMcp(args: string[], env = process.env) {
  return await new Promise<string>((done, reject) => {
    const child = spawn("go", args, {
      cwd: resolve(root, "mcp"),
      env,
      stdio: ["ignore", "pipe", "pipe"],
    });
    let output = "";
    child.stdout.on("data", (data) => {
      output += data;
    });
    child.stderr.on("data", (data) => {
      output += data;
    });
    const timeout = setTimeout(() => {
      child.kill("SIGKILL");
      reject(new Error(`MCP timed out: ${output}`));
    }, 90_000);
    child.on("error", (error) => {
      clearTimeout(timeout);
      reject(error);
    });
    child.on("exit", (code) => {
      clearTimeout(timeout);
      code === 0
        ? done(output)
        : reject(new Error(`MCP exited ${code}: ${output}`));
    });
  });
}
test("GitHub owner onboarding, cached MCP access, revocation and recovery preserve memory", async ({
  page,
}) => {
  await page.route(
    "https://github.com/login/oauth/authorize**",
    async (route) => {
      const state = new URL(route.request().url()).searchParams.get("state");
      await route.fulfill({
        status: 302,
        headers: {
          location: `${origin}/api/v1/auth/github/callback?code=synthetic-code&state=${state}`,
        },
      });
    },
  );
  test.setTimeout(240_000);
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
  await expect((await fetch(`${origin}/ready`)).json()).resolves.toMatchObject({
    database: databaseUrl ? "postgres" : "sqlite",
  });
  const token = await api.auth.createSetupToken();
  await page.goto(origin);
  await page.getByLabel("Setup or recovery token").fill(token);
  await page
    .getByRole("button", { name: "Sign in with GitHub", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Project memory", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "GitHub access", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("@synthetic-owner", { exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText(/GitHub-managed · authentication, signing/),
  ).toBeVisible();
  await page.getByRole("button", { name: "Add passkey", exact: true }).click();
  await expect(
    page.getByText("Additional passkey", { exact: true }),
  ).toBeVisible();

  // A GitHub-published key needs no manual enrollment. The shipped Go MCP
  // executable proves possession, writes fixtures, then reads scoped context.
  const workspace = resolve(temporary, "github-repo");
  command("git", ["init", workspace]);
  command(
    "git",
    [
      "remote",
      "add",
      "origin",
      "https://github.com/example/github-owner-fixture.git",
    ],
    workspace,
  );
  const binary = process.env.SCRATCHPAD_E2E_MCP_BINARY
    ? resolve(root, process.env.SCRATCHPAD_E2E_MCP_BINARY)
    : resolve(temporary, "scratchpad-mcp");
  if (!process.env.SCRATCHPAD_E2E_MCP_BINARY)
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
    workspace,
    "-binary",
    binary,
    "-state",
    resolve(temporary, "github-mcp-state.json"),
  ];
  expect(await realMcp([...args, "-phase", "capture"])).toContain(
    "PASS capture",
  );
  const unavailableSignerArgs = [...args];
  unavailableSignerArgs[unavailableSignerArgs.indexOf("-signing-key") + 1] =
    resolve(temporary, "missing-signing-key");
  await expect(
    realMcp([...unavailableSignerArgs, "-phase", "verify"]),
  ).rejects.toThrow(/sign challenge \(unlock your SSH agent\/key\)/);
  // Exercise the connect skill with two agent-backed candidates and an
  // existing Git signing preference, then verify real stdio MCP access.
  const secondKey = resolve(temporary, "github-second-identity");
  command("ssh-keygen", ["-q", "-t", "ed25519", "-N", "", "-f", secondKey]);
  extraPublicKey = readFileSync(`${secondKey}.pub`, "utf8").trim();
  command("git", ["config", "gpg.format", "ssh"], workspace);
  command("git", ["config", "user.signingkey", `${secondKey}.pub`], workspace);
  const agentOutput = command("ssh-agent", ["-s"]);
  const socket = agentOutput.match(/SSH_AUTH_SOCK=([^;]+);/)?.[1];
  const agentPid = agentOutput.match(/SSH_AGENT_PID=(\d+);/)?.[1];
  if (!socket || !agentPid)
    throw new Error("Disposable SSH agent did not start");
  const agentEnv = {
    ...process.env,
    SSH_AUTH_SOCK: socket,
    SSH_AGENT_PID: agentPid,
  };
  const agentCommand = (binary: string, arguments_: string[]) =>
    execFileSync(binary, arguments_, {
      env: agentEnv,
      cwd: workspace,
      encoding: "utf8",
      timeout: 60_000,
    });
  try {
    agentCommand("ssh-add", [key, secondKey]);
    await page
      .getByRole("button", { name: "Synchronize keys", exact: true })
      .click();
    await expect
      .poll(
        async () =>
          (
            await (
              await page.request.get(`${origin}/api/v1/auth/github`)
            ).json()
          ).keys.length,
      )
      .toBe(2);
    const status = await (
      await page.request.get(`${origin}/api/v1/auth/github`)
    ).json();
    const canonical = (value: string) =>
      value.trim().split(/\s+/).slice(0, 2).join(" ");
    const candidates = agentCommand("ssh-add", ["-L"])
      .trim()
      .split("\n")
      .filter((candidate) =>
        status.keys.some(
          (entry: { publicKey: string }) =>
            entry.publicKey === canonical(candidate),
        ),
      );
    expect(candidates).toHaveLength(2);
    const preference = agentCommand("git", [
      "config",
      "--get",
      "user.signingkey",
    ]).trim();
    const preferredPublicKey = canonical(readFileSync(preference, "utf8"));
    const selected = candidates.find(
      (candidate) => canonical(candidate) === preferredPublicKey,
    );
    expect(selected).toBeDefined();
    const selectedPath = resolve(temporary, "selected-agent-key.pub");
    writeFileSync(selectedPath, `${selected}\n`);
    const agentArgs = [...args];
    agentArgs[agentArgs.indexOf("-public-key") + 1] = selectedPath;
    agentArgs[agentArgs.indexOf("-signing-key") + 1] = "";
    expect(await realMcp([...agentArgs, "-phase", "read"], agentEnv)).toContain(
      "PASS read",
    );
    await expect(
      realMcp([...agentArgs, "-phase", "read"], {
        ...agentEnv,
        SSH_AUTH_SOCK: resolve(temporary, "absent-agent.sock"),
      }),
    ).rejects.toThrow(/sign challenge \(unlock your SSH agent\/key\)/);
    // No preference leaves a real ambiguity. Do not guess a credential.
    agentCommand("git", ["config", "--unset", "user.signingkey"]);
    expect(() =>
      agentCommand("git", ["config", "--get", "user.signingkey"]),
    ).toThrow();
  } finally {
    agentCommand("ssh-agent", ["-k"]);
    extraPublicKey = undefined;
    await page
      .getByRole("button", { name: "Synchronize keys", exact: true })
      .click();
    await expect
      .poll(
        async () =>
          (
            await (
              await page.request.get(`${origin}/api/v1/auth/github`)
            ).json()
          ).keys.length,
      )
      .toBe(1);
  }
  const enrolled = await machineSession();
  expect(enrolled.status).toBe(200);
  expect((await readProjects(enrolled.token)).ok).toBe(true);

  available = false;
  await page
    .getByRole("button", { name: "Synchronize keys", exact: true })
    .click();
  await expect(page.getByRole("alert").first()).toContainText(
    "GitHub is unavailable",
  );
  now += 23 * 60 * 60 * 1000;
  const renewed = await machineSession();
  expect(renewed.status).toBe(200);
  await api.close();
  openApi();
  expect(await realMcp([...args, "-phase", "verify"])).toContain("PASS verify");
  now += 60 * 60 * 1000 + 1;
  expect((await readProjects(renewed.token)).status).toBe(401);
  expect((await machineSession()).status).toBe(401);
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Welcome back.", exact: true }),
  ).toBeVisible();
  await page
    .getByRole("button", { name: "Sign in with GitHub", exact: true })
    .click();
  await expect(page.getByRole("alert")).toContainText("GitHub is unavailable");
  // Independent dashboard access survives the outage and the cache deadline.
  await page
    .getByRole("button", { name: "Sign in with passkey", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Project memory", exact: true }),
  ).toBeVisible();
  available = true;
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page
    .getByRole("button", { name: "Synchronize keys", exact: true })
    .click();
  await expect(
    page.getByText("GitHub keys synchronized.", { exact: true }),
  ).toBeVisible();
  const restored = await machineSession();
  expect(restored.status).toBe(200);

  published = false;
  await page
    .getByRole("button", { name: "Synchronize keys", exact: true })
    .click();
  await expect(
    page.getByText("No published SSH keys are available.", { exact: false }),
  ).toBeVisible();
  expect((await readProjects(restored.token)).status).toBe(401);
  expect((await machineSession()).status).toBe(401);
  published = true;
  await page
    .getByRole("button", { name: "Synchronize keys", exact: true })
    .click();
  await expect(page.getByRole("button", { name: /^Block key / })).toBeVisible();
  const republished = await machineSession();
  expect(republished.status).toBe(200);
  await page.getByRole("button", { name: /^Block key / }).click();
  await expect(
    page.getByRole("button", { name: /^Unblock key / }),
  ).toBeVisible();
  expect((await readProjects(republished.token)).status).toBe(401);
  await api.close();
  openApi();
  await page
    .getByRole("button", { name: "Synchronize keys", exact: true })
    .click();
  await expect(
    page.getByRole("button", { name: /^Unblock key / }),
  ).toBeVisible();
  expect((await machineSession()).status).toBe(401);
  await page.getByRole("button", { name: /^Unblock key / }).click();
  await expect(page.getByRole("button", { name: /^Block key / })).toBeVisible();
  expect((await machineSession()).status).toBe(200);

  // Recovery replaces the account binding without losing project memory.
  const recovery = await api.auth.createSetupToken(true);
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await page
    .getByRole("button", { name: "Recover access", exact: true })
    .click();
  await page.getByLabel("Setup or recovery token").fill(recovery);
  account = { id: 987654322, login: "synthetic-recovered-owner", type: "User" };
  await page
    .getByRole("button", { name: "Recover with GitHub", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Project memory", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await expect(
    page.getByText("@synthetic-recovered-owner", { exact: true }),
  ).toBeVisible();

  const beforeUnlink = await machineSession();
  expect(beforeUnlink.status).toBe(200);
  const state = JSON.parse(
    readFileSync(resolve(temporary, "github-mcp-state.json"), "utf8"),
  );
  const retainedRecord = await fetch(
    `${origin}/api/v1/records/${state.captures[0].recordId}`,
    { headers: { Authorization: `Bearer ${beforeUnlink.token}` } },
  );
  expect(retainedRecord.ok).toBe(true);
  expect((await retainedRecord.json()).record.title).toBe(
    "Integration decision",
  );
  await page
    .getByRole("region", { name: "GitHub access", exact: true })
    .screenshot({ path: "/tmp/scratchpad-github-settings.png" });
  await page
    .getByRole("button", { name: "Disconnect GitHub", exact: true })
    .click();
  await expect(
    page.getByText(
      "GitHub disconnected. Independent credentials remain available.",
      { exact: true },
    ),
  ).toBeVisible();
  expect((await readProjects(beforeUnlink.token)).status).toBe(401);
  expect((await machineSession()).status).toBe(401);
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
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
