---
title: Security & recovery
description: The single-owner authentication and recovery model.
---

:::note[Initial implementation]
The published server implements these authentication and recovery flows. An independent security review has not been performed.
:::

Scratchpad V1 is one owner per instance, with multiple browser credentials and agent credentials. Browser and MCP authentication use separate mechanisms.

## First owner

`npm run admin -- setup` on the server creates a 15-minute, single-use setup token. The owner opens the setup page at the configured origin, supplies the token, and registers the first passkey. Ordinary initial setup stays disabled after enrollment.

SimpleWebAuthn handles passkey verification. Verification checks the expected relying party, origin, challenge, and registration/authentication response.

## Agent enrollment

The authenticated owner enrolls an MCP public key through the dashboard. Enrollment requires proof of possession, not merely submitting a public-key string. The corresponding private key stays on the local machine or signing device.

Random challenges are short-lived and single-use. MCP sessions last up to 24 hours with no refresh-token system initially. Credential revocation must invalidate associated sessions immediately, including after a server restart.

## Recovery

Server administrative access is the recovery authority. `npm run admin -- recover` authorizes replacement-passkey registration. Recovery is explicit and audited, preserves records, and does not reopen first-time setup.

There is no email-delivery dependency or external identity provider in the initial flow.

## Provenance and untrusted content

The server supplies record IDs, receipt timestamps, and authenticated credential attribution. Caller-authored actor descriptions are useful context, not verified identity.

Captured text, imported logs, and AI output are evidence to inspect. They must never become executable instructions merely because they were retrieved from Scratchpad.

## Persistence and backups

Records, sessions, revocations, audit history, and retry identity belong to durable server state. A container restart must not revoke or resurrect authority accidentally. Operational release documentation must cover backups and tested restoration before deployment is advertised as production ready.
