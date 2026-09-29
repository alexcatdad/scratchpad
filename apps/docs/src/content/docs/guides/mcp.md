---
title: MCP & project context
description: The local stdio interface, project discovery, and authentication design.
---

The planned `scratchpad-mcp` binary runs locally and speaks MCP over stdio. It discovers Git context, authenticates to the central API, and optionally mirrors eligible captures into the selected checkout. Record business rules remain server-side.

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

## Installation status

The concrete tool inventory, launch flags, and client configuration belong to the implemented MCP and its tests. Follow the repository MCP README while this interface is being built; do not treat illustrative names in the design documents as a verified release API.
