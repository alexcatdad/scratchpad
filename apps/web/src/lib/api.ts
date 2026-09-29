export type Project = {
  id: string;
  name: string;
  kind: string;
  version: number;
  settings: {
    repoMirroring: { enabled: boolean; recordTypes: string[] };
    crossProjectAnalysis: boolean;
    aiProcessing: boolean;
  };
};
export type MemoryRecord = {
  id: string;
  projectId: string;
  type: string;
  title: string;
  content: string;
  authority: string;
  confidence: string;
  recordedAt: string;
  createdAt: string;
  version: number;
  payload: Record<string, unknown>;
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
export const label = (value: string) =>
  value === "qa"
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
