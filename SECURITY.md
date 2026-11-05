# Security Policy

## Reporting a vulnerability

Report vulnerabilities privately through the GitHub mirror's
[security advisory form](https://github.com/alexcatdad/scratchpad/security/advisories/new).
The canonical repository is on the maintainer's Forgejo instance; the GitHub
mirror's private vulnerability reporting is the monitored public channel. Do
not open a public issue for a suspected vulnerability. Expect an initial
response within seven days.

## Supported versions

Only the latest release is supported. New releases use CalVer
(`vYYYY.MM.DD.N`); earlier releases used semantic versions. Fixes ship as a new
release, never as a replacement of a published one.

## What Scratchpad does

- The server is self-hosted and single-owner. It stores captures in SQLite (or
  optional PostgreSQL) and authenticates the owner with passkeys, optional
  GitHub sign-in and SSH challenge proofs. An independent security review has
  not been performed.
- The macOS app bundles the stdio MCP adapter, a local capture service with an
  encrypted offline queue, and a Keychain helper. It talks only to the server
  the owner configures. SSH private keys never leave the local machine or
  signing device.
- Optional AI providers, GitHub profile enrichment and repository mirroring are
  off unless the owner configures them. Provider secrets are encrypted at rest
  with an owner-held key file.
- Captures are immutable.
- No telemetry, analytics or tracking.

See the [security guide](https://alexcatdad.github.io/scratchpad/reference/security/)
for the authentication and recovery model.

## Release trust

Releases are built and Developer ID signed locally, notarized by Apple and
stapled. Signing keys and notarization credentials stay in the maintainer's
local Keychain. GitHub releases are immutable; verify a release's attestation
with `gh release verify <tag> -R alexcatdad/scratchpad`. The Homebrew cask does
not remove quarantine.
