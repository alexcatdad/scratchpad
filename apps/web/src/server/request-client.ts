import { isIP } from "node:net";

const address = (value: string) =>
  value.startsWith("::ffff:") ? value.slice(7) : value;

/** Only server peer metadata and explicitly trusted proxy hops identify clients. */
export function requestClient(
  request: Request,
  peer: string | undefined,
  proxies: readonly string[] = [],
): string {
  if (!peer || !isIP(address(peer))) return "unknown";
  let client = address(peer);
  const trusted = new Set(proxies.map(address));
  const forwarded = request.headers.get("x-forwarded-for");
  if (!trusted.has(client) || !forwarded || forwarded.length > 8192)
    return client;
  const hops = forwarded.split(",");
  if (hops.length > 32) return client;
  while (trusted.has(client) && hops.length) {
    const hop = address(hops.pop()?.trim() ?? "");
    if (!isIP(hop)) return client;
    client = hop;
  }
  return client;
}
