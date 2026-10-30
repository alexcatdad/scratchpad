# Bounded Codex offline capture validation

Scope: active Codex Scratchpad MCP and its local collector only. Leave Claude,
network settings, and homelab infrastructure unchanged. Use synthetic content.
The owner authorized continuing the bounded validation on 2026-10-16.

1. Verify explicit `SCRATCHPAD_SIGNING_KEY` configuration and matching live MCP
   environment without printing values. Confirm the referenced file exists.
2. Confirm project identity using `get_project_context`. Require connected local
   collector, available storage/key, and zero pending/blocked/rejected/selection
   items. Require an unlocked session and a fresh successful protected-key read.
3. Before interruption, retain original collector arguments, environment and cwd
   privately in supervisor memory. Arm independent restoration. Restart through
   a process-local proxy restricted to the configured server; verify forwarding.
4. Reject proxy connections for at most 45 seconds. Through the active Codex tool,
   submit one synthetic `record_finding` with a stable request ID. Require
   `saved_pending` and independently look up its local receipt. Confirm a
   connectivity delivery code and rejected proxy requests.
5. Restore forwarding. Require the same receipt to become delivered; fetch its
   central record and compare content, project, original timestamp and provenance.
6. Restore original collector launch state. Verify connected status, key/storage,
   unchanged configuration/launch state and empty pending/blocked queue. Wait for
   supervisor completion. Retry identical input and require unchanged receipt and
   central record IDs.

Abort on failed preflight. After any interruption, prioritize verified recovery.
Do not interpret initialization, catalog exposure or a proxy probe as acceptance.

## Verified run: 2026-10-16

- Explicit signing-key reference existed and matched all inspected live Codex MCPs.
- Fresh protected-key read passed before interruption.
- Outage: 11:29:02.099–11:29:18.451 Europe/Bucharest (16.352 seconds).
- Actual Codex capture: `saved_pending`; local receipt `blocked/connectivity`.
- Receipt: `wV2hjrEZ-eien1SJYSbW6r902ott3zO9`.
- Request: `codex-offline-acceptance-20261016-0829-01`.
- Delivered 11:29:25.289 Europe/Bucharest; central record
  `rec_0272d8e0-808c-49a8-b966-4c47c7caf607` independently retrieved.
- Identical retry after restoration returned the same receipt and central ID.
- Supervisor exited successfully with launch-state match, connected collector,
  zero pending and blocked items. Claude and homelab were untouched.
- Local supervisor evidence: `/private/tmp/scratchpad-validation-uzHLms/evidence.json`.

This validates one synthetic finding through the active Codex client. It does not
establish all capture types, tray visual behavior, or machine/network outage behavior.
