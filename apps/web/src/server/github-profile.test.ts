import { createHash } from "node:crypto";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, describe, expect, it, vi } from "vitest";
import { createApi } from "./api";
import { fetchGithubProfile, presentGithubProfile } from "./github-profile";

const publicUser = {
  login: "octocat",
  type: "User",
  name: "Octocat",
  avatar_url: "https://avatars.githubusercontent.com/u/1?v=4",
  email: "ignored@example.test",
  private_repos: 20,
};
const response = () => Response.json(publicUser);
afterEach(() => vi.useRealTimers());

describe("public GitHub profile", () => {
  it("sanitizes imported presentation snapshots before rendering", () => {
    const value = {
      username: "octocat",
      displayName: "Octocat",
      avatarUrl: "http://127.0.0.1/private",
      profileUrl: "javascript:alert(1)",
      fetchedAt: new Date().toISOString(),
      secret: "ignored",
    };
    expect(presentGithubProfile(value)).toEqual({
      username: "octocat",
      displayName: "Octocat",
      avatarUrl: null,
      profileUrl: "https://github.com/octocat",
      fetchedAt: value.fetchedAt,
    });
    expect(presentGithubProfile({ ...value, username: "../admin" })).toBeNull();
  });

  it("uses the fixed public API, no credentials, and only presentation fields", async () => {
    const fetcher = vi.fn<typeof fetch>(async () => response());
    const profile = await fetchGithubProfile("octocat", fetcher);
    expect(fetcher).toHaveBeenCalledWith(
      "https://api.github.com/users/octocat",
      expect.objectContaining({
        redirect: "error",
        signal: expect.any(AbortSignal),
      }),
    );
    expect(
      new Headers(fetcher.mock.calls[0]?.[1]?.headers).has("authorization"),
    ).toBe(false);
    expect(profile).toEqual({
      username: "octocat",
      displayName: "Octocat",
      avatarUrl: publicUser.avatar_url,
      profileUrl: "https://github.com/octocat",
      fetchedAt: expect.any(String),
    });
  });
  it("rejects URL/path usernames before networking and drops unsafe avatar URLs", async () => {
    const fetcher = vi.fn<typeof fetch>(async () => response());
    await expect(fetchGithubProfile("../user", fetcher)).rejects.toThrow();
    expect(fetcher).not.toHaveBeenCalled();
    expect(
      (
        await fetchGithubProfile("octocat", async () =>
          Response.json({
            ...publicUser,
            avatar_url: "https://evil.example/avatar",
          }),
        )
      ).avatarUrl,
    ).toBeNull();
    await expect(
      fetchGithubProfile("octocat", async () =>
        Response.json({ ...publicUser, type: "Organization" }),
      ),
    ).rejects.toMatchObject({ code: "GITHUB_UNAVAILABLE" });
  });
  it("bounds provider failures, timeouts and response size without exposing remote bodies", async () => {
    await expect(
      fetchGithubProfile(
        "octocat",
        async () => new Response("private upstream details", { status: 403 }),
      ),
    ).rejects.toMatchObject({ code: "GITHUB_RATE_LIMITED", status: 503 });
    await expect(
      fetchGithubProfile(
        "octocat",
        async () => new Response("x".repeat(65537)),
      ),
    ).rejects.toMatchObject({ code: "GITHUB_UNAVAILABLE" });
    vi.useFakeTimers();
    const pending = fetchGithubProfile(
      "octocat",
      async (_url, init) =>
        new Promise((_resolve, reject) =>
          init?.signal?.addEventListener("abort", () =>
            reject(new Error("secret transport detail")),
          ),
        ),
    );
    const assertion = expect(pending).rejects.toMatchObject({
      code: "GITHUB_UNAVAILABLE",
    });
    await vi.advanceTimersByTimeAsync(5000);
    await assertion;
  });
  it("links, refreshes and unlinks with audit, optimistic concurrency and unchanged authentication", async () => {
    const fetcher = vi.fn<typeof fetch>(async () => response());
    const directory = mkdtempSync(join(tmpdir(), "scratchpad-github-profile-"));
    const databasePath = join(directory, "memory.sqlite");
    let api = createApi({
      databasePath,
      origin: "http://localhost:3000",
      githubFetch: fetcher,
    });
    try {
      await api.store.insert("owner", {
        id: "owner",
        displayName: "Test owner",
      });
      await api.store.insert("credential", {
        id: "test-key",
        kind: "ssh",
        fingerprint: "SHA256:test",
      });
      await api.store.insert("session", {
        id: createHash("sha256").update("token").digest("hex"),
        credentialId: "test-key",
        browser: false,
        expiresAt: new Date(Date.now() + 60000).toISOString(),
      });
      const call = async (method: string, body: unknown, token = true) =>
        api.handleRequest(
          new Request("http://localhost:3000/api/v1/profile/github", {
            method,
            headers: token ? { authorization: "Bearer token" } : {},
            body: JSON.stringify(body),
          }),
        );
      expect(
        (await call("POST", { username: "octocat", expectedVersion: 0 }, false))
          .status,
      ).toBe(401);
      expect(fetcher).not.toHaveBeenCalled();
      const linked = await (
        await call("POST", { username: "octocat", expectedVersion: 0 })
      ).json();
      expect(linked.profile.github.username).toBe("octocat");
      expect(linked.profile.displayName).toBe("Test owner");
      expect(
        (await call("POST", { username: "octocat", expectedVersion: 0 }))
          .status,
      ).toBe(409);
      await api.close();
      api = createApi({
        databasePath,
        origin: "http://localhost:3000",
        githubFetch: fetcher,
      });
      const loaded = await (
        await api.handleRequest(
          new Request("http://localhost:3000/api/v1/profile", {
            headers: { authorization: "Bearer token" },
          }),
        )
      ).json();
      expect(loaded.profile.github).toEqual(linked.profile.github);
      await api.store.insert("record", {
        id: "source-test",
        payload: { text: "Original source" },
      });
      const original = await api.store.get("record", "source-test");
      const refreshed = await (
        await call("POST", {
          username: "octocat",
          expectedVersion: linked.profile.version,
        })
      ).json();
      const removed = await (
        await call("DELETE", { expectedVersion: refreshed.profile.version })
      ).json();
      expect(removed.profile.github).toBeNull();
      // An in-flight fetch must not overwrite a concurrently changed profile.
      let release!: () => void;
      const gate = new Promise<void>((resolve) => {
        release = resolve;
      });
      fetcher.mockImplementationOnce(async () => {
        await gate;
        return response();
      });
      const racing = call("POST", {
        username: "octocat",
        expectedVersion: removed.profile.version,
      });
      await vi.waitFor(() => expect(fetcher).toHaveBeenCalledTimes(3));
      const changed = await api.store.update(
        "profile",
        { ...removed.profile, displayName: "Concurrent name" },
        removed.profile.version,
      );
      release();
      expect((await racing).status).toBe(409);
      expect(
        (await api.store.get("profile", "owner-profile"))?.displayName,
      ).toBe(changed.displayName);
      expect(
        (await api.store.get("profile", "owner-profile"))?.github,
      ).toBeNull();
      expect(await api.store.get("record", "source-test")).toEqual(original);
      expect(await api.store.list("credential")).toHaveLength(1);
      expect(await api.store.list("session")).toHaveLength(1);
      expect(
        (await api.store.list("audit")).map((a) => a.action).sort(),
      ).toEqual([
        "profile.github.unlinked",
        "profile.github.updated",
        "profile.github.updated",
      ]);
    } finally {
      await api.close();
      rmSync(directory, { recursive: true, force: true });
    }
  });
});
