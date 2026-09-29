# Dashboard design and verification

Use the generated concept at `/Users/alex/.codex/generated_images/01a0ec77-a0fa-7060-b8de-c35af0b8e0c0/exec-16dee617-10f7-48d5-b3bb-5539c9708ab4.png` as the layout reference. The concept is design-only illustrative data, not seeded product memory.

## Design system

Light neutral background #fafaf9, white navigation/detail surfaces, charcoal #202621, muted #69716d, olive #486247, hairline borders #dce0db. System sans for controls/body, Georgia serif for wordmark and main/detail titles. Desktop 224px sidebar, 32–48px content gutters; mobile collapses navigation and stacks detail below the list. Flat rows and horizontal dividers, no charts, invented statistics, or decorative cards.

## Inventory

Navigation: Scratchpad, Memory, Projects, Settings, Documentation, Sign out. Main heading: Project memory. Supporting copy: Decisions, findings, and the reasons behind your work. Actions: New record. Filters: Search your memory, All projects, All types. Record list/detail displays real API data, type, title, project, dates, content, rationale, authority, confidence, history and evidence. Setup, login, forms and settings extend the same visual system. Product workflows take precedence over illustrative rows/dates/author names in the concept.

## QA procedure

Run web server, inspect through the in-app browser first, verify setup/login, project/list/detail/search/create/settings/sign-out behavior, desktop and narrow layout, and capture a screenshot. Compare to the concept with image viewing: palette, typography, sidebar, spacing, list/detail arrangement, controls and overflow. Remove temporary QA outputs from tracked files. Use a virtual passkey only in disposable integration tests, never substitute test auth in production.
