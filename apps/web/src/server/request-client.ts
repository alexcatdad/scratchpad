import { isIP } from "node:net";

const address = (value: string): string | undefined => {
  if (isIP(value) === 4) return value;
  if (isIP(value) !== 6) return undefined;
  try {
    const canonical = new URL(`http://[${value}]`).hostname.slice(1, -1);
    const mapped = canonical.match(/^::ffff:([0-9a-f]+):([0-9a-f]+)$/);
    if (!mapped) return canonical;
    const high = Number.parseInt(mapped[1], 16);
    const low = Number.parseInt(mapped[2], 16);
    return [high >> 8, high & 255, low >> 8, low & 255].join(".");
  } catch {
    return undefined;
  }
};

/** Only server peer metadata and explicitly trusted proxy hops identify clients. */
export function requestClient(
  request: Request,
  peer: string | undefined,
  proxies: readonly string[] = [],
): string {
  let client = peer ? address(peer) : undefined;
  if (!client) return "unknown";
  const trusted = new Set(proxies.map(address));
  const forwarded = request.headers.get("x-forwarded-for");
  if (!trusted.has(client) || !forwarded || forwarded.length > 8192)
    return client;
  const hops = forwarded.split(",");
  if (hops.length > 32) return client;
  while (trusted.has(client) && hops.length) {
    const hop = address(hops.pop()?.trim() ?? "");
    if (!hop) return client;
    client = hop;
  }
  return client;
}
