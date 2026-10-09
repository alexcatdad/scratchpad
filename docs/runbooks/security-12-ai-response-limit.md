# Bound optional AI provider responses

Issue #12 caps successful provider bodies at 8 MiB of decompressed bytes before
UTF-8 decoding or JSON parsing. The shared completion and embedding transport
also covers semantic search and connectivity checks. A body exceeding the cap
is cancelled; the caller receives a safe `AI_UNAVAILABLE` error without provider
content or credentials. Exactly 8 MiB is permitted. Fetch decompression happens
before the reader; compressed Content-Length is not a trusted size bound.

Use Node from `.node-version` and the exact patched Go requirement in
`mcp/go.mod`. Synthetic fixtures cover oversized streams, early cancellation,
missing Content-Length, split multibyte sequences, the exact byte boundary, and
real gzip HTTP responses. The independent Go patch prerequisite resolves the
previous toolchain's canonical vulnerability-check failures.

1. Run `npm run test -w @scratchpad/web -- src/server/ai.test.ts`.
2. Run `npm run typecheck`, `npm run lint`, `npm test` and `npm run build`.
3. Check canonical GitHub Actions at the PR's current head. Local tests confirm
   core storage remains ready after optional provider failure; they do not
   establish deployed acceptance.

The limit bounds retained body chunks, not an individual network or decompressor
chunk allocated by the underlying transport. It does not change request-size
limits, provider consent, retry policy or the configured overall deadline.
