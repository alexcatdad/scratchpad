---
title: Releases & installation channels
description: Install Scratchpad v0.1.3 and understand how its release artifacts are verified.
---

## v0.1.3

[v0.1.3 on GitHub](https://github.com/alexcatdad/scratchpad/releases/tag/v0.1.3) provides native MCP archives, release checksums, and the versioned application image `ghcr.io/alexcatdad/scratchpad:v0.1.3`. This patch corrects exact timestamp ordering and SQLite readiness; it is not a claim that every deployment environment has been tested.

| Component   | Published format                                                              |
| ----------- | ----------------------------------------------------------------------------- |
| macOS MCP   | ARM64 and AMD64 ZIPs containing Developer ID signed, Apple-notarized binaries |
| Linux MCP   | ARM64 and AMD64 tar archives, also usable with Linux Homebrew on WSL          |
| Application | Versioned GHCR image for Linux ARM64 and AMD64                                |
| Homebrew    | `scratchpad-mcp` formula in the existing `alexcatdad/tap`                     |

Install the MCP through Homebrew:

```sh
brew install alexcatdad/tap/scratchpad-mcp
scratchpad-mcp --version
```

Homebrew follows the version currently delivered to the tap. For an exact release, use that release's named archive and verify it against its `checksums.txt`. See [installation](/scratchpad/guides/installation/) for server setup and key enrollment.

No npm package or native Windows installer is part of this release. AI processing, embeddings, pattern analysis, and PostgreSQL remain post-MVP; capturing and retrieving memory does not require them.

## Local signing, independent verification

The owner signs both macOS binaries with a Developer ID Application identity and submits their ZIP archives to Apple using credentials in the **local macOS Keychain**. Signing credentials and notarization passwords are not uploaded to GitHub.

After Apple accepts both submissions, the owner uploads the archives, notarization receipts, and a source/checksum manifest to a draft release. A manual workflow verifies those finished files against a fresh build of the tagged source, the configured public team identifier, and Apple trust assessment. A stable tag must identify a commit on `main` with successful CI at that exact revision.

The workflow builds Linux archives and the multiarchitecture image, publishes checksums, and updates the tap from verified release assets. It refuses to overwrite a public release or downgrade an existing formula. Tap-only recovery reuses the published bytes rather than rebuilding signed archives.

Bare command-line binaries and ZIPs do not support stapled notarization tickets. The published macOS ZIPs are the exact Apple-accepted archives; online Apple trust assessment remains relevant.

## Operating and upgrading

The [MVP acceptance guide](/scratchpad/guides/mvp-acceptance/) contains the completed scenario checklist and a reusable synthetic example.

Use the versioned image tag to make upgrades deliberate. Keep a full SQLite backup before upgrading, and retain the matching older application image for rollback. Follow the [deployment runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/deployment.md) and [release runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/release.md) for operations and maintainer procedures.

The [v0.1.3 fresh-runner release acceptance](https://github.com/alexcatdad/scratchpad/actions/runs/36859768053) passed macOS/Linux Homebrew installation, installed MCP authentication and capture, readiness for both image architectures, and published-image backup/restore. The expanded workflow also verifies contractor repositories, linked worktrees and explicit non-Git identity through the actual API. The [readiness ledger](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/mvp-readiness.md) records the completed scope and synthetic acceptance scenarios. Other projects are illustrative examples, not sources of truth for Scratchpad’s requirements. Verify browser sign-in and a real MCP session in your own deployment.
