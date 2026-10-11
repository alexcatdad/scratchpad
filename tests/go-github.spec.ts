import { execFileSync, spawn } from "node:child_process";
import { chmodSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { createServer as createHTTPServer } from "node:http";
import { createServer as createHTTPSServer } from "node:https";
import {
  connect,
  createServer as createTCPServer,
  type Socket,
} from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "@playwright/test";

test("Go GitHub OAuth binds a browser across the provider redirect", async ({
  page,
}) => {
  const dir = mkdtempSync(join(tmpdir(), "scratchpad-go-github-"));
  chmodSync(dir, 0o700);
  const sockets = new Set<Socket>();
  let server: ReturnType<typeof spawn> | undefined;
  let stopped = Promise.resolve();
  const key = join(dir, "tls-key.pem");
  const cert = join(dir, "tls-cert.pem");
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
        res.end("[]");
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
    await new Promise<void>((resolve) => proxy.listen(0, "127.0.0.1", resolve));
    const proxyAddress = proxy.address();
    if (!proxyAddress || typeof proxyAddress === "string")
      throw new Error("Missing proxy address");
    const binary = join(dir, "server");
    execFileSync(
      "go",
      ["build", "-race", "-o", binary, "./cmd/scratchpad-server"],
      { cwd: "backend" },
    );
    const port = await new Promise<number>((resolve) => {
      const listener = createTCPServer();
      listener.listen(0, "127.0.0.1", () => {
        const address = listener.address();
        if (!address || typeof address === "string")
          throw new Error("Missing application port");
        listener.close(() => resolve(address.port));
      });
    });
    const origin = `http://localhost:${port}`;
    const env = {
      ...process.env,
      SCRATCHPAD_DATABASE_URL: "",
      SCRATCHPAD_DATABASE_PATH: join(dir, "owner.sqlite"),
      SCRATCHPAD_LISTEN_ADDR: `127.0.0.1:${port}`,
      SCRATCHPAD_PUBLIC_URL: origin,
      SCRATCHPAD_GITHUB_CLIENT_ID: "synthetic-client",
      SCRATCHPAD_GITHUB_CLIENT_SECRET: "synthetic-secret",
      HTTPS_PROXY: `http://127.0.0.1:${proxyAddress.port}`,
      NO_PROXY: "",
      SSL_CERT_FILE: cert,
      SSL_CERT_DIR: dir,
    };
    const token = execFileSync(binary, ["setup"], { env, encoding: "utf8" })
      .trim()
      .split("\n")
      .at(-1);
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
    await page.route(`${origin}/`, (route) =>
      route.fulfill({
        contentType: "text/html",
        body: "<!doctype html><title>GitHub browser seam</title>",
      }),
    );
    await page.route(
      "https://github.com/login/oauth/authorize?**",
      async (route) => {
        const target = new URL(route.request().url());
        const callback = new URL(target.searchParams.get("redirect_uri") ?? "");
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
    const options = await page.evaluate(async (setupToken) => {
      const response = await fetch("/api/v1/auth/github/options", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ intent: "setup", setupToken }),
      });
      const value = (await response.json()) as { authorizationUrl: string };
      return value.authorizationUrl;
    }, token);
    expect(options).toContain("code_challenge_method=S256");
    await page.goto(options);
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
      keys: [],
    });
  } finally {
    if (server && server.exitCode === null) server.kill("SIGTERM");
    try {
      await stopped;
    } finally {
      for (const socket of sockets) socket.destroy();
      await Promise.all([
        new Promise<void>((resolve) => github.close(() => resolve())),
        new Promise<void>((resolve) => proxy.close(() => resolve())),
      ]);
      rmSync(dir, { recursive: true, force: true });
    }
  }
});
