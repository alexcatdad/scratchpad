import { randomUUID } from "node:crypto";
import { z } from "zod";

export const recordTypes = [
  "decision",
  "adr",
  "business_decision",
  "finding",
  "qa",
  "failure",
  "constraint",
  "project_state",
] as const;
export const authorityTypes = [
  "explicit",
  "observed",
  "inferred",
  "derived",
  "suggested",
] as const;
export const relationshipTypes = [
  "related_to",
  "supports",
  "contradicts",
  "refines",
  "replaces",
  "partially_replaces",
  "depends_on",
  "implements",
  "caused_by",
  "answers",
] as const;
const text = z.string().trim().min(1).max(100_000);
export const captureSchema = z.object({
  type: z.enum(recordTypes),
  title: text.max(500),
  authority: z.enum(authorityTypes),
  confidence: z.enum(["high", "medium", "low", "unknown"]).default("unknown"),
  confidenceReason: text.optional(),
  happenedAt: z.iso.datetime().optional(),
  payload: z.record(z.string(), z.unknown()),
  actor: z
    .object({
      kind: z.enum(["user", "agent", "import", "system"]),
      displayName: text.optional(),
      client: text.optional(),
      clientVersion: text.optional(),
    })
    .optional(),
});
export const settingsSchema = z.object({
  enabledRecordTypes: z.array(z.enum(recordTypes)).default([...recordTypes]),
  repoMirroring: z.object({
    enabled: z.boolean(),
    recordTypes: z.array(z.enum(recordTypes)),
  }),
  crossProjectAnalysis: z.boolean(),
  aiProcessing: z.boolean(),
});
export type ProjectSettings = z.infer<typeof settingsSchema>;
export type Capture = z.infer<typeof captureSchema>;
export type JsonObject = Record<string, unknown>;
export type Entity = JsonObject & {
  id: string;
  createdAt: string;
  version: number;
};
export type Actor = {
  kind: "user" | "agent" | "import" | "system";
  credentialFingerprint?: string;
  displayName?: string;
  client?: string;
  clientVersion?: string;
};
export function id(prefix: string): string {
  return `${prefix}_${randomUUID()}`;
}
export function now(): string {
  return new Date().toISOString();
}
export function defaults(kind: string): ProjectSettings {
  return {
    enabledRecordTypes: [...recordTypes],
    repoMirroring: {
      enabled: false,
      recordTypes: ["decision", "adr", "business_decision"],
    },
    crossProjectAnalysis: kind !== "external",
    aiProcessing: false,
  };
}
export class ApiError extends Error {
  constructor(
    public status: number,
    public code: string,
    message: string,
    public details?: unknown,
  ) {
    super(message);
  }
}
export function requireValue(
  condition: unknown,
  code: string,
  message: string,
  status = 400,
): asserts condition {
  if (!condition) throw new ApiError(status, code, message);
}
export function renderPayload(
  type: Capture["type"],
  payload: JsonObject,
): string {
  const required: Record<Capture["type"], string[]> = {
    decision: ["decision"],
    adr: ["decision"],
    business_decision: ["decision"],
    finding: ["finding"],
    qa: ["question", "answer"],
    failure: ["observed"],
    constraint: ["constraint"],
    project_state: ["state"],
  };
  for (const key of required[type])
    requireValue(
      typeof payload[key] === "string" && (payload[key] as string).trim(),
      "VALIDATION_FAILED",
      `payload.${key} is required.`,
    );
  return Object.entries(payload)
    .map(
      ([key, value]) =>
        `${key}: ${typeof value === "string" ? value : JSON.stringify(value)}`,
    )
    .join("\n\n");
}
/** Stable payload comparison ignores JSON object key order, preserving array order. */
export function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(",")}]`;
  if (value !== null && typeof value === "object")
    return `{${Object.entries(value)
      .sort(([a], [b]) => a.localeCompare(b))
      .map(([k, v]) => `${JSON.stringify(k)}:${canonical(v)}`)
      .join(",")}}`;
  return JSON.stringify(value) ?? "null";
}
export function normalizeRemote(remote: string): string {
  let value = remote.trim();
  const scp = /^(?:[^@\s]+@)?([^:/\s]+):(.+)$/.exec(value);
  if (!value.includes("://") && scp) value = `ssh://${scp[1]}/${scp[2]}`;
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    throw new ApiError(
      400,
      "PROJECT_IDENTITY_REQUIRED",
      "Use an absolute Git remote URL or SCP-style remote.",
    );
  }
  requireValue(
    ["https:", "http:", "ssh:", "git:"].includes(url.protocol) && url.hostname,
    "PROJECT_IDENTITY_REQUIRED",
    "Unsupported Git remote.",
  );
  return `${url.hostname.toLowerCase()}${url.port ? `:${url.port}` : ""}/${url.pathname.replace(/^\/+|\/+$/g, "").replace(/\.git$/, "")}`;
}
