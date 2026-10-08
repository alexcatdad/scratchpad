import { spawn } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { z } from "zod";
import { type Auth, fingerprint, type Identity, keySchema } from "./auth";
import {
  ApiError,
  type Entity,
  id,
  type JsonObject,
  requireValue,
} from "./domain";
import { githubUsername } from "./github-profile";
import type { Store } from "./store";

const hash = (s: string) => createHash("sha256").update(s).digest("hex");
const random = () => randomBytes(32).toString("base64url");
const cacheLifetime = 24 * 60 * 60 * 1000;
const accountSchema = z.object({
  id: z.number().int().positive().safe(),
  login: githubUsername,
  type: z.literal("User"),
});
const canonicalKey = (value: unknown) =>
  keySchema.parse(value).split(/\s+/).slice(0, 2).join(" ");
export type GithubOAuth = { clientId: string; clientSecret: string };

/** GitHub is an optional authority for one owner's browser and published SSH credentials. */
export class GithubAccess {
  private timer?: ReturnType<typeof setInterval>;
  private running?: Promise<void>;
  constructor(
    readonly store: Store,
    readonly auth: Auth,
    readonly config?: GithubOAuth,
    readonly fetcher: typeof fetch = fetch,
  ) {
    if (config)
      requireValue(
        Boolean(config.clientId.trim() && config.clientSecret.trim()),
        "CONFIG_INVALID",
        "Both GitHub OAuth client values are required.",
      );
  }
  private async json(
    url: string,
    init: RequestInit = {},
  ): Promise<{ data: unknown; link: string | null }> {
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 5000);
    try {
      const response = await this.fetcher(url, {
        ...init,
        headers: {
          Accept: "application/vnd.github+json",
          "X-GitHub-Api-Version": "2026-03-10",
          "User-Agent": "Scratchpad-owner-access",
          ...init.headers,
        },
        redirect: "error",
        signal: controller.signal,
      });
      if (response.status === 403 || response.status === 429)
        throw new ApiError(
          503,
          "GITHUB_RATE_LIMITED",
          "GitHub is limiting requests. Try again later.",
        );
      if (!response.ok || !response.body) throw new Error("Unavailable");
      const reader = response.body.getReader();
      const chunks: Uint8Array[] = [];
      let size = 0;
      try {
        for (;;) {
          const part = await reader.read();
          if (part.done) break;
          size += part.value.byteLength;
          if (size > 1024 * 1024) throw new Error("Oversized response");
          chunks.push(part.value);
        }
      } finally {
        await reader.cancel();
      }
      return {
        data: JSON.parse(Buffer.concat(chunks).toString("utf8")),
        link: response.headers.get("link"),
      };
    } catch (error) {
      if (error instanceof ApiError) throw error;
      throw new ApiError(
        503,
        "GITHUB_UNAVAILABLE",
        "GitHub is unavailable or returned an invalid response. Use an independent credential or try again later.",
      );
    } finally {
      clearTimeout(timer);
    }
  }
  private browser(identity: Identity): void {
    requireValue(
      identity.browser,
      "AUTH_INVALID",
      "Manage GitHub access through an authenticated browser.",
      403,
    );
  }
  private fresh(identity: Identity, independent = false): void {
    this.browser(identity);
    const time = Date.parse(
      String(identity.session.authenticatedAt ?? identity.session.createdAt),
    );
    requireValue(
      Number.isFinite(time) &&
        this.auth.clock() - time >= 0 &&
        this.auth.clock() - time < 5 * 60 * 1000 &&
        (!independent || identity.credential.source !== "github"),
      "AUTH_FRESH_REQUIRED",
      independent
        ? "Sign in again with an independent local credential before unlinking GitHub."
        : "Sign in again before replacing the GitHub account.",
      403,
    );
  }
  private cookie(value: string, age = 600): string {
    return `scratchpad_github_state=${value}; HttpOnly; SameSite=Lax; Path=/api/v1/auth/github; Max-Age=${age}${this.auth.origin.startsWith("https:") ? "; Secure" : ""}`;
  }
  clearStateCookie(): string {
    return this.cookie("", 0);
  }
  async options(
    body: JsonObject,
    request: Request,
  ): Promise<{ authorizationUrl: string; cookie: string }> {
    requireValue(
      this.config,
      "GITHUB_NOT_CONFIGURED",
      "The administrator has not configured GitHub sign-in.",
      409,
    );
    this.auth.checkOrigin(request);
    const intent = z
      .enum(["setup", "login", "link", "replace", "recover"])
      .parse(body.intent);
    let identity: Identity | undefined, token: Entity | undefined;
    const binding = await this.store.get("github_binding", "owner");
    if (intent === "setup" || intent === "recover") {
      token = await this.auth.setup(body.setupToken);
      requireValue(
        Boolean(token.recovery) === (intent === "recover"),
        "AUTH_INVALID",
        "Setup token does not authorize this operation.",
        401,
      );
    } else if (intent !== "login") {
      identity = await this.auth.identify(request);
      this.browser(identity);
      if (intent === "replace") this.fresh(identity);
      if (intent === "link")
        requireValue(
          !binding,
          "CONFLICT",
          "A GitHub account is already linked.",
          409,
        );
    } else
      requireValue(
        binding,
        "AUTH_INVALID",
        "No GitHub account is linked.",
        401,
      );
    for (const entry of await this.store.list("github_oauth"))
      if (String(entry.expiresAt) <= this.auth.now())
        await this.store.remove("github_oauth", entry.id);
    const state = random(),
      verifier = random(),
      browserNonce = random();
    await this.store.insert("github_oauth", {
      id: hash(state),
      intent,
      verifier,
      browserHash: hash(browserNonce),
      expiresAt: this.auth.expiry(10 * 60 * 1000),
      setupTokenId: token?.id,
      credentialId: identity?.credential.id,
      sessionId: identity?.session.id,
      bindingGeneration: binding?.generation,
    });
    const url = new URL("https://github.com/login/oauth/authorize");
    url.search = new URLSearchParams({
      client_id: this.config.clientId,
      redirect_uri: `${this.auth.origin}/api/v1/auth/github/callback`,
      scope: "",
      state,
      code_challenge: createHash("sha256").update(verifier).digest("base64url"),
      code_challenge_method: "S256",
      prompt: "select_account",
    }).toString();
    return { authorizationUrl: url.href, cookie: this.cookie(browserNonce) };
  }
  async callback(
    request: Request,
  ): Promise<{ accessToken: string; clearState: string }> {
    requireValue(
      this.config,
      "GITHUB_NOT_CONFIGURED",
      "GitHub sign-in is not configured.",
      409,
    );
    const params = new URL(request.url).searchParams;
    const state = z.string().min(20).max(200).parse(params.get("state"));
    const code = z.string().min(1).max(1000).parse(params.get("code"));
    const cookie = request.headers
      .get("cookie")
      ?.split(";")
      .map((v) => v.trim())
      .find((v) => v.startsWith("scratchpad_github_state="))
      ?.slice("scratchpad_github_state=".length);
    const challenge = await this.store.atomic(async () => {
      const entry = await this.store.get("github_oauth", hash(state));
      requireValue(
        entry &&
          !entry.consumedAt &&
          String(entry.expiresAt) > this.auth.now() &&
          cookie &&
          hash(cookie) === entry.browserHash,
        "AUTH_INVALID",
        "Invalid, expired or used GitHub authorization state.",
        401,
      );
      await this.store.update("github_oauth", {
        ...entry,
        consumedAt: this.auth.now(),
        verifier: undefined,
      });
      return entry;
    });
    const tokenResult = await this.json(
      "https://github.com/login/oauth/access_token",
      {
        method: "POST",
        headers: {
          Accept: "application/json",
          "Content-Type": "application/x-www-form-urlencoded",
        },
        body: new URLSearchParams({
          client_id: this.config.clientId,
          client_secret: this.config.clientSecret,
          code,
          redirect_uri: `${this.auth.origin}/api/v1/auth/github/callback`,
          code_verifier: String(challenge.verifier),
        }),
      },
    );
    const token = z
      .object({
        access_token: z.string().min(1).max(4096),
        token_type: z.literal("bearer"),
      })
      .safeParse(tokenResult.data);
    requireValue(
      token.success,
      "AUTH_INVALID",
      "GitHub authorization failed.",
      401,
    );
    const account = accountSchema.safeParse(
      (
        await this.json("https://api.github.com/user", {
          headers: { Authorization: `Bearer ${token.data.access_token}` },
        })
      ).data,
    );
    requireValue(
      account.success,
      "GITHUB_UNAVAILABLE",
      "GitHub returned an invalid account.",
      503,
    );
    const accessToken = await this.store.atomic(async () => {
      let binding = await this.store.get("github_binding", "owner");
      requireValue(
        binding?.generation === challenge.bindingGeneration,
        "CONFLICT",
        "GitHub binding changed during sign-in. Start again.",
        409,
      );
      if (challenge.credentialId) {
        const credential = await this.store.get(
          "credential",
          String(challenge.credentialId),
        );
        const session = await this.store.get(
          "session",
          String(challenge.sessionId),
        );
        requireValue(
          credential &&
            !credential.revokedAt &&
            session &&
            !session.revokedAt &&
            String(session.expiresAt) > this.auth.now(),
          "AUTH_INVALID",
          "Sign-in authorization expired or was revoked.",
          401,
        );
        await this.check(credential);
        if (challenge.intent === "replace")
          this.fresh({
            credential,
            session,
            browser: true,
            actor: { kind: "user" },
          });
      }
      if (challenge.setupTokenId) {
        const setup = await this.store.get(
          "setup_token",
          String(challenge.setupTokenId),
        );
        requireValue(
          setup &&
            String(setup.expiresAt) > this.auth.now() &&
            (setup.recovery || !(await this.auth.initialized())),
          "AUTH_INVALID",
          "Setup authorization expired or was consumed.",
          401,
        );
        await this.store.remove("setup_token", setup.id);
        if (!(await this.auth.initialized()))
          await this.store.insert("owner", {
            id: "owner",
            displayName: "Owner",
            userId: "scratchpad-owner",
          });
      }
      if (challenge.intent === "recover")
        for (const session of await this.store.list("session"))
          await this.store.update("session", {
            ...session,
            revokedAt: this.auth.now(),
          });
      if (challenge.intent === "login")
        requireValue(
          binding && binding.accountId === String(account.data.id),
          "AUTH_INVALID",
          "This GitHub account does not own the instance.",
          401,
        );
      else {
        if (binding) await this.invalidate();
        const next = {
          id: "owner",
          accountId: String(account.data.id),
          username: account.data.login,
          generation: id("binding"),
          keys: [],
          lastSuccessfulSyncAt: null,
          lastSyncError: null,
        };
        binding = binding
          ? await this.store.update("github_binding", { ...binding, ...next })
          : await this.store.insert("github_binding", next);
        await this.store.audit(
          `github.${challenge.intent}`,
          "owner",
          "owner",
          { kind: "user" },
          undefined,
          { accountId: binding.accountId },
        );
      }
      requireValue(
        binding,
        "AUTH_INVALID",
        "GitHub account is not linked.",
        401,
      );
      if (binding.username !== account.data.login)
        binding = await this.store.update("github_binding", {
          ...binding,
          username: account.data.login,
        });
      let credential = (await this.store.list("credential")).find(
        (c) =>
          c.kind === "github" &&
          c.generation === binding.generation &&
          !c.revokedAt,
      );
      if (!credential)
        credential = await this.store.insert("credential", {
          id: id("credential"),
          kind: "github",
          source: "github",
          generation: binding.generation,
          label: "GitHub browser sign-in",
          fingerprint: `github:${binding.accountId}`,
        });
      return (await this.auth.session(credential, true)).accessToken;
    });
    await this.sync().catch(() => {});
    return { accessToken, clearState: this.cookie("", 0) };
  }
  private async invalidate(): Promise<void> {
    const ids = new Set<string>();
    for (const credential of await this.store.list("credential"))
      if (credential.source === "github") {
        ids.add(credential.id);
        await this.store.update("credential", {
          ...credential,
          revokedAt: this.auth.now(),
        });
      }
    for (const session of await this.store.list("session"))
      if (ids.has(String(session.credentialId)))
        await this.store.update("session", {
          ...session,
          revokedAt: this.auth.now(),
        });
  }
  async unlink(identity: Identity): Promise<void> {
    this.fresh(identity, true);
    await this.store.atomic(async () => {
      await this.invalidate();
      await this.store.remove("github_binding", "owner");
      await this.store.audit(
        "github.unlinked",
        "owner",
        "owner",
        identity.actor,
      );
    });
  }
  async status(identity: Identity): Promise<JsonObject> {
    this.browser(identity);
    const binding = await this.store.get("github_binding", "owner");
    const keys = z
      .array(
        z.object({ publicKey: z.string(), categories: z.array(z.string()) }),
      )
      .parse(binding?.keys ?? []);
    const blocked = new Set(
      (await this.store.list("github_block")).map((b) => b.id),
    );
    const expiry = binding?.lastSuccessfulSyncAt
      ? new Date(
          Date.parse(String(binding.lastSuccessfulSyncAt)) + cacheLifetime,
        ).toISOString()
      : null;
    return {
      configured: Boolean(this.config),
      binding: binding
        ? { accountId: binding.accountId, username: binding.username }
        : null,
      lastSuccessfulSyncAt: binding?.lastSuccessfulSyncAt ?? null,
      cacheExpiresAt: expiry,
      cacheValid: Boolean(
        expiry &&
          expiry > this.auth.now() &&
          String(binding?.lastSuccessfulSyncAt) <= this.auth.now(),
      ),
      lastSyncError: binding?.lastSyncError ?? null,
      keys: keys.map((k) => ({
        ...k,
        fingerprint: fingerprint(k.publicKey),
        blocked: blocked.has(fingerprint(k.publicKey)),
      })),
      freshAuthenticationRequired:
        this.auth.clock() -
          Date.parse(
            String(
              identity.session.authenticatedAt ?? identity.session.createdAt,
            ),
          ) >=
        5 * 60 * 1000,
    };
  }
  async eligible(key: string): Promise<boolean> {
    const binding = await this.store.get("github_binding", "owner");
    return Boolean(
      binding?.lastSuccessfulSyncAt &&
        this.auth.clock() - Date.parse(String(binding.lastSuccessfulSyncAt)) >=
          0 &&
        this.auth.clock() - Date.parse(String(binding.lastSuccessfulSyncAt)) <
          cacheLifetime &&
        !(await this.store.get("github_block", fingerprint(key))) &&
        (binding.keys as { publicKey: string }[]).some(
          (k) => k.publicKey === key,
        ),
    );
  }
  async check(credential: Entity): Promise<void> {
    if (credential.publicKey)
      requireValue(
        !(await this.store.get(
          "github_block",
          fingerprint(String(credential.publicKey)),
        )),
        "AUTH_INVALID",
        "This key is locally blocked.",
        401,
      );
    if (credential.source !== "github") return;
    const binding = await this.store.get("github_binding", "owner");
    requireValue(
      binding && binding.generation === credential.generation,
      "AUTH_INVALID",
      "GitHub access was disconnected or replaced.",
      401,
    );
    if (credential.kind === "ssh")
      requireValue(
        await this.eligible(String(credential.publicKey)),
        "AUTH_INVALID",
        "GitHub key was removed, blocked or its cached authorization expired.",
        401,
      );
  }
  async block(body: JsonObject, identity: Identity): Promise<void> {
    this.browser(identity);
    const key = canonicalKey(body.publicKey),
      blocked = z.boolean().parse(body.blocked),
      keyId = fingerprint(key);
    await this.store.atomic(async () => {
      const existing = await this.store.get("github_block", keyId);
      if (blocked && !existing)
        await this.store.insert("github_block", { id: keyId });
      if (!blocked && existing) await this.store.remove("github_block", keyId);
      if (blocked) {
        const ids = new Set(
          (await this.store.list("credential"))
            .filter((c) => c.publicKey === key)
            .map((c) => c.id),
        );
        for (const s of await this.store.list("session"))
          if (ids.has(String(s.credentialId)))
            await this.store.update("session", {
              ...s,
              revokedAt: this.auth.now(),
            });
      }
      await this.store.audit(
        blocked ? "github.key_blocked" : "github.key_unblocked",
        "credential",
        keyId,
        identity.actor,
      );
    });
  }
  async sync(): Promise<void> {
    const syncRequest = id("sync");
    const binding = await this.store.atomic(async () => {
      const current = await this.store.get("github_binding", "owner");
      if (!current) return undefined;
      return await this.store.update("github_binding", {
        ...current,
        syncRequest,
        lastAttemptAt: this.auth.now(),
      });
    });
    if (!binding) return;

    try {
      const account = accountSchema.parse(
        (await this.json(`https://api.github.com/user/${binding.accountId}`))
          .data,
      );
      requireValue(
        String(account.id) === binding.accountId,
        "GITHUB_UNAVAILABLE",
        "GitHub account identity mismatch.",
        503,
      );
      const keys = new Map<string, Set<string>>();
      for (const category of ["keys", "ssh_signing_keys"]) {
        const base = `https://api.github.com/users/${encodeURIComponent(account.login)}/${category}`;
        let next: string | undefined = `${base}?per_page=100&page=1`;
        const seen = new Set<string>();
        for (let page = 0; next && page < 100; page++) {
          requireValue(
            !seen.has(next),
            "GITHUB_UNAVAILABLE",
            "GitHub pagination loop.",
            503,
          );
          seen.add(next);
          const result = await this.json(next);
          const entries = z
            .array(z.object({ key: keySchema }))
            .max(100)
            .parse(result.data);
          for (const entry of entries) {
            const key = canonicalKey(entry.key);
            // Use the same OpenSSH boundary as possession verification. A text
            // prefix and base64 alphabet alone do not establish a valid key.
            if (!keys.has(key)) {
              // Linux cannot reopen Node's subprocess stdin socket through
              // /dev/stdin. Use a regular file as possession verification does.
              const directory = mkdtempSync(join(tmpdir(), "scratchpad-key-"));
              try {
                const publicFile = join(directory, "public-key");
                writeFileSync(publicFile, `${key}\n`, { mode: 0o600 });
                await new Promise<void>((resolve, reject) => {
                  const child = spawn("ssh-keygen", ["-l", "-f", publicFile], {
                    stdio: "ignore",
                  });
                  const timer = setTimeout(() => child.kill("SIGKILL"), 5000);
                  child.once("error", (error) => {
                    clearTimeout(timer);
                    reject(error);
                  });
                  child.once("exit", (code) => {
                    clearTimeout(timer);
                    code === 0
                      ? resolve()
                      : reject(new Error("Invalid SSH key"));
                  });
                });
              } finally {
                rmSync(directory, { recursive: true, force: true });
              }
            }
            const cats = keys.get(key) ?? new Set<string>();
            cats.add(category === "keys" ? "authentication" : "signing");
            keys.set(key, cats);
          }
          // GitHub uses registered pagination relations. Accept quoted lists
          // and unquoted tokens; fail closed on every unrecognized link rather
          // than interpreting an incomplete response as the complete key set.
          const nextLinks: string[] = [];
          if (result.link !== null) {
            for (const link of result.link.split(",")) {
              const parsed = link.match(
                /^\s*<([^<>]+)>\s*;\s*rel\s*=\s*(?:"([a-z]+(?: [a-z]+)*)"|([a-z]+))\s*$/,
              );
              requireValue(
                parsed,
                "GITHUB_UNAVAILABLE",
                "Invalid GitHub pagination.",
                503,
              );
              const relations = (parsed[2] ?? parsed[3]).split(" ");
              requireValue(
                relations.every((rel) =>
                  ["next", "prev", "first", "last"].includes(rel),
                ),
                "GITHUB_UNAVAILABLE",
                "Unsupported GitHub pagination.",
                503,
              );
              if (relations.includes("next")) nextLinks.push(parsed[1]);
            }
          }
          requireValue(
            nextLinks.length <= 1,
            "GITHUB_UNAVAILABLE",
            "Invalid GitHub pagination.",
            503,
          );
          next = nextLinks[0];
          if (next) {
            const url = new URL(next);
            requireValue(
              url.origin === "https://api.github.com" &&
                url.pathname === new URL(base).pathname &&
                !url.username &&
                !url.password &&
                !url.hash &&
                url.searchParams.get("per_page") === "100" &&
                url.searchParams.get("page") === String(page + 2),
              "GITHUB_UNAVAILABLE",
              "Unsafe GitHub pagination.",
              503,
            );
          }
        }
        requireValue(
          !next,
          "GITHUB_UNAVAILABLE",
          "GitHub key pagination exceeded limit.",
          503,
        );
      }
      const confirmation = accountSchema.parse(
        (
          await this.json(
            `https://api.github.com/users/${encodeURIComponent(account.login)}`,
          )
        ).data,
      );
      requireValue(
        String(confirmation.id) === binding.accountId,
        "GITHUB_UNAVAILABLE",
        "GitHub username ownership changed.",
        503,
      );
      await this.store.atomic(async () => {
        const current = await this.store.get("github_binding", "owner");
        if (
          !current ||
          current.generation !== binding.generation ||
          current.syncRequest !== syncRequest
        )
          return;
        const removed = new Set<string>();
        for (const c of await this.store.list("credential"))
          if (
            c.source === "github" &&
            c.kind === "ssh" &&
            !keys.has(String(c.publicKey))
          )
            removed.add(c.id);
        for (const s of await this.store.list("session"))
          if (removed.has(String(s.credentialId)))
            await this.store.update("session", {
              ...s,
              revokedAt: this.auth.now(),
            });
        await this.store.update("github_binding", {
          ...current,
          username: account.login,
          keys: [...keys].map(([publicKey, categories]) => ({
            publicKey,
            categories: [...categories],
          })),
          lastSuccessfulSyncAt: this.auth.now(),
          lastSyncError: null,
          lastAttemptAt: this.auth.now(),
        });
        await this.store.audit(
          "github.synchronized",
          "owner",
          "owner",
          { kind: "system" },
          undefined,
          { keyCount: keys.size },
        );
      });
    } catch (error) {
      await this.store.atomic(async () => {
        const current = await this.store.get("github_binding", "owner");
        if (
          current &&
          current.generation === binding.generation &&
          current.syncRequest === syncRequest
        )
          await this.store.update("github_binding", {
            ...current,
            lastSyncError:
              error instanceof ApiError ? error.code : "GITHUB_UNAVAILABLE",
            lastAttemptAt: this.auth.now(),
          });
      });
      if (error instanceof ApiError) throw error;
      throw new ApiError(
        503,
        "GITHUB_UNAVAILABLE",
        "GitHub synchronization failed; the last complete key cache is preserved.",
      );
    }
  }
  async tick(): Promise<void> {
    if (this.running) return this.running;
    this.running = (async () => {
      const binding = await this.store.get("github_binding", "owner");
      if (
        binding &&
        (!binding.lastAttemptAt ||
          this.auth.clock() - Date.parse(String(binding.lastAttemptAt)) >=
            5 * 60 * 1000)
      )
        await this.sync().catch(() => {});
    })();
    try {
      await this.running;
    } finally {
      this.running = undefined;
    }
  }
  start(): void {
    if (!this.timer) {
      this.timer = setInterval(() => {
        void this.tick().catch(() => {});
      }, 30000);
      this.timer.unref();
      void this.tick().catch(() => {});
    }
  }
  async stop(): Promise<void> {
    if (this.timer) clearInterval(this.timer);
    this.timer = undefined;
    await this.running;
  }
}
