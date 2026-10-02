# MCP client documentation and acceptance

This runbook covers the local Codex/ChatGPT desktop connection and publication of real setup screenshots. It does not authorize modifying the owner's client configuration or deploying a hosted MCP adapter.

## Authority and scope

- Scratchpad's released MCP runs as a local stdio process. SSH signing authenticates its ordinary HTTPS API requests.
- Official OpenAI connection paths were checked on 2026-10-02: [MCP configuration](https://learn.chatgpt.com/docs/extend/mcp), [plugin server transport](https://developers.openai.com/plugins/build/mcp-server), and [private connectivity](https://developers.openai.com/blog/connect-private-mcp-servers-to-openai-products).
- ChatGPT desktop local configuration and ChatGPT web hosted plugins are separate paths. Verify the specific installed client UI before describing screenshot labels as observed behavior.
- Public examples use `memory.example.com` and generic absolute paths. Keep private instance hostnames, account identifiers, credentials, and personal project history out of screenshots and public pages.

## Read-only preparation

```sh
command -v scratchpad-mcp
scratchpad-mcp --version
codex mcp add --help
codex mcp list --help
```

Do not publish an unfiltered `codex mcp list` or configuration file: other servers may contain private URLs or credentials. Inspect only the necessary entry, and distinguish registration from a successful authenticated tool call.

## Owner connection and verification

Use the public [Codex guide](../../apps/docs/src/content/docs/guides/codex.md) or [ChatGPT macOS guide](../../apps/docs/src/content/docs/guides/chatgpt.md). The owner registers the actual executable/launcher and enrolled public key. When authorized, restart the client connection and verify a read with an explicit checkout path or `projectId`.

A passed check needs all of:

1. The client starts the installed binary and discovers Scratchpad tools.
2. An authenticated read reaches the intended instance.
3. The response resolves the intended project.

Only test capture when authorized. Use a clearly synthetic example, a fresh request ID, then retry the exact same input to verify no duplicate. Existing project history is an example dataset, not product truth.

## Screenshot capture

Capture the installed client's actual UI using the authorized computer-use tools, or use owner-provided screenshots. Do not generate mock setup screenshots or claim a configured server is connected without executing a tool.

Useful screenshots, in order:

1. Desktop **Settings → MCP servers**, showing where to add a server.
2. The actual **Add server → STDIO** form, with generic placeholders before private values are entered.
3. The connection/tool list after successful verification, cropped to exclude unrelated servers and private context.

If a screenshot contains personal data, capture a clean state or omit it. Do not copy private config or invoke credential prompts merely for illustration. Store approved screenshots under `apps/docs/public/images/mcp/`; embed them with `/scratchpad/images/mcp/...` links, descriptive alt text, and captions naming the observed client/version/date. Publish only files that actually exist.

## Documentation checks

```sh
npm exec -- prettier --check apps/docs/src/content/docs/guides/mcp.md apps/docs/src/content/docs/guides/codex.md apps/docs/src/content/docs/guides/chatgpt.md mcp/README.md docs/runbooks/client-connections.md
npm exec -- markdownlint-cli2 apps/docs/src/content/docs/guides/mcp.md apps/docs/src/content/docs/guides/codex.md apps/docs/src/content/docs/guides/chatgpt.md mcp/README.md docs/runbooks/client-connections.md
npm run check --workspace @scratchpad/docs
npm run build --workspace @scratchpad/docs
```

Check rendered pages at desktop and mobile widths, image paths, heading anchors, and client navigation links. A static build confirms output generation; a Pages deployment and a real client connection are separate acceptance checks.
