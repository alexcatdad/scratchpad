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
      customCss: ["./src/styles/custom.css"],
      logo: { src: "./public/favicon.svg", alt: "" },
      components: {
        Hero: "./src/components/Hero.astro",
        PageTitle: "./src/components/PageTitle.astro",
      },
      social: [
        {
          icon: "github",
          label: "GitHub",
          href: "https://github.com/alexcatdad/scratchpad",
        },
      ],
      editLink: {
        baseUrl:
          "https://github.com/alexcatdad/scratchpad/edit/main/apps/docs/",
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
            { label: "Optional PostgreSQL", slug: "guides/postgresql" },
            { label: "Optional AI & semantic memory", slug: "guides/ai" },
            {
              label: "Optional extensions & API clients",
              slug: "guides/optional-extensions",
            },
            { label: "MCP & project context", slug: "guides/mcp" },
            { label: "Agent integration", slug: "guides/agents" },
            { label: "Connect Codex", slug: "guides/codex" },
            { label: "Connect ChatGPT on macOS", slug: "guides/chatgpt" },
            {
              label: "Releases & installation channels",
              slug: "guides/releases",
            },
            { label: "MVP status & acceptance", slug: "guides/mvp-acceptance" },
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
