import { execFileSync, spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { chmodSync, mkdtempSync, rmSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, test } from "@playwright/test";
import postgres from "postgres";

for (const engine of ["sqlite", "postgres"] as const) {
  test(`Go owner enrollment, login and recovery persist on ${engine}`, async ({
    page,
  }) => {
    test.skip(
      engine === "postgres" && !process.env.TEST_POSTGRES_URL,
      "TEST_POSTGRES_URL is not configured",
    );
    const dir = mkdtempSync(join(tmpdir(), "scratchpad-go-passkeys-"));
    chmodSync(dir, 0o700);
    let server: ReturnType<typeof spawn> | undefined;
    let stopped = Promise.resolve();
    let databaseAdmin: ReturnType<typeof postgres> | undefined;
    let databaseCreated = false;
    const databaseName = `scratchpad_passkeys_${randomUUID().replaceAll("-", "")}`;
    try {
      const binary = join(dir, "server");
      execFileSync(
        "go",
        ["build", "-race", "-o", binary, "./cmd/scratchpad-server"],
        { cwd: "backend" },
      );
      const port = await new Promise<number>((resolve) => {
        const socket = createServer();
        socket.listen(0, "127.0.0.1", () => {
          const address = socket.address();
          if (!address || typeof address === "string")
            throw new Error("Missing test port");
          socket.close(() => resolve(address.port));
        });
      });
      const origin = `http://localhost:${port}`;
      const env = {
        ...process.env,
        SCRATCHPAD_DATABASE_URL: "",
        SCRATCHPAD_DATABASE_PATH: join(dir, "owner.sqlite"),
        SCRATCHPAD_LISTEN_ADDR: `127.0.0.1:${port}`,
        SCRATCHPAD_PUBLIC_URL: origin,
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
      let setupToken = execFileSync(binary, ["setup"], {
        env,
        encoding: "utf8",
      })
        .trim()
        .split("\n")
        .at(-1);
      const start = async () => {
        const child = spawn(binary, ["serve"], {
          env,
          stdio: ["ignore", "pipe", "pipe"],
        });
        server = child;
        let diagnostics = "";
        child.stderr.on("data", (chunk) => {
          diagnostics += String(chunk);
        });
        stopped = new Promise<void>((resolve, reject) =>
          child.once("exit", (code) =>
            code === 0
              ? resolve()
              : reject(new Error(`Go test server failed: ${diagnostics}`)),
          ),
        );
        await new Promise<void>((resolve, reject) => {
          child.stdout.once("data", () => resolve());
          child.once("error", reject);
          child.once("exit", () =>
            reject(new Error("Go server exited before readiness")),
          );
        });
      };
      await start();
      // Only the document is a test shell; every API request reaches the Go process.
      await page.route(`${origin}/`, (route) =>
        route.fulfill({
          contentType: "text/html",
          body: "<!doctype html><title>Passkey browser seam</title>",
        }),
      );
      await page.goto(origin);
      const abandoned = await page.evaluate(
        async (token) =>
          (
            await (
              await fetch("/api/v1/auth/register/options", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ setupToken: token }),
              })
            ).json()
          ).challengeId,
        setupToken,
      );
      const oldSetupToken = setupToken;
      setupToken = execFileSync(binary, ["setup"], { env, encoding: "utf8" })
        .trim()
        .split("\n")
        .at(-1);
      expect(
        await page.evaluate(
          async ({ token, challengeId }) =>
            (
              await (
                await fetch("/api/v1/auth/register/verify", {
                  method: "POST",
                  headers: { "Content-Type": "application/json" },
                  body: JSON.stringify({
                    setupToken: token,
                    challengeId,
                    response: {},
                  }),
                })
              ).json()
            ).error.message,
          { token: oldSetupToken, challengeId: abandoned },
        ),
      ).toBe("Invalid setup or recovery token.");
      const cdp = await page.context().newCDPSession(page);
      await cdp.send("WebAuthn.enable");
      const { authenticatorId } = await cdp.send(
        "WebAuthn.addVirtualAuthenticator",
        {
          options: {
            protocol: "ctap2",
            transport: "internal",
            hasResidentKey: true,
            hasUserVerification: true,
            isUserVerified: true,
            automaticPresenceSimulation: true,
          },
        },
      );
      const result = await page.evaluate(async (token) => {
        const optionsResponse = await fetch("/api/v1/auth/register/options", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ setupToken: token }),
        });
        if (!optionsResponse.ok)
          return { status: optionsResponse.status, authenticated: false };
        const { challengeId, options } = await optionsResponse.json();
        const publicKey =
          PublicKeyCredential.parseCreationOptionsFromJSON(options);
        const credential = (await navigator.credentials.create({
          publicKey,
        })) as PublicKeyCredential;
        const response = await fetch("/api/v1/auth/register/verify", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            setupToken: token,
            challengeId,
            response: credential.toJSON(),
          }),
        });
        return {
          status: response.status,
          authenticated: (await response.json()).authenticated,
        };
      }, setupToken);
      expect(result).toEqual({ status: 200, authenticated: true });
      const status = await page.evaluate(async () =>
        (await fetch("/api/v1/auth/status")).json(),
      );
      expect(status).toMatchObject({ initialized: true, authenticated: true });
      const cookie = (await page.context().cookies()).find(
        (item) => item.name === "scratchpad_session",
      );
      expect(cookie).toMatchObject({
        httpOnly: true,
        sameSite: "Strict",
        path: "/",
      });
      const credentials = await page.evaluate(async () => {
        const response = await fetch("/api/v1/auth/credentials");
        return response.ok ? response.json() : { status: response.status };
      });
      expect(credentials.credentials).toHaveLength(1);
      expect(credentials.credentials[0]).toMatchObject({
        kind: "webauthn",
        label: "Passkey",
        source: "local",
      });
      expect(credentials.credentials[0]).not.toHaveProperty("publicKey");
      await page.context().clearCookies();
      const login = await page.evaluate(async () => {
        const optionsResponse = await fetch("/api/v1/auth/login/options", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: "{}",
        });
        if (!optionsResponse.ok)
          return { status: optionsResponse.status, authenticated: false };
        const { challengeId, options } = await optionsResponse.json();
        const credential = (await navigator.credentials.get({
          publicKey: PublicKeyCredential.parseRequestOptionsFromJSON(options),
        })) as PublicKeyCredential;
        const response = await fetch("/api/v1/auth/login/verify", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ challengeId, response: credential.toJSON() }),
        });
        return {
          status: response.status,
          authenticated: (await response.json()).authenticated,
        };
      });
      expect(login).toEqual({ status: 200, authenticated: true });
      const oldCookie = (await page.context().cookies()).find(
        (item) => item.name === "scratchpad_session",
      );
      expect(
        await page.evaluate(
          async () =>
            (
              await fetch("/api/v1/auth/logout", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: "{}",
              })
            ).status,
        ),
      ).toBe(200);
      expect(
        await page.evaluate(
          async () =>
            (await (await fetch("/api/v1/auth/status")).json()).authenticated,
        ),
      ).toBe(false);
      const recoveryToken = execFileSync(binary, ["recover"], {
        env,
        encoding: "utf8",
      })
        .trim()
        .split("\n")
        .at(-1);
      const originalCredentials = await cdp.send("WebAuthn.getCredentials", {
        authenticatorId,
      });
      await cdp.send("WebAuthn.removeVirtualAuthenticator", {
        authenticatorId,
      });
      const recoveredAuthenticator = await cdp.send(
        "WebAuthn.addVirtualAuthenticator",
        {
          options: {
            protocol: "ctap2",
            transport: "internal",
            hasResidentKey: true,
            hasUserVerification: true,
            isUserVerified: true,
            automaticPresenceSimulation: true,
          },
        },
      );
      const recovery = await page.evaluate(async (token) => {
        const optionsResponse = await fetch("/api/v1/auth/register/options", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ setupToken: token, label: "Recovered owner" }),
        });
        const { challengeId, options } = await optionsResponse.json();
        const credential = (await navigator.credentials.create({
          publicKey: PublicKeyCredential.parseCreationOptionsFromJSON(options),
        })) as PublicKeyCredential;
        const response = await fetch("/api/v1/auth/register/verify", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            setupToken: token,
            challengeId,
            response: credential.toJSON(),
          }),
        });
        return {
          status: response.status,
          authenticated: (await response.json()).authenticated,
        };
      }, recoveryToken);
      expect(recovery).toEqual({ status: 200, authenticated: true });
      const recoveredCookie = (await page.context().cookies()).find(
        (item) => item.name === "scratchpad_session",
      );
      expect(recoveredCookie?.value).not.toBe(oldCookie?.value);
      const stale = await fetch(`${origin}/api/v1/auth/status`, {
        headers: { Cookie: `scratchpad_session=${cookie?.value}` },
      });
      expect((await stale.json()).authenticated).toBe(false);
      expect(
        await page.evaluate(
          async (token) =>
            (
              await fetch("/api/v1/auth/register/options", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify({ setupToken: token }),
              })
            ).status,
          recoveryToken,
        ),
      ).toBe(401);
      await cdp.send("WebAuthn.removeVirtualAuthenticator", {
        authenticatorId: recoveredAuthenticator.authenticatorId,
      });
      const restoredAuthenticator = await cdp.send(
        "WebAuthn.addVirtualAuthenticator",
        {
          options: {
            protocol: "ctap2",
            transport: "internal",
            hasResidentKey: true,
            hasUserVerification: true,
            isUserVerified: true,
            automaticPresenceSimulation: true,
          },
        },
      );
      for (const credential of originalCredentials.credentials)
        await cdp.send("WebAuthn.addCredential", {
          authenticatorId: restoredAuthenticator.authenticatorId,
          credential,
        });
      await page.context().clearCookies();
      const pendingLogin = await page.evaluate(async () => {
        const { challengeId, options } = await (
          await fetch("/api/v1/auth/login/options", {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: "{}",
          })
        ).json();
        const credential = (await navigator.credentials.get({
          publicKey: PublicKeyCredential.parseRequestOptionsFromJSON(options),
        })) as PublicKeyCredential;
        return { challengeId, response: credential.toJSON() };
      });
      server?.kill("SIGTERM");
      await stopped;
      await start();
      expect(
        await page.evaluate(
          async (body) =>
            (
              await fetch("/api/v1/auth/login/verify", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify(body),
              })
            ).status,
          pendingLogin,
        ),
      ).toBe(200);
      server?.kill("SIGTERM");
      await stopped;
      await start();
      expect(
        await page.evaluate(
          async () =>
            (await (await fetch("/api/v1/auth/status")).json()).authenticated,
        ),
      ).toBe(true);
      const enrollmentCookie = (await page.context().cookies()).find(
        (item) => item.name === "scratchpad_session",
      );
      if (!enrollmentCookie)
        throw new Error("Browser session unavailable after restart");
      await cdp.send("WebAuthn.removeVirtualAuthenticator", {
        authenticatorId: restoredAuthenticator.authenticatorId,
      });
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
      const additional = await page.evaluate(async () => {
        const response = await fetch("/api/v1/auth/register/options", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ label: "Additional device" }),
        });
        if (!response.ok)
          throw new Error("Authenticated enrollment unavailable");
        const { challengeId, options } = await response.json();
        const credential = (await navigator.credentials.create({
          publicKey: PublicKeyCredential.parseCreationOptionsFromJSON(options),
        })) as PublicKeyCredential;
        return { challengeId, response: credential.toJSON() };
      });
      await page.context().clearCookies();
      expect(
        await page.evaluate(
          async (body) =>
            (
              await fetch("/api/v1/auth/register/verify", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify(body),
              })
            ).status,
          additional,
        ),
      ).toBe(401);
      await page.context().addCookies([enrollmentCookie]);
      expect(
        await page.evaluate(
          async (body) =>
            (
              await fetch("/api/v1/auth/register/verify", {
                method: "POST",
                headers: { "Content-Type": "application/json" },
                body: JSON.stringify(body),
              })
            ).status,
          additional,
        ),
      ).toBe(200);
      expect(
        await page.evaluate(
          async (body) =>
            (
              await (
                await fetch("/api/v1/auth/register/verify", {
                  method: "POST",
                  headers: { "Content-Type": "application/json" },
                  body: JSON.stringify(body),
                })
              ).json()
            ).error.code,
          additional,
        ),
      ).toBe("AUTH_CHALLENGE_USED");
      expect(
        await page.evaluate(
          async () =>
            (await (await fetch("/api/v1/auth/credentials")).json()).credentials
              .length,
        ),
      ).toBe(3);
    } finally {
      try {
        server?.kill("SIGTERM");
        await stopped;
      } finally {
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
