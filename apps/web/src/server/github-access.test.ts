import { spawnSync } from "node:child_process";
import {
  createHash,
  generateKeyPairSync,
  randomBytes,
  sign,
} from "node:crypto";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { isoCBOR } from "@simplewebauthn/server/helpers";
import postgres from "postgres";
import { afterEach, expect, it } from "vitest";
import { sshNamespace, sshProof } from "../lib/ssh-proof";
import { createApi } from "./api";

/** A software authenticator signs actual WebAuthn bytes; no verification mocks. */
function authenticator() {
  const { privateKey, publicKey } = generateKeyPairSync("ec", {
    namedCurve: "prime256v1",
  });
  const jwk = publicKey.export({ format: "jwk" }),
    credentialId = randomBytes(32);
  if (!jwk.x || !jwk.y) throw new Error("EC key coordinates missing");
  const cose = isoCBOR.encode(
    new Map<number, number | Uint8Array>([
      [1, 2],
      [3, -7],
      [-1, 1],
      [-2, Buffer.from(jwk.x, "base64url")],
      [-3, Buffer.from(jwk.y, "base64url")],
    ]),
  );
  const rpHash = createHash("sha256").update("localhost").digest();
  function clientData(challenge: string, type: string, origin: string) {
    return Buffer.from(
      JSON.stringify({ type, challenge, origin, crossOrigin: false }),
    );
  }
  return {
    register(challenge: string, origin = "http://localhost:3000") {
      const length = Buffer.alloc(2);
      length.writeUInt16BE(credentialId.length);
      const authData = Buffer.concat([
        rpHash,
        Buffer.from([0x45]),
        Buffer.alloc(4),
        Buffer.alloc(16),
        length,
        credentialId,
        cose,
      ]);
      const attestation = isoCBOR.encode(
        new Map<string, string | Uint8Array | Map<string, string>>([
          ["fmt", "none"],
          ["authData", authData],
          ["attStmt", new Map()],
        ]),
      );
      return {
        id: credentialId.toString("base64url"),
        rawId: credentialId.toString("base64url"),
        type: "public-key",
        response: {
          attestationObject: Buffer.from(attestation).toString("base64url"),
          clientDataJSON: clientData(
            challenge,
            "webauthn.create",
            origin,
          ).toString("base64url"),
          transports: ["internal"],
        },
        clientExtensionResults: {},
        authenticatorAttachment: "platform",
      };
    },
    login(challenge: string, counter = 1, origin = "http://localhost:3000") {
      const count = Buffer.alloc(4);
      count.writeUInt32BE(counter);
      const authData = Buffer.concat([rpHash, Buffer.from([0x05]), count]);
      const client = clientData(challenge, "webauthn.get", origin);
      const signature = sign(
        "sha256",
        Buffer.concat([authData, createHash("sha256").update(client).digest()]),
        privateKey,
      );
      return {
        id: credentialId.toString("base64url"),
        rawId: credentialId.toString("base64url"),
        type: "public-key",
        response: {
          authenticatorData: authData.toString("base64url"),
          clientDataJSON: client.toString("base64url"),
          signature: signature.toString("base64url"),
          userHandle: null,
        },
        clientExtensionResults: {},
        authenticatorAttachment: "platform",
      };
    },
  };
}

