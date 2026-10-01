---
title: Project overview
description: What Scratchpad is for, who it serves, and the boundary of its first release.
---

Scratchpad is private developer memory for one owner working across projects, machines, and coding agents. It keeps decisions, findings, questions and answers, failures, constraints, and project history together with their provenance.

The useful unit is a capture. It can be short, imperfect, and worth keeping. More structure can emerge through use rather than being a prerequisite for recording anything.

## Intended workflow

1. An agent discovers the current project through the local MCP.
2. It records useful context without composing the same information twice.
3. The central server stores the original capture.
4. The owner or a later agent retrieves that context and checks its evidence.
5. New knowledge can clarify or supersede an earlier conclusion without erasing it.

Historical records are evidence, not automatically current policy. A past decision can be superseded, an experiment can fail, and an AI suggestion can remain unverified.

## Development status

The first implementation now includes the SQLite server, passkey dashboard, signed-challenge MCP access, typed captures, retrieval, native import/export, and optional repository mirroring. Install [v0.1.2](/scratchpad/guides/installation/) through Homebrew and the published container, or build from source. Signed/notarized macOS packages, Linux packages, both container architectures, and disposable installation/workflow acceptance are verified. See [release validation](/scratchpad/guides/releases/) for evidence and scope; historical scenarios A and B still need source evidence before full MVP acceptance.

The first internal milestone is authenticated capture and retrieval backed by SQLite and visible in the browser. It is smaller than the complete MVP.

## V1 direction

- One owner per instance, with multiple devices and agent credentials.
- A central HTTP API and browser dashboard.
- A thin local Go stdio MCP with Git-aware project discovery.
- Immutable raw captures and audited changes to curated knowledge.
- Import/export and optional repository mirroring.
- Container deployment and native MCP distribution.
- English application and documentation.

PostgreSQL, embeddings, clustering, and optional OpenAI-compatible processing come after the core loop. Scratchpad must remain useful without them.

## Source of truth

The repository contains the detailed [product requirements](https://github.com/alexcatdad/scratchpad/blob/main/docs/prd.md), [architecture](https://github.com/alexcatdad/scratchpad/blob/main/docs/architecture.md), and [API baseline](https://github.com/alexcatdad/scratchpad/blob/main/docs/api.md). Public guides explain those contracts without reproducing private discovery material.
