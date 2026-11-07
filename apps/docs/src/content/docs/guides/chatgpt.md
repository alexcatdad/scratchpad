---
title: Connect ChatGPT on macOS
description: Use Scratchpad through the desktop app's local MCP connection, and understand web compatibility.
---

The ChatGPT desktop app supports local STDIO MCP connections on its Codex host. Scratchpad uses this path: ChatGPT starts the bundled `scratchpad-mcp` on your Mac, which hands reads and captures to the Scratchpad capture service. The capture service talks to your server with your SSH key.

Local Work/Codex chats that can run shell commands can also use the [`scratchpad` command](/scratchpad/guides/agents/) directly, the recommended path for coding agents. Set up the MCP server below when you want the tools in ChatGPT's MCP list.

## Add the local server

First [install the app](/scratchpad/guides/installation/) and connect it to your server in **Connection**. Have these values ready:

| Field     | Value                                                          |
| --------- | -------------------------------------------------------------- |
| Name      | `scratchpad`                                                   |
| Transport | **STDIO**                                                      |
| Command   | `/Applications/Scratchpad.app/Contents/Helpers/scratchpad-mcp` |

No environment variables are needed; the server address and key live in the app.

In the desktop app:

1. Open **Settings → MCP servers**.
2. Choose **Add server** and select **STDIO**.
3. Enter the command above.
4. Save the server and select **Restart**.
5. Open a local Work/Codex chat and use `/mcp` to inspect the connection.

The desktop app, Codex CLI, and IDE extension share MCP configuration on the same host. If you already registered Scratchpad with the [Codex guide](/scratchpad/guides/codex/), inspect that entry before adding a duplicate. The desktop steps and shared configuration are described in [OpenAI's MCP guide](https://learn.chatgpt.com/docs/extend/mcp#configure-in-the-chatgpt-desktop-app).

## Try a read

Use a local chat that has access to your checkout, then ask:

> Use Scratchpad to retrieve context for the repository at /absolute/path/to/my-project. Pass that path as workingDirectory and tell me which project was resolved.

The MCP performs Git discovery on the computer running it. For a project without a local Git checkout, supply its Scratchpad `projectId` instead. Confirm the resolved project before requesting a capture. See [Codex troubleshooting](/scratchpad/guides/codex/#troubleshooting) for capture service, signing and networking problems.

## ChatGPT web and hosted chats

ChatGPT web uses hosted plugin tools and does not read your Mac's `~/.codex/config.toml`. Configuring a local desktop MCP does not establish a connection for an ordinary web chat or hosted Work chat. [OpenAI documents these distinct connection paths](https://learn.chatgpt.com/docs/extend/mcp#use-mcp-backed-tools-in-chatgpt-web).

Scratchpad provides a **local stdio MCP server**, a local command, an HTTPS REST API and SSH challenge authentication. It does not provide a hosted MCP endpoint or an OAuth plugin integration. The dashboard URL, REST API URL, and an invented `/mcp` suffix are not MCP server endpoints.

A hosted connection needs additional integration work. OpenAI supports [Streamable HTTP MCP plugins](https://developers.openai.com/plugins/build/mcp-server) and [Secure MCP Tunnel for private servers](https://developers.openai.com/blog/connect-private-mcp-servers-to-openai-products). Those options address a different connection path; neither makes Scratchpad's existing REST API an MCP endpoint automatically. This guide covers the supported local desktop connection.
