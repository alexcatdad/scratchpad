---
title: MVP status & acceptance
description: The verified MVP scope, acceptance scenarios, and a clearly labeled synthetic paused-project example.
---

## Current status

**The MVP is ready to use in [v0.1.3](https://github.com/alexcatdad/scratchpad/releases/tag/v0.1.3).** Start with [installation](/scratchpad/guides/installation/) to run your private server and connect the MCP.

The release includes eight typed capture tools, deterministic search and filtering, project context, immutable source records, audited metadata and relationships, passkey browser authentication, SSH challenge authentication for MCP, optional repository mirroring, import/export, and SQLite backup/restore. Native MCP packages and the application image are published through GitHub Releases, Homebrew and GHCR.

The v0.1.3 MVP excludes AI processing, embeddings, pattern analysis and PostgreSQL. The current source implements those optional additions; their [remaining-product acceptance ledger](https://github.com/alexcatdad/scratchpad/blob/main/docs/post-mvp-readiness.md) is separate from this historical MVP evidence. The capture and retrieval workflow works without them.

## What acceptance means

Acceptance exercises the actual server, browser dashboard and stdio MCP with disposable databases, credentials and repositories. Clearly labeled synthetic data supplies representative decisions and interrupted-project scenarios.

USB Boop and other projects are illustrative examples, not sources of truth for Scratchpad's requirements. Their original conversations or complete history are not prerequisites for acceptance. Real-history imports are optional datasets for checking import fidelity and retrieval; they retain their source provenance and uncertainty.

| Scenario                          | Verified behavior                                                                                                                |
| --------------------------------- | -------------------------------------------------------------------------------------------------------------------------------- |
| A — recover a technical choice    | Retrieve the original decision, rationale and supporting source without reconstructing the investigation.                        |
| B — resume an interrupted project | Recover the paused state, reason, follow-up work and linked decisions/findings/failures/constraints, including after restore.    |
| C — explain a stakeholder request | Retrieve the request, rationale and later related history while preserving the original source.                                  |
| D — contractor repository         | Capture centrally with repository mirroring denied; working-tree and Git-metadata file contents remain unchanged.                |
| E — worktrees                     | Resolve linked checkouts to one project while retaining distinct branch, commit, root and worktree provenance.                   |
| F — non-Git project               | Return an explicit identity error, then capture successfully after explicit project resolution without inventing Git provenance. |
| G — restart and restore           | Preserve records, credentials, browser sessions, settings, audit history and retry identities.                                   |

## Synthetic paused-project example

This example is invented test data for a fictional hardware project. It is not a statement about USB Boop or another real project.

The [canonical JSONL fixture](https://github.com/alexcatdad/scratchpad/blob/main/tests/fixtures/synthetic-paused-project.jsonl) contains one record:

<!-- prettier-ignore -->
```jsonl
{"id":"legacy-paused","type":"project_state","title":"Paused for hardware validation","state":"paused","reason":"Awaiting a test device","followUp":"Validate the remaining hardware behavior before resuming","date":"2026-01-15","synthetic":true,"source":{"kind":"synthetic_test_fixture","project":"Synthetic Hardware Project","description":"Invented acceptance data; not a claim about USB Boop or any real project's history."}}
```

To try it in a disposable project:

1. Save the line as `synthetic-paused-project.jsonl`.
2. In **Settings → Import a decision log**, choose the project and select the file.
3. Open the project's context, then **Paused for hardware validation**. Inspect the state, reason, follow-up and original source.

Imported lifecycle claims remain historical evidence until reviewed. Importing this example does not silently establish a real project's current state. The automated MCP workflow separately captures a synthetic current state and verifies its reason and follow-up in fresh MCP processes before and after restore.

## Verification evidence

- [Exact-revision CI](https://github.com/alexcatdad/scratchpad/actions/runs/36864169395) passed application/documentation checks, server tests, Go race and vulnerability checks, the authenticated browser/MCP workflow, and container backup/restore.
- [Release publication](https://github.com/alexcatdad/scratchpad/actions/runs/36859022794) passed native verification, image publication, GitHub Release publication and Homebrew delivery.
- [Fresh-runner release acceptance](https://github.com/alexcatdad/scratchpad/actions/runs/36859768053) passed macOS/Linux Homebrew installation, published-binary authentication and capture, both image architectures, checksums and published-image backup/restore.

The [readiness ledger](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/mvp-readiness.md) records exact revisions, operational scope and optional dataset results. See [release validation](/scratchpad/guides/releases/) for signing and delivery details. Check browser sign-in and a real MCP session in your own deployment after installation.
