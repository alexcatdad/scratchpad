# Opaque route identifier verification

Issue #16 is independently based on main. No deployed records are rewritten by this change.

1. Use Node 24.21.0 and Go 1.27.2. Run npm run test, npm run lint, npm run typecheck and npm run build.
2. Run npm run test:e2e for the browser workflows. The opaque-ids browser test serves synthetic API responses containing a malformed stored suggestion ID, clicks the actual displayed Reject and verifies no write request is sent.
3. The real API regression verifies atomic native-import rejection, legacy colon-ID reachability, metadata optimistic concurrency, and native import/export round trips.

The accepted entity alphabet is ASCII letters, digits, underscore, dot, colon, tilde and hyphen, limited to 500 characters, with complete . and .. segments forbidden. Each dashboard entity URL encodes its ID, the dashboard request boundary rejects malformed segments before fetch, and the server decodes exactly once before validation and lookup. Source repository identity strings have a separate format and are not entity IDs.

The baseline admitted a malformed native ID and the browser could turn an unencoded Reject URL into another object's Accept URL. Record detail URLs already encoded IDs, but the old server did not decode route segments; this caused a colon-ID detail request to return 404. This ticket corrects that reachability inconsistency while preventing encoded delimiters from acquiring routing meaning. Existing malformed IDs remain immutable/exportable and dashboard actions fail safely. No live cleanup or data migration is performed.

Hosted review also identified imported audit entity references. The public API regression reproduced a malformed audit `entityId` importing successfully, then passed after applying the same opaque identifier rule. All eight unsafe identifier variants now reject the complete transaction, while ordinary audit history round trips unchanged. Historical audit payload values remain immutable evidence.

Integration review found that real GitHub key-block audits contain SHA-256 fingerprint references with standard Base64 delimiters. A public OAuth/block/export/import regression failed with HTTP 400 before the correction and passed after preserving exactly those credential audit references. Wrong actions, entity types and malformed references still reject the archive. These metadata references never become route identifiers.

Additional integration regressions reproduce lone UTF-16 surrogate IDs throwing during URL construction and historical duplicate audit rows bypassing reference validation. URL construction now maps every invalid ID to a deliberately invalid encoded segment, which the request guard rejects before fetch. Audit reference validation precedes duplicate-row skipping, so identical malformed rows still roll back the entire native import.
