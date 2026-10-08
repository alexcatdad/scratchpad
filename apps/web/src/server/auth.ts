import { spawnSync } from "node:child_process";
import { createHash, randomBytes } from "node:crypto";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import {
  type AuthenticationResponseJSON,
  generateAuthenticationOptions,
  generateRegistrationOptions,
  type RegistrationResponseJSON,
  verifyAuthenticationResponse,
  verifyRegistrationResponse,
} from "@simplewebauthn/server";
import { z } from "zod";
import {
  type Actor,
  ApiError,
  type Entity,
  id,
  type JsonObject,
  requireValue,
} from "./domain";
import type { Store } from "./store";

const lifetime = 24 * 60 * 60 * 1000;
const challengeLifetime = 2 * 60 * 1000;
const hash = (value: string) =>
  createHash("sha256").update(value).digest("hex");
const random = () => randomBytes(32).toString("base64url");
export const keySchema = z
  .string()
  .trim()
  .min(20)
  .max(16_384)
  .refine(
    (value) =>
      /^(ssh-ed25519|ssh-rsa|ecdsa-sha2-nistp\d+|sk-ssh-ed25519@openssh.com|sk-ecdsa-sha2-nistp256@openssh.com) [A-Za-z0-9+/=]+(?: [^\r\n]*)?$/.test(
        value,
      ),
    "Use a single OpenSSH public key.",
  );
export type Identity = {
  credential: Entity;
  session: Entity;
  actor: Actor;
  browser: boolean;
};

