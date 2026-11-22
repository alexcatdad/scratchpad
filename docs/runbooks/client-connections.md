# Client connection documentation and acceptance

This runbook covers documenting and verifying local agent connections (Claude Code, Codex and ChatGPT desktop local chats) through the `scratchpad` command, its hooks and the companion skill. It does not authorize modifying the owner's client configuration or deploying a hosted MCP adapter.

## Authority and scope

- Agents call the `scratchpad` command bundled in the app. It hands work to the local capture service, which signs the ordinary HTTPS API requests with the owner's SSH key. The app no longer ships an MCP server.
- Hooks (`scratchpad hooks print --agent claude-code|codex`) exist only for Claude Code and Codex. They report mistake signals; they do not give the agent memory.
- Official OpenAI connection paths were checked on 2026-10-02: [MCP configuration](https://learn.chatgpt.com/docs/extend/mcp), [plugin server transport](https://developers.openai.com/plugins/build/mcp-server), and [private connectivity](https://developers.openai.com/blog/connect-private-mcp-servers-to-openai-products). ChatGPT web and hosted chats cannot reach the local command; Scratchpad has no hosted MCP endpoint.
- Verify the specific installed client UI before describing its labels as observed behavior.
- Public examples use `memory.example.com` and generic absolute paths. Keep private instance hostnames, account identifiers, credentials, and personal project history out of screenshots and public pages.

## Read-only preparation

```sh
command -v scratchpad
scratchpad --version
scratchpad status
scratchpad hooks print --agent codex
claude mcp get scratchpad
codex mcp get scratchpad
```

`hooks print` changes no file. `claude mcp get` health-checks the entry it shows. Inspect only the `scratchpad` entry, which from an earlier app version points at a binary that no longer exists; do not publish an unfiltered server list or configuration file, because other entries may contain private URLs or credentials. Distinguish installation from a successful authenticated tool call.

## Owner connection and verification

Use the public [agent integration guide](../../apps/docs/src/content/docs/guides/agents.md), [Codex guide](../../apps/docs/src/content/docs/guides/codex.md) or [ChatGPT macOS guide](../../apps/docs/src/content/docs/guides/chatgpt.md). The owner connects the capture service, installs hooks and the skill, and removes any old `scratchpad` MCP entry. When authorized, verify a read with an explicit checkout path or `projectId`.

A passed check needs all of:

1. The agent runs the installed `scratchpad` command.
2. An authenticated read reaches the intended instance.
3. The response resolves the intended project.

Only test capture when authorized. Use a clearly synthetic example, a fresh request ID, then retry the exact same input to verify no duplicate. Existing project history is an example dataset, not product truth.

## Screenshots

Capture the installed client's actual UI using the authorized computer-use tools, or use owner-provided screenshots. Do not generate mock setup screenshots or claim a connection works without executing a tool. If a screenshot contains personal data, capture a clean state or omit it. Do not copy private config or invoke credential prompts merely for illustration. Publish only files that actually exist, with descriptive alt text and captions naming the observed client/version/date.

## Documentation checks

```sh
npm exec -- prettier --check apps/docs/src/content/docs/guides/agents.md apps/docs/src/content/docs/guides/codex.md apps/docs/src/content/docs/guides/chatgpt.md mcp/README.md docs/runbooks/client-connections.md
npm exec -- markdownlint-cli2 apps/docs/src/content/docs/guides/agents.md apps/docs/src/content/docs/guides/codex.md apps/docs/src/content/docs/guides/chatgpt.md mcp/README.md docs/runbooks/client-connections.md
npm run check --workspace @scratchpad/docs
npm run build --workspace @scratchpad/docs
```

Check rendered pages at desktop and mobile widths, image paths, heading anchors, and client navigation links. A static build confirms output generation; a Pages deployment and a real client connection are separate acceptance checks.
