# Scratchpad

## Purpose

Private, self-hosted memory for developers and coding agents: decisions,
findings, questions, failures, constraints and project history, with reasoning
and provenance intact. A Go server (`backend/`) with a React dashboard, a Go
MCP adapter and capture service (`mcp/`), and a native macOS menu-bar app
(Swift 6, macOS 13+, Apple Silicon) that bundles them for the desktop.

Read `docs/prd.md`, `docs/architecture.md` and `docs/api.md`, including accepted
clarifications, before changing behavior. `docs/archive/` is historical, not
current authority.

## Invariants

- English only for product UI and documentation.
- Captures are immutable, provenance is explicit, the central API is
  authoritative, and repository mirroring stays optional.
- Development starts with the authenticated SQLite loop; the full MVP remains
  defined by the PRD. Local tests are not deployed acceptance.
- Secrets live only in the macOS Keychain on the desktop and in owner-held
  secret configuration on the server. No plaintext fallback, no secrets in logs
  or captures.
- Notifications are posted by the app bundle: opt-in, permission requested from
  the user toggle, silent unless the user enables sound. Never `osascript`,
  `terminal-notifier` or a bare binary.
- The tray uses the 18pt template icon, status badge and status-aware
  accessibility label. Scratchpad is the reference look for `alexcatdad/menubar-kit`.
- No third-party dependencies in the shipped macOS app without a recorded
  decision. First-party `alexcatdad/menubar-kit` is allowed, pinned to an exact
  revision.
- Use maintained dependencies; verify the latest stable version before adding or
  upgrading one and pin exact versions. Node uses the latest Node 24 LTS patch
  per the architecture.

## Layout

- `backend/` — Go server: authentication, persistence, migrations, HTTP API, optional AI/RAG.
- `mcp/` — Go stdio MCP adapter and local capture service.
- `macos/` — Swift package: app target, `ScratchpadNative` library, Keychain helper, tests.
- `apps/web/` — retained React dashboard; `apps/docs/` — documentation site (GitHub Pages).
- `packages/clients/` — generated TypeScript and Go HTTP clients.
- `deploy/`, `nix/`, `flake.nix`, `Dockerfile` — homelab and container deployment.
- `docs/` — PRD, architecture, API, ADRs and runbooks. `tests/` — Playwright acceptance.

## Build & verify

- `task check` — the full local gate: npm lint, typecheck and tests; gofmt, vet
  and race tests for `backend/` and `mcp/`; `swift test`. Run it before pushing.
- `task lint`, `task build`, `task test`, `task security` — what Forgejo Actions
  calls. `task security` runs `govulncheck` per Go module and
  `npm audit --omit=dev --audit-level=high`.
- Forgejo CI runs on the Linux `homelab` runner with `SKIP_SWIFT=1`, so it does
  not cover Swift. Swift build and tests run only in local `task check` and
  `task release:check`; run them before merging any `macos/` change.
- PostgreSQL, pgvector, browser (`npm run test:e2e`) and container acceptance need
  extra services; see `docs/runbooks/development.md`.
- Toolchain: Go 1.27.3, Swift 6 on Xcode for macOS 26, Task, Node 24 + npm.
- Multi-command project activities need a runbook under `docs/runbooks/`.

## Release

- Local only, with release-tools (`task release:check`, `task release:prepare`,
  `task release`); see `docs/runbooks/release.md`. Developer ID signed,
  notarized and stapled; keys stay in the local Keychain. CI never signs,
  publishes, deploys or updates the tap.
- Versions move to CalVer `vYYYY.MM.DD.N` when `RELTOOL_TAG` is bumped to the
  release-tools tag that supports it; until then the release runbook's scheme
  applies. Releases are immutable: never replace published assets or move tags.
- Distribution: Homebrew cask in `alexcatdad/homebrew-tap`. Never strip quarantine.

## Git

- Canonical host: Forgejo `alexcatdad/scratchpad` (homelab, automated
  deployment), with `github.com/alexcatdad/scratchpad` as a push mirror. GitHub
  Actions is disabled on the mirror; only `pages.yml` would run there if it is
  re-enabled.
- Branch from `main`, PR back to `main` on Forgejo, squash-merge. Conventional
  commit messages. Make small coherent commits; never commit secrets, local
  databases or test artifacts.
- Forgejo Actions runs `task lint`, `task build` and `task test` on pull
  requests and `task security` weekly. Workflows start from `permissions: {}`
  where the host supports it; GitHub actions are pinned to full commit SHAs.
- Do not wait on or poll CI; one status check when a decision depends on it.
- Agent parallelism is allowed. Coordinate file ownership, do not overwrite
  concurrent work, and stop agents when their task is done.

## Memory

Decisions, constraints and project state live in Scratchpad (project
`scratchpad`), not in repo files. Read context there before substantial work and
record meaningful decisions there.

Migration pending: `decisions.jsonl` (checked by `scripts/check-decisions.mjs`
in `npm run lint`) still holds the historical decision log. Until it is imported
into Scratchpad and deleted, preserve its entries and do not add new ones. If the
Scratchpad service is unreachable, state the decision in the pull request
description and record it once the service is back.
