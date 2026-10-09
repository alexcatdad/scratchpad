import { z } from "zod";

export const sshNamespace = "scratchpad-auth-v2";
export type SshPurpose = "ssh_login" | "ssh_enroll";
const challengeSchema = z.object({
  version: z.literal(2),
  namespace: z.literal(sshNamespace),
  recipient: z.string(),
  purpose: z.enum(["ssh_login", "ssh_enroll"]),
  challengeId: z.string().min(1),
  nonce: z.string().min(1),
  expiresAt: z.string().datetime(),
});

/** The recipient comes from local configuration, never from the challenger. */
export function sshProof(
  challenge: unknown,
  publicKey: string,
  trustedRecipient: string,
  purpose: SshPurpose,
  now = Date.now(),
): string {
  const parsed = challengeSchema.safeParse(challenge);
  const recipient = new URL(trustedRecipient).origin;
  if (
    !parsed.success ||
    parsed.data.recipient !== recipient ||
    parsed.data.purpose !== purpose ||
    Date.parse(parsed.data.expiresAt) <= now
  )
    throw new Error("Invalid SSH signing challenge for this instance.");
  const value = parsed.data;
  return JSON.stringify([
    "scratchpad-ssh-proof",
    2,
    recipient,
    purpose,
    value.challengeId,
    value.nonce,
    publicKey.trim().split(/\s+/).slice(0, 2).join(" "),
    value.expiresAt,
  ]);
}
