---
title: Contributing
description: Make focused changes, verify them, and keep project memory useful.
---

Scratchpad is in active early development. Start with the repository README, its runbooks, and the [architecture baseline](https://github.com/alexcatdad/scratchpad/blob/main/docs/architecture.md).

## Working agreements

- Keep the application and documentation in English.
- Log important decisions in `decisions.jsonl`, including rationale and evidence.
- Document repeatable multi-command activities in a runbook.
- Make focused commits with meaningful validation.
- Use maintained components and verify current stable versions before adding dependencies.
- Preserve other contributors' work and keep concurrent changes scoped.

## Verification

Changes must pass the repository lint, formatting, type, test, and build checks appropriate to their language and surface. GitHub Actions is the target CI runner. Follow the root scripts rather than assuming that a documentation build also verifies the API or MCP.

For documentation changes, run:

```sh
npm run check --workspace @scratchpad/docs
npm run build --workspace @scratchpad/docs
```

Also inspect the rendered page, its mobile navigation, and links beneath `/scratchpad/`. A static build alone cannot detect every visual or deployment problem.

## Documentation ownership

`docs/prd.md`, `docs/architecture.md`, and `docs/api.md` hold the detailed planning baseline. `apps/docs/src/content/docs` holds the public, reader-facing guides.

Do not publish private attachment paths, client examples, captured credentials, or historical source data when translating planning notes into public documentation. Clearly label planned capabilities and update instructions alongside working implementation.

## Release claims

Build, test, merge, publish, deploy, and operational acceptance are separate outcomes. Report exactly which were verified. Native signing/notarization, release artifacts, container publication, and GitHub Pages deployment each need their own evidence.
