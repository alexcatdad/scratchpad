import { createFileRoute } from "@tanstack/react-router";
import { getRequestIP } from "@tanstack/react-start/server";
import { handleApiRequest } from "../server/api";

const handle = ({ request }: { request: Request }) =>
  handleApiRequest(request, getRequestIP({ xForwardedFor: false }));
export const Route = createFileRoute("/api/v1/$")({
  server: {
    handlers: { GET: handle, POST: handle, PATCH: handle, DELETE: handle },
  },
});