export class Auth {
  readonly origin: string;
  readonly rpID: string;
  github?: {
    eligible(key: string): Promise<boolean>;
    check(credential: Entity): Promise<void>;
  };
  now() {
    return new Date(this.clock()).toISOString();
  }
  expiry(ms: number) {
    return new Date(this.clock() + ms).toISOString();
  }
  constructor(
    readonly store: Store,
    origin: string,
    readonly clock: () => number = Date.now,
  ) {
    const url = new URL(origin);
    this.origin = url.origin;
    this.rpID = url.hostname;
    requireValue(
      url.protocol === "https:" ||
        (url.protocol === "http:" &&
          ["localhost", "127.0.0.1", "[::1]"].includes(url.hostname)),
      "CONFIG_INVALID",
      "Public URL must use HTTPS, except localhost.",
    );
  }
  async throttle(): Promise<void> {
    await this.store.atomic(async () => {
      const bucket = Math.floor(this.clock() / 60000);
      const current = await this.store.get("rate", "auth");
      const count = current?.bucket === bucket ? Number(current.count) : 0;
      requireValue(
        count < 120,
        "RATE_LIMITED",
        "Too many authentication attempts. Try again in a minute.",
        429,
      );
      if (current)
        await this.store.update("rate", {
          ...current,
          bucket,
          count: count + 1,
        });
      else await this.store.insert("rate", { id: "auth", bucket, count: 1 });
    });
  }
  async initialized(): Promise<boolean> {
    return (await this.store.list("owner")).length > 0;
  }
  async createSetupToken(recovery = false): Promise<string> {
    requireValue(
      recovery ? await this.initialized() : !(await this.initialized()),
      "SETUP_DISABLED",
      recovery
        ? "Recovery requires an existing owner."
        : "Initial setup is disabled after enrollment.",
      409,
    );
    return await this.store.atomic(async () => {
      for (const token of await this.store.list("setup_token"))
        await this.store.remove("setup_token", token.id);
      const token = random();
      await this.store.insert("setup_token", {
        id: hash(token),
        recovery,
        expiresAt: this.expiry(15 * 60 * 1000),
      });
      await this.store.audit(
        recovery ? "owner.recovery_authorized" : "owner.setup_authorized",
        "owner",
        "owner",
        { kind: "system" },
      );
      return token;
    });
  }
  async setup(token: unknown): Promise<Entity> {
    requireValue(
      typeof token === "string",
      "AUTH_REQUIRED",
      "A setup token is required.",
      401,
    );
    const entry = await this.store.get("setup_token", hash(token));
    requireValue(
      entry && String(entry.expiresAt) > this.now(),
      "AUTH_INVALID",
      "Invalid or expired setup token.",
      401,
    );
    requireValue(
      entry.recovery || !(await this.initialized()),
      "SETUP_DISABLED",
      "Initial setup is disabled.",
      409,
    );
    return entry;
  }
  private async challenge(
    kind: string,
    details: JsonObject = {},
  ): Promise<Entity> {
    for (const item of await this.store.list("challenge"))
      if (String(item.expiresAt) < this.now())
        await this.store.remove("challenge", item.id);
    return await this.store.insert("challenge", {
      id: id("challenge"),
      kind,
      nonce: random(),
      expiresAt: this.expiry(challengeLifetime),
      ...details,
    });
  }
  private async getChallenge(value: unknown, kind: string): Promise<Entity> {
    requireValue(
      typeof value === "string",
      "AUTH_INVALID",
      "Invalid challenge.",
      401,
    );
    const entry = await this.store.get("challenge", value);
    requireValue(
      entry && entry.kind === kind,
      "AUTH_INVALID",
      "Unknown challenge.",
      401,
    );
    requireValue(
      !entry.consumedAt,
      "AUTH_CHALLENGE_USED",
      "Challenge has already been consumed.",
      401,
    );
    requireValue(
      String(entry.expiresAt) > this.now(),
      "AUTH_CHALLENGE_EXPIRED",
      "Challenge expired.",
      401,
    );
    return entry;
  }
  private async consume(challenge: Entity): Promise<void> {
    const fresh = await this.getChallenge(challenge.id, String(challenge.kind));
    await this.store.update("challenge", { ...fresh, consumedAt: this.now() });
  }
  async session(
    credential: Entity,
    browser: boolean,
  ): Promise<{ accessToken: string; expiresAt: string }> {
    const accessToken = random(),
      expiresAt = this.expiry(lifetime);
    await this.store.insert("session", {
      id: hash(accessToken),
      credentialId: credential.id,
      expiresAt,
      browser,
      authenticatedAt: this.now(),
    });
    await this.store.update("credential", {
      ...credential,
      lastUsedAt: this.now(),
    });
    return { accessToken, expiresAt };
  }
  cookie(token: string): string {
    return `scratchpad_session=${token}; HttpOnly; SameSite=Strict; Path=/; Max-Age=86400${this.origin.startsWith("https:") ? "; Secure" : ""}`;
  }
  clearCookie(): string {
    return `scratchpad_session=; HttpOnly; SameSite=Strict; Path=/; Max-Age=0${this.origin.startsWith("https:") ? "; Secure" : ""}`;
  }
  async identify(request: Request): Promise<Identity> {
    const bearer = request.headers
      .get("authorization")
      ?.match(/^Bearer ([A-Za-z0-9_-]+)$/)?.[1];
    const cookie = request.headers
      .get("cookie")
      ?.split(";")
      .map((s) => s.trim())
      .find((s) => s.startsWith("scratchpad_session="))
      ?.slice("scratchpad_session=".length);
    const token = bearer ?? cookie;
    requireValue(token, "AUTH_REQUIRED", "Sign in to Scratchpad.", 401);
    const session = await this.store.get("session", hash(token));
    requireValue(
      session && !session.revokedAt && String(session.expiresAt) > this.now(),
      "AUTH_INVALID",
      "Session expired or revoked.",
      401,
    );
    const credential = await this.store.get(
      "credential",
      String(session.credentialId),
    );
    requireValue(
      credential && !credential.revokedAt,
      "AUTH_INVALID",
      "Credential revoked.",
      401,
    );
    await this.github?.check(credential);
    requireValue(
      Boolean(session.browser) === !bearer,
      "AUTH_INVALID",
      "Use the correct session transport.",
      401,
    );
    if (!bearer && !["GET", "HEAD", "OPTIONS"].includes(request.method))
      this.checkOrigin(request);
    return {
      credential,
      session,
      browser: !bearer,
      actor: {
        kind: bearer ? "agent" : "user",
        credentialFingerprint: String(credential.fingerprint ?? credential.id),
      },
    };
  }
  checkOrigin(request: Request): void {
    requireValue(
      request.headers.get("origin") === this.origin,
      "AUTH_INVALID",
      "Request origin does not match this instance.",
      403,
    );
  }
  async logout(identity: Identity): Promise<void> {
    await this.store.update("session", {
      ...identity.session,
      revokedAt: this.now(),
    });
  }
  async publicCredentials(): Promise<JsonObject[]> {
    return (await this.store.list("credential")).map(
      ({ id, kind, label, fingerprint, createdAt, lastUsedAt, revokedAt }) => ({
        id,
        kind,
        label,
        fingerprint,
        createdAt,
        lastUsedAt,
        revokedAt,
      }),
    );
  }
  async revoke(credentialId: string, identity: Identity): Promise<void> {
    const credential = await this.store.get("credential", credentialId);
    requireValue(credential, "NOT_FOUND", "Credential not found.", 404);
    requireValue(
      credential.kind !== "webauthn" ||
        (await this.store.get("github_binding", "owner")) !== undefined ||
        (await this.store.list("credential")).some(
          (c) => c.kind === "webauthn" && !c.revokedAt && c.id !== credentialId,
        ),
      "CONFLICT",
      "Enroll another passkey before revoking the last passkey.",
      409,
    );
    await this.store.atomic(async () => {
      await this.store.update("credential", {
        ...credential,
        revokedAt: this.now(),
      });
      await this.store.audit(
        "credential.revoked",
        "credential",
        credentialId,
        identity.actor,
      );
    });
  }
  async registrationOptions(
    body: JsonObject,
    identity?: Identity,
  ): Promise<JsonObject> {
    if (identity)
      requireValue(
        identity.browser,
        "AUTH_INVALID",
        "Enroll passkeys through an authenticated browser.",
        403,
      );
    const token = identity ? undefined : await this.setup(body.setupToken);
    const owner = await this.store.get("owner", "owner");
    const options = await generateRegistrationOptions({
      rpName: "Scratchpad",
      rpID: this.rpID,
      userName: "owner",
      userID: new TextEncoder().encode(
        String(owner?.userId ?? "scratchpad-owner"),
      ),
      attestationType: "none",
      authenticatorSelection: {
        residentKey: "required",
        userVerification: "required",
      },
      excludeCredentials: (await this.store.list("credential"))
        .filter((c) => c.kind === "webauthn" && !c.revokedAt)
        .map((c) => ({ id: String(c.webauthnId) })),
    });
    const challenge = await this.challenge("registration", {
      nonce: options.challenge,
      setupTokenId: token?.id,
      credentialId: identity?.credential.id,
      label:
        typeof body.label === "string" ? body.label.slice(0, 200) : "Passkey",
    });
    return { challengeId: challenge.id, options };
  }
  async registrationVerify(
    body: JsonObject,
    identity?: Identity,
  ): Promise<{ accessToken: string; expiresAt: string }> {
    const challenge = await this.getChallenge(body.challengeId, "registration");
    const token = challenge.setupTokenId
      ? await this.setup(body.setupToken)
      : undefined;
    requireValue(
      token
        ? token.id === challenge.setupTokenId
        : identity && identity.credential.id === challenge.credentialId,
      "AUTH_INVALID",
      "Registration context mismatch.",
      401,
    );
    const result = await verifyRegistrationResponse({
      response: body.response as RegistrationResponseJSON,
      expectedChallenge: String(challenge.nonce),
      expectedOrigin: this.origin,
      expectedRPID: this.rpID,
      requireUserVerification: true,
    }).catch(() => {
      throw new ApiError(401, "AUTH_INVALID", "Passkey verification failed.");
    });
    requireValue(
      result.verified && result.registrationInfo,
      "AUTH_INVALID",
      "Passkey verification failed.",
      401,
    );
    const info = result.registrationInfo;
    return await this.store.atomic(async () => {
      await this.consume(challenge);
      if (token) {
        await this.setup(body.setupToken);
        await this.store.remove("setup_token", token.id);
      }
      requireValue(
        !(await this.store.list("credential")).some(
          (c) => c.webauthnId === info.credential.id,
        ),
        "CONFLICT",
        "Passkey already enrolled.",
        409,
      );
      if (!(await this.initialized()))
        await this.store.insert("owner", {
          id: "owner",
          displayName: "Owner",
          userId: "scratchpad-owner",
        });
      if (token?.recovery)
        for (const session of await this.store.list("session"))
          await this.store.update("session", {
            ...session,
            revokedAt: this.now(),
          });
      const credential = await this.store.insert("credential", {
        id: id("credential"),
        kind: "webauthn",
        label: challenge.label,
        webauthnId: info.credential.id,
        publicKey: Buffer.from(info.credential.publicKey).toString("base64url"),
        counter: info.credential.counter,
        transports: info.credential.transports,
      });
      await this.store.audit(
        token?.recovery ? "owner.recovered" : "credential.enrolled",
        "credential",
        credential.id,
        { kind: "user", credentialFingerprint: credential.id },
      );
      return await this.session(credential, true);
    });
  }
  async loginOptions(): Promise<JsonObject> {
    requireValue(
      await this.initialized(),
      "SETUP_REQUIRED",
      "Complete owner setup first.",
      409,
    );
    const options = await generateAuthenticationOptions({
      rpID: this.rpID,
      userVerification: "required",
    });
    const challenge = await this.challenge("login", {
      nonce: options.challenge,
    });
    return { challengeId: challenge.id, options };
  }
  async loginVerify(
    body: JsonObject,
  ): Promise<{ accessToken: string; expiresAt: string }> {
    const challenge = await this.getChallenge(body.challengeId, "login");
    const response = body.response as AuthenticationResponseJSON;
    const credential = (await this.store.list("credential")).find(
      (c) =>
        c.kind === "webauthn" && c.webauthnId === response?.id && !c.revokedAt,
    );
    requireValue(credential, "AUTH_INVALID", "Unknown passkey.", 401);
    const result = await verifyAuthenticationResponse({
      response,
      expectedChallenge: String(challenge.nonce),
      expectedOrigin: this.origin,
      expectedRPID: this.rpID,
      credential: {
        id: String(credential.webauthnId),
        publicKey: Buffer.from(String(credential.publicKey), "base64url"),
        counter: Number(credential.counter),
      },
      requireUserVerification: true,
    }).catch(() => {
      throw new ApiError(401, "AUTH_INVALID", "Passkey verification failed.");
    });
    requireValue(
      result.verified,
      "AUTH_INVALID",
      "Passkey verification failed.",
      401,
    );
    return await this.store.atomic(async () => {
      await this.consume(challenge);
      const fresh = await this.store.get("credential", credential.id);
      requireValue(
        fresh && !fresh.revokedAt && fresh.version === credential.version,
        "AUTH_INVALID",
        "Credential changed. Please sign in again.",
        401,
      );
      const updated = await this.store.update("credential", {
        ...credential,
        counter: result.authenticationInfo.newCounter,
      });
      return await this.session(updated, true);
    });
  }
  async sshChallenge(
    body: JsonObject,
    enrollment?: Identity,
  ): Promise<JsonObject> {
    const publicKey = keySchema
      .parse(body.publicKey)
      .split(/\s+/)
      .slice(0, 2)
      .join(" ");
    if (!enrollment)
      requireValue(
        (await this.store.list("credential")).some(
          (c) => c.publicKey === publicKey && c.kind === "ssh" && !c.revokedAt,
        ) || (await this.github?.eligible(publicKey)),
        "AUTH_INVALID",
        "Public key is not enrolled.",
        401,
      );
    const existing = (await this.store.list("credential")).find(
      (c) => c.kind === "ssh" && c.publicKey === publicKey && !c.revokedAt,
    );
    if (existing && !enrollment) await this.github?.check(existing);
    const binding = await this.store.get("github_binding", "owner");
    const challenge = await this.challenge(enrollment ? "ssh_enroll" : "ssh", {
      bindingGeneration: binding?.generation,

      publicKey,
      enrolledBy: enrollment?.credential.id,
      label:
        typeof body.label === "string" ? body.label.slice(0, 200) : "MCP key",
    });
    return {
      challengeId: challenge.id,
      nonce: challenge.nonce,
      namespace: "scratchpad-auth",
      expiresAt: challenge.expiresAt,
    };
  }
  async sshVerify(
    body: JsonObject,
    enrollment?: Identity,
  ): Promise<JsonObject> {
    const challenge = await this.getChallenge(
      body.challengeId,
      enrollment ? "ssh_enroll" : "ssh",
    );
    const publicKey = keySchema
      .parse(body.publicKey)
      .split(/\s+/)
      .slice(0, 2)
      .join(" ");
    requireValue(
      publicKey === challenge.publicKey &&
        (!enrollment || enrollment.credential.id === challenge.enrolledBy),
      "AUTH_INVALID",
      "Challenge key does not match.",
      401,
    );
    const signature = z.string().max(32_768).parse(body.signature);
    const directory = mkdtempSync(join(tmpdir(), "scratchpad-verify-"));
    try {
      writeFileSync(join(directory, "allowed"), `owner ${publicKey}\n`, {
        mode: 0o600,
      });
      writeFileSync(join(directory, "signature"), signature, { mode: 0o600 });
      const result = spawnSync(
        "ssh-keygen",
        [
          "-Y",
          "verify",
          "-f",
          join(directory, "allowed"),
          "-I",
          "owner",
          "-n",
          "scratchpad-auth",
          "-s",
          join(directory, "signature"),
        ],
        { input: String(challenge.nonce), timeout: 5000, maxBuffer: 65536 },
      );
      requireValue(
        result.status === 0,
        "AUTH_INVALID",
        "Signature verification failed.",
        401,
      );
    } finally {
      rmSync(directory, { recursive: true, force: true });
    }
    return await this.store.atomic(async () => {
      await this.consume(challenge);
      let credential = (await this.store.list("credential")).find(
        (c) => c.kind === "ssh" && c.publicKey === publicKey && !c.revokedAt,
      );
      if (enrollment) {
        requireValue(
          !(await this.store.list("credential")).some(
            (c) => c.source === "github" && c.publicKey === publicKey,
          ) && !(await this.github?.eligible(publicKey)),
          "CONFLICT",
          "GitHub-managed keys cannot be converted into local credentials.",
          409,
        );
        requireValue(!credential, "CONFLICT", "Key is already enrolled.", 409);
        credential = await this.store.insert("credential", {
          id: id("credential"),
          kind: "ssh",
          publicKey,
          label: challenge.label,
          fingerprint: `SHA256:${createHash("sha256")
            .update(Buffer.from(publicKey.split(" ")[1] ?? "", "base64"))
            .digest("base64")
            .replace(/=+$/, "")}`,
        });
        await this.store.audit(
          "credential.enrolled",
          "credential",
          credential.id,
          enrollment.actor,
        );
        return {
          credentialId: credential.id,
          fingerprint: credential.fingerprint,
        };
      }
      if (!enrollment && (!credential || credential.source === "github"))
        requireValue(
          challenge.bindingGeneration ===
            (await this.store.get("github_binding", "owner"))?.generation,
          "AUTH_INVALID",
          "GitHub authorization changed during key proof.",
          401,
        );
      if (!credential && (await this.github?.eligible(publicKey)))
        credential = await this.store.insert("credential", {
          id: id("credential"),
          kind: "ssh",
          source: "github",
          generation: (await this.store.get("github_binding", "owner"))
            ?.generation,
          publicKey,
          label: "GitHub-managed key",
          fingerprint: fingerprint(publicKey),
        });
      requireValue(credential, "AUTH_INVALID", "Key is not enrolled.", 401);
      await this.github?.check(credential);
      return await this.session(credential, false);
    });
  }
}

export function fingerprint(publicKey: string): string {
  return `SHA256:${createHash("sha256")
    .update(Buffer.from(publicKey.split(" ")[1] ?? "", "base64"))
    .digest("base64")
    .replace(/=+$/, "")}`;
}
