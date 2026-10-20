import { execFileSync, spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import {
  chmodSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  rmSync,
} from "node:fs";
import { createServer as createHTTPServer } from "node:http";
import { createServer as createHTTPSServer } from "node:https";
import {
  connect,
  createServer as createTCPServer,
  type Socket,
} from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { expect, test } from "@playwright/test";
import postgres from "postgres";
import { sshProof } from "../apps/web/src/lib/ssh-proof";

async function unusedPort(): Promise<number> {
  return await new Promise<number>((done) => {
    const listener = createTCPServer();
    listener.listen(0, "127.0.0.1", () => {
      const address = listener.address();
      if (!address || typeof address === "string")
        throw new Error("Missing application port");
      listener.close(() => done(address.port));
    });
  });
}

for (const engine of ["sqlite", "postgres"] as const) {
  test(`Go GitHub OAuth and key management use the retained dashboard on ${engine}`, async ({
    page,
  }) => {
    test.skip(
      engine === "postgres" && !process.env.TEST_POSTGRES_URL,
      "TEST_POSTGRES_URL is not configured",
    );
    const dir = mkdtempSync(join(tmpdir(), "scratchpad-go-github-"));
    chmodSync(dir, 0o700);
    const sockets = new Set<Socket>();
    let server: ReturnType<typeof spawn> | undefined;
    let stopped = Promise.resolve();
    let renderer: ReturnType<typeof spawn> | undefined;
    let rendererStopped = Promise.resolve();
    let databaseAdmin: ReturnType<typeof postgres> | undefined;
    let databaseCreated = false;
    const databaseName = `scratchpad_github_dashboard_${randomUUID().replaceAll("-", "")}`;
    const signingKey = join(dir, "synthetic-ssh-key");
    execFileSync("ssh-keygen", [
      "-q",
      "-t",
      "ed25519",
      "-N",
      "",
      "-f",
      signingKey,
    ]);
    const publicKey = readFileSync(`${signingKey}.pub`, "utf8").trim();
    const certificates = join(dir, "certificates");
    mkdirSync(certificates, { mode: 0o700 });
    const key = join(dir, "tls-key.pem");
    const cert = join(certificates, "tls-cert.pem");
    execFileSync(
      "openssl",
      [
        "req",
        "-x509",
        "-newkey",
        "rsa:2048",
        "-nodes",
        "-days",
        "1",
        "-subj",
        "/CN=Synthetic GitHub fixture",
        "-addext",
        "subjectAltName=DNS:github.com,DNS:api.github.com",
        "-keyout",
        key,
        "-out",
        cert,
      ],
      { stdio: "ignore" },
    );
    let exchangeCount = 0;
    const github = createHTTPSServer(
      { key: readFileSync(key), cert: readFileSync(cert) },
      (req, res) => {
        res.setHeader("Content-Type", "application/json");
        if (req.url === "/login/oauth/access_token") {
          exchangeCount++;
          res.end(
            JSON.stringify({
              access_token: "synthetic-browser-token",
              token_type: "bearer",
            }),
          );
        } else if (
          req.url === "/user" ||
          req.url === "/user/42" ||
          req.url === "/users/owner-example"
        ) {
          res.end(
            JSON.stringify({ id: 42, login: "owner-example", type: "User" }),
          );
        } else if (req.url?.startsWith("/users/owner-example/")) {
          res.end(JSON.stringify([{ key: publicKey }]));
        } else {
          res.writeHead(404).end();
        }
      },
    );
    const proxy = createHTTPServer((_req, res) => res.writeHead(403).end());
    for (const service of [github, proxy])
      service.on("connection", (socket) => {
        sockets.add(socket);
        socket.once("close", () => sockets.delete(socket));
      });
    try {
      await new Promise<void>((resolve) =>
        github.listen(0, "127.0.0.1", resolve),
      );
      const githubAddress = github.address();
      if (!githubAddress || typeof githubAddress === "string")
        throw new Error("Missing fixture address");
      proxy.on("connect", (req, socket, head) => {
        if (!["github.com:443", "api.github.com:443"].includes(req.url ?? "")) {
          socket.end("HTTP/1.1 403 Forbidden\r\n\r\n");
          return;
        }
        const upstream = connect(githubAddress.port, "127.0.0.1", () => {
          socket.write("HTTP/1.1 200 Connection Established\r\n\r\n");
          upstream.write(head);
          socket.pipe(upstream);
          upstream.pipe(socket);
        });
        upstream.on("error", () => socket.destroy());
        socket.on("error", () => upstream.destroy());
        socket.once("close", () => upstream.destroy());
      });
      await new Promise<void>((resolve) =>
        proxy.listen(0, "127.0.0.1", resolve),
      );
      const proxyAddress = proxy.address();
      if (!proxyAddress || typeof proxyAddress === "string")
        throw new Error("Missing proxy address");
      const binary = join(dir, "server");
      execFileSync(
        "go",
        ["build", "-race", "-o", binary, "./cmd/scratchpad-server"],
        { cwd: "backend" },
      );
      const port = await unusedPort();
      const uiPort = await unusedPort();
      const origin = `http://localhost:${port}`;
      const env = {
        ...process.env,
        SCRATCHPAD_DATABASE_URL: "",
        SCRATCHPAD_DATABASE_PATH: join(dir, "owner.sqlite"),
        SCRATCHPAD_LISTEN_ADDR: `127.0.0.1:${port}`,
        SCRATCHPAD_PUBLIC_URL: origin,
        SCRATCHPAD_DASHBOARD_URL: `http://127.0.0.1:${uiPort}`,
        SCRATCHPAD_GITHUB_CLIENT_ID: "synthetic-client",
        SCRATCHPAD_GITHUB_CLIENT_SECRET: "synthetic-secret",
        HTTPS_PROXY: `http://127.0.0.1:${proxyAddress.port}`,
        NO_PROXY: "",
        SSL_CERT_FILE: cert,
        SSL_CERT_DIR: certificates,
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
      const token = execFileSync(binary, ["setup"], { env, encoding: "utf8" })
        .trim()
        .split("\n")
        .at(-1);
      const ui = spawn(process.execPath, [resolve("apps/web/server.mjs")], {
        env: {
          ...env,
          SCRATCHPAD_UI_ONLY: "true",
          HOST: "127.0.0.1",
          PORT: String(uiPort),
          SCRATCHPAD_GITHUB_CLIENT_ID: "",
          SCRATCHPAD_GITHUB_CLIENT_SECRET: "",
        },
        stdio: "ignore",
      });
      renderer = ui;
      rendererStopped = new Promise<void>((done, fail) => {
        ui.once("error", fail);
        ui.once("exit", (code) =>
          code === 0 ? done() : fail(new Error("OAuth renderer failed")),
        );
      });
      await expect
        .poll(async () => {
          try {
            return (await fetch(`http://127.0.0.1:${uiPort}`)).status;
          } catch {
            return 0;
          }
        })
        .toBe(200);
      const child = spawn(binary, ["serve"], {
        env,
        stdio: ["ignore", "pipe", "pipe"],
      });
      server = child;
      stopped = new Promise<void>((resolve, reject) => {
        child.once("error", reject);
        child.once("exit", (code) =>
          code === 0
            ? resolve()
            : reject(new Error("Go OAuth test server failed")),
        );
      });
      await new Promise<void>((resolve, reject) => {
        child.stdout.once("data", () => resolve());
        child.once("error", reject);
        child.once("exit", () =>
          reject(new Error("Server exited before readiness")),
        );
      });
      await page.route(
        "https://github.com/login/oauth/authorize?**",
        async (route) => {
          const target = new URL(route.request().url());
          expect(target.searchParams.get("code_challenge_method")).toBe("S256");
          const stateCookies = await page
            .context()
            .cookies(`${origin}/api/v1/auth/github/callback`);
          expect(
            stateCookies.find(
              (cookie) => cookie.name === "scratchpad_github_state",
            ),
          ).toMatchObject({
            httpOnly: true,
            sameSite: "Lax",
            path: "/api/v1/auth/github",
          });
          const callback = new URL(
            target.searchParams.get("redirect_uri") ?? "",
          );
          callback.searchParams.set("code", "synthetic-browser-code");
          callback.searchParams.set(
            "state",
            target.searchParams.get("state") ?? "",
          );
          await route.fulfill({
            contentType: "text/html",
            body: `<script>location.href=${JSON.stringify(callback.toString())}</script>`,
          });
        },
      );
      await page.goto(origin);
      await page
        .getByLabel("Setup or recovery token", { exact: false })
        .fill(token ?? "");
      await page
        .getByRole("button", { name: "Sign in with GitHub", exact: true })
        .click();
      await expect(
        page.getByRole("heading", { name: "Project memory", exact: true }),
      ).toBeVisible();
      await page.waitForURL(`${origin}/`);
      expect(
        await page.evaluate(async () =>
          (await fetch("/api/v1/auth/status")).json(),
        ),
      ).toMatchObject({
        initialized: true,
        authenticated: true,
        githubConfigured: true,
      });
      const cookies = await page.context().cookies(origin);
      expect(
        cookies.find((cookie) => cookie.name === "scratchpad_session"),
      ).toMatchObject({ httpOnly: true, sameSite: "Strict", path: "/" });
      expect(
        cookies.some((cookie) => cookie.name === "scratchpad_github_state"),
      ).toBe(false);
      expect(exchangeCount).toBe(1);
      expect(
        await page.evaluate(async () =>
          (await fetch("/api/v1/auth/github")).json(),
        ),
      ).toMatchObject({
        binding: { accountId: "42", username: "owner-example" },
        cacheValid: true,
        keys: [{ categories: ["authentication", "signing"], blocked: false }],
      });
      await page.getByRole("button", { name: "Settings", exact: true }).click();
      await page
        .getByRole("button", { name: "Access & MCP", exact: true })
        .click();
      const access = page.getByRole("region", {
        name: "GitHub access",
        exact: true,
      });
      await expect(
        access.getByText("@owner-example", { exact: true }),
      ).toBeVisible();
      const challenge = await page.evaluate(async (publicKey) => {
        const response = await fetch("/api/v1/auth/mcp/challenge", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ publicKey }),
        });
        return (await response.json()) as {
          namespace: string;
          challengeId: string;
        };
      }, publicKey);
      const signature = execFileSync(
        "ssh-keygen",
        ["-Y", "sign", "-f", signingKey, "-n", challenge.namespace],
        {
          encoding: "utf8",
          input: sshProof(challenge, publicKey, origin, "ssh_login"),
        },
      );
      const grant = await page.evaluate(
        async (proof) => {
          const response = await fetch("/api/v1/auth/mcp/verify", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(proof),
          });
          return (await response.json()) as { accessToken: string };
        },
        { challengeId: challenge.challengeId, publicKey, signature },
      );
      expect(typeof grant.accessToken).toBe("string");
      const machineAuthenticated = async () =>
        await page.evaluate(
          async (token) =>
            (
              await (
                await fetch("/api/v1/auth/status", {
                  credentials: "omit",
                  headers: { Authorization: `Bearer ${token}` },
                })
              ).json()
            ).authenticated as boolean,
          grant.accessToken,
        );
      expect(await machineAuthenticated()).toBe(true);
      await access.getByRole("button", { name: /^Block key / }).click();
      await expect(access.getByText(/Blocked locally/)).toBeVisible();
      expect(await machineAuthenticated()).toBe(false);
      await access
        .getByRole("button", { name: "Synchronize keys", exact: true })
        .click();
      await expect(
        access.getByText("GitHub keys synchronized.", { exact: true }),
      ).toBeVisible();
      await expect(access.getByText(/Blocked locally/)).toBeVisible();
      await access.getByRole("button", { name: /^Unblock key / }).click();
      await expect(
        access.getByText(/Eligible after possession proof/),
      ).toBeVisible();
      expect(await machineAuthenticated()).toBe(false);
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
      await page
        .getByRole("button", { name: "Add passkey", exact: true })
        .click();
      await expect(
        page.getByText("Additional passkey", { exact: true }),
      ).toBeVisible();
      await access
        .getByRole("button", { name: "Disconnect GitHub", exact: true })
        .click();
      await expect(
        access.getByText(
          "GitHub disconnected. Independent credentials remain available.",
          { exact: true },
        ),
      ).toBeVisible();
      await expect(
        access.getByRole("button", {
          name: "Link GitHub account",
          exact: true,
        }),
      ).toBeVisible();
      expect(await machineAuthenticated()).toBe(false);
      await page.getByRole("button", { name: "Sign out", exact: true }).click();
      await page
        .getByRole("button", { name: "Sign in with passkey", exact: true })
        .click();
      await expect(
        access.getByRole("button", {
          name: "Link GitHub account",
          exact: true,
        }),
      ).toBeVisible();
    } finally {
      if (server && server.exitCode === null) server.kill("SIGTERM");
      if (renderer && renderer.exitCode === null) renderer.kill("SIGTERM");
      try {
        await Promise.all([stopped, rendererStopped]);
      } finally {
        for (const socket of sockets) socket.destroy();
        await Promise.all([
          new Promise<void>((resolve) => github.close(() => resolve())),
          new Promise<void>((resolve) => proxy.close(() => resolve())),
        ]);
        try {
          if (databaseAdmin && databaseCreated)
            await databaseAdmin.unsafe(
              `DROP DATABASE "${databaseName}" WITH (FORCE)`,
            );
        } finally {
          await databaseAdmin?.end();
          rmSync(dir, { recursive: true, force: true });
        }
      }
    }
  });
}
