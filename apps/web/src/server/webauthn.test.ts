import {
  createHash,
  generateKeyPairSync,
  randomBytes,
  sign,
} from "node:crypto";
import { isoCBOR } from "@simplewebauthn/server/helpers";
import { describe, expect, it } from "vitest";
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

describe("WebAuthn enrollment and recovery", () => {
  it("enrolls, signs in, rejects incorrect origin and replay, then recovers with old sessions invalidated", async () => {
    const api = createApi({
      databasePath: ":memory:",
      origin: "http://localhost:3000",
    });
    try {
      const call = async (path: string, body: unknown, cookie?: string) => {
        const result = await api.handleRequest(
          new Request(`http://localhost:3000/api/v1${path}`, {
            method: "POST",
            headers: {
              origin: "http://localhost:3000",
              ...(cookie ? { cookie } : {}),
            },
            body: JSON.stringify(body),
          }),
        );
        return {
          status: result.status,
          data: await result.json(),
          cookie: result.headers.get("set-cookie")?.split(";")[0],
        };
      };
      const device = authenticator(),
        setupToken = await api.auth.createSetupToken();
      const options = await call("/auth/register/options", { setupToken });
      expect(options.status).toBe(200);
      const rejected = await call("/auth/register/verify", {
        setupToken,
        challengeId: options.data.challengeId,
        response: device.register(
          options.data.options.challenge,
          "https://evil.example",
        ),
      });
      expect(rejected.status).toBe(401);
      const enrolled = await call("/auth/register/verify", {
        setupToken,
        challengeId: options.data.challengeId,
        response: device.register(options.data.options.challenge),
      });
      expect(enrolled.status).toBe(200);
      expect(enrolled.cookie).toBeTruthy();
      expect(await api.auth.initialized()).toBe(true);
      expect(
        (await call("/auth/register/options", { setupToken })).status,
      ).toBe(401);
      const loginOptions = await call("/auth/login/options", {});
      const loginBody = {
        challengeId: loginOptions.data.challengeId,
        response: device.login(loginOptions.data.options.challenge),
      };
      const loggedIn = await call("/auth/login/verify", loginBody);
      expect(loggedIn.status).toBe(200);
      expect(
        (await call("/auth/login/verify", loginBody)).data.error.code,
      ).toBe("AUTH_CHALLENGE_USED");
      const originAttack = await api.handleRequest(
        new Request("http://localhost:3000/api/v1/projects/resolve-explicit", {
          method: "POST",
          headers: {
            cookie: loggedIn.cookie ?? "",
            origin: "https://evil.example",
          },
          body: JSON.stringify({ name: "attacker" }),
        }),
      );
      expect(originAttack.status).toBe(403);
      const recoveryToken = await api.auth.createSetupToken(true),
        replacement = authenticator();
      const recoveryOptions = await call("/auth/register/options", {
        setupToken: recoveryToken,
      });
      const recovered = await call("/auth/register/verify", {
        setupToken: recoveryToken,
        challengeId: recoveryOptions.data.challengeId,
        response: replacement.register(recoveryOptions.data.options.challenge),
      });
      expect(recovered.status).toBe(200);
      const oldSession = await api.handleRequest(
        new Request("http://localhost:3000/api/v1/projects", {
          headers: { cookie: loggedIn.cookie ?? "" },
        }),
      );
      expect(oldSession.status).toBe(401);
      const recoveredSession = await api.handleRequest(
        new Request("http://localhost:3000/api/v1/projects", {
          headers: { cookie: recovered.cookie ?? "" },
        }),
      );
      expect(recoveredSession.status).toBe(200);
      expect(
        (await api.store.list("audit")).some(
          (a) => a.action === "owner.recovered",
        ),
      ).toBe(true);
    } finally {
      await api.close();
    }
  });
});
