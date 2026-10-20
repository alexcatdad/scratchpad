import { execFileSync, spawn } from "node:child_process";
import { createHash, randomUUID } from "node:crypto";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { createServer as createHTTPServer, type Server } from "node:http";
import { createServer as createHTTPSServer } from "node:https";
import { connect, type Socket } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { expect, test } from "@playwright/test";
import { createRequire } from "node:module";
import postgres from "postgres";
import { sshProof } from "../apps/web/src/lib/ssh-proof";
import { startDurableMcpService } from "./durable-mcp-service";
import { buildGoServer, runGoAdmin, startGoServer, stopGoServer } from "./go-test-server";

const root = resolve(import.meta.dirname, "..");
const temporary = mkdtempSync(join(tmpdir(), "scratchpad-github-workflow-"));
chmodSync(temporary, 0o700);
const databasePath = resolve(temporary, "github.sqlite");
const serverBinary = resolve(temporary, "scratchpad-server");
let environment: NodeJS.ProcessEnv;
let command: (binary: string, args: string[], cwd?: string) => string;
let server: ReturnType<typeof startGoServer> | undefined;
let serverOutput = "";
let admin: (...args: string[]) => string;
let databaseAdmin: ReturnType<typeof postgres> | undefined;
let databaseName: string | undefined;
let databaseUrl: string | undefined;
let originalDatabaseUrl: string | undefined;
const originalProxyEnvironment = {
  HTTPS_PROXY: process.env.HTTPS_PROXY,
  NO_PROXY: process.env.NO_PROXY,
  SSL_CERT_FILE: process.env.SSL_CERT_FILE,
  SSL_CERT_DIR: process.env.SSL_CERT_DIR,
};
let origin = "";
let available = true;
let published = true;
let github: ReturnType<typeof createHTTPSServer>;
let proxy: Server;
const sockets = new Set<Socket>();
let key: string;
let stopDurableMcpService: (() => Promise<void>) | undefined;
let publicKey: string;
let extraPublicKey: string | undefined;
let account = { id: 987654321, login: "synthetic-owner", type: "User" };
async function unusedPort(): Promise<number> {
  return await new Promise((done) => {
    const listener = createHTTPServer();
    listener.listen(0, "127.0.0.1", () => {
      const address = listener.address();
      if (!address || typeof address === "string") throw new Error("Missing port");
      listener.close(() => done(address.port));
    });
  });
}
test.beforeAll(async () => {
  command = (binary, args, cwd = root) => execFileSync(binary, args, { cwd, encoding: "utf8", timeout: 60_000, ...(binary === "openssl" ? { stdio: "ignore" } : {}) });
  key = resolve(temporary, "github-identity");
  command("ssh-keygen", ["-q", "-t", "ed25519", "-N", "", "-f", key]);
  publicKey = readFileSync(`${key}.pub`, "utf8").trim();
  const tlsKey = resolve(temporary, "github-tls-key.pem");
  const tlsCert = resolve(temporary, "github-tls-cert.pem");
  command("openssl", ["req", "-x509", "-newkey", "rsa:2048", "-nodes", "-days", "1", "-subj", "/CN=Scratchpad GitHub fixture", "-addext", "subjectAltName=DNS:github.com,DNS:api.github.com", "-keyout", tlsKey, "-out", tlsCert]);
  github = createHTTPSServer({ key: readFileSync(tlsKey), cert: readFileSync(tlsCert) }, (req, res) => {
    if (!available) { res.writeHead(503).end("unavailable"); return; }
    res.setHeader("Content-Type", "application/json");
    if (req.url === "/login/oauth/access_token") {
      res.end(JSON.stringify({ access_token: "synthetic-token", token_type: "bearer", scope: "" }));
    } else if (
      req.url === "/user" ||
      req.url === `/user/${account.id}` ||
      req.url === `/users/${account.login}`
    )
      res.end(JSON.stringify(account));
    else if (req.url?.startsWith(`/users/${account.login}/keys`)) res.end(JSON.stringify(published ? [{ id: 1, key: publicKey }, ...(extraPublicKey ? [{ id: 3, key: extraPublicKey }] : [])] : []));
    else if (req.url?.startsWith(`/users/${account.login}/ssh_signing_keys`)) res.end(JSON.stringify(published ? [{ id: 2, key: publicKey }] : []));
    else res.writeHead(404).end();
  });
  github.on("connection", (socket) => { sockets.add(socket); socket.once("close", () => sockets.delete(socket)); });
  await new Promise<void>((done) => github.listen(0, "127.0.0.1", done));
  const address = github.address();
  if (!address || typeof address === "string") throw new Error("Missing GitHub fixture port");
  proxy = createHTTPServer((_req, res) => res.writeHead(403).end());
  proxy.on("connection", (socket) => { sockets.add(socket); socket.once("close", () => sockets.delete(socket)); });
  proxy.on("connect", (req, socket, head) => {
    if (!["github.com:443", "api.github.com:443"].includes(req.url ?? "")) { socket.end("HTTP/1.1 403 Forbidden\r\n\r\n"); return; }
    const upstream = connect(address.port, "127.0.0.1", () => { socket.write("HTTP/1.1 200 Connection Established\r\n\r\n"); upstream.write(head); socket.pipe(upstream); upstream.pipe(socket); });
    upstream.on("error", () => socket.destroy()); socket.on("error", () => upstream.destroy()); socket.once("close", () => upstream.destroy());
  });
  await new Promise<void>((done) => proxy.listen(0, "127.0.0.1", done));
  const proxyAddress = proxy.address();
  if (!proxyAddress || typeof proxyAddress === "string") throw new Error("Missing proxy port");
  process.env.HTTPS_PROXY = `http://127.0.0.1:${proxyAddress.port}`;
  process.env.NO_PROXY = "localhost,127.0.0.1,::1";
  process.env.SSL_CERT_FILE = tlsCert;
  process.env.SSL_CERT_DIR = temporary;
  originalDatabaseUrl = process.env.SCRATCHPAD_E2E_DATABASE_URL;
  if (originalDatabaseUrl) {
    databaseAdmin = postgres(originalDatabaseUrl, { max: 1 });
    databaseName = `scratchpad_github_e2e_${randomUUID().replaceAll("-", "")}`;
    await databaseAdmin.unsafe(`CREATE DATABASE "${databaseName}"`);
    const database = new URL(originalDatabaseUrl);
    database.pathname = `/${databaseName}`;
    databaseUrl = process.env.SCRATCHPAD_E2E_DATABASE_URL = database.toString();
  }
  const port = await unusedPort();
  const uiPort = await unusedPort();
  origin = `http://localhost:${port}`;
  environment = {
    ...process.env,
    SCRATCHPAD_DATABASE_PATH: databasePath,
    SCRATCHPAD_DATABASE_URL: databaseUrl ?? "",
    SCRATCHPAD_LISTEN_ADDR: `127.0.0.1:${port}`,
    SCRATCHPAD_UI_PORT: String(uiPort),
    SCRATCHPAD_PUBLIC_URL: origin,
    SCRATCHPAD_DASHBOARD_URL: `http://127.0.0.1:${uiPort}`,
    SCRATCHPAD_GITHUB_CLIENT_ID: "synthetic-client",
    SCRATCHPAD_GITHUB_CLIENT_SECRET: "synthetic-secret",
    HTTPS_PROXY: `http://127.0.0.1:${proxyAddress.port}`,
    NO_PROXY: "localhost,127.0.0.1,::1",
    SSL_CERT_FILE: tlsCert,
    SSL_CERT_DIR: temporary,
  };
  buildGoServer(root, serverBinary, environment);
  admin = (...args) => runGoAdmin(serverBinary, args, environment, root);
  serverOutput = "";
  server = startGoServer(root, serverBinary, environment, (chunk) => {
    serverOutput += chunk.toString();
  });
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    try {
      if (
        (await fetch(`${origin}/ready`)).ok &&
        (await fetch(origin)).ok
      ) return;
    } catch {}
    await new Promise((done) => setTimeout(done, 50));
  }
  throw new Error(`Go GitHub workflow server did not become ready: ${serverOutput}`);
});
test.afterAll(async () => {
  await stopDurableMcpService?.();
  await stopGoServer(server);
  if (databaseAdmin && databaseName) {
    await databaseAdmin.unsafe(`DROP DATABASE "${databaseName}" WITH (FORCE)`);
    await databaseAdmin.end();
  }
  if (originalDatabaseUrl === undefined) delete process.env.SCRATCHPAD_E2E_DATABASE_URL;
  else process.env.SCRATCHPAD_E2E_DATABASE_URL = originalDatabaseUrl;
  for (const [name, value] of Object.entries(originalProxyEnvironment)) {
    if (value === undefined) delete process.env[name];
    else process.env[name] = value;
  }
  for (const socket of sockets) socket.destroy();
  if (proxy?.listening)
    await new Promise<void>((done) => proxy.close(() => done()));
  if (github?.listening)
    await new Promise<void>((done) => github.close(() => done()));
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
async function restartServer() {
  await stopGoServer(server);
  serverOutput = "";
  server = startGoServer(root, serverBinary, environment, (chunk) => {
    serverOutput += chunk.toString();
  });
  const deadline = Date.now() + 30_000;
  while (Date.now() < deadline) {
    try {
      if (
        (await fetch(`${origin}/ready`)).ok &&
        (await fetch(origin)).ok
      ) return;
    } catch {}
    await new Promise((done) => setTimeout(done, 50));
  }
  throw new Error(`Go GitHub workflow server did not restart: ${serverOutput}`);
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
        : reject(
            new Error(
              `MCP phase ${args[args.indexOf("-phase") + 1]} exited ${code}: ${output}`,
            ),
          );
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
  const token = admin("setup").trim().split("\n").at(-1) ?? "";
  await page.goto(origin);
  await page.getByLabel("Setup or recovery token").fill(token);
  await page
    .getByRole("button", { name: "Sign in with GitHub", exact: true })
    .click();
  await expect(
    page.getByRole("heading", { name: "Project memory", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("button", { name: "Access & MCP", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "GitHub access", exact: true }),
  ).toBeVisible();
  await expect(
    page.getByText("@synthetic-owner", { exact: true }),
  ).toBeVisible();
  const githubStatus = await (await page.request.get(`${origin}/api/v1/auth/github`)).json();
  expect(githubStatus.lastSyncError).toBeNull();
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
    "-service-socket",
    "",
    "-state",
    resolve(temporary, "github-mcp-state.json"),
  ];
  const durableService = await startDurableMcpService({
    environment: process.env,
    origin,
    publicKey: `${key}.pub`,
    signingKey: key,
    workspace,
    binary,
    statePath: resolve(temporary, "github-mcp-state.json"),
    temporary,
  });
  stopDurableMcpService = durableService.stop;
  args[args.indexOf("-service-socket") + 1] = durableService.socketPath;
  expect(await realMcp([...args, "-phase", "capture"])).toContain(
    "PASS capture",
  );
  const unavailableSignerArgs = [...args];
  unavailableSignerArgs[unavailableSignerArgs.indexOf("-signing-key") + 1] =
    resolve(temporary, "missing-signing-key");
  // Caller signing flags cannot override the collector-owned identity.
  expect(
    await realMcp([...unavailableSignerArgs, "-phase", "verify"]),
  ).toContain("PASS verify");
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
  let stopAgentCollector: (() => Promise<void>) | undefined;
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
    const agentTemporary = resolve(temporary, "agent-collector");
    mkdirSync(agentTemporary);
    const agentCollector = await startDurableMcpService({
      environment: agentEnv,
      origin,
      publicKey: selectedPath,
      signingKey: selectedPath,
      workspace,
      binary,
      statePath: resolve(temporary, "github-mcp-state.json"),
      temporary: agentTemporary,
    });
    stopAgentCollector = agentCollector.stop;
    const agentArgs = [...args];
    agentArgs[agentArgs.indexOf("-service-socket") + 1] =
      agentCollector.socketPath;
    agentArgs[agentArgs.indexOf("-public-key") + 1] = selectedPath;
    agentArgs[agentArgs.indexOf("-signing-key") + 1] = "";
    expect(await realMcp([...agentArgs, "-phase", "read"], agentEnv)).toContain(
      "PASS read",
    );
    // The MCP needs no SSH agent; the collector owns its signing environment.
    expect(
      await realMcp([...agentArgs, "-phase", "read"], {
        ...agentEnv,
        SSH_AUTH_SOCK: resolve(temporary, "absent-agent.sock"),
      }),
    ).toContain("PASS read");
    // No preference leaves a real ambiguity. Do not guess a credential.
    agentCommand("git", ["config", "--unset", "user.signingkey"]);
    expect(() =>
      agentCommand("git", ["config", "--get", "user.signingkey"]),
    ).toThrow();
  } finally {
    await stopAgentCollector?.();
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
  const updateEntityField = async (kind: string, id: string, field: string, value: string) => {
    if (databaseAdmin && databaseUrl) {
      const client = postgres(databaseUrl, { max: 1 });
      try {
        await client.unsafe(`UPDATE entities SET data=jsonb_set(data, ARRAY[$1]::text[], to_jsonb($2::text)) WHERE kind=$3 AND id=$4`, [field, value, kind, id]);
      } finally { await client.end(); }
    } else {
      const require = createRequire(resolve(root, "apps/web/package.json"));
      const SQLite = require("better-sqlite3") as new (file: string) => {
        prepare(sql: string): { run(...args: string[]): unknown };
        close(): void;
      };
      const db = new SQLite(environment.SCRATCHPAD_DATABASE_PATH!);
      try {
        db.prepare("UPDATE entities SET data=json_set(data, ?, ?) WHERE kind=? AND id=?").run(`$.${field}`, value, kind, id);
      } finally {
        db.close();
      }
    }
  };
  await stopGoServer(server);
  await updateEntityField("github_binding", "owner", "lastSuccessfulSyncAt", new Date(Date.now() - 23 * 60 * 60 * 1000).toISOString());
  await restartServer();
  const renewed = await machineSession();
  expect(renewed.status).toBe(200);
  await restartServer();
  expect(await realMcp([...args, "-phase", "verify"])).toContain("PASS verify");
  await stopGoServer(server);
  await updateEntityField("github_binding", "owner", "lastSuccessfulSyncAt", new Date(Date.now() - 25 * 60 * 60 * 1000).toISOString());
  const sessionId = (value: string) => createHash("sha256").update(value).digest("hex");
  await updateEntityField("session", sessionId(renewed.token), "expiresAt", "2000-01-01T00:00:00.000Z");
  const browserCookie = (await page.context().cookies(origin)).find((cookie) => cookie.name === "scratchpad_session");
  expect(browserCookie).toBeDefined();
  await updateEntityField("session", sessionId(browserCookie!.value), "expiresAt", "2000-01-01T00:00:00.000Z");
  await restartServer();
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
  await page.getByRole("button", { name: "Access & MCP", exact: true }).click();
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
  await restartServer();
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
  const recovery = admin("recover").trim().split("\n").at(-1) ?? "";
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await page
    .getByRole("button", { name: "Recover access", exact: true })
    .click();
  await page.getByLabel("Setup or recovery token").fill(recovery);
  account = { id: 987654322, login: "synthetic-recovered-owner", type: "User" };
  await page
    .getByRole("button", { name: "Recover with GitHub", exact: true })
    .click();
  await page.waitForURL(`${origin}/`);
  await page.reload();
  await expect(
    page.getByRole("heading", { name: "Project memory", exact: true }),
  ).toBeVisible();
  await expect
    .poll(async () => {
      const result = await page.evaluate(async () => {
        const response = await fetch("/api/v1/auth/status");
        return { status: response.status, body: await response.json() };
      });
      return result;
    })
    .toMatchObject({ status: 200, body: { authenticated: true } });
  await page.getByRole("button", { name: "Settings", exact: true }).click();
  await page.getByRole("button", { name: "Access & MCP", exact: true }).click();
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
