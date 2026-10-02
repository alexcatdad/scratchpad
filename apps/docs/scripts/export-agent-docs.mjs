import { access, mkdir, readdir, readFile, writeFile } from "node:fs/promises";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = fileURLToPath(new URL("../", import.meta.url));
const source = join(root, "src/content/docs");
const output = join(root, "dist");
const site = "https://alexcatdad.github.io/scratchpad";
const priority = [
  "guides/agents",
  "guides/mcp",
  "guides/codex",
  "guides/chatgpt",
  "guides/installation",
  "guides/configuration",
  "reference/records",
  "reference/security",
  "guides/ai",
];

/**
 * @typedef {{ slug: string, title: string, description: string, body: string, markdown: string }} Document
 */

/**
 * @param {string} directory
 * @param {string} prefix
 * @returns {Promise<Document[]>}
 */
async function collect(directory, prefix = "") {
  const documents = [];
  for (const entry of await readdir(directory, { withFileTypes: true })) {
    const path = join(directory, entry.name);
    const relative = `${prefix}${entry.name}`;
    if (entry.isDirectory()) {
      documents.push(...(await collect(path, `${relative}/`)));
    } else if (entry.name.endsWith(".md")) {
      const text = await readFile(path, "utf8");
      const match = text.match(/^---\r?\n([\s\S]*?)\r?\n---\r?\n/);
      if (!match) throw new Error(`Missing frontmatter: ${relative}`);
      const title = match[1].match(/^title: (.+)$/m)?.[1];
      const description = match[1].match(/^description: (.+)$/m)?.[1];
      if (!title || !description)
        throw new Error(`Missing metadata: ${relative}`);
      documents.push({
        slug: relative.slice(0, -3),
        title: title.replace(/^(["'])(.*)\1$/, "$2"),
        description: description.replace(/^(["'])(.*)\1$/, "$2"),
        body: text.slice(match[0].length).trim(),
        markdown: "",
      });
    }
  }
  return documents;
}

const documents = await collect(source);
const slugs = new Set(documents.map((document) => document.slug));
for (const slug of priority) {
  if (!slugs.has(slug)) throw new Error(`Missing integration guide: ${slug}`);
}
documents.sort((a, b) => {
  /** @param {string} slug */
  const rank = (slug) => {
    const index = priority.indexOf(slug);
    return index < 0 ? priority.length : index;
  };
  return rank(a.slug) - rank(b.slug) || a.slug.localeCompare(b.slug, "en");
});

for (const document of documents) {
  // Verify the readable counterpart was built before publishing its Markdown.
  await access(join(output, document.slug, "index.html"));
  const localLinks = [
    ...document.body.matchAll(/\]\((\/scratchpad\/[^\s)]*)\)/g),
  ];
  for (const [, link] of localLinks) {
    const pathname = link.split(/[?#]/)[0].slice("/scratchpad/".length);
    if (["llms.txt", "llms-full.txt"].includes(pathname)) continue;
    await access(
      join(output, pathname.endsWith("/") ? `${pathname}index.html` : pathname),
    );
  }
  document.markdown = `# ${document.title}\n\n${document.description}\n\n${document.body}\n`;
  document.markdown = document.markdown.replace(
    /\]\((\/scratchpad\/[^\s)]*)\)/g,
    (_, link) => {
      const url = new URL(link, site);
      const slug = url.pathname.slice("/scratchpad/".length).replace(/\/$/, "");
      if (slugs.has(slug)) url.pathname = `/scratchpad/${slug}.md`;
      return `](${url.href})`;
    },
  );
  const target = join(output, `${document.slug}.md`);
  await mkdir(dirname(target), { recursive: true });
  await writeFile(target, document.markdown);
}

const intro = `# Scratchpad\n\n> Private, self-hosted project memory for developers and coding agents. Capture decisions, findings, Q&A, failures, constraints and project state with immutable evidence and explicit provenance.\n\nScratchpad uses a local stdio MCP binary connected to a central HTTP API. The server URL is not a hosted MCP endpoint. Discover the running binary's exact tool schemas through MCP tools/list. AI is optional; capture and deterministic retrieval work without it. These exports contain public product documentation only.\n`;
const index = `${intro}\n## Start here\n\n- [Complete documentation bundle](${site}/llms-full.txt): All exported Markdown guides in one document, with MCP integration first.\n- [OpenAPI contract](https://raw.githubusercontent.com/alexcatdad/scratchpad/main/docs/openapi.json): Implemented HTTP routes and schemas.\n\n## Guides and reference\n\n${documents.map((document) => `- [${document.title}](${site}/${document.slug}.md): ${document.description}`).join("\n")}\n`;
await writeFile(join(output, "llms.txt"), index);
await writeFile(
  join(output, "llms-full.txt"),
  `${intro}\n${documents.map((document) => `---\n\nSource: ${site}/${document.slug}.md\n\n${document.markdown}`).join("\n")}`,
);
console.log(
  `Exported llms.txt, llms-full.txt and ${documents.length} Markdown guides.`,
);
