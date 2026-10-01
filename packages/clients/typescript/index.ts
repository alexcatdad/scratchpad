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
  return createClient<paths>({
    baseUrl: options.baseUrl.replace(/\/$/, ""),
    fetch: options.fetch,
    credentials: options.credentials,
    headers: {
      ...(options.token ? { Authorization: `Bearer ${options.token}` } : {}),
      ...(options.origin ? { Origin: options.origin } : {}),
    },
  });
}
