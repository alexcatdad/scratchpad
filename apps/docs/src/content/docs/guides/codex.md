---
title: Connect Codex
description: Give Codex Scratchpad memory through the scratchpad command, its hooks and the companion skill.
---

Codex uses Scratchpad through the `scratchpad` command: Codex already has a
shell, and each call runs the installed version. The command goes through the
capture service on your Mac, which holds the server connection and your SSH
signer, so the server can live on another machine while Git discovery and
signing stay local.

## Before you connect

1. [Install the app](/scratchpad/guides/installation/) and
   [connect the capture service](/scratchpad/guides/agents/#connect-the-capture-service)
   to your server.
2. Check the capture service from a terminal:

   ```sh
   /Applications/Scratchpad.app/Contents/Helpers/scratchpad status
   ```

3. Confirm this computer can reach your server. A private server may require
   your VPN or tailnet connection.

## Set up the command, hooks and skill

Make the command available on your `PATH`, as described in
[Agent integration](/scratchpad/guides/agents/#put-the-command-on-your-path), or
tell Codex its full path.

To record failed tool calls and other mistake signals automatically, print the
Codex hook configuration:

```sh
scratchpad hooks print --agent codex
```

Merge it into `~/.codex/hooks.json`, then run `/hooks` in Codex and trust the
new hooks. Events are stored only for projects whose `agentEvents` setting is
on; see [hooks](/scratchpad/guides/agents/#report-agent-mistakes-with-hooks).

Then install the
[`scratchpad-memory` skill](/scratchpad/guides/agents/#add-the-companion-skills)
so Codex knows when and how to use the command. From a Scratchpad source
checkout:

```sh
mkdir -p "$HOME/.agents/skills"
cp -R -i skills/scratchpad-memory "$HOME/.agents/skills/"
```

The command never starts the capture service. A service started from Codex's
sandboxed shell would inherit the sandbox, so if the service is stopped Codex
sees an error asking you to start it. Choose **Start Capture Service** in the
Scratchpad menu bar panel and ask Codex to retry.

The command talks to the capture service over a private socket in `/tmp`. If
Codex's sandbox blocks it, allow that call or run it outside the sandbox.

If you registered Scratchpad as an MCP server with an earlier version of the
app, [remove that entry](/scratchpad/guides/agents/#remove-an-old-mcp-entry):
`codex mcp remove scratchpad`.

## Check the first read

Open Codex in a Git checkout and ask:

> Use Scratchpad to get the project context for this checkout and show the resolved project before making any captures.

A successful call confirms the capture service, server reachability,
authentication and project selection together. An empty project's context is a
valid response. Once the resolved project is correct, you can ask Codex to
record decisions, findings or questions.

The command resolves the project from the directory it runs in. Pass
`workingDirectory` or an explicit `projectId` when Codex runs it elsewhere.

## Troubleshooting

| Symptom                              | Check                                                                                                                                                                  |
| ------------------------------------ | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| “The capture service is not running” | Start it from the Scratchpad menu bar panel, then retry. The command does not start a service you stopped yourself.                                                    |
| `scratchpad: command not found`      | Use the full path in the app bundle, or add the symlink from [Agent integration](/scratchpad/guides/agents/#put-the-command-on-your-path).                             |
| Signing or authentication fails      | Run `scratchpad status` and open **Connection** in the app. Confirm the key is enrolled or eligible from your GitHub key set and the matching private key is unlocked. |
| Cannot reach the server              | Check the server address, HTTPS certificate, server availability and VPN or tailnet access from this computer.                                                         |
| Project discovery fails              | Run from the checkout, or supply its absolute `workingDirectory`, or an explicit project identity for non-Git work.                                                    |
| Hooks do not run                     | Run `/hooks` in Codex and trust them again; Codex skips a hook that is untrusted or whose definition changed.                                                          |
| Scratchpad MCP tools fail to start   | Remove the old entry with `codex mcp remove scratchpad`; the app no longer ships an MCP server.                                                                        |

Configuration on your Mac does not install the app or provide signing
credentials inside a Codex cloud container. Scratchpad works only where the app
and its capture service run.
