---
name: scratchpad-connect
description: Connect a laptop or VM to Scratchpad MCP using an existing SSH key and verify authenticated project access. Use for first-time client setup or authentication troubleshooting.
---

# Connect to Scratchpad

Configure the local stdio MCP for the owner's instance using the agent's preferred
Git/SSH tooling. Keep private keys on the machine or signing device. Completion
requires a successful authenticated read with confirmed project scope; installing
a binary, listing public GitHub keys or discovering MCP tools alone is insufficient.

## Confirm the instance

Reuse the owner's confirmed server origin and project identity from the session.
Otherwise obtain the intended Scratchpad server URL and checkout or project ID.
The public documentation URL is not an instance, and the HTTP API is not a hosted
MCP endpoint. Use HTTPS outside loopback. Check the installed binary version and
client's supported local stdio configuration before changing it. Preserve unrelated
MCP entries and existing credentials.

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

## Configure and prove access

Use the client's supported configuration mechanism and the absolute path to
`scratchpad-mcp`. Set `SCRATCHPAD_URL` to the confirmed server origin and
`SCRATCHPAD_PUBLIC_KEY` to an absolute OpenSSH public-key file path. A selected
agent-only public key can be saved to a dedicated local public-key file, preserving
existing files. The matching key must be available to `ssh-agent`; if the owner
already uses a local signing-key file, `SCRATCHPAD_SIGNING_KEY` may reference its
absolute path. The MCP signs locally using OpenSSH. Git tooling helps discover the
identity but cannot replace the MCP's supported signer. Keep mirroring off unless
explicitly authorized.

Restart/reconnect the client as required and discover the running binary's tools.
Call `get_project_context` with the absolute `workingDirectory` or confirmed
`projectId`, using the discovered schema. The MCP performs a single-use signed
challenge and obtains its own session; GitHub tokens/PATs are not MCP credentials.
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
