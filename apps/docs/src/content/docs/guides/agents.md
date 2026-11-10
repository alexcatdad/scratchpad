---
title: Agent integration
description: Give coding agents Scratchpad memory through the scratchpad command, and use project memory correctly.
---

Coding agents use Scratchpad through the **`scratchpad` command** that ships
inside the app. Each call is a short process: it runs the installed version, asks
the local capture service to do the work and exits. The capture service holds
the server connection and signs in with your SSH key, so agents never see a URL,
token or key. Captures and deterministic retrieval do not need an LLM provider.

The [MCP server](/scratchpad/guides/mcp/) exposes the same tools for clients
that can only talk MCP. Both run the same tool registry, with the same
validation and the same results; the command is simpler for agents that have a
shell, and an app upgrade never leaves a stale long-running server behind.

Share the [agent documentation index](/scratchpad/llms.txt) or the
[complete Markdown bundle](/scratchpad/llms-full.txt) with another agent. These
files and Markdown copies of the guides are generated from the public
documentation on every site build. They contain no private project memory or
deployment credentials.

## Before you start

1. [Install the app](/scratchpad/guides/installation/) and connect it to your
   server in **Connection**.
2. Check that the capture service is running and connected:

   ```sh
   /Applications/Scratchpad.app/Contents/Helpers/scratchpad status
   ```

   It prints the service's lifecycle, connection state and queue counts as JSON.

## Put the command on your PATH

The app bundles the command at
`/Applications/Scratchpad.app/Contents/Helpers/scratchpad`. The Homebrew cask
does not link it onto your `PATH` yet, so either call it by that full path or
add a symlink in a directory that is already on your `PATH`, for example:

```sh
mkdir -p ~/.local/bin
ln -sf /Applications/Scratchpad.app/Contents/Helpers/scratchpad ~/.local/bin/scratchpad
scratchpad --version
```

The symlink follows the app, so upgrades through Homebrew need no further step.
The examples below assume `scratchpad` is on your `PATH`.

## Run tools

Every tool is a subcommand. Dashes and underscores are interchangeable, so
`record_decision` is `scratchpad record-decision`.

```sh
scratchpad tools                          # list the tools
scratchpad tools record-decision          # one tool's input schema
scratchpad get-project-context -F version=2
scratchpad search-memory -f query="release signing"
scratchpad record-finding --input finding.json   # or --input - for stdin
scratchpad status                         # local capture service status
scratchpad --version
```

- `-f key=value` sets a string field. `-F key=value` sets a JSON field
  (numbers, booleans, arrays, objects) and falls back to plain text when the
  value is not JSON.
- `--input FILE` reads a JSON object of fields from a file, or from stdin with
  `--input -`. Use it for long or structured captures, so shell quoting cannot
  mangle them. Flags after `--input` add to or override its fields.
- Output is the tool's JSON result; `--text` prints the tool's text content
  instead. A non-zero exit status means the call failed, with the reason on
  stderr.
- The project resolves from the current directory. Run commands from the active
  checkout, or pass `-f workingDirectory=/absolute/path` or a confirmed
  `-f projectId=…`.

The command **never starts the capture service**. A service started from a
sandboxed agent shell would inherit that sandbox, so when the service is stopped
the command says so and exits. Open Scratchpad (`open -a Scratchpad`) or choose
**Start Capture Service** from its menu bar panel, then retry.

`SCRATCHPAD_SERVICE_SOCKET` selects a different capture service socket for
isolated tests. `SCRATCHPAD_MIRROR=true` and `SCRATCHPAD_MIRROR_PATH` opt into
[repository mirroring](/scratchpad/guides/import-export/) for that call.

## Add the companion skills

