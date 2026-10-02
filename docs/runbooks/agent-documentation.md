# Publish agent-readable documentation

## Scope and sources

Public documentation lives in `apps/docs/src/content/docs/`. The agent integration
guide explains local stdio setup, project scope, implemented tools, retries and
optional capabilities. Exact runtime schemas come from MCP `tools/list`; the
implemented HTTP contract is `docs/openapi.json`.

The docs build runs `apps/docs/scripts/export-agent-docs.mjs` after Astro. It
exports every plain Markdown guide without frontmatter, rewrites internal guide
links to absolute Markdown URLs where an export exists, and checks readable
counterparts and internal links against the built site. The MDX homepage is not
exported because it contains presentation components.

Generated files live only in ignored `apps/docs/dist/`:

- `llms.txt`: product introduction and linked documentation index.
- `llms-full.txt`: all Markdown guides, with agent/MCP setup first.
- `<guide-path>.md`: individual Markdown guides.

These are generated from public documentation, never from application records,
local environment files or private deployment runbooks. Do not add private URLs,
credentials or owner memory to the public source. The exports help agents read
documentation; they do not configure an MCP client or grant access to memory.

## Build and verify

Use the Node version pinned in `.node-version`, from the repository root:

```sh
npm run check -w @scratchpad/docs
npm run build -w @scratchpad/docs
npm run lint
```

The exporter fails on missing metadata, priority guides, readable counterparts or
broken internal Markdown links. Inspect `apps/docs/dist/llms.txt` and
`apps/docs/dist/guides/agents.md` for readable headings, absolute links, valid
examples and the distinction between implemented capabilities and optional AI.

The generated files are produced by the existing Pages workflow; no separately
maintained copies are committed. Development mode does not run this post-build
exporter. Use a built preview to inspect text exports locally.

## Publication

Commit the sources, decision record and runbook. Push when publication is
requested. Wait for the Documentation workflow to deploy the exact commit; also
check canonical CI results. Verify successful responses from:

- <https://alexcatdad.github.io/scratchpad/llms.txt>
- <https://alexcatdad.github.io/scratchpad/llms-full.txt>
- <https://alexcatdad.github.io/scratchpad/guides/agents.md>
- <https://alexcatdad.github.io/scratchpad/guides/agents/>

Check the live index's linked Markdown documents and compare the deployed text
with the local generated artifacts. A local build alone is not publication.
