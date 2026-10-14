import { createServer } from "node:http";
import { createApi } from "../apps/web/src/server/api";

// The retained API implementation runs in a real process and HTTP listener.
// This fixture exercises API rollback, separately from dashboard rendering.
const origin = process.env.SCRATCHPAD_PUBLIC_URL;
if (!origin) throw new Error("A disposable public origin is required");
const api = createApi({
  origin,
  databasePath: ":memory:",
  databaseUrl: process.env.SCRATCHPAD_DATABASE_URL,
});
const server = createServer(async (request, response) => {
  try {
    const headers = new Headers();
    for (const [name, value] of Object.entries(request.headers)) {
      if (value !== undefined) headers.set(name, String(value));
    }
    const result = await api.handleRequest(
      new Request(new URL(request.url ?? "/", origin), {
        method: request.method,
        headers,
      }),
      request.socket.remoteAddress,
    );
    response.writeHead(result.status, Object.fromEntries(result.headers));
    response.end(Buffer.from(await result.arrayBuffer()));
  } catch {
    response.writeHead(500).end();
  }
});
server.listen(Number(new URL(origin).port), "127.0.0.1");
process.once("SIGTERM", () => {
  server.close(async () => {
    await api.close();
    process.exit(0);
  });
  server.closeAllConnections();
});
