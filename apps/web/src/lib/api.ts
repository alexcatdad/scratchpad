export type Project = {
  id: string;
  name: string;
  kind: string;
  version: number;
  settings: {
    repoMirroring: { enabled: boolean; recordTypes: string[] };
    crossProjectAnalysis: boolean;
    aiProcessing: boolean;
    enabledRecordTypes?: string[];
  };
};
export type MemoryRecord = {
  id: string;
  projectId: string;
  type: string;
  title: string;
  content: string;
  authority: string | null;
  confidence: string;
  recordedAt: string;
  createdAt: string;
  version: number;
  payload: Record<string, unknown>;
  actor?: Record<string, unknown>;
  provenance?: Record<string, unknown>;
  applicability?: string;
  gitContext?: Record<string, unknown>;
  confidenceReason?: string;
  happenedAt?: string;
  metadata?: { displayTitle?: string; tags?: string[] };
  revisions?: unknown[];
  evidence?: unknown[];
  relationships?: unknown[];
};
export const types = [
  "decision",
  "adr",
  "business_decision",
  "finding",
  "qa",
  "failure",
  "constraint",
  "project_state",
] as const;
export const label = (value: string | null | undefined): string =>
  !value
    ? "Unknown"
    : value === "qa"
      ? "Question & answer"
      : value === "adr"
        ? "ADR"
        : value.replaceAll("_", " ").replace(/^./, (c) => c.toUpperCase());
export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const response = await fetch(`/api/v1${path}`, {
    ...init,
    headers: { "Content-Type": "application/json", ...init.headers },
  });
  const data = await response.json();
  if (!response.ok)
    throw new Error(
      data.error?.message ?? `Request failed (${response.status})`,
    );
  return data as T;
}
export function post(body: unknown): RequestInit {
  return { method: "POST", body: JSON.stringify(body) };
}
export function githubSignInError(
  code: string | undefined,
): string | undefined {
  if (!code) return undefined;
  const messages: Record<string, string> = {
    AUTH_INVALID:
      "GitHub sign-in was denied, expired, or used a different owner's account. Start again, or recover access with an administrator token.",
    AUTH_FRESH_REQUIRED: "Sign in again before replacing the GitHub account.",
    CONFLICT: "Account access changed during sign-in. Start again.",
    GITHUB_NOT_CONFIGURED:
      "Ask the administrator to configure GitHub sign-in, or use a passkey.",
    GITHUB_UNAVAILABLE:
      "GitHub is unavailable. Use an independent passkey or try again later. Cached machine access follows its existing expiry.",
    GITHUB_RATE_LIMITED:
      "GitHub is limiting requests. Use a passkey or try again later.",
  };
  return (
    messages[code] ?? "GitHub sign-in could not be completed. Please try again."
  );
}
export async function signInWithGithub(
  intent: "setup" | "login" | "link" | "replace" | "recover",
  setupToken?: string,
): Promise<void> {
  const result = await api<{ authorizationUrl: string }>(
    "/auth/github/options",
    post({ intent, ...(setupToken ? { setupToken } : {}) }),
  );
  window.location.assign(result.authorizationUrl);
}
