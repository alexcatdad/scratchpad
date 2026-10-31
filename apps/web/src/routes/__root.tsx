import {
  createRootRoute,
  HeadContent,
  Outlet,
  Scripts,
} from "@tanstack/react-router";
import "../styles.css";
export const Route = createRootRoute({
  head: () => ({
    meta: [
      { charSet: "utf-8" },
      { name: "viewport", content: "width=device-width, initial-scale=1" },
      {
        name: "theme-color",
        content: "#f2f2f5",
        media: "(prefers-color-scheme: light)",
      },
      {
        name: "theme-color",
        content: "#1e1e21",
        media: "(prefers-color-scheme: dark)",
      },
      { title: "Scratchpad — Project memory" },
    ],
  }),
  component: Root,
});
function Root() {
  return (
    <html lang="en">
      <head>
        <HeadContent />
      </head>
      <body>
        <Outlet />
        <Scripts />
      </body>
    </html>
  );
}
