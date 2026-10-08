---
status: accepted
---

# GitHub owner sign-in and synchronized machine access

The owner wants simpler onboarding for one person using a laptop and VMs. The design interview on 2026-10-08 selects GitHub browser sign-in and synchronized published SSH keys, with automatic machine access after proof of possession instead of individual dashboard enrollment. Passkey-only setup remains available; administrator-configured instances may offer GitHub-first setup. The owner confirmed the complete design on 2026-10-08. This accepted direction expands the presentation-only GitHub scope in PRD §44 and decision scratchpad-20261002-046; current implementation remains unchanged.

The owner accepted synchronization every five minutes, with cached GitHub-managed keys usable for up to 24 hours after the last successful synchronization. Machines may renew API sessions against that cache during an outage; expired GitHub browser sessions require GitHub to become available again. After the cache limit, access through GitHub-managed keys is denied until successful synchronization. A successful synchronization detecting key removal denies further requests and invalidates that key's sessions. Immediate local blocking must be available.

GitHub-first setup requires the administrator's single-use setup token; existing instances require authenticated linking. The owner binding uses GitHub's stable account ID. Both published SSH authentication keys and SSH signing keys qualify after possession proof; GPG support is deferred. Local key blocking persists across synchronization and removal/re-addition on GitHub, until explicitly unblocked locally.

Passkeys remain optional. Replacing the linked GitHub account requires fresh authentication with an existing credential, or administrator recovery when access is lost. Replacement preserves project data, invalidates GitHub-derived sessions and key permissions, and retains independent local credentials.

Machine onboarding belongs in an agent skill. The agent uses its preferred Git and SSH tooling to identify an available signing key and configure its Scratchpad connection, rather than relying on a dedicated MCP setup command. The skill must verify possession and actual authenticated access.

Manual SSH enrollment remains available for independent, locally managed credentials. Unlinking GitHub requires fresh authentication with an independent local credential, or administrator recovery if none is available, and invalidates GitHub-derived sessions and key permissions while preserving local credentials and project data.

The operator configures a GitHub OAuth app for standard browser sign-in. One Scratchpad instance's OAuth configuration serves its owner across devices. The owner accepts this one-time administrator setup cost; the onboarding skill documents it rather than introducing shared authentication infrastructure.

Synchronization must establish the complete key set from both SSH categories before applying removals or refreshing cache freshness. An unavailable endpoint, failed page or malformed response must not be interpreted as an empty successful key set. Persist the last successful synchronization time across restart; restart does not extend the cache limit. Enforce local blocking and the 24-hour cache limit on requests as well as session renewal. Never automatically convert GitHub-managed keys into independent local credentials when GitHub access is removed. Existing descriptive profile linkage is not evidence of account ownership and must not be automatically promoted into an authentication binding.

The design interview is complete. Acceptance establishes the design direction; authentication implementation and deployment have not begun. Implementation must reconcile the current PRD, architecture and API authentication contracts with this decision.
