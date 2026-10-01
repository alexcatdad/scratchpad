---
title: Project overview
description: What Scratchpad is for, who it serves, and its optional AI and PostgreSQL capabilities.
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

v0.2.0 includes the SQLite server, passkey dashboard, signed-challenge MCP access, typed captures, retrieval, native import/export, and optional repository mirroring, plus optional AI processing and PostgreSQL. Follow [installation](/scratchpad/guides/installation/) for Homebrew and container setup or build from source. See [release verification](/scratchpad/guides/releases/) for publication and installation evidence. Historical MVP acceptance in v0.1.3 used clearly labeled synthetic scenarios against the actual application. Other projects provide optional examples and import datasets; their specific history is not an acceptance prerequisite.

The first internal milestone was authenticated capture and retrieval backed by SQLite and visible in the browser. The current release also supports the project boundaries, history, import/export and operational recovery needed for regular use.

## V1 direction

See [MVP status and acceptance](/scratchpad/guides/mvp-acceptance/) for the completed scope, scenario checklist and synthetic paused-project example.

- One owner per instance, with multiple devices and agent credentials.
- A central HTTP API and browser dashboard.
- A thin local Go stdio MCP with Git-aware project discovery.
- Immutable raw captures and audited changes to curated knowledge.
- Import/export and optional repository mirroring.
- Container deployment and native MCP distribution.
- English application and documentation.

v0.2.0 adds [PostgreSQL](/scratchpad/guides/postgresql/), [optional AI and embeddings](/scratchpad/guides/ai/), source-linked suggestions/patterns, and private Markdown documents. Their verification is separate from the historical v0.1.3 MVP acceptance. Scratchpad remains useful with SQLite and without AI.

## Source of truth

The repository contains the detailed [product requirements](https://github.com/alexcatdad/scratchpad/blob/main/docs/prd.md), [architecture](https://github.com/alexcatdad/scratchpad/blob/main/docs/architecture.md), and [API baseline](https://github.com/alexcatdad/scratchpad/blob/main/docs/api.md). Public guides explain those contracts without reproducing private discovery material.
