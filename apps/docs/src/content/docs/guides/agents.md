---
title: Agent integration
description: Set up Scratchpad MCP, discover its capabilities, and use project memory correctly.
---

Scratchpad gives coding agents persistent project memory through a **local stdio MCP server**. The `scratchpad-mcp` process connects to your separately deployed Scratchpad HTTP API. Captures and deterministic retrieval do not need an LLM provider.

Share the [agent documentation index](/scratchpad/llms.txt) or the [complete Markdown bundle](/scratchpad/llms-full.txt) with another agent. These files and Markdown copies of the guides are generated from the public documentation on every site build. They contain no private project memory or deployment credentials.

## Add the companion skill

The optional [`scratchpad-memory` skill](https://github.com/alexcatdad/scratchpad/tree/main/skills/scratchpad-memory) teaches agents when to retrieve memory, which capture tool to choose, how to preserve provenance, and how to handle retries and repository files. It complements the MCP connection; it does not install or authenticate the binary.

From a Scratchpad source checkout containing `skills/scratchpad-memory/SKILL.md`, install the complete folder into your client's supported skill location. For Codex, a user-level installation makes it available across repositories:

```sh
mkdir -p "$HOME/.agents/skills"
cp -R -i skills/scratchpad-memory "$HOME/.agents/skills/"
```

Keep one active installation of this skill. Inspect an existing copy before replacing it; do not also install a second copy under the target repository's `.agents/skills`. The source folder `skills/` is for distribution and is not automatically discovered by Codex. Other clients need their own supported skill-loading mechanism.

Invoke it explicitly with `$scratchpad-memory` in Codex, or let the client select it when a task needs Scratchpad project memory. For example:

- “Use $scratchpad-memory to resume this project and identify relevant constraints.”
- “Why did we choose this approach, and has that decision been replaced?”
- “Record the agreed decision and the reason we rejected the alternative.”

The skill follows existing task authorization and project instructions. Core capture and retrieval still work in clients without skill support. See [official skill guidance](https://learn.chatgpt.com/docs/build-skills) for Codex discovery and invocation. Source availability, client installation, and published documentation are separate steps; use the checked-out skill until the corresponding source revision has been published.

## Connect with an agent

The optional [`scratchpad-connect` skill](https://github.com/alexcatdad/scratchpad/tree/main/skills/scratchpad-connect) handles first-time connection and authentication troubleshooting using existing Git/SSH tooling. Install its complete folder in your client’s supported skill location as above, preserving any existing installation, then invoke `$scratchpad-connect` with your server origin and checkout. It discovers a usable public key, configures the local stdio MCP and verifies a real authenticated read in the intended project. It does not publish keys, copy private material or change server identity. Installation and tool discovery alone do not prove access.

A key synchronized from your verified GitHub account needs no separate dashboard approval; each machine proves possession automatically. GitHub integration is in current server source and requires operator OAuth configuration. Released v0.3.0 servers predate it. See [Security & recovery](/scratchpad/reference/security/) for outage, revocation and independent-key behavior.

## Connect once

1. Install `scratchpad-mcp` using the [installation guide](/scratchpad/guides/installation/). Homebrew installs the local binary; it does not start the server.
2. Choose an eligible SSH key synchronized from the owner’s verified GitHub account, or enroll an independent OpenSSH public key in the authenticated dashboard under **Settings → Connect an MCP key**. Sign the enrollment challenge locally with the matching key. Never upload a private key.
3. Configure your client's stdio command with the absolute binary path. Set `SCRATCHPAD_URL` to the server origin and `SCRATCHPAD_PUBLIC_KEY` to the absolute enrolled public-key path. Make the matching private key available through `ssh-agent`, or set `SCRATCHPAD_SIGNING_KEY` to its local path.
4. Follow [Codex setup](/scratchpad/guides/codex/) or [ChatGPT macOS setup](/scratchpad/guides/chatgpt/), restart the connection, and discover tools with MCP `tools/list`.

The MCP performs SSH challenge authentication automatically. Session tokens remain in process memory; restarting the process authenticates again. A dashboard URL or REST API URL cannot be used as a hosted MCP endpoint. Local setup also depends on the binary, Git, OpenSSH, and key being available on the machine running the client.

## Start each project session

Call `get_project_context` with the checkout's absolute `workingDirectory`:

```json
{
  "workingDirectory": "/absolute/path/to/project"
}
```

Inspect the resolved project and returned context before capturing. A global MCP process may start outside the active checkout, so its launch directory is not sufficient evidence of project scope. Git remotes provide durable identity; worktrees share a project and retain branch/commit provenance.

For a non-Git project or ambiguous remotes, use an explicit known `projectId`. If project identity is missing or ambiguous, ask the owner to identify the project, then use `resolve_project` with the confirmed name. Do not silently choose another project. An empty context can be a valid new project.

Search relevant records, inspect their source evidence and decision history, and follow corrections or replacement relationships before treating historical conclusions as current policy. Record explicit owner decisions as `explicit`, direct observations as `observed`, and interpretations as `inferred`. Keep suggestions distinguishable from accepted decisions.

## Capabilities

The running binary's `tools/list` response is authoritative for exact input schemas and available tools. The names below describe the current implementation; older binaries may expose fewer tools.

| Purpose     | Tools                                                       | Behavior                                                                                 |
| ----------- | ----------------------------------------------------------- | ---------------------------------------------------------------------------------------- |
| Capture     | `record_decision`, `record_adr`, `record_business_decision` | Persist a decision, rationale and optional supporting context.                           |
| Capture     | `record_finding`, `record_qa`, `record_failure`             | Persist an observation, question/answer, or failure and lesson.                          |
| Capture     | `record_constraint`, `record_project_state`                 | Persist a constraint or project state transition.                                        |
| Retrieve    | `get_project_context`, `search_memory`, `get_record`        | Resume a project, search its memory and inspect original records.                        |
| Trace       | `get_decision_history`, `find_related`                      | Examine history and stored relationships; relationships are not inferred by these tools. |
| Resolve     | `resolve_project`                                           | Resolve a project using owner-confirmed identity.                                        |
| Optional AI | `semantic_search`, `get_suggestions`                        | Retrieve embedding matches or source-linked derived knowledge.                           |
| Optional AI | `process_memory`, `get_ai_jobs`, `generate_document`        | Queue processing, inspect status, or generate a private document.                        |

## Capture a decision

After the owner has actually made a decision, call `record_decision` with arguments like this illustrative example:

```json
{
  "workingDirectory": "/absolute/path/to/project",
  "requestId": "a-unique-id-for-this-capture",
  "title": "Keep generated reports private",
  "authority": "explicit",
  "confidence": "high",
  "decision": "Generated reports remain private to the project owner.",
  "rationale": "The owner explicitly chose this access boundary."
}
```

All capture tools accept `requestId`, `title`, `authority`, `confidence` and their type-specific fields. Supported authority values are `explicit`, `observed`, `inferred`, `derived` and `suggested`; confidence values are `high`, `medium`, `low` and `unknown`. Use the tool schema for optional fields. The server generates readable content from the original typed payload, so no duplicate prose field is needed.

Inspect the returned project and record ID. Original captures are immutable; curated metadata and derived knowledge have separate revision/audit history.

For a retry, reuse **the same request ID and input**. A changed capture using the same ID conflicts; a separate intentional capture needs a fresh ID. If optional repository mirroring fails after central persistence, preserve the returned central record ID and report the mirror failure. Do not create a new capture just to repair a mirror.

## Optional AI and mirroring

AI requires server configuration, an available provider and project participation/consent. The MCP does not need the provider's API key. `process_memory` queues `analyze` or `embed`; `generate_document` supports `handoff`, `architecture`, `decisions`, `client_history` and `adr`. Poll `get_ai_jobs`, then inspect resulting artifacts and their source records. Generated suggestions remain reviewable derived knowledge.

Single-project AI calls use normal project scope. Cross-project calls require explicit `crossProject: true`; `projectIds` can restrict participating projects. Access permissions still apply. See [Optional AI](/scratchpad/guides/ai/).

Repository mirroring is off by default. It requires both local `SCRATCHPAD_MIRROR=true` and server eligibility/policy. The central API remains authoritative; a local JSONL mirror is optional and the MCP does not commit or push Git changes. See [Import, export & mirroring](/scratchpad/guides/import-export/).

## Diagnose a connection

- No tools: check the configured executable path, restart the client and inspect MCP stderr diagnostics. Stdout carries the MCP protocol.
- Authentication failure: check local enrollment or GitHub key eligibility, last successful synchronization, local blocks, key availability and the configured origin. HTTP is permitted only on loopback.
- Wrong or missing project: pass an explicit absolute `workingDirectory` or confirmed `projectId`.
- Missing AI tools: check the installed binary version with `scratchpad-mcp --version` and inspect its discovered tools.
- AI unavailable: inspect project participation, provider configuration and persisted job errors. Core capture and retrieval remain available independently of AI.

For direct API integrations, use the [implemented OpenAPI contract](https://raw.githubusercontent.com/alexcatdad/scratchpad/main/docs/openapi.json) and [Records & API](/scratchpad/reference/records/). The broader design contract includes planned behavior; it is not a substitute for the implemented schemas.
