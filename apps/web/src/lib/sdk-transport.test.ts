import { expect, it, vi } from "vitest";
import { createScratchpadClient } from "../../../../packages/clients/typescript/index";

for (const baseUrl of [
  "http://127.1",
  "http://2130706433",
  "http://0x7f000001",
  "http://127.0.0.1.",
  "https://example.test:65536",
  "https://example.test:0",
  "https://example.test:",
  "http://%6cocalhost",
  "https://example.test?",
  "https://example.test#",
  "https://@example.test",
  "https://:@example.test",
  "http://remote.example.test",
  "http://localhost.example.test",
  "http://127.0.0.1.example.test",
  "http://localhost@remote.example.test",
  "http://[::ffff:127.0.0.1]",
  "http://localhost.",
  "file:///tmp/synthetic",
  "http://",
  "https://synthetic-user:synthetic-password@example.test",
]) {
  it(`rejects unsafe SDK origin ${baseUrl} before any request`, () => {
    const fetcher = vi.fn<typeof fetch>(async () => Response.json({}));
    let message = "";
    try {
      createScratchpadClient({
        baseUrl,
        token: "synthetic-owner-token",
        fetch: fetcher,
      });
    } catch (error) {
      message = error instanceof Error ? error.message : "unknown";
    }
    expect(message).not.toBe("");
    expect(message).not.toContain("synthetic-owner-token");
    expect(message).not.toContain("synthetic-password");
    expect(fetcher).not.toHaveBeenCalled();
  });
}
for (const baseUrl of [
  "https://remote.example.test",
  "http://localhost:3000",
  "http://127.0.0.1:3000",
  "http://[::1]:3000",
  "http://LOCALHOST:3000",
  "http://[0:0:0:0:0:0:0:1]:3000",
]) {
  it(`retains authenticated SDK requests for ${baseUrl}`, async () => {
    const fetcher = vi.fn<typeof fetch>(async (input, init) => {
      const request =
        input instanceof Request ? input : new Request(input, init);
      expect(request.headers.get("Authorization")).toBe(
        "Bearer synthetic-owner-token",
      );
      expect(request.redirect).toBe("error");
      return Response.json({ projects: [] });
    });
    const client = createScratchpadClient({
      baseUrl,
      token: "synthetic-owner-token",
      fetch: fetcher,
    });
    expect((await client.GET("/api/v1/projects")).data).toEqual({
      projects: [],
    });
    expect(fetcher).toHaveBeenCalledOnce();
  });
}

for (const baseUrl of [
  "http://remote.example.test",
  "https://other.example.test",
])
  it(`rejects per-request origin override ${baseUrl} before credential forwarding`, async () => {
    const fetcher = vi.fn<typeof fetch>(async () =>
      Response.json({ projects: [] }),
    );
    const client = createScratchpadClient({
      baseUrl: "https://memory.example.test",
      token: "synthetic-owner-token",
      fetch: fetcher,
    });
    await expect(client.GET("/api/v1/projects", { baseUrl })).rejects.toThrow(
      "instance origin",
    );
    expect(fetcher).not.toHaveBeenCalled();
  });

it("retains the configured origin and redirect guard on per-request options", async () => {
  const fetcher = vi.fn<typeof fetch>(async (input, init) => {
    const request = input instanceof Request ? input : new Request(input, init);
    expect(request.url).toBe("https://memory.example.test/api/v1/projects");
    expect(request.redirect).toBe("error");
    return Response.json({ projects: [] });
  });
  const client = createScratchpadClient({
    baseUrl: "https://memory.example.test",
    token: "synthetic-owner-token",
    fetch: fetcher,
  });
  await client.GET("/api/v1/projects", {
    baseUrl: "https://memory.example.test",
    redirect: "follow",
  });
  expect(fetcher).toHaveBeenCalledOnce();
});
