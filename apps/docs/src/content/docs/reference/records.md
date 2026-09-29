---
title: Records & API
description: Raw payloads, provenance, retries, and concurrent edits.
---

A capture belongs to an explicit resolved project. Its original typed payload is immutable. Readable content is generated from that payload; the caller does not need to submit the same information a second time.

## Small schemas that can evolve

Keep required fields minimal and add optional structure as actual usage warrants. A new renderer or schema version must not rewrite an old capture to pretend it always followed the newer format. Imported source content and unknown legacy fields must be preserved.

The server assigns record identity, receipt time, and verified credential attribution. Descriptive author and source fields remain distinguishable from those server-owned facts.

## Retries

- Same request identity and same capture: return the existing record.
- Same request identity with different content: return a conflict.
- Different request identities with identical text: preserve two intentional captures.

Retry identity is persistent and scoped to the authenticated credential. Comparison includes the resolved project and normalized capture payload, but excludes rediscovered Git context; the original capture retains its original Git provenance. A server restart must not turn a harmless retry into a duplicate.

## Concurrent changes

Raw records are immutable. An edit to mutable knowledge must include a revision precondition. If another edit has already changed that revision, return a conflict rather than overwriting it. The initial design requires no automatic merge.

## Initial HTTP surface

The implementation exposes MCP challenge verification, project resolution and listing, record creation and retrieval, search, relationships, native import/export, and browser credential enrollment under `/api/v1`.

Use the implementation and tests to verify exact request and response formats. The [API baseline](https://github.com/alexcatdad/scratchpad/blob/main/docs/api.md) describes the larger contract; it is not a claim that every listed endpoint is already live.

## Inspect a record in the dashboard

Open a record to view its original typed payload, authority, confidence, and capture provenance. Git context, occurrence time, capture time, and recorded actor remain distinguishable. Imported records expose their preserved original entry.

**Decision chain and relationships** shows links to other records and lets you navigate them. Replacement and partial-replacement relationships express different historical meanings; a partial replacement does not discard every earlier constraint. Proposed and accepted relationship states remain visible.

Curated title, tags, summary, and archive status are editable metadata. Revision history and audit events record changes separately from original evidence. Evidence links retain their source references; they do not turn a past conclusion into current policy automatically.
