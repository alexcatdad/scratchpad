import { existsSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { serve } from "srvx/node";
import { staticMiddleware } from "srvx/static";

for (const name of [".env.production", ".env"]) {
  const path = fileURLToPath(new URL(name, import.meta.url));
  if (existsSync(path)) process.loadEnvFile(path);
}
process.env.NODE_ENV ??= "production";
const { default: entry } = await import("./dist/server/server.js");

/** @type {import("srvx").ServerMiddleware} */
const accessLog = async (request, next) => {
  const started = performance.now();
  const path = new URL(request.url).pathname;
  let status = 500;
  try {
    const response = await next();
    status = response.status;
    return response;
  } finally {
    console.log(
      `${request.method} ${path} ${status} ${(performance.now() - started).toFixed(2)}ms`,
    );
  }
};

const server = serve({
  ...entry,
  gracefulShutdown: true,
  middleware: [
    accessLog,
    staticMiddleware({
      dir: fileURLToPath(new URL("./dist/client", import.meta.url)),
    }),
  ],
  error: () => {
    console.error("Request failed with an internal server error.");
    return new Response("Internal server error.", { status: 500 });
  },
});
await server.ready();
