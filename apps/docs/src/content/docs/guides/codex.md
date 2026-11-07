---
title: Connect Codex
description: Give Codex Scratchpad memory through the scratchpad command, or through the bundled MCP server.
---

Codex can use Scratchpad two ways. The recommended one is the `scratchpad`
command: Codex already has a shell, and each call runs the installed version.
The bundled MCP server remains available if you prefer tools in the MCP list.
Both go through the capture service on your Mac, which holds the server
connection and your SSH signer, so the server can live on another machine while
Git discovery and signing stay local.

## Before you connect

1. [Install the app](/scratchpad/guides/installation/) and connect it to your
   server in **Connection**.
2. Check the capture service from a terminal:

   ```sh
   /Applications/Scratchpad.app/Contents/Helpers/scratchpad status
   ```

3. Confirm this computer can reach your server. A private server may require
   your VPN or tailnet connection.

## Use the scratchpad command

Make the command available on your `PATH`, as described in
[Agent integration](/scratchpad/guides/agents/#put-the-command-on-your-path), or
tell Codex its full path. Then install the
[`scratchpad-memory` skill](/scratchpad/guides/agents/#add-the-companion-skills)
so Codex knows when and how to use it. From a Scratchpad source checkout:

```sh
mkdir -p "$HOME/.agents/skills"
cp -R -i skills/scratchpad-memory "$HOME/.agents/skills/"
```

The command never starts the capture service. A service started from Codex's
sandboxed shell would inherit the sandbox, so if the service is stopped Codex
sees an error asking you to open Scratchpad. Start it from the menu bar panel
and ask Codex to retry.

The command talks to the capture service over a private socket in `/tmp`. If
Codex's sandbox blocks it, allow that call or run it outside the sandbox.

## Or register the MCP server

In the app, open **AI Tools** and choose **Set Up…** next to Codex. It shows the
lines it will add to `~/.codex/config.toml` and applies them when you confirm.
To add it yourself, use the CLI:

```sh
codex mcp add scratchpad -- /Applications/Scratchpad.app/Contents/Helpers/scratchpad-mcp
codex mcp list
```

or add this entry to `~/.codex/config.toml` instead (not both):

```toml
[mcp_servers.scratchpad]
command = "/Applications/Scratchpad.app/Contents/Helpers/scratchpad-mcp"
```

No environment variables are needed: the MCP server ignores the old
`SCRATCHPAD_URL` and `SCRATCHPAD_PUBLIC_KEY` settings and gets everything from
the capture service. `codex mcp login` is for OAuth servers and is not a step
for Scratchpad. Local desktop and IDE clients on the same Codex host share this
configuration. Restart the client's MCP connection after registration. These
steps follow
[OpenAI's MCP documentation](https://learn.chatgpt.com/docs/extend/mcp?surface=cli#configure-with-the-cli).

## Check the first read

Open Codex in a Git checkout and ask:

> Use Scratchpad to get the project context for this checkout and show the resolved project before making any captures.

A successful call confirms the capture service, server reachability,
authentication and project selection together. An empty project's context is a
valid response. Once the resolved project is correct, you can ask Codex to
record decisions, findings or questions.

Codex may launch a globally configured MCP server outside the checkout. With the
MCP server, pass `workingDirectory` for repository-scoped calls, or use an
explicit `projectId`. The command resolves the project from the directory it
runs in.

## Troubleshooting

| Symptom                                 | Check                                                                                                                                                                  |
| --------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| “The capture service is not running”    | Start it from the Scratchpad menu bar panel, then retry. Neither path starts a service you stopped yourself.                                                           |
| `scratchpad: command not found`         | Use the full path in the app bundle, or add the symlink from [Agent integration](/scratchpad/guides/agents/#put-the-command-on-your-path).                             |
| Signing or authentication fails         | Run `scratchpad status` and open **Connection** in the app. Confirm the key is enrolled or eligible from your GitHub key set and the matching private key is unlocked. |
| Cannot reach the server                 | Check the server address, HTTPS certificate, server availability and VPN or tailnet access from this computer.                                                         |
| Project discovery fails                 | Run from the checkout, or supply its absolute `workingDirectory`, or an explicit project identity for non-Git work.                                                    |
| MCP tools are absent after registration | Restart the connection or client, then inspect `/mcp`. Existing chats may still have their earlier tool catalog.                                                       |

Configuration on your Mac does not install the app or provide signing
credentials inside a Codex cloud container. Scratchpad works only where the app
and its capture service run.
