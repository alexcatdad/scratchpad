---
title: MCP & project context
description: The local stdio interface, project discovery, and authentication design.
---

The `scratchpad-mcp` binary runs locally and speaks MCP over stdio. It discovers Git context, authenticates to the central API, and optionally mirrors eligible captures into the selected checkout. Record business rules remain server-side.

## Connect your client

Start with the guide for your client:

- [Codex setup](/scratchpad/guides/codex/) — CLI registration, configuration, and a first read.
- [ChatGPT macOS setup](/scratchpad/guides/chatgpt/) — the desktop STDIO connection and the distinction from ChatGPT web.

Install the binary and enroll your public key before adding it to either client. Homebrew installs the MCP; your Scratchpad server runs separately.

## Selecting the project

By default, discovery starts from the process launch directory. Clients do not all launch a globally configured MCP inside the active repository, so project-scoped calls can pass `workingDirectory` explicitly.

Git identity comes from repository remotes. Worktrees of the same repository share a project while preserving branch and commit provenance. Missing or ambiguous identity must produce a structured error instead of silently selecting a different project.

Non-Git projects use explicit server project identity. Paths are local context, not durable project identifiers.

Every result should identify its resolved project so the caller can see where a capture went.

## Capturing information

Typed tools accept natural fields: a decision and its rationale, or a question and its answer. The caller supplies them once. The server persists the original payload and generates readable content deterministically, without requiring an AI provider.

Initial payloads stay small and flexible. Adding optional fields later must not make old captures unreadable or rewrite their evidence.

## Authentication

The MCP proves possession of an enrolled signing key. The server provides a random, short-lived, single-use challenge; successful verification yields a session lasting up to 24 hours.

Tokens stay in the MCP process memory. Restarting MCP requires authentication again. Revoking its credential immediately invalidates its sessions. Private signing keys remain local.

## Build and enroll

Build the local binary using the [installation guide](/scratchpad/guides/installation/). Open **Settings → Connect an MCP key** in the authenticated dashboard, paste an OpenSSH public key, and create an enrollment challenge.

Sign the exact displayed nonce **without a newline**, using the displayed namespace and your matching local key. Paste the armored SSH signature into the dashboard to verify and enroll the credential. Private keys are never uploaded. The MCP handles subsequent authentication challenges automatically.

## Client configuration

Point your MCP client's stdio command at the absolute path to the installed `scratchpad-mcp` binary, or your locally built `dist/scratchpad-mcp`. Find an installed binary with `command -v scratchpad-mcp`. Set these environment variables in that client's configuration:

| Variable                 | Meaning                                                                       |
| ------------------------ | ----------------------------------------------------------------------------- |
| `SCRATCHPAD_URL`         | Required server origin; HTTP is allowed only on loopback                      |
| `SCRATCHPAD_PUBLIC_KEY`  | Required absolute path to the enrolled OpenSSH public key                     |
| `SCRATCHPAD_SIGNING_KEY` | Optional private-key path; otherwise use the public-key path with `ssh-agent` |
| `SCRATCHPAD_MIRROR`      | Set to `true` to opt into local mirroring; off by default                     |
| `SCRATCHPAD_MIRROR_PATH` | Repository-relative path; defaults to `scratchpad/decisions.jsonl`            |

Keep the private key local and available through `ssh-agent` or the explicit signing-key path. A locked or missing key prevents authentication.

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
