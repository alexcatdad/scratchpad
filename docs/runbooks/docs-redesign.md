# Documentation design and verification

## Purpose

The public Pages site introduces Scratchpad and helps developers install, connect,
and operate it. It is not the owner dashboard. Preserve Astro/Starlight navigation,
search, theme switching, code copying, and the existing document routes.

## Design specification

Use an indigo engineering notebook: white page, pale lavender chrome, slate ink,
and cobalt links. The signature is a three-page context folio, showing how a
capture becomes useful context while its history stays recoverable. It is an
explicit illustrative example, never an application screenshot.

Tokens: white `#ffffff`, lavender `#f5f5fc`, ink `#1c2236`, muted `#5d657d`,
cobalt `#4a50dc`, rule `#e4e6f0`. Dark mode uses ink navy `#151827`, raised
navy `#1e2235`, pale text `#edf0ff`, muted text `#a9b0ca`, and periwinkle
`#b5baff`. Avoid gradients and tinted imagery. Use Avenir Next with system
sans fallbacks for display, system sans for body, and system monospace for code.

The homepage opens with “Leave a trail worth finding.”, the existing product
framing and explanatory paragraph, then “Get started” and “Connect your MCP”.
Below, retain the capture, resume, and ownership concepts as open prose columns.
Finish with an open list of concrete documentation routes. Inner pages retain a
left navigation rail, readable central article, and compact right contents rail.

Keep the folio quiet enough that the headline remains the first focal point.
Do not add metrics, badges, decorative eyebrows, nested cards, or fake controls.
On mobile, put the example below the actions and collapse prose and route lists
into a single column. Keep Starlight's native mobile navigation.

## Development

From the repository root:

```sh
npm ci
npm run dev --workspace @scratchpad/docs -- --host 127.0.0.1 --port 4321
```

Open `http://127.0.0.1:4321/scratchpad/` and an inner guide, such as
`/scratchpad/guides/installation/`.

## Verification

```sh
npm run check --workspace @scratchpad/docs
npm run build --workspace @scratchpad/docs
npm run lint
```

Use the in-app browser for desktop and mobile review. Check homepage actions,
sidebar links, table of contents, keyboard focus, search, theme selection, code
copying, and reduced-motion behavior. Compare concept and browser captures with
`view_image`; inspect copy, composition, palette, typography, spacing, asset
framing, and narrow-screen overflow. Keep evidence outside the public site.

A local build is not a Pages deployment. Publication follows the repository's
existing GitHub Actions workflow after the reviewed changes reach its trigger.

## Design review record

The concept set covers the homepage opening, its continuation, and an inner
installation guide. A separate transparent folio asset is the production
illustration; the conceptual mockups are not embedded as UI.

| Comparison | Implementation and review                                                                                                |
| ---------- | ------------------------------------------------------------------------------------------------------------------------ |
| Hierarchy  | Split headline/folio opening; article title and description precede the guide body.                                      |
| Palette    | White content, pale lavender navigation, slate ink, cobalt actions; corresponding navy dark theme.                       |
| Typography | Avenir Next display with portable fallbacks, explicit body/control sizes, native mono code.                              |
| Containers | Open prose columns and guide rows; preserve article/sidebar/contents rails.                                              |
| Actions    | Cobalt primary action with modest corner radius; text MCP action; real guide destinations.                               |
| Assets     | Transparent folio with short illustrative copy and explicit example caption; no historical dates or invented record IDs. |
| Spacing    | Isolate custom home content from Markdown sibling margins so repeated columns align.                                     |

Intentional deviations from the generated mockups: retain the existing cat brand
mark, retain Starlight's functional search/header arrangement, and simplify the
folio's wording to the specified example. Actual guide code and prose remain
canonical; generated mockup examples do not replace the real installation
instructions. The illustration caption makes its example status explicit.

No new product features are introduced by the visual changes. Connection-guide
copy is verified independently from layout. Browser captures belong outside the
repository; retain only the production illustration and this repeatable runbook.

The implementation was reviewed against all three concept surfaces at the
concept's desktop viewport (1505 × 1045) and a 390 × 844 mobile viewport. The
final review checked the headline scale and line breaks, paragraph measures,
CTA shape and color, folio transparency and caption, aligned prose columns,
open guide rows, article rails, dark contrast, and mobile stacking. Material
repairs included increasing the desktop type scale, widening the homepage
measure, replacing pill-shaped CTAs, and removing inherited Markdown margins.
The permitted homepage copy matches the specification; the example caption is
an intentional clarity addition. No material visual mismatch remained in the
reviewed captures beyond the documented brand, chrome, and canonical-copy
deviations.
