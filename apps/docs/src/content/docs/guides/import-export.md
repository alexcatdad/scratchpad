---
title: Import, export & mirroring
description: Bring existing history into Scratchpad and retain control of your data.
---

Import, export, and repository dual-write are part of the planned MVP. They follow the first authenticated capture/retrieval milestone. This page describes their required behavior, not currently available CLI commands.

## Import existing logs

Legacy JSONL logs are a first real-world input. Import must preserve original content, source attribution, and unrecognized fields. A parser should report malformed records clearly instead of silently discarding them.

Original history should remain distinguishable from new captures and later derived interpretation. Migration must not falsely imply that imported descriptive authors were authenticated Scratchpad credentials.

## Export and recovery

An export should retain enough identity, provenance, payload, and relationship information to understand the history outside Scratchpad. A readable export and a restorable operational database backup serve different purposes; verify both against their intended use.

## Optional repository mirrors

The central API remains authoritative. Mirroring is enabled only when both server-owned project settings and local MCP configuration permit it.

The default eligible types are decisions, ADRs, and business decisions. The flow is:

1. Persist the capture centrally.
2. Append the eligible capture to the selected checkout's JSONL mirror.
3. Report the central identity and mirror outcome to the caller.

If the append fails, the result must clearly say the record was saved centrally and the mirror failed. Retrying must duplicate neither the central record nor the local line.

Mirroring covers captures through that local MCP instance. It is not automatic browser-to-repository synchronization. Scratchpad never automatically commits or pushes the mirrored file.
