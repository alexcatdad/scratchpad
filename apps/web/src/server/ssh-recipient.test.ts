import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, it } from "vitest";
import { sshNamespace, sshProof } from "../lib/ssh-proof";
import { createApi } from "./api";
import type { Identity } from "./auth";

it("binds a real SSH proof to local recipient trust, purpose and one-use expiry", async () => {
  const directory = mkdtempSync(join(tmpdir(), "scratchpad-proof-"));
  let now = Date.now();
  const first = createApi({
    databasePath: ":memory:",
    origin: "https://first.example.test",
    clock: () => now,
  });
  const second = createApi({
    databasePath: ":memory:",
    origin: "https://second.example.test",
    clock: () => now,
  });
  try {
    const key = join(directory, "synthetic-key");
    expect(
      spawnSync("ssh-keygen", ["-t", "ed25519", "-N", "", "-f", key]).status,
    ).toBe(0);
    const publicKey = readFileSync(`${key}.pub`, "utf8")
      .trim()
      .split(/\s+/)
      .slice(0, 2)
      .join(" ");
    for (const api of [first, second]) {
      await api.store.insert("owner", { id: "owner" });
      await api.store.insert("credential", {
        id: "key",
        kind: "ssh",
        publicKey,
      });
    }
    const sign = (message: string, namespace = sshNamespace) => {
      const result = spawnSync(
        "ssh-keygen",
        ["-Y", "sign", "-f", key, "-n", namespace],
        { input: message, encoding: "utf8" },
      );
      expect(result.status).toBe(0);
      return result.stdout;
    };
    const challenge = await second.auth.sshChallenge({ publicKey });
    expect(() =>
      sshProof(challenge, publicKey, first.auth.origin, "ssh_login", now),
    ).toThrow("Invalid SSH");
    const relayed = sshProof(
      { ...challenge, recipient: first.auth.origin },
      publicKey,
      first.auth.origin,
      "ssh_login",
      now,
    );
    const body = {
      challengeId: challenge.challengeId,
      publicKey,
      signature: sign(relayed),
    };
    await expect(second.auth.sshVerify(body)).rejects.toMatchObject({
      code: "AUTH_INVALID",
    });
    await expect(
      second.auth.sshVerify({
        ...body,
        signature: sign(String(challenge.nonce), "scratchpad-auth"),
      }),
    ).rejects.toMatchObject({ code: "AUTH_INVALID" });
    const wrongPurpose = JSON.parse(relayed) as unknown[];
    wrongPurpose[2] = second.auth.origin;
    wrongPurpose[3] = "ssh_enroll";
    await expect(
      second.auth.sshVerify({
        ...body,
        signature: sign(JSON.stringify(wrongPurpose)),
      }),
    ).rejects.toMatchObject({ code: "AUTH_INVALID" });
    const proof = sshProof(
      challenge,
      publicKey,
      second.auth.origin,
      "ssh_login",
      now,
    );
    expect(() =>
      sshProof(challenge, publicKey, second.auth.origin, "ssh_enroll", now),
    ).toThrow("Invalid SSH");
    const valid = { ...body, signature: sign(proof) };
    await expect(second.auth.sshVerify(valid)).resolves.toHaveProperty(
      "accessToken",
    );
    await expect(second.auth.sshVerify(valid)).rejects.toMatchObject({
      code: "AUTH_CHALLENGE_USED",
    });
    const browserCredential = await second.store.insert("credential", {
      id: "browser-key",
      kind: "webauthn",
    });
    const browserSession = await second.store.insert("session", {
      id: "browser-session",
      credentialId: browserCredential.id,
    });
    const browser: Identity = {
      credential: browserCredential,
      session: browserSession,
      browser: true,
      actor: { kind: "user", credentialFingerprint: browserCredential.id },
    };
    const enrollment = await second.auth.sshChallenge({ publicKey }, browser);
    const enrollmentProof = sshProof(
      enrollment,
      publicKey,
      second.auth.origin,
      "ssh_enroll",
      now,
    );
    const enrollmentAsLogin = JSON.parse(enrollmentProof) as unknown[];
    enrollmentAsLogin[3] = "ssh_login";
    await expect(
      second.auth.sshVerify(
        {
          publicKey,
          challengeId: enrollment.challengeId,
          signature: sign(JSON.stringify(enrollmentAsLogin)),
        },
        browser,
      ),
    ).rejects.toMatchObject({ code: "AUTH_INVALID" });
    await expect(
      second.auth.sshVerify({
        publicKey,
        challengeId: enrollment.challengeId,
        signature: sign(enrollmentProof),
      }),
    ).rejects.toMatchObject({ code: "AUTH_INVALID" });
    const expiring = await first.auth.sshChallenge({ publicKey });
    const signature = sign(
      sshProof(expiring, publicKey, first.auth.origin, "ssh_login", now),
    );
    now += 120001;
    await expect(
      first.auth.sshVerify({
        challengeId: expiring.challengeId,
        publicKey,
        signature,
      }),
    ).rejects.toMatchObject({ code: "AUTH_CHALLENGE_EXPIRED" });
  } finally {
    await Promise.all([first.close(), second.close()]);
    rmSync(directory, { recursive: true, force: true });
  }
});
