import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, expect, it } from "vitest";
import { createApi } from "./api";

const cleanup: (() => Promise<void>)[] = [];
afterEach(async () => {
  for (const fn of cleanup.splice(0)) await fn();
});
async function fixture() {
  const dir = mkdtempSync(join(tmpdir(), "scratchpad-github-"));
  let time = Date.now();
  const provider = {
    id: 42,
    login: "owner",
    keys: [] as { key: string }[],
    unavailable: false,
    signingKeys: [] as { key: string }[],
    failSigning: false,
    override: undefined as
      | ((url: string) => Promise<Response | undefined>)
      | undefined,
  };
  const fetcher: typeof fetch = async (input) => {
    if (provider.unavailable) return new Response("", { status: 503 });
    const url = String(input);
    const overridden = await provider.override?.(url);
    if (overridden) return overridden;
    if (provider.failSigning && url.includes("ssh_signing_keys"))
      return new Response("", { status: 503 });
    return Response.json(
      url.includes("access_token")
        ? { access_token: "synthetic-token", token_type: "bearer" }
        : url.endsWith("/user") ||
            /\/user\/\d+$/.test(url) ||
            /\/users\/[^/]+$/.test(url)
          ? { id: provider.id, login: provider.login, type: "User" }
          : url.includes("ssh_signing_keys")
            ? provider.signingKeys
            : provider.keys,
    );
  };
  const config = {
    databasePath: join(dir, "db.sqlite"),
    origin: "http://localhost:3000",
    githubOAuth: { clientId: "synthetic", clientSecret: "synthetic-secret" },
    githubAuthFetch: fetcher,
    clock: () => time,
  };
  let api = createApi(config);
  cleanup.push(async () => {
    await api.close();
    rmSync(dir, { recursive: true, force: true });
  });
  const call = async (
    path: string,
    method = "GET",
    body?: unknown,
    cookie = "",
  ) => {
    const response = await api.handleRequest(
      new Request(`http://localhost:3000/api/v1${path}`, {
        method,
        headers: { origin: "http://localhost:3000", cookie },
        body: body === undefined ? undefined : JSON.stringify(body),
      }),
    );
    return {
      response,
      data: response.status === 302 ? null : await response.json(),
      cookie: response.headers.get("set-cookie")?.split(";")[0] ?? "",
    };
  };
  const oauth = async (intent: string, setupToken?: string, cookie = "") => {
    const start = await call(
      "/auth/github/options",
      "POST",
      { intent, setupToken },
      cookie,
    );
    expect(start.response.status).toBe(200);
    const state = new URL(start.data.authorizationUrl).searchParams.get(
      "state",
    );
    return await call(
      `/auth/github/callback?code=synthetic-code&state=${state}`,
      "GET",
      undefined,
      [cookie, start.cookie].filter(Boolean).join("; "),
    );
  };
  spawnSync("ssh-keygen", ["-t", "ed25519", "-N", "", "-f", join(dir, "key")]);
  const publicKey = readFileSync(join(dir, "key.pub"), "utf8").trim();
  const machine = async () => {
    const challenge = await call("/auth/mcp/challenge", "POST", { publicKey });
    if (challenge.response.status !== 200) return challenge;
    const file = join(dir, "nonce");
    writeFileSync(file, challenge.data.nonce);
    rmSync(`${file}.sig`, { force: true });
    expect(
      spawnSync("ssh-keygen", [
        "-Y",
        "sign",
        "-f",
        join(dir, "key"),
        "-n",
        "scratchpad-auth",
        file,
      ]).status,
    ).toBe(0);
    return await call("/auth/mcp/verify", "POST", {
      publicKey,
      challengeId: challenge.data.challengeId,
      signature: readFileSync(`${file}.sig`, "utf8"),
    });
  };
  const read = async (accessToken: string) =>
    api.handleRequest(
      new Request("http://localhost:3000/api/v1/projects", {
        headers: { authorization: `Bearer ${accessToken}` },
      }),
    );
  return {
    get api() {
      return api;
    },
    restart: async () => {
      await api.close();
      api = createApi(config);
    },
    call,
    oauth,
    provider,
    machine,
    read,
    publicKey,
    advance: (ms: number) => {
      time += ms;
    },
  };
}
it("initializes one owner through token-authorized GitHub OAuth and rejects wrong account login", async () => {
  const f = await fixture();
  const token = await f.api.auth.createSetupToken();
  const login = await f.oauth("setup", token);
  expect(login.response.status).toBe(302);
  const status = await f.call("/auth/github", "GET", undefined, login.cookie);
  expect(status.data.binding).toEqual({ accountId: "42", username: "owner" });
  expect(
    (await f.call("/auth/status", "GET", undefined, login.cookie)).data
      .authenticated,
  ).toBe(true);
  f.provider.id = 99;
  expect((await f.oauth("login")).response.status).toBe(401);
});
it("automatically authorizes a published SSH key, renews during outage and denies cached access at exactly 24 hours", async () => {
  const f = await fixture();
  f.provider.keys = [{ key: f.publicKey }];
  const browser = await f.oauth("setup", await f.api.auth.createSetupToken());
  const first = await f.machine();
  expect(first.response.status).toBe(200);
  expect((await f.read(first.data.accessToken)).status).toBe(200);
  f.provider.unavailable = true;
  f.advance(23 * 60 * 60 * 1000);
  expect(
    (await f.call("/auth/github/sync", "POST", {}, browser.cookie)).response
      .status,
  ).toBe(503);
  const renewed = await f.machine();
  expect(renewed.response.status).toBe(200);
  f.advance(60 * 60 * 1000);
  expect((await f.read(renewed.data.accessToken)).status).toBe(401);
  expect((await f.machine()).response.status).toBe(401);
});
it("removal invalidates existing tokens, readdition requires new proof and local blocks survive removal and readdition", async () => {
  const f = await fixture();
  f.provider.keys = [{ key: f.publicKey }];
  const browser = await f.oauth("setup", await f.api.auth.createSetupToken());
  const first = await f.machine();
  expect(first.response.status).toBe(200);
  f.provider.keys = [];
  expect(
    (await f.call("/auth/github/sync", "POST", {}, browser.cookie)).response
      .status,
  ).toBe(200);
  expect((await f.read(first.data.accessToken)).status).toBe(401);
  f.provider.keys = [{ key: f.publicKey }];
  await f.call("/auth/github/sync", "POST", {}, browser.cookie);
  expect((await f.read(first.data.accessToken)).status).toBe(401);
  const second = await f.machine();
  expect(second.response.status).toBe(200);
  await f.call(
    "/auth/github/block",
    "POST",
    { publicKey: f.publicKey, blocked: true },
    browser.cookie,
  );
  expect((await f.read(second.data.accessToken)).status).toBe(401);
  f.provider.keys = [];
  await f.call("/auth/github/sync", "POST", {}, browser.cookie);
  f.provider.keys = [{ key: f.publicKey }];
  await f.call("/auth/github/sync", "POST", {}, browser.cookie);
  expect((await f.machine()).response.status).toBe(401);
  await f.call(
    "/auth/github/block",
    "POST",
    { publicKey: f.publicKey, blocked: false },
    browser.cookie,
  );
  expect((await f.machine()).response.status).toBe(200);
});
it("binds OAuth to the initiating browser and rejects callback replay", async () => {
  const f = await fixture();
  const start = await f.call("/auth/github/options", "POST", {
    intent: "setup",
    setupToken: await f.api.auth.createSetupToken(),
  });
  const url = new URL(start.data.authorizationUrl);
  expect(url.searchParams.get("code_challenge_method")).toBe("S256");
  const path = `/auth/github/callback?code=synthetic-code&state=${url.searchParams.get("state")}`;
  expect((await f.call(path)).response.status).toBe(401);
  expect(
    (await f.call(path, "GET", undefined, start.cookie)).response.status,
  ).toBe(302);
  expect(
    (await f.call(path, "GET", undefined, start.cookie)).response.status,
  ).toBe(401);
});
it("replacement requires fresh auth and invalidates old managed access while denying unlink through GitHub alone", async () => {
  const f = await fixture();
  f.provider.keys = [{ key: f.publicKey }];
  const browser = await f.oauth("setup", await f.api.auth.createSetupToken());
  expect(
    (await f.call("/auth/github", "DELETE", {}, browser.cookie)).response
      .status,
  ).toBe(403);
  const machine = await f.machine();
  f.provider.id = 99;
  f.provider.login = "replacement";
  const replacement = await f.oauth("replace", undefined, browser.cookie);
  expect(replacement.response.status).toBe(302);
  expect((await f.read(machine.data.accessToken)).status).toBe(401);
  expect(
    (await f.call("/auth/status", "GET", undefined, browser.cookie)).data
      .authenticated,
  ).toBe(false);
  f.advance(5 * 60 * 1000);
  expect(
    (
      await f.call(
        "/auth/github/options",
        "POST",
        { intent: "replace" },
        replacement.cookie,
      )
    ).response.status,
  ).toBe(403);
});
it("preserves cache age across restart and failed partial snapshots; a complete empty snapshot revokes keys", async () => {
  const f = await fixture();
  f.provider.keys = [{ key: f.publicKey }];
  const browser = await f.oauth("setup", await f.api.auth.createSetupToken());
  const first = await f.machine();
  const before = (
    await f.call("/auth/github", "GET", undefined, browser.cookie)
  ).data.lastSuccessfulSyncAt;
  f.advance(5 * 60 * 1000);
  f.provider.keys = [];
  f.provider.failSigning = true;
  expect(
    (await f.call("/auth/github/sync", "POST", {}, browser.cookie)).response
      .status,
  ).toBe(503);
  await f.restart();
  expect(
    (await f.call("/auth/github", "GET", undefined, browser.cookie)).data
      .lastSuccessfulSyncAt,
  ).toBe(before);
  expect((await f.read(first.data.accessToken)).status).toBe(200);
  f.provider.failSigning = false;
  await f.call("/auth/github/sync", "POST", {}, browser.cookie);
  expect((await f.read(first.data.accessToken)).status).toBe(401);
});
it("accepts signing keys, deduplicates categories, follows complete pagination and keeps old keys on a failed page", async () => {
  const f = await fixture();
  f.provider.signingKeys = [{ key: f.publicKey }];
  const browser = await f.oauth("setup", await f.api.auth.createSetupToken());
  expect((await f.machine()).response.status).toBe(200);
  f.provider.override = async (url) =>
    url.includes("/keys?")
      ? url.endsWith("page=1")
        ? Response.json([{ key: f.publicKey }], {
            headers: {
              link: '<https://api.github.com/users/owner/keys?per_page=100&page=2>; rel="next"',
            },
          })
        : Response.json([])
      : undefined;
  await f.call("/auth/github/sync", "POST", {}, browser.cookie);
  const status = (
    await f.call("/auth/github", "GET", undefined, browser.cookie)
  ).data;
  expect(status.keys).toHaveLength(1);
  expect(status.keys[0].categories).toEqual(["authentication", "signing"]);
  f.advance(5 * 60 * 1000);
  f.provider.override = async (url) =>
    url.includes("/keys?")
      ? url.endsWith("page=1")
        ? Response.json([], {
            headers: {
              link: '<https://api.github.com/users/owner/keys?per_page=100&page=2>; rel="next"',
            },
          })
        : new Response("", { status: 429 })
      : undefined;
  expect(
    (await f.call("/auth/github/sync", "POST", {}, browser.cookie)).response
      .status,
  ).toBe(503);
  expect(
    (await f.call("/auth/github", "GET", undefined, browser.cookie)).data
      .lastSuccessfulSyncAt,
  ).toBe(status.lastSuccessfulSyncAt);
  expect((await f.machine()).response.status).toBe(200);
});
it("rejects malformed and unsafe provider snapshots without replacing the last complete cache", async () => {
  const f = await fixture();
  f.provider.keys = [{ key: f.publicKey }];
  const browser = await f.oauth("setup", await f.api.auth.createSetupToken());
  const first = await f.machine();
  for (const response of [
    Response.json({ keys: [] }),
    Response.json([{ key: "garbage" }]),
    Response.json([], {
      headers: { link: '<https://attacker.example/keys?page=2>; rel="next"' },
    }),
  ]) {
    f.provider.override = async (url) =>
      url.includes("/keys?") ? response : undefined;
    expect(
      (await f.call("/auth/github/sync", "POST", {}, browser.cookie)).response
        .status,
    ).toBe(503);
    expect((await f.read(first.data.accessToken)).status).toBe(200);
  }
});
it("five-minute scheduling refreshes the persisted cache, and restart does not extend its 24-hour limit", async () => {
  const f = await fixture();
  f.provider.keys = [{ key: f.publicKey }];
  const browser = await f.oauth("setup", await f.api.auth.createSetupToken());
  const first = await f.machine();
  f.provider.keys = [];
  f.advance(5 * 60 * 1000 - 1);
  await f.api.github.tick();
  expect((await f.read(first.data.accessToken)).status).toBe(200);
  f.advance(1);
  await f.api.github.tick();
  expect((await f.read(first.data.accessToken)).status).toBe(401);
  f.provider.keys = [{ key: f.publicKey }];
  await f.call("/auth/github/sync", "POST", {}, browser.cookie);
  f.provider.unavailable = true;
  f.advance(23 * 60 * 60 * 1000);
  const renewed = await f.machine();
  await f.restart();
  f.advance(60 * 60 * 1000);
  expect((await f.read(renewed.data.accessToken)).status).toBe(401);
});
