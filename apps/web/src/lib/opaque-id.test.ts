import { afterEach, expect, it, vi } from "vitest";
import { api, post } from "./api";
import { encodedId } from "./opaque-id";

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
  "\ud800",
  "\udfff",
])
  it(`keeps malformed stored ID ${JSON.stringify(id)} inert for selected actions`, async () => {
    const fetcher = vi.fn(async () => Response.json({}));
    vi.stubGlobal("fetch", fetcher);
    for (const path of [
      `/projects/${encodedId(id)}/settings`,
      `/projects/${encodedId(id)}/context`,
      `/records/${encodedId(id)}/revisions`,
      `/relationships/${encodedId(id)}/reject`,
      `/suggestions/${encodedId(id)}/reject`,
      `/auth/credentials/${encodedId(id)}`,
      `/ai/jobs/${encodedId(id)}/retry`,
    ])
      await expect(api(path, post({ expectedVersion: 1 }))).rejects.toThrow(
        "identifier",
      );
    expect(fetcher).not.toHaveBeenCalled();
  });
