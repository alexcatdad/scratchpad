import { createRootRoute, Outlet } from "@tanstack/react-router";

// The HTML document, meta tags and title live in index.html.
export const Route = createRootRoute({ component: Outlet });
