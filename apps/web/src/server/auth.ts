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
  now,
  requireValue,
} from "./domain";
import type { Store } from "./store";

const lifetime = 24 * 60 * 60 * 1000;
const challengeLifetime = 2 * 60 * 1000;
const hash = (value: string) =>
  createHash("sha256").update(value).digest("hex");
const random = () => randomBytes(32).toString("base64url");
const expiry = (ms: number) => new Date(Date.now() + ms).toISOString();
const keySchema = z
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
  constructor(
    readonly store: Store,
    origin: string,
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
  throttle(): void {
    this.store.atomic(() => {
      const bucket = Math.floor(Date.now() / 60000);
      const current = this.store.get("rate", "auth");
      const count = current?.bucket === bucket ? Number(current.count) : 0;
      requireValue(
        count < 120,
        "RATE_LIMITED",
        "Too many authentication attempts. Try again in a minute.",
        429,
      );
      if (current)
        this.store.update("rate", { ...current, bucket, count: count + 1 });
      else this.store.insert("rate", { id: "auth", bucket, count: 1 });
    });
  }
  initialized(): boolean {
    return this.store.list("owner").length > 0;
  }
  createSetupToken(recovery = false): string {
    requireValue(
      recovery ? this.initialized() : !this.initialized(),
      "SETUP_DISABLED",
      recovery
        ? "Recovery requires an existing owner."
        : "Initial setup is disabled after enrollment.",
      409,
    );
    return this.store.atomic(() => {
      for (const token of this.store.list("setup_token"))
        this.store.remove("setup_token", token.id);
      const token = random();
      this.store.insert("setup_token", {
        id: hash(token),
        recovery,
        expiresAt: expiry(15 * 60 * 1000),
      });
      this.store.audit(
        recovery ? "owner.recovery_authorized" : "owner.setup_authorized",
        "owner",
        "owner",
        { kind: "system" },
      );
      return token;
    });
  }
  private setup(token: unknown): Entity {
    requireValue(
      typeof token === "string",
      "AUTH_REQUIRED",
      "A setup token is required.",
      401,
    );
    const entry = this.store.get("setup_token", hash(token));
    requireValue(
      entry && String(entry.expiresAt) > now(),
      "AUTH_INVALID",
      "Invalid or expired setup token.",
      401,
    );
    requireValue(
      entry.recovery || !this.initialized(),
      "SETUP_DISABLED",
      "Initial setup is disabled.",
      409,
    );
    return entry;
  }
  private challenge(kind: string, details: JsonObject = {}): Entity {
    for (const item of this.store.list("challenge"))
      if (String(item.expiresAt) < now())
        this.store.remove("challenge", item.id);
    return this.store.insert("challenge", {
      id: id("challenge"),
      kind,
      nonce: random(),
      expiresAt: expiry(challengeLifetime),
      ...details,
    });
  }
  private getChallenge(value: unknown, kind: string): Entity {
    requireValue(
      typeof value === "string",
      "AUTH_INVALID",
      "Invalid challenge.",
      401,
    );
    const entry = this.store.get("challenge", value);
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
      String(entry.expiresAt) > now(),
      "AUTH_CHALLENGE_EXPIRED",
      "Challenge expired.",
      401,
    );
    return entry;
  }
  private consume(challenge: Entity): void {
    const fresh = this.getChallenge(challenge.id, String(challenge.kind));
    this.store.update("challenge", { ...fresh, consumedAt: now() });
  }
  private session(
    credential: Entity,
    browser: boolean,
  ): { accessToken: string; expiresAt: string } {
    const accessToken = random(),
      expiresAt = expiry(lifetime);
    this.store.insert("session", {
      id: hash(accessToken),
      credentialId: credential.id,
      expiresAt,
      browser,
    });
    this.store.update("credential", { ...credential, lastUsedAt: now() });
    return { accessToken, expiresAt };
  }
  cookie(token: string): string {
    return `scratchpad_session=${token}; HttpOnly; SameSite=Strict; Path=/; Max-Age=86400${this.origin.startsWith("https:") ? "; Secure" : ""}`;
  }
  clearCookie(): string {
    return `scratchpad_session=; HttpOnly; SameSite=Strict; Path=/; Max-Age=0${this.origin.startsWith("https:") ? "; Secure" : ""}`;
  }
  identify(request: Request): Identity {
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
    const session = this.store.get("session", hash(token));
    requireValue(
      session && !session.revokedAt && String(session.expiresAt) > now(),
      "AUTH_INVALID",
      "Session expired or revoked.",
      401,
    );
    const credential = this.store.get(
      "credential",
      String(session.credentialId),
    );
    requireValue(
      credential && !credential.revokedAt,
      "AUTH_INVALID",
      "Credential revoked.",
      401,
    );
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
  logout(identity: Identity): void {
    this.store.update("session", { ...identity.session, revokedAt: now() });
  }
  publicCredentials(): JsonObject[] {
    return this.store
      .list("credential")
      .map(
        ({
          id,
          kind,
          label,
          fingerprint,
          createdAt,
          lastUsedAt,
          revokedAt,
        }) => ({
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
  revoke(credentialId: string, identity: Identity): void {
    const credential = this.store.get("credential", credentialId);
    requireValue(credential, "NOT_FOUND", "Credential not found.", 404);
    requireValue(
      credential.kind !== "webauthn" ||
        this.store
          .list("credential")
          .some(
            (c) =>
              c.kind === "webauthn" && !c.revokedAt && c.id !== credentialId,
          ),
      "CONFLICT",
      "Enroll another passkey before revoking the last passkey.",
      409,
    );
    this.store.atomic(() => {
      this.store.update("credential", { ...credential, revokedAt: now() });
      this.store.audit(
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
    const token = identity ? undefined : this.setup(body.setupToken);
    const owner = this.store.get("owner", "owner");
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
      excludeCredentials: this.store
        .list("credential")
        .filter((c) => c.kind === "webauthn" && !c.revokedAt)
        .map((c) => ({ id: String(c.webauthnId) })),
    });
    const challenge = this.challenge("registration", {
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
    const challenge = this.getChallenge(body.challengeId, "registration");
    const token = challenge.setupTokenId
      ? this.setup(body.setupToken)
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
    return this.store.atomic(() => {
      this.consume(challenge);
      if (token) {
        this.setup(body.setupToken);
        this.store.remove("setup_token", token.id);
      }
      requireValue(
        !this.store
          .list("credential")
          .some((c) => c.webauthnId === info.credential.id),
        "CONFLICT",
        "Passkey already enrolled.",
        409,
      );
      if (!this.initialized())
        this.store.insert("owner", {
          id: "owner",
          displayName: "Owner",
          userId: "scratchpad-owner",
        });
      if (token?.recovery)
        for (const session of this.store.list("session"))
          this.store.update("session", { ...session, revokedAt: now() });
      const credential = this.store.insert("credential", {
        id: id("credential"),
        kind: "webauthn",
        label: challenge.label,
        webauthnId: info.credential.id,
        publicKey: Buffer.from(info.credential.publicKey).toString("base64url"),
        counter: info.credential.counter,
        transports: info.credential.transports,
      });
      this.store.audit(
        token?.recovery ? "owner.recovered" : "credential.enrolled",
        "credential",
        credential.id,
        { kind: "user", credentialFingerprint: credential.id },
      );
      return this.session(credential, true);
    });
  }
  async loginOptions(): Promise<JsonObject> {
    requireValue(
      this.initialized(),
      "SETUP_REQUIRED",
      "Complete owner setup first.",
      409,
    );
    const options = await generateAuthenticationOptions({
      rpID: this.rpID,
      userVerification: "required",
    });
    const challenge = this.challenge("login", { nonce: options.challenge });
    return { challengeId: challenge.id, options };
  }
  async loginVerify(
    body: JsonObject,
  ): Promise<{ accessToken: string; expiresAt: string }> {
    const challenge = this.getChallenge(body.challengeId, "login");
    const response = body.response as AuthenticationResponseJSON;
    const credential = this.store
      .list("credential")
      .find(
        (c) =>
          c.kind === "webauthn" &&
          c.webauthnId === response?.id &&
          !c.revokedAt,
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
    return this.store.atomic(() => {
      this.consume(challenge);
      const fresh = this.store.get("credential", credential.id);
      requireValue(
        fresh && !fresh.revokedAt && fresh.version === credential.version,
        "AUTH_INVALID",
        "Credential changed. Please sign in again.",
        401,
      );
      const updated = this.store.update("credential", {
        ...credential,
        counter: result.authenticationInfo.newCounter,
      });
      return this.session(updated, true);
    });
  }
  sshChallenge(body: JsonObject, enrollment?: Identity): JsonObject {
    const publicKey = keySchema
      .parse(body.publicKey)
      .split(/\s+/)
      .slice(0, 2)
      .join(" ");
    if (!enrollment)
      requireValue(
        this.store
          .list("credential")
          .some(
            (c) =>
              c.publicKey === publicKey && c.kind === "ssh" && !c.revokedAt,
          ),
        "AUTH_INVALID",
        "Public key is not enrolled.",
        401,
      );
    const challenge = this.challenge(enrollment ? "ssh_enroll" : "ssh", {
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
  sshVerify(body: JsonObject, enrollment?: Identity): JsonObject {
    const challenge = this.getChallenge(
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
    return this.store.atomic(() => {
      this.consume(challenge);
      let credential = this.store
        .list("credential")
        .find(
          (c) => c.kind === "ssh" && c.publicKey === publicKey && !c.revokedAt,
        );
      if (enrollment) {
        requireValue(!credential, "CONFLICT", "Key is already enrolled.", 409);
        credential = this.store.insert("credential", {
          id: id("credential"),
          kind: "ssh",
          publicKey,
          label: challenge.label,
          fingerprint: `SHA256:${createHash("sha256")
            .update(Buffer.from(publicKey.split(" ")[1] ?? "", "base64"))
            .digest("base64")
            .replace(/=+$/, "")}`,
        });
        this.store.audit(
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
      requireValue(credential, "AUTH_INVALID", "Key is not enrolled.", 401);
      return this.session(credential, false);
    });
  }
}
