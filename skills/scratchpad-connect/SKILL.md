---
name: scratchpad-connect
description: Connect a laptop or VM's Scratchpad capture service to a server using an existing SSH key and verify authenticated project access through the scratchpad command. Use for first-time client setup or authentication troubleshooting.
---

# Connect to Scratchpad

Connect this machine's local capture service to the owner's instance, then verify
access through the `scratchpad` command. The service owns the server URL and SSH
key; the command has no connection settings of its own. Keep private keys on the
machine or signing device. Completion requires a successful authenticated read
with confirmed project scope; installing a binary, listing public GitHub keys or
listing tools alone is insufficient.

## Confirm the instance

Reuse the owner's confirmed server origin and project identity from the session.
Otherwise obtain the intended Scratchpad server URL and checkout or project ID.
The public documentation URL is not an instance, and the HTTP API is not a hosted
MCP endpoint. Use HTTPS outside loopback. Preserve existing credentials.

On macOS the Scratchpad app provides the command at
`/Applications/Scratchpad.app/Contents/Helpers/scratchpad`. Use `scratchpad` if it
is on `PATH`, otherwise that full path. Check the current state first:

```sh
/Applications/Scratchpad.app/Contents/Helpers/scratchpad status
```

It prints JSON with the service status (lifecycle, connection state, queue
counts) and, once one is saved, the configuration with server and key. If the
service is unavailable or stopped, have the owner open the app and choose
**Start Capture Service** from its menu-bar icon; the command never starts it. If
it already shows the intended server and connection state `connected`, skip to
proving access.

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
the independent key enrollment workflow in the
[agent integration guide](https://alexcatdad.github.io/scratchpad/guides/agents.md).
Publishing a new GitHub key or generating an identity requires separate owner
instruction. Never read, copy, upload or print private key contents; inspect public
material only. Hardware/agent unlocking remains a local owner interaction.

## Connect the capture service

```sh
scratchpad connect --server <origin>
```

`connect` reads public keys from the SSH agent, the 1Password agent and
`~/.ssh/*.pub`, asks the server which it accepts, proves possession through the
agent and saves the connection only after the proof succeeds. Pass
`--public-key <file.pub>` to pick one of several accepted keys and
`--agent-socket <path>` for a non-default agent. The matching private key must be
loaded in the agent; the owner approves or unlocks it. The capture service must
already be running. The app's **Connection** page performs the same steps. Do not
set `SCRATCHPAD_URL`, `SCRATCHPAD_PUBLIC_KEY` or `SCRATCHPAD_SIGNING_KEY`; the
current binaries ignore them.

Failure messages map to the diagnosis below: no accepted key means the GitHub link
or key sync is missing; `authentication` after an accepted key means the server
rejected the proof, which is a server-side problem to report.

## Prove access

Keep mirroring off unless explicitly authorized. Read the tool's schema with
`scratchpad tools get-project-context`, then call it with the absolute
`workingDirectory` or confirmed `projectId`; compact limits require `version: 2`:

```sh
scratchpad get-project-context -F version=2 -f workingDirectory=<absolute path>
```

The capture service performs the signed challenge and holds the session; GitHub
tokens/PATs are not Scratchpad credentials.
Inspect the returned project identity before declaring success. Missing or
ambiguous project identity needs an owner-confirmed selection, not another guessed
project. An empty context in the expected project is a valid authenticated read.
Use a read rather than a test capture to verify setup.

If the client still has a `scratchpad` MCP server entry from an earlier app
version, it points at a binary the app no longer ships. Report it and offer to
remove it with the owner's approval (`claude mcp remove scratchpad` or
`codex mcp remove scratchpad`).

## Diagnose and report

Authentication failure: inspect key availability, origin, linked GitHub identity,
last successful synchronization and local block status. Successful synchronization
runs every five minutes and includes both SSH key categories. Failed synchronization
preserves the last complete set. Cached GitHub-managed keys can authenticate and
renew sessions for up to 24 hours after that successful synchronization; request
authorization also checks freshness. A valid token cannot bypass a block, detected
removal or stale cache. Restart does not extend the limit. Browser session expiry
during a GitHub outage requires GitHub to return or an independent passkey.

A local block persists even if GitHub removes and re-adds the key. Have the owner
resolve blocks or use independent local credentials; repeated retries cannot fix
these conditions. Unlinking or replacing the GitHub account invalidates its derived
access. Manual local credentials remain independent. Report the exact failed step
and owner/operator action needed while continuing unrelated authorized work.

Report the server origin, public-key fingerprint, resolved project and
authenticated-read result, or the blocker. Omit tokens and private material from
the report. Source availability, skill installation and live access are separate
outcomes. For subsequent memory workflows, use the `scratchpad-memory` companion
skill.
