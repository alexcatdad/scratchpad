import { afterEach, expect, it, vi } from "vitest";
import { api, post } from "./api";

afterEach(() => vi.unstubAllGlobals());

for (const id of [
  "victim/accept?",
  "../../auth/logout#",
  ".",
  "..",
  "victim#",
  "victim?",
  "victim%2faccept",
  "victim\\accept",
])
  it(`keeps malformed stored ID ${JSON.stringify(id)} inert for selected actions`, async () => {
    const fetcher = vi.fn(async () => Response.json({}));
    vi.stubGlobal("fetch", fetcher);
    for (const path of [
      `/projects/${encodeURIComponent(id)}/settings`,
      `/projects/${encodeURIComponent(id)}/context`,
      `/records/${encodeURIComponent(id)}/revisions`,
      `/relationships/${encodeURIComponent(id)}/reject`,
      `/suggestions/${encodeURIComponent(id)}/reject`,
      `/auth/credentials/${encodeURIComponent(id)}`,
      `/ai/jobs/${encodeURIComponent(id)}/retry`,
    ])
      await expect(api(path, post({ expectedVersion: 1 }))).rejects.toThrow(
        "identifier",
      );
    expect(fetcher).not.toHaveBeenCalled();
  });
