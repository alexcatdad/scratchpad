---
name: scratchpad-connect
description: Connect a laptop or VM to Scratchpad MCP using an existing SSH key and verify authenticated project access. Use for first-time client setup or authentication troubleshooting.
---

# Connect to Scratchpad

Connect this machine's local capture service to the owner's instance, then register
the local stdio MCP with the client. The service owns the server URL and SSH key;
the MCP has no connection settings of its own. Keep private keys on the machine or
signing device. Completion requires a successful authenticated read with confirmed
project scope; installing a binary, listing public GitHub keys or discovering MCP
tools alone is insufficient.

## Confirm the instance

Reuse the owner's confirmed server origin and project identity from the session.
Otherwise obtain the intended Scratchpad server URL and checkout or project ID.
The public documentation URL is not an instance, and the HTTP API is not a hosted
MCP endpoint. Use HTTPS outside loopback. Preserve unrelated MCP entries and
existing credentials.

On macOS the Scratchpad app provides the binaries in
`/Applications/Scratchpad.app/Contents/Helpers/`. Check the current state first:

```sh
/Applications/Scratchpad.app/Contents/Helpers/scratchpad-mcp status
```

It prints the service lifecycle, connection state and saved server and key as
JSON. If it reports the service unavailable, have the owner open the app and choose
**Start Capture Service** from its menu-bar icon. If it already shows the intended server
and `connected`, skip to registering the MCP.

For server initialization, OAuth configuration or recovery, consult the
[security guide](https://alexcatdad.github.io/scratchpad/reference/security.md).
An administrator's single-use setup token authorizes the first owner; GitHub login
alone cannot claim an initialized instance. Existing owners link GitHub through
an authenticated dashboard. An old descriptive GitHub profile is not such a link.
Report missing server configuration as the blocker; client setup cannot repair it.

## Select a key

Use available Git/SSH tooling to inspect public identity and signing availability:
for example Git's SSH signing configuration, public-key files, or an SSH agent's
public key list. A Git username or successful repository access does not prove
Scratchpad authentication. Match the public key fingerprint to the linked owner's
published SSH authentication or SSH signing keys. GPG keys do not qualify.

If one usable key matches, use it. If several match, use an already confirmed
preference or ask the owner to choose. If none match, report the mismatch and offer
the independent manual enrollment workflow in the
[MCP guide](https://alexcatdad.github.io/scratchpad/guides/mcp.md).
Publishing a new GitHub key or generating an identity requires separate owner
instruction. Never read, copy, upload or print private key contents; inspect public
material only. Hardware/agent unlocking remains a local owner interaction.

## Connect the capture service

```sh
/Applications/Scratchpad.app/Contents/Helpers/scratchpad-mcp connect --server <origin>
```

`connect` reads public keys from the SSH agent, the 1Password agent and
`~/.ssh/*.pub`, asks the server which it accepts, proves possession through the
agent and saves the connection only after the proof succeeds. Pass
`--public-key <file.pub>` to pick one of several accepted keys and
`--agent-socket <path>` for a non-default agent. The matching private key must be
loaded in the agent; the owner approves or unlocks it. The app's
**Settings → Authentication** page performs the same steps. Do not set
`SCRATCHPAD_URL`, `SCRATCHPAD_PUBLIC_KEY` or `SCRATCHPAD_SIGNING_KEY`; the current
binary ignores them.

Failure messages map to the diagnosis below: no accepted key means the GitHub link
or key sync is missing; `authentication` after an accepted key means the server
rejected the proof, which is a server-side problem to report.

## Register the MCP and prove access

Register the same binary as a stdio server with no arguments or environment, for
example in Claude Code:

```sh
claude mcp add --scope user scratchpad -- /Applications/Scratchpad.app/Contents/Helpers/scratchpad-mcp
```

Use the client's own mechanism elsewhere. Keep mirroring off unless explicitly
authorized. Restart/reconnect the client as required and discover the running
binary's tools.
Call `get_project_context` with the absolute `workingDirectory` or confirmed
`projectId`, using the discovered schema; compact limits require `version: 2`.
The capture service performs the signed challenge and holds the session; GitHub
tokens/PATs are not MCP credentials.
Inspect the returned project identity before declaring success. Missing or
ambiguous project identity needs an owner-confirmed selection, not another guessed
project. An empty context in the expected project is a valid authenticated read.
Use a read rather than a test capture to verify setup.

## Diagnose and report

Authentication failure: inspect key availability, origin, linked GitHub identity,
last successful synchronization and local block status. Successful synchronization
runs every five minutes and includes both SSH key categories. Failed synchronization
preserves the last complete set. Cached GitHub-managed keys can authenticate and
renew MCP sessions for up to 24 hours after that successful synchronization; request
authorization also checks freshness. A valid token cannot bypass a block, detected
removal or stale cache. Restart does not extend the limit. Browser session expiry
during a GitHub outage requires GitHub to return or an independent passkey.

A local block persists even if GitHub removes and re-adds the key. Have the owner
resolve blocks or use independent local credentials; repeated retries cannot fix
these conditions. Unlinking or replacing the GitHub account invalidates its derived
access. Manual local credentials remain independent. Report the exact failed step
and owner/operator action needed while continuing unrelated authorized work.

Report the configured client, server origin, public-key fingerprint, resolved
project and authenticated-read result, or the blocker. Omit tokens and private
material from the report. Source availability, skill installation, client discovery
and live access are separate outcomes. For subsequent memory workflows, use the
`scratchpad-memory` companion skill.
