import { z } from "zod";
import { ApiError } from "./domain";

export const githubUsername = z
  .string()
  .trim()
  .min(1)
  .max(39)
  .regex(/^[a-z\d](?:[a-z\d]|-(?=[a-z\d]))*$/i);
export type GithubProfile = {
  username: string;
  displayName: string | null;
  avatarUrl: string | null;
  profileUrl: string;
  fetchedAt: string;
};

/** Public presentation only: never credentials, ownership verification or authorization. */
export async function fetchGithubProfile(
  username: string,
  fetcher: typeof fetch = fetch,
): Promise<GithubProfile> {
  const selected = githubUsername.parse(username);
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 5000);
  try {
    const result = await fetcher(
      `https://api.github.com/users/${encodeURIComponent(selected)}`,
      {
        headers: {
          Accept: "application/vnd.github+json",
          "X-GitHub-Api-Version": "2026-03-10",
          "User-Agent": "Scratchpad-public-profile",
        },
        redirect: "error",
        signal: controller.signal,
      },
    );
    if (result.status === 404)
      throw new ApiError(
        404,
        "GITHUB_PROFILE_NOT_FOUND",
        "Public GitHub profile was not found.",
      );
    if (result.status === 403 || result.status === 429)
      throw new ApiError(
        503,
        "GITHUB_RATE_LIMITED",
        "GitHub is limiting public profile requests. Try again later.",
      );
    if (!result.ok || !result.body) throw new Error("Unavailable");
    const reader = result.body.getReader();
    const chunks: Uint8Array[] = [];
    let bytes = 0;
    try {
      for (;;) {
        const part = await reader.read();
        if (part.done) break;
        bytes += part.value.byteLength;
        if (bytes > 65536) throw new Error("Oversized response");
        chunks.push(part.value);
      }
    } finally {
      await reader.cancel();
    }
    const data = z
      .object({
        login: githubUsername,
        type: z.literal("User"),
        name: z.string().max(200).nullable(),
        avatar_url: z.string().max(2048).nullable(),
      })
      .parse(JSON.parse(Buffer.concat(chunks).toString("utf8")));
    // Usernames are case insensitive. Do not silently follow renamed accounts or redirects.
    if (data.login.toLowerCase() !== selected.toLowerCase())
      throw new Error("Mismatched profile");
    let avatarUrl: string | null = null;
    if (data.avatar_url) {
      const avatar = new URL(data.avatar_url);
      if (
        avatar.protocol === "https:" &&
        avatar.hostname === "avatars.githubusercontent.com" &&
        !avatar.username &&
        !avatar.password &&
        !avatar.port
      )
        avatarUrl = avatar.href;
    }
    return {
      username: data.login,
      displayName: data.name,
      avatarUrl,
      profileUrl: `https://github.com/${data.login}`,
      fetchedAt: new Date().toISOString(),
    };
  } catch (error) {
    if (error instanceof ApiError) throw error;
    throw new ApiError(
      503,
      "GITHUB_UNAVAILABLE",
      "Could not retrieve the public GitHub profile. Try again later.",
    );
  } finally {
    clearTimeout(timer);
  }
}

/** Portable imports are untrusted too; never render arbitrary imported URLs/fields. */
export function presentGithubProfile(value: unknown): GithubProfile | null {
  const parsed = z
    .object({
      username: githubUsername,
      displayName: z.string().max(200).nullable(),
      avatarUrl: z.string().max(2048).nullable(),
      fetchedAt: z.iso.datetime(),
    })
    .safeParse(value);
  if (!parsed.success) return null;
  let avatarUrl: string | null = null;
  try {
    if (parsed.data.avatarUrl) {
      const url = new URL(parsed.data.avatarUrl);
      if (
        url.protocol === "https:" &&
        url.hostname === "avatars.githubusercontent.com" &&
        !url.username &&
        !url.password &&
        !url.port
      )
        avatarUrl = url.href;
    }
  } catch {
    /* Invalid imported avatar is omitted. */
  }
  return {
    ...parsed.data,
    avatarUrl,
    profileUrl: `https://github.com/${parsed.data.username}`,
  };
}
