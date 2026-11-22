---
title: Connect ChatGPT on macOS
description: Use Scratchpad from the desktop app's local chats through the scratchpad command, and understand why web chats cannot connect.
---

The ChatGPT desktop app's local Work/Codex chats can run shell commands on your Mac. They use Scratchpad the same way as other coding agents: through the [`scratchpad` command](/scratchpad/guides/agents/), which hands reads and captures to the Scratchpad capture service. The capture service talks to your server with your SSH key.

## Set up a local chat

1. [Install the app](/scratchpad/guides/installation/) and [connect the capture service](/scratchpad/guides/agents/#connect-the-capture-service) to your server.
2. [Put the command on your PATH](/scratchpad/guides/agents/#put-the-command-on-your-path), or tell the chat its full path, `/Applications/Scratchpad.app/Contents/Helpers/scratchpad`.
3. Install the [`scratchpad-memory` skill](/scratchpad/guides/agents/#add-the-companion-skills) where your local chats load skills, so the chat knows when and how to use the command.

If you added Scratchpad under **Settings → MCP servers** with an earlier version of the app, remove that entry: the app no longer ships the `scratchpad-mcp` server it pointed at. See [Remove an old MCP entry](/scratchpad/guides/agents/#remove-an-old-mcp-entry).

## Try a read

Use a local chat that has access to your checkout, then ask:

> Use Scratchpad to retrieve context for the repository at /absolute/path/to/my-project. Pass that path as workingDirectory and tell me which project was resolved.

The command performs Git discovery on your Mac. For a project without a local Git checkout, supply its Scratchpad `projectId` instead. Confirm the resolved project before requesting a capture. See [Codex troubleshooting](/scratchpad/guides/codex/#troubleshooting) for capture service, signing and networking problems.

## ChatGPT web and hosted chats

Ordinary ChatGPT web chats and hosted Work chats have no Scratchpad connection. They run on OpenAI's servers, cannot run the `scratchpad` command on your Mac and do not reach its capture service. [OpenAI documents these distinct connection paths](https://learn.chatgpt.com/docs/extend/mcp#use-mcp-backed-tools-in-chatgpt-web).

Scratchpad provides a local command, an HTTPS REST API and SSH challenge authentication. It does not provide a hosted MCP endpoint or an OAuth plugin integration. The dashboard URL, REST API URL, and an invented `/mcp` suffix are not MCP server endpoints.