The [`scratchpad-memory` skill](https://github.com/alexcatdad/scratchpad/tree/main/skills/scratchpad-memory)
teaches agents when to retrieve memory, which capture tool to choose, how to
preserve provenance, and how to handle retries and repository files. It drives
the `scratchpad` command and falls back to MCP tools when a client has those
instead. It does not install or authenticate anything.

From a Scratchpad source checkout, install the complete folder into your
client's supported skill location. For Codex, a user-level installation makes it
available across repositories:

```sh
mkdir -p "$HOME/.agents/skills"
cp -R -i skills/scratchpad-memory "$HOME/.agents/skills/"
```

Keep one active installation of each skill. Inspect an existing copy before
replacing it; do not also install a second copy under the target repository's
`.agents/skills`. The source folder `skills/` is for distribution and is not
automatically discovered. Other clients need their own skill-loading mechanism.

Invoke it explicitly with `$scratchpad-memory` in Codex, or let the client select
it when a task needs project memory. For example:

- “Use $scratchpad-memory to resume this project and identify relevant constraints.”
- “Why did we choose this approach, and has that decision been replaced?”
- “Record the agreed decision and the reason we rejected the alternative.”

The [`scratchpad-connect` skill](https://github.com/alexcatdad/scratchpad/tree/main/skills/scratchpad-connect)
handles first-time connection and authentication troubleshooting with existing
Git and SSH tooling. It finds a usable public key, checks the capture service
and verifies a real authenticated read in the intended project. It does not
publish keys, copy private material or change server identity. Installing a
binary or listing tools does not prove access.

A key synchronized from your verified GitHub account needs no separate
dashboard approval; each machine proves possession automatically. See
[Security & recovery](/scratchpad/reference/security/) for outage, revocation
and independent-key behavior.

## Start each project session

Run `get-project-context` from the checkout, or name it explicitly:

```sh
scratchpad get-project-context -F version=2 -f workingDirectory="$PWD"
```

`version: 2` returns compact context; `limit`, `maxChars` and `previewChars`
require it. Inspect the resolved project and returned context before capturing.
Git remotes provide durable identity; worktrees share a project and retain
branch and commit provenance.

For a non-Git project or ambiguous remotes, use an explicit known `projectId`.
If project identity is missing or ambiguous, ask the owner to identify the
project, then use `resolve-project` with the confirmed name. Do not silently
choose another project. An empty context can be a valid new project.

Search relevant records, inspect their source evidence and decision history, and
follow corrections or replacement relationships before treating historical
conclusions as current policy. Record explicit owner decisions as `explicit`,
direct observations as `observed`, and interpretations as `inferred`. Keep
suggestions distinguishable from accepted decisions.

## Capabilities

`scratchpad tools` on the installed version is authoritative for the exact
tools and input schemas. The names below describe the current implementation.

| Purpose     | Tools                                                       | Behavior                                                                                 |
| ----------- | ----------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| Capture     | `record_decision`, `record_adr`, `record_business_decision` | Persist a decision, rationale and optional supporting context.                           |
| Capture     | `record_finding`, `record_qa`, `record_failure`             | Persist an observation, question/answer, or failure and lesson.                          |
| Capture     | `record_constraint`, `record_project_state`                 | Persist a constraint or project state transition.                                        |
| Retrieve    | `get_project_context`, `search_memory`, `get_record`        | Resume a project, search its memory and inspect original records.                        |
| Trace       | `get_decision_history`, `find_related`                      | Examine history and stored relationships; relationships are not inferred by these tools. |
| Resolve     | `resolve_project`                                           | Resolve a project using owner-confirmed identity.                                        |
| Receipts    | `get_capture_receipt`                                       | Check whether a capture saved on this Mac has reached the server.                        |
| Optional AI | `semantic_search`, `get_suggestions`, `retrieve_evidence`   | Retrieve embedding matches, source-linked derived knowledge or cited evidence.           |
| Optional AI | `process_memory`, `get_ai_jobs`, `generate_document`        | Queue processing, inspect status, or generate a private document.                        |

## Capture a decision

After the owner has actually made a decision, write its fields to a file:

```json
{
  "requestId": "a-unique-id-for-this-capture",
  "title": "Keep generated reports private",
  "authority": "explicit",
  "confidence": "high",
  "decision": "Generated reports remain private to the project owner.",
  "rationale": "The owner explicitly chose this access boundary."
}
```

Then capture it from the checkout:

```sh
scratchpad record-decision --input decision.json
```

All capture tools accept `requestId`, `title`, `authority`, `confidence` and
their type-specific fields. Supported authority values are `explicit`,
`observed`, `inferred`, `derived` and `suggested`; confidence values are `high`,
`medium`, `low` and `unknown`. Read `scratchpad tools <tool>` for optional
fields. The server generates readable content from the original typed payload,
so no duplicate prose field is needed.

The capture service acknowledges a capture once it is durably saved on this Mac
and returns a receipt. It syncs to the server in the background; a pending
receipt has no central record ID yet. Original captures are immutable; curated
metadata and derived knowledge have separate revision and audit history.

For a retry, reuse **the same request ID and input**. A changed capture using
the same ID conflicts; a separate intentional capture needs a fresh ID. If
optional repository mirroring fails after central persistence, keep the
returned record ID and report the mirror failure. Do not create a new capture
just to repair a mirror.

## Report agent mistakes with hooks

An agent only records a failure when it notices one. Hooks close that gap: the
agent's own harness calls `scratchpad hook` on a few events, whether or not the
agent thought anything went wrong. Scratchpad keeps these **agent events** apart
from memory records, so that repeated mistakes can be found later.

Only mistake signals are kept: failed and denied tool calls, your prompts, the
end of each turn, interrupts, failed model requests and the start and end of a
session. Successful tool calls are dropped and tool output is never stored.
Credential-shaped values are replaced before an event is queued. Events expire;
they are not exported and never appear in search or context.

Print the configuration for your agent:

```sh
scratchpad hooks print --agent claude-code
scratchpad hooks print --agent codex
```

The command prints JSON and changes no file.

- **Claude Code:** merge the printed `hooks` object into
  `~/.claude/settings.json`. Start a new session if a running one does not pick
  the hooks up.
- **Codex:** merge it into `~/.codex/hooks.json`, then run `/hooks` in Codex and
  trust the new hooks. Codex skips a hook until you trust it, and again if its
  definition ever changes.

Every hook is the same command, runs in the background and prints nothing. If
the capture service is stopped, the event is not recorded and the agent is not
disturbed.

Storage is opt-in per project and off by default. The server stores events only
for a project whose `agentEvents` setting is `true`, and discards events for
repositories it does not know; it never creates a project for them. Set it with
`PATCH /api/v1/projects/{id}/settings`, sending the project's current settings
with `"agentEvents": true` and its `expectedVersion`. The dashboard has no
switch for it yet.

## Optional AI

AI requires server configuration, an available provider and project
participation. Agents never need the provider's API key. `process-memory` queues
`analyze` or `embed`; `generate-document` supports `handoff`, `architecture`,
`decisions`, `client_history` and `adr`. Poll `get-ai-jobs`, then inspect the
resulting artifacts and their source records. Generated suggestions remain
reviewable derived knowledge.

Single-project AI calls use normal project scope. Cross-project calls require
explicit `crossProject: true` (`-F crossProject=true`); `projectIds` can restrict
participating projects. See [Optional AI](/scratchpad/guides/ai/).

## Diagnose a problem

- “The Scratchpad capture service is not running”: open the app and start the
  service, then retry. The command will not start it for you.
- `command not found`: call the full path in the app bundle, or add the symlink
  above.
- Authentication or connection failures: run `scratchpad status` and check the
  connection state, then open **Connection** in the app. Check local enrollment
  or GitHub key eligibility, key availability and the server origin.
- Wrong or missing project: pass an explicit absolute `workingDirectory` or a
  confirmed `projectId`.
- A tool is missing: compare `scratchpad --version` with the latest release and
  upgrade with `brew upgrade --cask scratchpad`.
- AI unavailable: inspect project participation, provider configuration and
  persisted job errors. Capture and retrieval keep working without AI.

For direct API integrations, use the
[implemented OpenAPI contract](https://raw.githubusercontent.com/alexcatdad/scratchpad/main/docs/openapi.json)
and [Records & API](/scratchpad/reference/records/).
