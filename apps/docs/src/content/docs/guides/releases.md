---
title: Releases & installation channels
description: How Scratchpad releases are versioned, built, signed and delivered through Homebrew.
---

## What a release contains

Each release is one archive, `scratchpad-<version>-darwin-arm64.zip`, holding
`Scratchpad.app` for Apple silicon on macOS 13 or later. The app bundles
everything a Mac needs:

| Helper                                | Purpose                                              |
| ------------------------------------- | ---------------------------------------------------- |
| `Contents/Helpers/scratchpad`         | The command coding agents use.                       |
| `Contents/Helpers/scratchpad-mcp`     | The stdio MCP server for MCP-only clients.           |
| `Contents/Helpers/scratchpad-service` | The capture service and its encrypted offline queue. |

Each helper reports the release version with `--version`. The app shows the same
version at the bottom of its menu bar panel and in **Settings → About**.

Versions are daily CalVer, `YYYY.MM.DD.N`, with tags `vYYYY.MM.DD.N`. Releases
are immutable: published assets are never replaced and tags never move.

## Where releases are published

Releases are published only on the owner's private Forgejo instance, reachable
on the owner's tailnet. GitHub is a source mirror with no releases. The
`alexcatdad/tap/scratchpad` Homebrew cask downloads from Forgejo with a
read-only token, so it installs and upgrades only on machines on that network;
see [installation](/scratchpad/guides/installation/). The cask keeps macOS
quarantine in place; the app is notarized and stapled, so Gatekeeper accepts it.

The deprecated `alexcatdad/tap/scratchpad-mcp` formula receives no further
releases. Earlier `v0.x.y` and monthly `vYYYY.M.N` releases remain on GitHub as
history. v0.3.0 also published a `ghcr.io/alexcatdad/scratchpad:v0.3.0`
server image, which predates the Go server.

## How a release is made

Releases are explicit and local. The owner runs the release tasks on a Mac with
[release-tools](https://github.com/alexcatdad/release-tools); no CI workflow
builds, signs, publishes or updates the tap. The build signs the app and its
helpers with a Developer ID identity, submits them to Apple for notarization and
staples the ticket. Signing keys and notarization credentials stay in the local
macOS Keychain.

release-tools then publishes the archive with `SHA256SUMS` and a manifest, and
updates the cask's version and checksum. A release tag does not deploy the
server; deployment is a separate step.

See the [release runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/release.md)
for the maintainer procedure and the
[distribution runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/macos-app-distribution.md)
for installation checks.

## Upgrading

```sh
brew upgrade --cask scratchpad
```

After an upgrade the capture service keeps running the previous version until it
restarts. The menu bar panel shows **Update installed**; choose **Restart Capture
Service**. Restarting keeps every queued capture. Upgrade your server alongside
the app when a release notes a server change; captures wait on your Mac while the
server is older than the app needs.

The [MVP acceptance guide](/scratchpad/guides/mvp-acceptance/) keeps the
historical v0.1.3 acceptance checklist and a reusable synthetic example.
