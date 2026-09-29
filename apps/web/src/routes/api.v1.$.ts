import { createFileRoute } from "@tanstack/react-router";
import { handleApiRequest } from "../server/api";

const handle = ({ request }: { request: Request }) => handleApiRequest(request);
export const Route = createFileRoute("/api/v1/$")({
  server: {
    handlers: { GET: handle, POST: handle, PATCH: handle, DELETE: handle },
  },
});
