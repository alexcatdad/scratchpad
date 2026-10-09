# Recipient-bound SSH proof verification

Issue #13 changes both enrollment and machine login to version 2. See the canonical transcript in docs/api.md. No deployment or credential rotation is performed by this change.

1. Use Node 24.21.0 and Go 1.27.2. Run npm run sdk:check and npm run sdk:test.
2. Run npm run test, npm run lint, npm run typecheck and npm run build.
3. Run npm run test:e2e for ordinary browser enrollment, MCP login, revocation and restart behavior. Run go test -race ./... from mcp.
4. Coordinate server, MCP binary and external signing integration upgrades. Request new challenges after upgrade; sign the displayed JSON proof without adding a newline. No nonce-only fallback exists.

Existing issued sessions keep their prior expiry and revocation policy; renewal requires version 2. Downgrading requires a coordinated server/client rollback and restores the vulnerable protocol. Real OpenSSH tests use disposable keys and two synthetic instance origins, including recipient tampering, legacy namespace, wrong purpose, expiry and replay rejection. The signer trusts its configured URL origin, never an origin chosen by a returned challenge.
