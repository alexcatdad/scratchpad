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

Retry identity is persistent. A server restart must not turn a harmless retry into a duplicate.

## Concurrent changes

Raw records are immutable. An edit to mutable knowledge must include a revision precondition. If another edit has already changed that revision, return a conflict rather than overwriting it. The initial design requires no automatic merge.

## Initial HTTP surface

The design's first subset includes MCP challenge verification, project resolution, project listing, record creation, record listing, and record detail. Authentication enrollment and browser session endpoints support that path.

Use the implementation and tests to verify exact request and response formats as they land. The [API baseline](https://github.com/alexcatdad/scratchpad/blob/main/docs/api.md) describes the larger contract; it is not a claim that every listed endpoint is already live.
