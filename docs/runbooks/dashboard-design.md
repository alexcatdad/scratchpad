# OLED dashboard design and verification

## Scope and tokens

The private dashboard is a working notebook for captured project reasoning.
Use pure black (`#000000`) for the canvas and navigation, near-black (`#0d0e11`)
for controls and inspectors, soft gray (`#dfdfe6`) text, readable secondary gray
(`#999ba8`), subtle rules (`#25272e`) and muted violet (`#aaa5ed`) accents.
Headings use Avenir Next with system fallbacks; body and controls use system sans.
Keep existing API behavior, authentication, capture authority and settings intact.

The memory list remains open rows, with a narrow selected marker. The inspector,
capture dialog, project forms, insights, settings and authentication all share
the same dark tokens. Native controls use `color-scheme: dark`; no external font
requests, glow, gradients or large bright surfaces. A black canvas is the chosen
presentation default, independent of the system appearance setting.

## Local verification

Use the Node version in `.node-version` from the repository root:

```sh
npm run lint
npm run typecheck -w @scratchpad/web
npm run build -w @scratchpad/web
npm run test:e2e -- tests/workflow.spec.ts
```

For visual work, use a disposable loopback-only fixture with synthetic records,
never a copy of the owner's authentication state. Fixture responses prove visual
rendering only. Actual authentication and persistence remain covered by the
existing workflow test and separately verified deployed reads.

Check desktop at 1500 × 1050 and mobile at 390 × 844 in the in-app browser. Verify
navigation, search and empty results, record selection and close, advanced
filters, capture open/close, forms and native selects, long titles, project/date
visibility, focus, and console health. Do not submit live captures or change
settings just for visual QA. Save concepts and screenshots outside the repository.

## Deployment

Commit coherent frontend changes and wait for CI on that exact source commit.
Use the existing private CT deployment configuration and preserve its database,
origin, credentials, optional AI configuration, environment file and volume.
Build the candidate server image before switching the service. Retain the current
image/configuration and a fresh operational database backup for rollback.
Deploy only after the existing workflow passes; verify readiness and the owner's
signed-in browser against the stable HTTPS origin. A dashboard theme update does
not require native MCP distribution or Apple signing.

If rollout fails, reconnect the preserved image through the same Compose
configuration. No persistence migration is part of this change.

## Fidelity and functional review

The design concept is `exec-a881a38c-96e8-4edb-a629-4e62e4f43a52.png`,
kept outside the repository. Browser evidence uses the same 1500 × 1050
desktop dimensions and 390 × 844 mobile dimensions. Both concept and rendered
captures were inspected with image viewing.

| Comparison | Verified implementation                                                                          |
| ---------- | ------------------------------------------------------------------------------------------------ |
| Palette    | Pure-black canvas, near-black surfaces, gray text, muted violet actions; no gradients or glow.   |
| Hierarchy  | Sidebar, project-memory heading, search toolbar, open rows and record inspector retained.        |
| Typography | Sans display hierarchy, explicit body/control sizes, readable secondary text.                    |
| Selection  | Quiet selected row with a narrow violet marker; the inspector updates and closes.                |
| Controls   | Outlined record labels, consistent dark inputs, native dark selects and visible focus.           |
| Mobile     | Navigation wraps, inspector stacks, project/date remain visible; repaired cramped select labels. |
| Copy       | Existing headings, navigation, actions and payload-specific detail content remain authoritative. |

Intentional deviations: preserve the existing scratch brand glyph and actual
inspector sections, provenance disclosure and evidence/history controls. The
inspector stays below the shared heading/toolbar rather than replacing that
layout with a new full-height rail. Illustrative concept data is never seeded
into production. Mobile rows retain project and date information instead of
hiding them. Settings, insights and forms extend the same token system.

Search returns the expected empty state; record selection/close, navigation,
and capture open/close work in the read-only fixture. The console is clean,
no framework overlay appeared, and no horizontal mobile overflow was observed.
The existing disposable end-to-end workflow passed enrollment, capture/search,
MCP and restart persistence. No live capture or settings mutation is needed
for this presentation change.

## Previous design history (superseded)

The original light design is superseded by the owner-requested OLED presentation.
Its earlier evidence is retained below for audit continuity.

Initial light dashboard design:

Use the generated concept at `/Users/alex/.codex/generated_images/01a0ec77-a0fa-7060-b8de-c35af0b8e0c0/exec-16dee617-10f7-48d5-b3bb-5539c9708ab4.png` as the layout reference. The concept is design-only illustrative data, not seeded product memory.

### Design system

Light neutral background #fafaf9, white navigation/detail surfaces, charcoal #202621, muted #69716d, olive #486247, hairline borders #dce0db. System sans for controls/body, Georgia serif for wordmark and main/detail titles. Desktop 224px sidebar, 32–48px content gutters; mobile collapses navigation and stacks detail below the list. Flat rows and horizontal dividers, no charts, invented statistics, or decorative cards.

### Inventory

Navigation: Scratchpad, Memory, Projects, Settings, Documentation, Sign out. Main heading: Project memory. Supporting copy: Decisions, findings, and the reasons behind your work. Actions: New record. Filters: Search your memory, All projects, All types. Record list/detail displays real API data, type, title, project, dates, content, rationale, authority, confidence, history and evidence. Setup, login, forms and settings extend the same visual system. Product workflows take precedence over illustrative rows/dates/author names in the concept.

### QA procedure

Run web server, inspect through the in-app browser first, verify setup/login, project/list/detail/search/create/settings/sign-out behavior, desktop and narrow layout, and capture a screenshot. Compare to the concept with image viewing: palette, typography, sidebar, spacing, list/detail arrangement, controls and overflow. Remove temporary QA outputs from tracked files. Use a virtual passkey only in disposable integration tests, never substitute test auth in production.

### Initial verification

The in-app browser rendered setup and the deployed Starlight presentation. Disposable Chromium tests exercised the authenticated dashboard with a virtual passkey and saved desktop (1440px) and mobile (390px) screenshots. Both were inspected against the concept: olive actions, serif headings, neutral surfaces, sidebar/list/detail layout, and readable stacked mobile detail are present. The implementation uses real captured data, a visible Search button, and a simpler text navigation. The narrow search placeholder is clipped within its control but the page has no horizontal overflow.
