import { createFileRoute } from "@tanstack/react-router";

const handle = () =>
  Response.json(
    { error: { code: "GO_API_REQUIRED", message: "Use the configured Go API." } },
    { status: 503, headers: { "Cache-Control": "no-store" } },
  );
export const Route = createFileRoute("/health")({
  server: {
    handlers: { GET: handle, POST: handle, PATCH: handle, DELETE: handle },
  },
});
