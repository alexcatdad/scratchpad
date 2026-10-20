import { createFileRoute } from "@tanstack/react-router";

const handle = () =>
  Response.json(
    {
      error: { code: "DASHBOARD_ONLY", message: "Use the configured Go API." },
    },
    { status: 503, headers: { "Cache-Control": "no-store" } },
  );
export const Route = createFileRoute("/ready")({
  server: {
    handlers: { GET: handle, POST: handle, PATCH: handle, DELETE: handle },
  },
});
