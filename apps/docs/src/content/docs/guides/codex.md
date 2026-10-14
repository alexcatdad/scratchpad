---
title: Connect Codex
description: Add Scratchpad to Codex through its local stdio MCP connection.
---

Codex runs `scratchpad-mcp` on your computer. The binary connects to your Scratchpad server, so the server can live on another machine while Git discovery and SSH signing stay local.

## Before you connect

1. [Install Scratchpad MCP](/scratchpad/guides/installation/).
2. Sign into your dashboard and [select a synchronized GitHub SSH key or enroll an independent SSH public key](/scratchpad/guides/mcp/#build-and-enroll).
3. Make the matching signing key available locally. If using `ssh-agent`, check `ssh-add -l` in the environment that launches Codex.
4. Confirm this computer can reach your server. A private server may require your VPN connection.

Locate the executable:

```sh
command -v scratchpad-mcp
scratchpad-mcp --version
```

## Register with the CLI

Replace the example origin and both absolute paths with your own values:

```sh
codex mcp add scratchpad \
  --env SCRATCHPAD_URL=https://memory.example.com \
  --env SCRATCHPAD_PUBLIC_KEY=/absolute/path/to/id_ed25519.pub \
  -- /absolute/path/to/scratchpad-mcp
codex mcp list
```

If you already have a launcher that supplies these variables and runs the binary, register the launcher instead:

```sh
codex mcp add scratchpad -- /absolute/path/to/scratchpad-launcher
```

The launcher must preserve the stdio protocol: print no banners or other text to standard output. Use `exec` to run the binary and send diagnostics to standard error.

Codex stores MCP configuration in `~/.codex/config.toml`. Local desktop and IDE clients on the same Codex host share this configuration. Restart the client's MCP connection after registration. These steps follow [OpenAI's MCP documentation](https://learn.chatgpt.com/docs/extend/mcp?surface=cli#configure-with-the-cli).

## Configure with TOML instead

Add this entry to your existing configuration, replacing the examples. Use this method **instead of** registering the same server again with the CLI.

```toml
[mcp_servers.scratchpad]
command = "/absolute/path/to/scratchpad-mcp"

[mcp_servers.scratchpad.env]
SCRATCHPAD_URL = "https://memory.example.com"
SCRATCHPAD_PUBLIC_KEY = "/absolute/path/to/id_ed25519.pub"
```

Scratchpad uses SSH challenge authentication. `codex mcp login` is for OAuth servers and is not an enrollment step for this binary. The dashboard URL belongs in `SCRATCHPAD_URL`; do not configure it as a Streamable HTTP MCP URL.

## Check the first read

Open Codex in a Git checkout and ask:

> Use Scratchpad to get the project context for this checkout. Pass its absolute path as workingDirectory and show the resolved project before making any captures.

In the CLI, `/mcp` shows active connections. A configured entry in `codex mcp list` confirms registration; a successful tool call confirms authentication, server reachability, and project selection. An empty project's context is a valid response.

Clients may launch a globally configured MCP outside the checkout. Pass `workingDirectory` for repository-scoped calls, or use an explicit `projectId`. Once the resolved project is correct, you can ask Codex to record decisions, findings, or questions through the [available tools](/scratchpad/guides/mcp/#available-tools).

## Troubleshooting

| Symptom                             | Check                                                                                                                                                                                                                                          |
| ----------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Executable cannot start             | Use an existing absolute executable path; confirm the installed version.                                                                                                                                                                       |
| Signing fails                       | Confirm the public key is locally enrolled or eligible in the synchronized GitHub key set and the matching private key is unlocked and available to this process. A desktop app may have a different SSH agent environment from your terminal. |
| Cannot reach the server             | Check the origin, HTTPS certificate, server availability, and VPN access from this computer.                                                                                                                                                   |
| Project discovery fails             | Supply the checkout's absolute `workingDirectory`, or an explicit project identity for non-Git work.                                                                                                                                           |
| Tools are absent after registration | Restart the connection or client, then inspect `/mcp`. Existing chats may still have their earlier tool catalog.                                                                                                                               |

Configuration on your Mac does not install the executable or provide signing credentials inside a Codex cloud container. Verify those separately before using a remote execution environment.