const cleanup: (() => Promise<void>)[] = [];
afterEach(async () => {
  for (const fn of cleanup.splice(0).reverse()) await fn();
});
async function fixture(
  databaseUrl?: string,
  algorithm = "ed25519",
  bits?: number,
) {
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
      | ((url: string, init?: RequestInit) => Promise<Response | undefined>)
      | undefined,
  };
  const fetcher: typeof fetch = async (input, init) => {
    if (provider.unavailable) return new Response("", { status: 503 });
    const url = String(input);
    const overridden = await provider.override?.(url, init);
    if (overridden) return overridden;
    if (provider.failSigning && url.includes("ssh_signing_keys"))
      return new Response("", { status: 503 });
    return Response.json(
      url.includes("access_token")
        ? { access_token: "synthetic-token", token_type: "bearer" }
        : url.endsWith("/user") ||
            /\/user\/\d+$/.test(url) ||
            /\/users\/[^/]+$/.test(url)
          ? {
              id: provider.id,
              login: provider.login,
              type: "User",
              name: "Synthetic owner",
              avatar_url: null,
            }
          : url.includes("ssh_signing_keys")
            ? provider.signingKeys
            : provider.keys,
    );
  };
  const config = {
    databasePath: join(dir, "db.sqlite"),
    databaseUrl,
    origin: "http://localhost:3000",
    githubOAuth: { clientId: "synthetic", clientSecret: "synthetic-secret" },
    githubAuthFetch: fetcher,
    githubFetch: fetcher,
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
      data:
        response.status >= 300 && response.status < 400
          ? null
          : await response.json(),
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
  spawnSync("ssh-keygen", [
    "-t",
    algorithm,
    ...(bits ? ["-b", String(bits)] : []),
    "-N",
    "",
    "-f",
    join(dir, "key"),
  ]);
  const publicKey = readFileSync(join(dir, "key.pub"), "utf8").trim();
  const machine = async (keyName = "key", enrollmentCookie?: string) => {
    const publicKey = readFileSync(join(dir, `${keyName}.pub`), "utf8").trim();
    const challenge = await call(
      enrollmentCookie ? "/auth/credentials/challenge" : "/auth/mcp/challenge",
      "POST",
      { publicKey },
      enrollmentCookie,
    );
    if (challenge.response.status !== 200) return challenge;
    const file = join(dir, "nonce");
    writeFileSync(
      file,
      sshProof(
        challenge.data,
        publicKey,
        "http://localhost:3000",
        enrollmentCookie ? "ssh_enroll" : "ssh_login",
      ),
    );
    rmSync(`${file}.sig`, { force: true });
    expect(
      spawnSync("ssh-keygen", [
        "-Y",
        "sign",
        "-f",
        join(dir, keyName),
        "-n",
        sshNamespace,
        file,
      ]).status,
    ).toBe(0);
    return await call(
      enrollmentCookie ? "/auth/credentials/verify" : "/auth/mcp/verify",
      "POST",
      {
        publicKey,
        challengeId: challenge.data.challengeId,
        signature: readFileSync(`${file}.sig`, "utf8"),
      },
      enrollmentCookie,
    );
  };
  const enrollLocal = async (cookie: string) => {
    spawnSync("ssh-keygen", [
      "-t",
      "ed25519",
      "-N",
      "",
      "-f",
      join(dir, "local"),
    ]);
    return await machine("local", cookie);
  };
  const device = authenticator();
  const passkey = async (cookie?: string, setupToken?: string) => {
    const options = await call(
      "/auth/register/options",
      "POST",
      { setupToken },
      cookie,
    );
    expect(options.response.status).toBe(200);
    return await call(
      "/auth/register/verify",
      "POST",
      {
        setupToken,
        challengeId: options.data.challengeId,
        response: device.register(options.data.options.challenge),
      },
      cookie,
    );
  };
  let counter = 0;
  const passkeyLogin = async () => {
    const options = await call("/auth/login/options", "POST", {});
    return await call("/auth/login/verify", "POST", {
      challengeId: options.data.challengeId,
      response: device.login(options.data.options.challenge, ++counter),
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
    restart: async (githubConfigured = true) => {
      await api.close();
      api = createApi({
        ...config,
        githubOAuth: githubConfigured ? config.githubOAuth : undefined,
      });
    },
    call,
    oauth,
    passkey,
    passkeyLogin,
    enrollLocal,
    provider,
    machine,
    read,
    publicKey,
    advance: (ms: number) => {
      time += ms;
    },
  };
}
it("refreshes public GitHub keys using OAuth app authentication while browser identity uses its bearer token", async () => {
  const f = await fixture();
  f.provider.keys = [{ key: f.publicKey }];
  f.provider.override = async (url, init) => {
    if (url.includes("synthetic-secret") || url.includes("client_id="))
      return new Response("Credentials must not be in URLs", { status: 400 });
    const authorization = new Headers(init?.headers).get("Authorization");
    if (url.startsWith("https://api.github.com/")) {
      const expected = url.endsWith("/user")
        ? "Bearer synthetic-token"
        : "Basic c3ludGhldGljOnN5bnRoZXRpYy1zZWNyZXQ=";
      if (authorization !== expected)
        return new Response("Authentication required", { status: 401 });
    } else if (authorization)
      return new Response("Unexpected authentication", { status: 400 });
    if (url.includes("/keys?") && url.endsWith("page=1"))
      return Response.json([], {
        headers: {
          link: '<https://api.github.com/users/owner/keys?per_page=100&page=2>; rel="next"',
        },
      });
    return undefined;
  };
  const browser = await f.oauth("setup", await f.api.auth.createSetupToken());
  expect(browser.response.status).toBe(302);
  expect(
    (await f.call("/auth/github/sync", "POST", {}, browser.cookie)).response
      .status,
  ).toBe(200);
  expect((await f.machine()).response.status).toBe(200);
  f.provider.keys = [];
  f.advance(5 * 60 * 1000);
  await f.api.github.tick();
  expect((await f.machine()).response.status).toBe(401);
});
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
  expect((await f.oauth("login")).response.status).toBe(303);
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
  expect((await f.call(path)).response.status).toBe(303);
  expect(
    (await f.call(path, "GET", undefined, start.cookie)).response.status,
  ).toBe(302);
  expect(
    (await f.call(path, "GET", undefined, start.cookie)).response.status,
  ).toBe(303);
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
    Response.json([{ key: f.publicKey.replace("ssh-ed25519", "ssh-rsa") }]),
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
it.each([
  { algorithm: "rsa", bits: 2048 },
  { algorithm: "ecdsa", bits: 256 },
  { algorithm: "ecdsa", bits: 384 },
  { algorithm: "ecdsa", bits: 521 },
])(
  "synchronizes and proves possession of a valid $algorithm $bits key",
  async ({ algorithm, bits }) => {
    const f = await fixture(undefined, algorithm, bits);
    f.provider.keys = [{ key: f.publicKey }];
    const browser = await f.oauth("setup", await f.api.auth.createSetupToken());
    expect(
      (await f.call("/auth/github", "GET", undefined, browser.cookie)).data
        .keys,
    ).toHaveLength(1);
    expect((await f.machine()).response.status).toBe(200);
  },
);
it("rejects a regex-compatible malformed SSH blob without changing keys, freshness or sessions", async () => {
  const f = await fixture();
  f.provider.keys = [{ key: f.publicKey }];
  const browser = await f.oauth("setup", await f.api.auth.createSetupToken());
  const machine = await f.machine();
  const before = (
    await f.call("/auth/github", "GET", undefined, browser.cookie)
  ).data;
  f.advance(5 * 60 * 1000);
  f.provider.keys = [{ key: "ssh-ed25519 AAAAAAAAAAAAAAAAAAAA" }];
  expect(
    (await f.call("/auth/github/sync", "POST", {}, browser.cookie)).response
      .status,
  ).toBe(503);
  const after = (await f.call("/auth/github", "GET", undefined, browser.cookie))
    .data;
  expect(after.keys).toEqual(before.keys);
  expect(after.lastSuccessfulSyncAt).toBe(before.lastSuccessfulSyncAt);
  expect((await f.read(machine.data.accessToken)).status).toBe(200);
});
it.each(['rel="next"', "rel=next", 'rel="next last"'])(
  "follows valid pagination with %s before committing removals",
  async (relation) => {
    const f = await fixture();
    f.provider.keys = [{ key: f.publicKey }];
    const browser = await f.oauth("setup", await f.api.auth.createSetupToken());
    const machine = await f.machine();
    f.provider.override = async (url) =>
      url.includes("/keys?")
        ? url.endsWith("page=1")
          ? Response.json([], {
              headers: {
                link: `<https://api.github.com/users/owner/keys?per_page=100&page=2>; ${relation}`,
              },
            })
          : Response.json([{ key: f.publicKey }])
        : undefined;
    expect(
      (await f.call("/auth/github/sync", "POST", {}, browser.cookie)).response
        .status,
    ).toBe(200);
    expect((await f.read(machine.data.accessToken)).status).toBe(200);
  },
);
it.each([
  'rel="next',
  "rel=",
  "rel=unknown",
  "rel=next; rel=last",
  'title="next"',
  "rel=next, broken",
])(
  "fails closed on unsupported or malformed pagination %s",
  async (relation) => {
    const f = await fixture();
    f.provider.keys = [{ key: f.publicKey }];
    const browser = await f.oauth("setup", await f.api.auth.createSetupToken());
    const machine = await f.machine();
    const before = (
      await f.call("/auth/github", "GET", undefined, browser.cookie)
    ).data;
    f.advance(5 * 60 * 1000);
    f.provider.override = async (url) =>
      url.includes("/keys?")
        ? Response.json([], {
            headers: {
              link: `<https://api.github.com/users/owner/keys?per_page=100&page=2>; ${relation}`,
            },
          })
        : undefined;
    expect(
      (await f.call("/auth/github/sync", "POST", {}, browser.cookie)).response
        .status,
    ).toBe(503);
    const after = (
      await f.call("/auth/github", "GET", undefined, browser.cookie)
    ).data;
    expect(after.keys).toEqual(before.keys);
    expect(after.lastSuccessfulSyncAt).toBe(before.lastSuccessfulSyncAt);
    expect((await f.read(machine.data.accessToken)).status).toBe(200);
  },
);
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
it("a late synchronization cannot restore keys removed by a newer synchronization", async () => {
  const f = await fixture();
  f.provider.keys = [{ key: f.publicKey }];
  const browser = await f.oauth("setup", await f.api.auth.createSetupToken());
  let release: ((value: Response) => void) | undefined;
  let started: (() => void) | undefined;
  const pending = new Promise<void>((resolve) => {
    started = resolve;
  });
  f.provider.override = async (url) => {
    if (!url.includes("/keys?")) return undefined;
    if (!release)
      return await new Promise<Response>((resolve) => {
        release = resolve;
        started?.();
      });
    return Response.json([]);
  };
  f.provider.keys = [];
  const old = f.call("/auth/github/sync", "POST", {}, browser.cookie);
  await pending;
  expect(
    (await f.call("/auth/github/sync", "POST", {}, browser.cookie)).response
      .status,
  ).toBe(200);
  release?.(Response.json([{ key: f.publicKey }]));
  await old;
  expect((await f.machine()).response.status).toBe(401);
});
it.each(["blocked", "stale"])(
  "cannot convert a never-proved %s synchronized key into an independent credential",
  async (condition) => {
    const f = await fixture();
    const passkey = await f.passkey(
      undefined,
      await f.api.auth.createSetupToken(),
    );
    f.provider.keys = [{ key: f.publicKey }];
    await f.oauth("link", undefined, passkey.cookie);
    let cookie = passkey.cookie;
    if (condition === "blocked") {
      expect(
        (
          await f.call(
            "/auth/github/block",
            "POST",
            { publicKey: f.publicKey, blocked: true },
            cookie,
          )
        ).response.status,
      ).toBe(200);
    } else {
      f.advance(24 * 60 * 60 * 1000 + 1);
      cookie = (await f.passkeyLogin()).cookie;
    }
    const before = (await f.call("/auth/credentials", "GET", undefined, cookie))
      .data.credentials;
    expect(
      before.filter((entry: { kind: string }) => entry.kind === "ssh"),
    ).toHaveLength(0);
    expect((await f.machine("key", cookie)).response.status).toBe(409);
    expect(
      (await f.call("/auth/credentials", "GET", undefined, cookie)).data
        .credentials,
    ).toEqual(before);
  },
);
it("preserves passkeys, local SSH credentials and projects through GitHub linking, unlinking and administrator recovery", async () => {
  const f = await fixture();
  const local = await f.passkey(undefined, await f.api.auth.createSetupToken());
  expect(local.response.status).toBe(200);
  const project = await f.call(
    "/projects/resolve-explicit",
    "POST",
    { name: "Synthetic recovery project" },
    local.cookie,
  );
  expect(project.response.status).toBe(200);
  expect((await f.enrollLocal(local.cookie)).response.status).toBe(200);
  const localMachine = await f.machine("local");
  expect(localMachine.response.status).toBe(200);
  f.provider.keys = [{ key: f.publicKey }];
  const github = await f.oauth("link", undefined, local.cookie);
  expect(github.response.status).toBe(302);
  const managed = await f.machine();
  expect(managed.response.status).toBe(200);
  expect(
    (await f.call("/auth/github", "DELETE", {}, local.cookie)).response.status,
  ).toBe(200);
  expect((await f.read(managed.data.accessToken)).status).toBe(401);
  expect((await f.read(localMachine.data.accessToken)).status).toBe(200);
  f.provider.id = 99;
  f.provider.login = "recovered";
  const recovery = await f.oauth(
    "recover",
    await f.api.auth.createSetupToken(true),
  );
  expect(recovery.response.status).toBe(302);
  const projects = await f.call("/projects", "GET", undefined, recovery.cookie);
  expect(projects.data.projects.map((p: { name: string }) => p.name)).toContain(
    "Synthetic recovery project",
  );
  const login = await f.passkeyLogin();
  expect(login.response.status).toBe(200);
  expect(
    (await f.call("/auth/github", "DELETE", {}, login.cookie)).response.status,
  ).toBe(200);
  expect((await f.machine("local")).response.status).toBe(200);
});
it("retains the only independent passkey when OAuth configuration is removed despite a persisted binding", async () => {
  const f = await fixture();
  const local = await f.passkey(undefined, await f.api.auth.createSetupToken());
  f.provider.keys = [{ key: f.publicKey }];
  await f.oauth("link", undefined, local.cookie);
  await f.restart(false);
  const status = await f.call("/auth/status", "GET", undefined, local.cookie);
  expect(status.data.githubConfigured).toBe(false);
  const credentials = (
    await f.call("/auth/credentials", "GET", undefined, local.cookie)
  ).data.credentials;
  const passkeyId = credentials.find(
    (entry: { kind: string }) => entry.kind === "webauthn",
  ).id;
  expect(
    (
      await f.call(
        `/auth/credentials/${passkeyId}`,
        "DELETE",
        undefined,
        local.cookie,
      )
    ).response.status,
  ).toBe(409);
  expect((await f.passkeyLogin()).response.status).toBe(200);
  expect(
    (await f.call("/auth/github/options", "POST", { intent: "login" })).response
      .status,
  ).toBe(409);
  f.provider.override = async (url, init) =>
    url.startsWith("https://api.github.com/") &&
    new Headers(init?.headers).has("Authorization")
      ? new Response("Unexpected authentication", { status: 400 })
      : undefined;
  expect(
    (await f.call("/auth/github/sync", "POST", {}, local.cookie)).response
      .status,
  ).toBe(200);
});
it("can revoke the last passkey while configured GitHub remains a working browser sign-in", async () => {
  const f = await fixture();
  const local = await f.passkey(undefined, await f.api.auth.createSetupToken());
  await f.oauth("link", undefined, local.cookie);
  const credentials = (
    await f.call("/auth/credentials", "GET", undefined, local.cookie)
  ).data.credentials;
  const passkeyId = credentials.find(
    (entry: { kind: string }) => entry.kind === "webauthn",
  ).id;
  expect(
    (
      await f.call(
        `/auth/credentials/${passkeyId}`,
        "DELETE",
        undefined,
        local.cookie,
      )
    ).response.status,
  ).toBe(200);
  expect((await f.oauth("login")).response.status).toBe(302);
});
it("does not promote descriptive GitHub profile linkage into owner authentication", async () => {
  const f = await fixture();
  const local = await f.passkey(undefined, await f.api.auth.createSetupToken());
  expect(
    (
      await f.call(
        "/profile/github",
        "POST",
        { username: "owner", expectedVersion: 0 },
        local.cookie,
      )
    ).response.status,
  ).toBe(200);
  expect(
    (await f.call("/auth/github", "GET", undefined, local.cookie)).data.binding,
  ).toBeNull();
  expect(
    (await f.call("/auth/github/options", "POST", { intent: "login" })).response
      .status,
  ).toBe(401);
});
it("allows existing browser sessions during an outage but requires GitHub for new sign-in, and accepts account rename by stable ID", async () => {
  const f = await fixture();
  const browser = await f.oauth("setup", await f.api.auth.createSetupToken());
  f.provider.login = "renamed";
  expect((await f.oauth("login")).response.status).toBe(302);
  expect(
    (await f.call("/auth/github", "GET", undefined, browser.cookie)).data
      .binding,
  ).toEqual({ accountId: "42", username: "renamed" });
  f.provider.unavailable = true;
  expect(
    (await f.call("/projects", "GET", undefined, browser.cookie)).response
      .status,
  ).toBe(200);
  expect((await f.oauth("login")).response.status).toBe(303);
  f.advance(24 * 60 * 60 * 1000);
  expect(
    (await f.call("/projects", "GET", undefined, browser.cookie)).response
      .status,
  ).toBe(401);
});
it("never permits username reuse to replace the bound stable identity", async () => {
  const f = await fixture();
  f.provider.keys = [{ key: f.publicKey }];
  const browser = await f.oauth("setup", await f.api.auth.createSetupToken());
  const first = await f.machine();
  f.provider.override = async (url) =>
    url.endsWith("/users/owner")
      ? Response.json({ id: 99, login: "owner", type: "User" })
      : undefined;
  f.provider.keys = [];
  expect(
    (await f.call("/auth/github/sync", "POST", {}, browser.cookie)).response
      .status,
  ).toBe(503);
  expect((await f.read(first.data.accessToken)).status).toBe(200);
});
it("disables ordinary setup after initialization and rejects an expired OAuth state", async () => {
  const f = await fixture();
  const token = await f.api.auth.createSetupToken();
  const first = await f.call("/auth/github/options", "POST", {
    intent: "setup",
    setupToken: token,
  });
  const second = await f.call("/auth/github/options", "POST", {
    intent: "setup",
    setupToken: token,
  });
  const callback = (start: Awaited<ReturnType<typeof f.call>>) =>
    `/auth/github/callback?code=synthetic-code&state=${new URL(start.data.authorizationUrl).searchParams.get("state")}`;
  expect(
    (await f.call(callback(first), "GET", undefined, first.cookie)).response
      .status,
  ).toBe(302);
  expect(
    (await f.call(callback(second), "GET", undefined, second.cookie)).response
      .status,
  ).toBe(303);
  expect(
    (
      await f.call("/auth/github/options", "POST", {
        intent: "setup",
        setupToken: token,
      })
    ).response.status,
  ).toBe(401);
  const login = await f.call("/auth/github/options", "POST", {
    intent: "login",
  });
  f.advance(10 * 60 * 1000);
  expect(
    (await f.call(callback(login), "GET", undefined, login.cookie)).response
      .status,
  ).toBe(303);
});
it("late synchronization for a replaced account cannot apply an old account's keys", async () => {
  const f = await fixture();
  f.provider.keys = [{ key: f.publicKey }];
  const browser = await f.oauth("setup", await f.api.auth.createSetupToken());
  let release: ((response: Response) => void) | undefined;
  let started: (() => void) | undefined;
  const pending = new Promise<void>((resolve) => {
    started = resolve;
  });
  f.provider.override = async (url) => {
    if (url.includes("/users/owner/keys?"))
      return await new Promise<Response>((resolve) => {
        release = resolve;
        started?.();
      });
    return undefined;
  };
  const old = f.call("/auth/github/sync", "POST", {}, browser.cookie);
  await pending;
  f.provider.id = 99;
  f.provider.login = "replacement";
  f.provider.keys = [];
  const replacement = await f.oauth("replace", undefined, browser.cookie);
  expect(replacement.response.status).toBe(302);
  release?.(Response.json([{ key: f.publicKey }]));
  await old;
  expect((await f.machine()).response.status).toBe(401);
});
it("fails closed if the persisted key cache appears to come from the future", async () => {
  const f = await fixture();
  f.provider.keys = [{ key: f.publicKey }];
  const browser = await f.oauth("setup", await f.api.auth.createSetupToken());
  f.advance(-1000);
  expect((await f.machine()).response.status).toBe(401);
  expect(
    (await f.call("/auth/github", "GET", undefined, browser.cookie)).data
      .cacheValid,
  ).toBe(false);
});
it.skipIf(!process.env.TEST_POSTGRES_URL)(
  "supports GitHub OAuth, cache persistence and immediate revocation on PostgreSQL",
  async () => {
    const admin = postgres(process.env.TEST_POSTGRES_URL ?? "", { max: 1 });
    const name = `scratchpad_github_${randomBytes(12).toString("hex")}`;
    await admin.unsafe(`CREATE DATABASE "${name}"`);
    const url = new URL(process.env.TEST_POSTGRES_URL ?? "");
    url.pathname = `/${name}`;
    cleanup.unshift(async () => {
      await admin.unsafe(`DROP DATABASE "${name}" WITH (FORCE)`);
      await admin.end();
    });
    const f = await fixture(url.href);
    f.provider.keys = [{ key: f.publicKey }];
    const browser = await f.oauth("setup", await f.api.auth.createSetupToken());
    const first = await f.machine();
    expect((await f.read(first.data.accessToken)).status).toBe(200);
    await f.restart();
    expect((await f.read(first.data.accessToken)).status).toBe(200);
    await f.call(
      "/auth/github/block",
      "POST",
      { publicKey: f.publicKey, blocked: true },
      browser.cookie,
    );
    expect((await f.read(first.data.accessToken)).status).toBe(401);
    await f.restart();
    expect((await f.machine()).response.status).toBe(401);
  },
);
