---
title: MCP & project context
description: The local stdio interface, project discovery, and authentication design.
---

The `scratchpad-mcp` binary runs locally and speaks MCP over stdio. It discovers Git context and sends reads and captures through the local capture service, which holds the server connection, authenticates and queues captures while offline. It can optionally mirror eligible captures into the selected checkout. Record business rules remain server-side.

## Connect your client

For an agent-oriented workflow and shareable Markdown documentation, see [Agent integration](/scratchpad/guides/agents/).

Start with the guide for your client:

- [Codex setup](/scratchpad/guides/codex/) — CLI registration, configuration, and a first read.
- [ChatGPT macOS setup](/scratchpad/guides/chatgpt/) — the desktop STDIO connection and the distinction from ChatGPT web.

Before adding it to a client, [install the macOS app](/scratchpad/guides/installation/), start its capture service and [connect it to your server](#connect-the-capture-service). Your Scratchpad server runs separately.

## Connect the capture service

Choose **Start service** from the Scratchpad menu-bar icon, then connect with an eligible synchronized GitHub SSH key or an [independently enrolled key](#enroll-an-independent-key). Either use **Settings → Authentication** in the app (**Find SSH keys**, then **Connect with selected key**), or the bundled command:

```sh
/Applications/Scratchpad.app/Contents/Helpers/scratchpad-mcp connect --server https://memory.example.com
/Applications/Scratchpad.app/Contents/Helpers/scratchpad-mcp status
```

`connect` reads public keys from your SSH agent, the 1Password agent and `~/.ssh/*.pub`, asks the server which it accepts, proves possession through the agent and saves the connection only after the proof succeeds. Use `--public-key <file.pub>` to choose among several accepted keys and `--agent-socket <path>` for a non-default agent. `status` prints the service state and saved connection as JSON. The matching private key must be loaded in your SSH agent.

## Selecting the project

By default, discovery starts from the process launch directory. Clients do not all launch a globally configured MCP inside the active repository, so project-scoped calls can pass `workingDirectory` explicitly.

Git identity comes from repository remotes. Worktrees of the same repository share a project while preserving branch and commit provenance. Missing or ambiguous identity must produce a structured error instead of silently selecting a different project.

Non-Git projects use explicit server project identity. Paths are local context, not durable project identifiers.

Every result should identify its resolved project so the caller can see where a capture went.

## Capturing information

Typed tools accept natural fields: a decision and its rationale, or a question and its answer. The caller supplies them once. The server persists the original payload and generates readable content deterministically, without requiring an AI provider.

Initial payloads stay small and flexible. Adding optional fields later must not make old captures unreadable or rewrite their evidence.

## Authentication

The capture service proves possession of an independent enrolled key or an eligible SSH authentication/signing key synchronized from the owner’s verified GitHub binding. GitHub-managed keys need no individual dashboard approval. Current server source supports GitHub access; released v0.3.0 servers predate it. The server provides a random, short-lived, single-use challenge; successful verification yields a session lasting up to 24 hours.

Sessions stay in the capture service; MCP clients never hold tokens. Revoking its credential immediately invalidates its sessions. Private signing keys remain local in your SSH agent.

GitHub-managed requests and renewals also require an unblocked key and cache freshness within 24 hours of the last complete successful synchronization. Detected removal, unlinking or account replacement invalidates derived access immediately. Independent manual credentials remain locally managed. See [Security & recovery](/scratchpad/reference/security/).

Use the [connection skill](/scratchpad/guides/agents/#connect-with-an-agent) for agent-assisted discovery, client configuration and a scoped authenticated read.

## Enroll an independent key

This manual workflow creates an independent local credential. Use it when a key is not published on GitHub or independent access is desired.

Open **Settings → Connect an MCP key** in the authenticated dashboard, paste an OpenSSH public key, and create an enrollment challenge.

Sign the exact displayed JSON proof **without a newline**, using the displayed namespace and your matching local key. Paste the armored SSH signature into the dashboard to verify and enroll the credential, then [connect the capture service](#connect-the-capture-service) with `--public-key` pointing at that key. Private keys are never uploaded. The capture service handles subsequent authentication challenges automatically. Version 2 binds each proof to the locally configured instance, key, purpose and expiry. Upgrade the server and MCP together; old nonce-only signatures are rejected. Existing sessions retain their expiry and revocation rules until renewal.

## Client configuration

Register the bundled binary as a stdio server with no arguments. The app's **MCP setup** page previews and applies this for Codex and Claude Code, or prints a generic entry. For Claude Code:

```sh
claude mcp add --scope user scratchpad -- /Applications/Scratchpad.app/Contents/Helpers/scratchpad-mcp
```

The server connection and key come from the capture service; the binary needs no connection variables. Two optional variables control mirroring:

| Variable                 | Meaning                                                            |
| ------------------------ | ------------------------------------------------------------------ |
| `SCRATCHPAD_MIRROR`      | Set to `true` to opt into local mirroring; off by default          |
| `SCRATCHPAD_MIRROR_PATH` | Repository-relative path; defaults to `scratchpad/decisions.jsonl` |

The historical v0.3.0 standalone formula used `SCRATCHPAD_URL` and `SCRATCHPAD_PUBLIC_KEY`; the current binary ignores them.

## Available tools

Capture tools are `record_decision`, `record_adr`, `record_business_decision`, `record_finding`, `record_qa`, `record_failure`, `record_constraint`, and `record_project_state`.

They accept common `title`, `authority`, `confidence`, and `requestId` fields plus type-specific payloads. Reuse the original request ID and input on retry. Use a fresh ID for a separate intentional capture.

Read tools are `search_memory`, `get_record`, `get_project_context`, `get_decision_history`, and `find_related`. Related records come from stored relationships, not AI inference. `resolve_project` supports owner-confirmed project resolution when discovery is insufficient.

The binary exposes its exact typed schemas through MCP tool discovery. Consult the [MCP README](https://github.com/alexcatdad/scratchpad/blob/main/mcp/README.md) for the wire contract and development checks.

## Optional AI tools

The v0.2.0 binary adds these tools. Earlier v0.1.3 binaries predate them.

| Tool                | Behavior                                                                                    |
| ------------------- | ------------------------------------------------------------------------------------------- |
| `semantic_search`   | Query a compatible embedding index and return source records with similarity scores.        |
| `get_suggestions`   | Retrieve source-linked summaries, patterns and reviewable suggestions.                      |
| `process_memory`    | Queue `analyze` or `embed` work without changing project permissions or raw captures.       |
| `get_ai_jobs`       | Inspect persisted processing status, retries and failures.                                  |
| `generate_document` | Queue a private `handoff`, `architecture`, `decisions`, `client_history` or `adr` document. |

Single-project calls use `projectId` or normal checkout/`workingDirectory` discovery. Cross-project calls explicitly set `crossProject: true`; optional `projectIds` restrict that scope to the requested projects. Only permitted projects participate. Poll job status, then inspect the generated artifacts and their sources. Suggestion review remains an owner-controlled dashboard/API action.

Use [Optional AI](/scratchpad/guides/ai/) to configure the provider and participating projects. The MCP never needs a provider API key.
