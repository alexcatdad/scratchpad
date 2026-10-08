# GitHub owner access implementation

## Scope

Implement GitHub issue #3 on the `codex/github-owner-access` integration branch. The accepted GitHub owner-access ADR and contributor decision `scratchpad-20261008-063` establish the design. GitHub Issues owns implementation ticket state.

## Procedure

1. Read the canonical PRD, architecture, API, glossary and accepted ADR; preserve unrelated changes.
2. Build backend authentication and synchronization and onboarding documentation in isolated worktrees. Use red-green vertical slices at the owner-approved HTTP and browser-to-MCP seams.
3. Merge backend into the integration branch, then implement dashboard and synthetic workflow coverage from that tip. Reconcile documentation with final contracts.
4. Merge each ticket with a merger agent, push the integration branch, and keep one PR closing the spec and tickets. Never merge the PR without owner authorization.
5. Run lint, types, tests, build, generated-client checks and Go verification. Run SQLite and PostgreSQL synthetic acceptance through the existing harness; GitHub Actions remains canonical.
6. Review standards and spec coverage independently, resolve findings and inspect exact-head CI, human/tool reviews and discussion threads.
7. Maintain a recurring PR monitor until merge or closure; notify only on actionable changes, failures or readiness changes. Preserve the distinction between tested code, PR readiness and live deployment.

## Live configuration

No production changes are included in implementation. Configure a per-instance OAuth app only in an explicitly authorized environment. Keep private keys, OAuth secrets and tokens out of repository files and logs.

## Review regression validation

Run the GitHub HTTP tests to confirm invalid OpenSSH key blobs and unsupported or
malformed pagination preserve the entire last successful snapshot, its original
freshness and existing sessions. The browser-to-MCP workflow additionally uses a
disposable SSH agent with two published keys, selects the existing Git signing
preference, and verifies authenticated stdio MCP access using only the agent-backed
public-key path. An unavailable agent must fail possession proof. Private key
contents are never inspected. Without a confirmed preference, several eligible
keys require owner selection as described by the connect skill.

Run both owner workflows and the harness lifecycle regression. Each suite owns its
temporary directory, subprocess and Docker container/volume state; cleaning one
scope must preserve another scope in the same worker. Run SQLite and PostgreSQL
acceptance independently and retain canonical CI evidence for the current head.

## Downgrade and rollback

The previous server does not enforce GitHub credential sources, cache expiry or
local key blocks. Restoring an old executable against the upgraded database without
preparation can therefore restore access that this version denies.

Before downgrading, verify an independent passkey and recovery path, sign in freshly
with that passkey, and disconnect GitHub through Settings. This revokes its derived
sessions and removes its machine permissions while preserving project knowledge
and independent credentials. Resolve every locally blocked independent SSH key:
revoke compromised local credentials and their sessions rather than relying on a
GitHub block that the old executable cannot enforce. Verify those keys are denied
and the independent passkey still works before taking the operational backup.

Retain the current executable and full database backup, including authentication
state, until downgrade validation completes. Test the previous executable against
a disposable copy first: prove independent dashboard access, revoked-key denial,
and retained project knowledge. If preparation cannot complete, keep the current
server or restore a known pre-feature operational backup with explicit attention
to its older credential and project state; do not downgrade the live upgraded
database merely to regain login. Deployment and live rollback remain outside this
implementation task.

## Review safeguards for GitHub authority

Configured instances authenticate public synchronization requests with their existing
OAuth app client ID and secret using an HTTP Basic authorization header, sent only
to the fixed GitHub REST origin. GitHub's
[OAuth app rate-limit documentation](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api#primary-rate-limit-for-oauth-apps)
provides this server-side method for public data. Keep the secret in operator
configuration; never put it in request URLs, browser code, exports or logs. Browser
identity lookup continues to use the freshly exchanged bearer token. No PAT or
additional stored secret is required. Existing bindings can retain cached machine
access when OAuth configuration is disabled, but such a binding does not qualify
as an alternative browser sign-in when revoking the last passkey.

Local enrollment must reject keys in the last persisted synchronized snapshot even
when they are blocked, stale or have never successfully proved possession. Those
conditions cannot convert GitHub-managed access into an independent credential.
Retain separate manual enrollment for independently managed keys outside that
snapshot. Validate these safeguards at the HTTP seam using synthetic provider
credentials, paginated/scheduled refreshes, configuration removal and actual
OpenSSH possession proofs.
