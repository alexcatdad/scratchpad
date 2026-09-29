---
title: Releases & installation channels
description: Native platforms, release verification, and the remaining publication prerequisites.
---

:::caution[Not published yet]
Release automation is implemented. Signed binaries, a published application image, and a delivered Homebrew formula have not yet been verified. Use [local builds](/scratchpad/guides/installation/) for now.
:::

## Distribution targets

| Component   | Planned published format                                                      |
| ----------- | ----------------------------------------------------------------------------- |
| macOS MCP   | ARM64 and AMD64 ZIPs containing Developer ID signed, Apple-notarized binaries |
| Linux MCP   | ARM64 and AMD64 tar archives, also usable with Linux Homebrew on WSL          |
| Application | Versioned GHCR image for Linux ARM64 and AMD64                                |
| Homebrew    | `scratchpad-mcp` formula in the existing `alexcatdad/tap`                     |

No npm package or native Windows installer is required for this release plan.

## What the pipeline checks

A stable `vX.Y.Z` tag must point to a commit on `main` with successful CI at that exact source revision. Native macOS artifacts must pass signature verification and receive Apple's `Accepted` notarization result before publication continues.

The workflow generates checksums, publishes the complete GitHub Release inventory, builds the versioned multiarchitecture image, and updates the tap from downloaded and verified release assets. It refuses to overwrite a public release or downgrade an existing tap version. A separate recovery option retries tap delivery without rebuilding previously published files.

These are workflow guarantees to verify during the first real release. Passing local tests alone does not prove that Apple, GitHub Packages, or Homebrew delivery accepted the artifacts.

## Remaining setup

The repository still needs its release environment configured with a Developer ID certificate, signing/notarization credentials, and the existing GitHub App's tap-writing credentials. The [release runbook](https://github.com/alexcatdad/scratchpad/blob/main/docs/runbooks/release.md) names the required secrets and documents provisioning, publication, and recovery.

After publication, validate checksum downloads, both image architectures, clean Homebrew installation, and a real MCP session before treating that version as delivered.
