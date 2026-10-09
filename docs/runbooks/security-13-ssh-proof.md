# Recipient-bound SSH proof verification

Issue #13 changes both enrollment and machine login to version 2. See the canonical transcript in docs/api.md. No deployment or credential rotation is performed by this change.

1. Use Node 24.21.0 and Go 1.27.2. Run npm run sdk:check and npm run sdk:test.
2. Run npm run test, npm run lint, npm run typecheck and npm run build.
3. Run npm run test:e2e for ordinary browser enrollment, MCP login, revocation and restart behavior. Run go test -race ./... from mcp.
4. Coordinate server, MCP binary and external signing integration upgrades. Request new challenges after upgrade; sign the displayed JSON proof without adding a newline. No nonce-only fallback exists.

Existing issued sessions keep their prior expiry and revocation policy; renewal requires version 2. Downgrading requires a coordinated server/client rollback and restores the vulnerable protocol. Real OpenSSH tests use disposable keys and two synthetic instance origins, including recipient tampering, legacy namespace, wrong purpose, expiry and replay rejection. The signer trusts its configured URL origin, never an origin chosen by a returned challenge.

The Go signer canonicalizes internationalized hostnames with the official golang.org/x/net/idna lookup profile. Version v0.60.0 was verified as latest stable through the official Go module proxy on 2026-10-09 and pinned exactly; its transitive x/text version is pinned by go.mod/go.sum. A synthetic IDN origin is verified by real OpenSSH without external DNS. Both generated Go challenge responses preserve expiry as a string, including fractional trailing zeroes required by the signed transcript.
Recipient canonicalization applies IDNA lookup to the original hostname, preserving Unicode mappings such as dotted capital I (`İ` → `xn--i-9bb`). Numeric ports are serialized canonically before default-port removal (`:0443` → no HTTPS port; `:08443` → `:8443`). Synthetic challenge and real OpenSSH signature regressions compare these cases against Node's URL origin.

IPv4-mapped IPv6 recipients use WHATWG hexadecimal tails (`[::ffff:127.0.0.1]` → `[::ffff:7f00:1]`). Ordinary IPv6 retains canonical longest-zero-run compression; real signature tests cover mapped zero/nonzero tails, default/nondefault ports, uppercase input, expanded literals and compression ties.

Already-ASCII internal hosts retain WHATWG-compatible names such as
`scratch_pad.example`, lowercased without stricter DNS label validation. Forbidden
host code points are rejected; non-ASCII names still use IDNA lookup. Real
OpenSSH regressions cover underscore names, case and default-port normalization.
