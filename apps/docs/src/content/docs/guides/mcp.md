---
title: MCP & project context
description: The local stdio interface, project discovery, and authentication design.
---

`scratchpad-mcp` is a local stdio MCP server bundled with the app at
`/Applications/Scratchpad.app/Contents/Helpers/scratchpad-mcp`. It exposes the
same tools as the [`scratchpad` command](/scratchpad/guides/agents/), discovers
Git context on your Mac and hands reads and captures to the local capture
service, which holds the server connection. Record business rules remain
server-side.

**For coding agents with a shell, use the `scratchpad` command instead.** It runs
the same tools with the same results, as a short process that always matches the
installed app. Use the MCP server for clients that can only talk MCP.

## Connect your client

Configure the server and SSH key once in the app's **Connection** page. The MCP
server has no connection settings of its own: it ignores the old
`SCRATCHPAD_URL`, `SCRATCHPAD_PUBLIC_KEY` and `SCRATCHPAD_SIGNING_KEY`
variables, and no token or key is ever handed to it.

The easiest path is **AI Tools → Set Up…** in the app. It adds one entry to the
client's configuration, shows you the change first, and leaves other entries
alone. Restart the client's MCP connection afterwards. For a manual setup, point
the client's stdio command at the bundled binary, for example in Codex:

```toml
[mcp_servers.scratchpad]
command = "/Applications/Scratchpad.app/Contents/Helpers/scratchpad-mcp"
```

Client walkthroughs:

- [Codex setup](/scratchpad/guides/codex/): the command, the MCP alternative
  and a first read.
- [ChatGPT macOS setup](/scratchpad/guides/chatgpt/): the desktop STDIO
  connection and the distinction from ChatGPT web.

When it launches, `scratchpad-mcp` starts the capture service if it is not
running and waits up to 12 seconds for it. A service you stopped yourself stays
stopped: start it from the menu bar panel. If startup fails, the server still
lists its tools and reports the problem on stderr; reads and captures fail until
the service is available. Exiting a client does not stop the service.

| Variable                    | Meaning                                                              |
| --------------------------- | -------------------------------------------------------------------- |
| `SCRATCHPAD_MIRROR`         | Set to `true` to opt into local repository mirroring; off by default |
| `SCRATCHPAD_MIRROR_PATH`    | Repository-relative path; defaults to `scratchpad/decisions.jsonl`   |
| `SCRATCHPAD_SERVICE_SOCKET` | A different capture service socket, for isolated tests               |

## Selecting the project

By default, discovery starts from the process launch directory. Clients do not all launch a globally configured MCP inside the active repository, so project-scoped calls can pass `workingDirectory` explicitly.

Git identity comes from repository remotes. Worktrees of the same repository share a project while preserving branch and commit provenance. Missing or ambiguous identity must produce a structured error instead of silently selecting a different project.

Non-Git projects use explicit server project identity. Paths are local context, not durable project identifiers.

Every result should identify its resolved project so the caller can see where a capture went.

## Capturing information

Typed tools accept natural fields: a decision and its rationale, or a question and its answer. The caller supplies them once. The server persists the original payload and generates readable content deterministically, without requiring an AI provider.

Initial payloads stay small and flexible. Adding optional fields later must not make old captures unreadable or rewrite their evidence.

## Authentication

The capture service proves possession of an independent enrolled key or an eligible SSH authentication/signing key synchronized from the owner’s verified GitHub binding. GitHub-managed keys need no individual dashboard approval. The server provides a random, short-lived, single-use challenge; successful verification yields a session lasting up to 24 hours.

The capture service performs the challenge and keeps the session token in memory; neither the MCP server nor the `scratchpad` command sees it. Revoking its credential immediately invalidates its sessions. Private signing keys remain local.

GitHub-managed requests and renewals also require an unblocked key and cache freshness within 24 hours of the last complete successful synchronization. Detected removal, unlinking or account replacement invalidates derived access immediately. Independent manual credentials remain locally managed. See [Security & recovery](/scratchpad/reference/security/).

Use the [connection skill](/scratchpad/guides/agents/#add-the-companion-skills) for agent-assisted discovery, client configuration and a scoped authenticated read.

## Build and enroll

This manual workflow creates an independent local credential. Use it when a key is not published on GitHub or independent access is desired.

Open **Settings → Connect an MCP key** in the authenticated dashboard, paste an OpenSSH public key, and create an enrollment challenge.

Sign the exact displayed JSON proof **without a newline**, using the displayed namespace and your matching local key. Paste the armored SSH signature into the dashboard to verify and enroll the credential. Private keys are never uploaded. The capture service handles subsequent authentication challenges automatically. Version 2 binds each proof to the locally configured instance, key, purpose and expiry. Upgrade the server and the app together; old nonce-only signatures are rejected. Existing sessions retain their expiry and revocation rules until renewal.

## Available tools

Capture tools are `record_decision`, `record_adr`, `record_business_decision`, `record_finding`, `record_qa`, `record_failure`, `record_constraint`, and `record_project_state`.

They accept common `title`, `authority`, `confidence`, and `requestId` fields plus type-specific payloads. Reuse the original request ID and input on retry. Use a fresh ID for a separate intentional capture.

Read tools are `search_memory`, `get_record`, `get_project_context`, `get_decision_history`, and `find_related`. Related records come from stored relationships, not AI inference. `resolve_project` supports owner-confirmed project resolution when discovery is insufficient.

The binary exposes its exact typed schemas through MCP tool discovery, and `scratchpad tools <tool>` prints the same schemas. Consult the [MCP README](https://github.com/alexcatdad/scratchpad/blob/main/mcp/README.md) for the wire contract and development checks.

## Optional AI tools

These tools need AI configured on the server.

| Tool                | Behavior                                                                                    |
| ------------------- | ------------------------------------------------------------------------------------------- |
| `semantic_search`   | Query a compatible embedding index and return source records with similarity scores.        |
| `get_suggestions`   | Retrieve source-linked summaries, patterns and reviewable suggestions.                      |
| `process_memory`    | Queue `analyze` or `embed` work without changing project permissions or raw captures.       |
| `get_ai_jobs`       | Inspect persisted processing status, retries and failures.                                  |
| `generate_document` | Queue a private `handoff`, `architecture`, `decisions`, `client_history` or `adr` document. |
| `retrieve_evidence` | Retrieve cited evidence for a question from the PostgreSQL RAG backend.                     |

Single-project calls use `projectId` or normal checkout/`workingDirectory` discovery. Cross-project calls explicitly set `crossProject: true`; optional `projectIds` restrict that scope to the requested projects. Only permitted projects participate. Poll job status, then inspect the generated artifacts and their sources. Suggestion review remains an owner-controlled dashboard/API action.

Use [Optional AI](/scratchpad/guides/ai/) to configure the provider and participating projects. The MCP never needs a provider API key.
