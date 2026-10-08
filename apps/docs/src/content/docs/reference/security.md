---
title: Security & recovery
description: The single-owner authentication and recovery model.
---

Scratchpad is one owner per instance, with multiple browser and machine credentials.
Passkeys and SSH challenge authentication remain available without GitHub. Current
server source adds optional GitHub owner sign-in and synchronized machine keys;
released v0.3.0 servers predate this addition. An independent security review has
not been performed.

## First owner and sign-in

`npm run admin -- setup` on the server creates a 15-minute, single-use setup token.
The owner supplies it at the configured origin and chooses a first passkey or,
when the operator configures OAuth, GitHub sign-in. Initial setup closes after
ownership is established. GitHub authentication binds the stable GitHub account
ID; a username change preserves identity. Another GitHub user cannot sign into
an already bound instance. An old descriptive GitHub profile is not verified
ownership and is never promoted automatically.

The operator configures one OAuth app per instance; see the
[OAuth runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/github-oauth.md).
Client machines share that server configuration. Passkeys remain optional with
GitHub-first setup. SimpleWebAuthn checks relying party, origin, challenge and
registration/authentication responses for independent passkey access.

## Machine access and synchronization

A verified GitHub binding makes published SSH authentication and SSH signing keys
eligible automatically. Each machine must prove possession through Scratchpad's
signed challenge. GPG keys are unsupported. Manual dashboard enrollment remains
available for independent local SSH credentials, including unpublished keys.
Private material stays on the local machine or signing device.

The server synchronizes every five minutes. A successful synchronization requires
the complete key set from both categories, including all pages. Failed endpoints,
failed pages or malformed responses preserve the previous set and last-success
time. The dashboard shows binding, freshness, errors and key block state.

Cached GitHub-managed keys may authenticate and renew sessions for up to 24 hours
since the last successful synchronization. After that deadline, GitHub-managed
machine access is denied until synchronization succeeds. Authorization checks
freshness on each request, even for an otherwise valid token. Restart does not
extend the deadline. During an outage, a removed or compromised key may retain
cached eligibility until the deadline unless blocked locally: this bounded
revocation delay is the accepted availability tradeoff.

Detected key removal denies further requests and invalidates associated sessions.
A local block takes effect immediately and persists across synchronization and
GitHub removal/re-addition until explicitly unblocked locally. Removing a key
from GitHub therefore takes effect after the next successful synchronization;
block it locally for immediate action. Independent manual keys remain locally
managed and unaffected by GitHub cache expiry.

Random challenges are short-lived and single-use. MCP sessions last up to 24 hours
with no refresh-token system and stay in MCP process memory. Restarting the MCP
requires proof again. The server persists session validity and revocation.

## Outages and independent access

A valid browser session continues locally during a GitHub outage. An expired
GitHub browser session needs GitHub to return, or an independent passkey. Machines
can renew sessions against eligible cached keys within the 24-hour freshness
limit. Network access to the Scratchpad server is still required from every device.

## Linking, replacing and disconnecting

Existing owners link GitHub through authenticated Settings. Replacement requires
fresh authentication through an existing credential; disconnecting requires fresh
independent local authentication. Sign in again immediately before these actions:
freshness is at most five minutes from an actual authentication proof, rather
than session renewal. Administrator recovery is available if access is lost or no
independent credential remains.

Replacement and unlinking invalidate GitHub-derived sessions and key permissions,
preserve project data and independent local credentials, and produce audit events.
Synchronized keys are never silently converted into independent local credentials.
Descriptive profile linkage remains a separate presentation setting.

## Recovery

Server administrative access is the recovery authority. `npm run admin -- recover`
authorizes the separate audited recovery flow. Register a replacement passkey for
independent access or replace the GitHub binding through authorized recovery.
Recovery preserves records and does not reopen ordinary first-owner setup.
Keep an independent credential available if browser access during a GitHub outage
matters. No email delivery is required.

## Provenance and untrusted content

The server supplies record IDs, receipt timestamps and authenticated credential
attribution. Caller-authored actor descriptions are context, not verified identity.
Captured text, imported logs and AI output are evidence to inspect; retrieval does
not authorize executing instructions in that content.

## Persistence and backups

Records, account bindings, synchronization freshness, blocks, sessions, revocations,
audit history and retry identity belong to durable server state. A restart must
not resurrect revoked authority or reset cache age. Knowledge exports omit
credentials, sessions and OAuth secrets. Full operational backups preserve
administrative state and need private storage and a tested disposable restore.
Local checks, canonical CI, releases and live deployment acceptance remain
separate outcomes.
