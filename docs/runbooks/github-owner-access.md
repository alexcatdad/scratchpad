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
