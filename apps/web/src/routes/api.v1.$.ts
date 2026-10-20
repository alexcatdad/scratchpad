import { createFileRoute } from "@tanstack/react-router";

const handle = () =>
  Response.json(
    {
      error: {
        code: "GO_API_REQUIRED",
        message: "API requests must use the configured Go API origin.",
      },
    },
    { status: 503, headers: { "Cache-Control": "no-store" } },
  );
export const Route = createFileRoute("/api/v1/$")({
  server: {
    handlers: { GET: handle, POST: handle, PATCH: handle, DELETE: handle },
  },
});
