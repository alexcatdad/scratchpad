---
title: Project overview
description: What Scratchpad is for, who it serves, and its optional AI and PostgreSQL capabilities.
---

Scratchpad is private developer memory for one owner working across projects, machines, and coding agents. It keeps decisions, findings, questions and answers, failures, constraints, and project history together with their provenance.

The useful unit is a capture. It can be short, imperfect, and worth keeping. More structure can emerge through use rather than being a prerequisite for recording anything.

## Intended workflow

1. An agent discovers the current project through the `scratchpad` command on the developer's Mac.
2. It records useful context without composing the same information twice.
3. The local capture service saves the capture on the Mac, then delivers it to the central server, which stores the original.
4. The owner or a later agent retrieves that context and checks its evidence.
5. New knowledge can clarify or supersede an earlier conclusion without erasing it.

Historical records are evidence, not automatically current policy. A past decision can be superseded, an experiment can fail, and an AI suggestion can remain unverified.

## Current status

Scratchpad ships as a macOS menu bar app, released as daily CalVer versions through a Homebrew cask (see [installation](/scratchpad/guides/installation/) and [releases](/scratchpad/guides/releases/)). The app bundles the capture service with its encrypted offline queue and the `scratchpad` command agents use. Agents learn when to use it from the `scratchpad-memory` skill, and hooks for Claude Code and Codex report agent mistakes (see [agent integration](/scratchpad/guides/agents/)).

The server is a Go service with SQLite by default and an embedded browser dashboard: passkey and optional GitHub sign-in, signed-challenge machine access, typed captures, retrieval, native import/export and optional repository mirroring, plus optional AI processing, PostgreSQL, passage retrieval with pgvector, public GitHub profiles and generated API clients. You deploy and operate the server yourself.

Historical MVP acceptance in v0.1.3 used clearly labeled synthetic scenarios against the actual application.

## V1 direction

See [MVP status and acceptance](/scratchpad/guides/mvp-acceptance/) for the completed scope, scenario checklist and synthetic paused-project example.

- One owner per instance, with multiple devices and agent credentials.
- A central HTTP API and browser dashboard.
- A local Go command and capture service with Git-aware project discovery.
- Immutable raw captures and audited changes to curated knowledge.
- Import/export and optional repository mirroring.
- Self-hosted server deployment and a native macOS app.
- English application and documentation.

PostgreSQL, [optional AI and embeddings](/scratchpad/guides/ai/), source-linked suggestions/patterns and private Markdown documents were added after the MVP. Their verification is separate from the historical v0.1.3 MVP acceptance. Scratchpad remains useful with SQLite and without AI.

## Source of truth

The repository contains the detailed [product requirements](https://github.com/alexcatdad/scratchpad/blob/main/docs/prd.md), [architecture](https://github.com/alexcatdad/scratchpad/blob/main/docs/architecture.md), and [API baseline](https://github.com/alexcatdad/scratchpad/blob/main/docs/api.md). Public guides explain those contracts without reproducing private discovery material.
