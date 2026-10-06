# Dashboard workflow improvements

## Scope

The owner requested a usable developer dashboard and authorized homelab deployment.
Keep the existing dark theme and API, immutable captures, provenance, project consent,
and independent passkey/SSH authentication. Improve reading and navigation before
visual polish. Do not merge the PR without an explicit merge request.

## Implementation and verification

1. Work on `codex/dashboard-ux`; preserve unrelated changes.
2. Use existing TanStack Router search state for navigation and filters. Give records
   a dedicated reader, preserve result context, and inherit project scope in capture.
3. Render Markdown and structured payloads without executing HTML or loading remote
   images. Retain original payload inspection. Use native modal keyboard behavior.
4. Group settings and optional AI workflows. Keep every existing operation available
   and server-controlled; no automatic provider calls, jobs or consent changes.
5. Run lint, type checks, unit tests, production build and authenticated browser/MCP
   workflows with disposable synthetic data. Add regressions for URL reload/back,
   mobile record visibility, project-scoped capture and keyboard dismissal.
6. Inspect the rendered candidate at desktop and phone sizes. Keep screenshots and
   runtime fixtures outside tracked source. Record checks against the exact PR head.
7. Commit, push and open a PR; monitor GitHub Actions through terminal success.

## Deployment and rollback

1. Read current homelab inventory and runbooks; verify SSH identity and the live
   Scratchpad deployment image, container name, service, database references and
   replica readiness. Keep credentials out of logs and preserve existing state.
2. Build a uniquely tagged application image from the tested source. Transfer/import
   it through the host's supported container tooling. Record exact source and image.
3. Preserve the prior workload specification privately, then update only Scratchpad's
   application image. Keep PostgreSQL, passkeys, private HTTPS identity and service
   configuration intact. No database migration is required by this frontend change.
4. Wait for rollout completion. Verify private readiness, authenticated dashboard
   reads, responsive record opening, navigation and session survival. Verify the
   actual image identity; a successful build is not deployed acceptance.
5. If rollout or acceptance fails, restore the recorded prior image and verify
   readiness. Do not alter database state or rotate credentials as rollback.
6. Capture source, PR/check evidence, image, rollout and limitations in Scratchpad.
   Preserve the operational deployment receipt in the homelab's existing inventory.
