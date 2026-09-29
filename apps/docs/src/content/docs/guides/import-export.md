---
title: Import, export & mirroring
description: Bring existing history into Scratchpad and retain control of your data.
---

The first implementation includes native export/import, legacy JSONL import through the API, and optional local MCP mirroring. These features are available in source builds; published release packages remain outstanding.

## Import existing logs

Legacy JSONL import accepts `POST /api/v1/import` with `format: "jsonl"`, a resolved `projectId`, and a `jsonl` string. Each original source object is preserved; historical authority remains explicitly unverified. The result reports imported/skipped counts and per-line diagnostics for invalid entries.

Original history should remain distinguishable from new captures and later derived interpretation. Migration must not falsely imply that imported descriptive authors were authenticated Scratchpad credentials.

## Export and recovery

In the authenticated dashboard, open **Settings → Download export** to save native JSON. **Import Scratchpad export** accepts that file. Native imports run transactionally: identical stable IDs are skipped, conflicting existing IDs abort the import.

Exports retain knowledge and audit history, but exclude credentials, challenges, sessions, and setup tokens. They are not full operational backups. Stop the server before copying SQLite files, or use SQLite's online backup API; separately verify restoration.

## Optional repository mirrors

The central API remains authoritative. Mirroring is enabled only when both server-owned project settings and local MCP configuration permit it.

The default eligible types are decisions, ADRs, and business decisions. The flow is:

1. Persist the capture centrally.
2. Append the eligible capture to the selected checkout's JSONL mirror.
3. Report the central identity and mirror outcome to the caller.

If the append fails, the result must clearly say the record was saved centrally and the mirror failed. Retrying must duplicate neither the central record nor the local line.

Mirroring covers captures through that local MCP instance. It is not automatic browser-to-repository synchronization. Scratchpad never automatically commits or pushes the mirrored file.

## Mirror recovery

Mirror writes use a per-file `.lock` directory to coordinate concurrent processes. After an interrupted writer, stop every MCP writer and confirm none remains before removing that specific empty lock directory. Then retry the original capture. Locks do not expire automatically because a paused process may still be alive.

Malformed or partial JSONL is reported rather than silently truncated. Save a copy before repairing a malformed tail. Mirror paths must remain within the repository; symlink escapes are rejected.
