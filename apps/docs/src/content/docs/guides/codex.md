---
title: Connect Codex
description: Add Scratchpad to Codex through its local stdio MCP connection.
---

Codex runs `scratchpad-mcp` on your computer. The binary talks to the local Scratchpad capture service, which holds your server connection, so the server can live on another machine while Git discovery and SSH signing stay local.

## Before you connect

1. [Install the Scratchpad macOS app](/scratchpad/guides/installation/) and choose **Start service** from its menu-bar icon.
2. [Connect the capture service](/scratchpad/guides/mcp/#connect-the-capture-service) with a synchronized GitHub SSH key or an independently enrolled key, then confirm with `scratchpad-mcp status`.
3. Confirm this computer can reach your server. A private server may require your VPN connection.

## Register with the CLI

The app's **MCP setup** page can preview and apply this entry for you. To register it yourself:

```sh
codex mcp add scratchpad -- /Applications/Scratchpad.app/Contents/Helpers/scratchpad-mcp
codex mcp list
```

The binary needs no connection variables; it reads the server and key from the capture service.

Codex stores MCP configuration in `~/.codex/config.toml`. Local desktop and IDE clients on the same Codex host share this configuration. Restart the client's MCP connection after registration. These steps follow [OpenAI's MCP documentation](https://learn.chatgpt.com/docs/extend/mcp?surface=cli#configure-with-the-cli).

## Configure with TOML instead

Add this entry to your existing configuration, replacing the examples. Use this method **instead of** registering the same server again with the CLI.

```toml
[mcp_servers.scratchpad]
command = "/Applications/Scratchpad.app/Contents/Helpers/scratchpad-mcp"
```

Scratchpad uses SSH challenge authentication through the capture service. `codex mcp login` is for OAuth servers and is not an enrollment step for this binary. Do not configure the dashboard URL as a Streamable HTTP MCP URL.

## Check the first read

Open Codex in a Git checkout and ask:

> Use Scratchpad to get the project context for this checkout. Pass its absolute path as workingDirectory and show the resolved project before making any captures.

In the CLI, `/mcp` shows active connections. A configured entry in `codex mcp list` confirms registration; a successful tool call confirms authentication, server reachability, and project selection. An empty project's context is a valid response.

Clients may launch a globally configured MCP outside the checkout. Pass `workingDirectory` for repository-scoped calls, or use an explicit `projectId`. Once the resolved project is correct, you can ask Codex to record decisions, findings, or questions through the [available tools](/scratchpad/guides/mcp/#available-tools).

## Troubleshooting

| Symptom                             | Check                                                                                                                                                                                                                                          |
| ----------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Executable cannot start             | Use an existing absolute executable path; confirm the installed version.                                                                                                                                                                       |
| Capture service unavailable         | Choose **Start service** from the Scratchpad menu-bar icon, then check `scratchpad-mcp status`.                                                                                                                                                |
| Signing fails                       | Confirm the public key is locally enrolled or eligible in the synchronized GitHub key set and the matching private key is loaded in your SSH agent, then rerun `scratchpad-mcp connect`.                                                       |
| Cannot reach the server             | Check the origin, HTTPS certificate, server availability, and VPN access from this computer.                                                                                                                                                   |
| Project discovery fails             | Supply the checkout's absolute `workingDirectory`, or an explicit project identity for non-Git work.                                                                                                                                           |
| Tools are absent after registration | Restart the connection or client, then inspect `/mcp`. Existing chats may still have their earlier tool catalog.                                                                                                                               |

Configuration on your Mac does not install the executable or provide signing credentials inside a Codex cloud container. Verify those separately before using a remote execution environment.
