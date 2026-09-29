import starlight from "@astrojs/starlight";
import { defineConfig } from "astro/config";

export default defineConfig({
  site: "https://alexcatdad.github.io",
  base: "/scratchpad",
  trailingSlash: "always",
  // Bundle each importer's YAML dependency; hoisted lint tooling uses another major.
  vite: {
    environments: { prerender: { resolve: { noExternal: ["js-yaml"] } } },
  },
  integrations: [
    starlight({
      title: "Scratchpad",
      description: "Private project memory for developers and coding agents.",
      defaultLocale: "root",
      locales: { root: { label: "English", lang: "en" } },
      favicon: "/favicon.svg",
      social: [
        {
          icon: "github",
          label: "GitHub",
          href: "https://github.com/alexcatdad/scratchpad",
        },
      ],
      editLink: {
        baseUrl:
          "https://github.com/alexcatdad/scratchpad/edit/main/apps/docs/src/content/docs/",
      },
      sidebar: [
        { label: "Overview", slug: "overview" },
        {
          label: "Get started",
          items: [
            {
              label: "Installation & development",
              slug: "guides/installation",
            },
            { label: "Configuration", slug: "guides/configuration" },
            { label: "MCP & project context", slug: "guides/mcp" },
          ],
        },
        {
          label: "Understand Scratchpad",
          items: [
            { label: "Architecture", slug: "reference/architecture" },
            { label: "Security & recovery", slug: "reference/security" },
            { label: "Records & API", slug: "reference/records" },
            {
              label: "Import, export & mirroring",
              slug: "guides/import-export",
            },
          ],
        },
        { label: "Contributing", slug: "contributing" },
      ],
    }),
  ],
});
