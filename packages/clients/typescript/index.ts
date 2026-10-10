import createClient from "openapi-fetch";
import type { components, paths } from "./schema.js";

export type { components, paths };
export type ApiError = components["schemas"]["Error"];

/** baseUrl is the instance origin. Tokens come from the existing SSH challenge flow. */
export function createScratchpadClient(options: {
  baseUrl: string;
  token?: string;
  fetch?: typeof globalThis.fetch;
  credentials?: RequestCredentials;
  origin?: string;
}) {
  const invalidOrigin = () =>
    new Error(
      "Use an HTTPS instance origin or supported loopback HTTP origin.",
    );
  let url: URL;
  try {
    url = new URL(options.baseUrl);
  } catch {
    throw invalidOrigin();
  }
  const suppliedAuthority = options.baseUrl.match(
    /^https?:\/\/([^/?#\\\s]+)\/?$/i,
  )?.[1];
  const suppliedHost = suppliedAuthority?.startsWith("[")
    ? suppliedAuthority.slice(0, suppliedAuthority.indexOf("]") + 1)
    : suppliedAuthority?.split(":")[0].toLowerCase();
  if (
    !suppliedAuthority ||
    suppliedAuthority.endsWith(":") ||
    url.port === "0" ||
    !["http:", "https:"].includes(url.protocol) ||
    !url.hostname ||
    url.username ||
    url.password ||
    url.search ||
    url.hash ||
    url.pathname !== "/" ||
    (url.protocol === "http:" &&
      url.hostname !== "[::1]" &&
      !["localhost", "127.0.0.1"].includes(suppliedHost ?? "")) ||
    (url.protocol === "http:" &&
      !["localhost", "127.0.0.1", "[::1]"].includes(url.hostname))
  )
    throw invalidOrigin();
  const client = createClient<paths>({
    baseUrl: url.origin,
    redirect: "error",
    fetch: options.fetch,
    credentials: options.credentials,
    headers: {
      ...(options.token ? { Authorization: `Bearer ${options.token}` } : {}),
      ...(options.origin ? { Origin: options.origin } : {}),
    },
  });
  client.use({
    onRequest({ request }) {
      if (new URL(request.url).origin !== url.origin) throw invalidOrigin();
      return new Request(request, { redirect: "error" });
    },
  });
  return client;
}
