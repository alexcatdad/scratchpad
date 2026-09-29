---
title: Installation & development
description: Build the documentation and follow the development installation path.
---

:::caution[Development build]
There is no production release yet. Published containers, native release binaries, and Homebrew installation are V1 distribution targets, not currently guaranteed installation methods.
:::

## Work from source

Clone [the repository](https://github.com/alexcatdad/scratchpad) and use its pinned runtime and package versions. The application targets Node.js 24 LTS. npm workspaces manage the TypeScript application and documentation; the Go MCP has a separate build domain.

From the repository root, install the locked dependencies:

```sh
npm ci
```

To run this documentation site:

```sh
npm run dev --workspace @scratchpad/docs
```

Open the local address printed by Astro and include the `/scratchpad/` base path.

## Check and build the docs

```sh
npm run check --workspace @scratchpad/docs
npm run build --workspace @scratchpad/docs
```

The build output is `apps/docs/dist`. It is a static site for GitHub Pages; it contains documentation, not your private Scratchpad records or running application.

```sh
npm run preview --workspace @scratchpad/docs
```

Preview serves the built site locally. A successful build does not prove a GitHub Pages deployment succeeded; inspect the deployment workflow and live URL separately.

## Application development

Use the checked-in repository README and development runbook for the application and Docker commands as the first slice lands. This guide intentionally does not advertise unimplemented enrollment commands or unpublished images.

The deployment design uses one application container with a persistent SQLite volume. A later release must document the exact image version, migration command, health check, backup/restore procedure, and owner-enrollment workflow before being considered ready for operational use.
