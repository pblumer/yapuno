# ADR-DRAFT: Semantic skin token contract (schema v2)

- **Status:** Proposed
- **Implementation:** First slice landed on a branch: Go contract, validation, CSS serialization, six first-party skins and the family dashboard as first consumer (pending review)
- **Date:** 2026-11-09
- **Deciders:** maintainers

## Context and problem statement
Issue [#16](https://github.com/pblumer/yapuno/issues/16) requires radically different, user-selectable skins across child and guardian surfaces "from day one", and asks for the design-token contract to exist before the first full UI is built. The v1 contract in `internal/skin` carried three colors, a density and a shape. Design exploration for the child chat (four themes: vivid, night, cloud, terminal) showed that themes differ in roughly 30 values: typography, bubble treatment, radius, wallpaper, avatar style and motion as well as color. Components that hard-code colors today (the family dashboard) would have to be rewritten for every new theme.

Constraints from [AGENTS.md](../../AGENTS.md): deny by default, privacy by default (no trackers, no third-party assets), Mobile First Experience and accessibility as delivery gates, Go standard library first, no unsupported safety claims.

## Decision drivers
- A third or tenth theme must be data only, without touching messaging, identity or domain code (#16 acceptance).
- A skin must never be able to change security affordances, consent and approval UI, privacy notices or message status wording.
- Accessibility must hold for every skin by construction, not by review discipline: text contrast, focus visibility, minimum input size, reduced motion.
- No skin may introduce network access, executable code, tracking pixels or untrusted fonts and images.
- The same manifest should later be consumable by native iOS/Android clients.

## Considered options
1. **Free-form theme CSS per skin.** Maximum flexibility; impossible to validate for contrast or exfiltration (`url()`, `@import`, attribute selectors), and lets a skin hide or restyle safety UI.
2. **Semantic token manifest with closed vocabularies and enforced contrast** (proposed). Skins assign values to fixed roles; every value is a `#rrggbb` color or a member of a reviewed enum; validation computes WCAG contrast for every pairing the UI uses.
3. **Separate templates per theme.** Allows different layouts immediately; multiplies every security-relevant template and makes "skins cannot change safety UI" untestable.
4. **Third-party or user-uploaded theme packages.** Out of scope until asset sanitization, CSP and review processes exist.

## Proposed decision outcome
Chosen option (if approved): **option 2**, with layout variants deferred to a later, explicitly enumerated "component slot" extension.

### Contract
- `Definition` schema version **2**: `colors`, `typography`, `shape`, `wallpaper`, `avatar`, `density`, `motion`, plus `id` and a display-only `name`.
- **Color roles (16):** background, surface, text, text_muted, line, accent, on_accent, accent_text, own_bubble, on_own_bubble, other_bubble, on_other_bubble, badge, on_badge, danger_text, focus. `on_*` is the text/icon color on the matching fill; `accent_text` is the accent hue used as text on the background.
- **Closed vocabularies:** fonts `system | jakarta | bricolage | nunito | mono` (mapped to fixed font stacks with system fallbacks); base size 16–20 px; radius `square | soft | round`; bubble `flat | outlined | raised`; wallpaper `none | dots | lines` (generated CSS gradients from validated colors, never `url()`); avatar `initials | figure`; density `comfortable | compact`; motion `calm | standard`.
- **Validation fails closed** (`ErrInvalidSkin`) on unknown versions, ids, vocabulary values, malformed colors, control characters in names and on contrast below **4.5:1** for each of ten text pairings and **3:1** for the focus indicator against the background.
- **Rendering** emits only CSS custom properties (`--yp-*`). Components must reference these variables and must not hard-code colors; a test enforces this for the family dashboard.
- **Reduced motion always wins:** the stylesheet forces `--yp-motion:none` under `prefers-reduced-motion:reduce`, whatever the skin says.
- **Fallback:** `Resolve` returns a vetted fallback for unknown or invalid preferences; an invalid fallback yields an unusable zero value rather than an unchecked skin.
- **Stable ids:** `classic` and `playful` remain valid ids so stored preferences keep working; new ids are `vivid`, `night`, `cloud`, `terminal`.

### Not skinnable (by construction)
Status labels ("sending", "sent", "not sent"), contact-approval and consent UI, privacy and safety notices, report/block entry points, minimum touch target size, focus visibility and reduced-motion behavior. These live in templates and domain code; the manifest has no field that can express them.

### Consequences
- Positive: new themes are data; accessibility minimums are enforced in tests for every first-party skin; skins cannot load remote resources; a single manifest format can serve web and native clients.
- Negative / accepted trade-offs: radically different *layouts* (e.g. bottom vs. side navigation) are not yet expressible; vocabularies must be extended by code review, which slows community theming; WCAG 2.x contrast is a minimum, not proof of readability (APCA and real-user testing may disagree).
- Open questions and review gates:
  - **Fonts:** font families are named in stacks but not shipped. They must be self-hosted (no Google Fonts requests) with verified licenses before use; until then system fonts render.
  - **Preferences:** per-subject persistence (guardian or child, per tenant) and a guardian "Mein Look" picker exist as an engineering preview ([Mein Look](../planning/mein-look.md)); child access, guardian age-appropriate restrictions and deletion/retention are not implemented.
  - **Layout slots:** define an enumerated set of component variants before any theme needs a different layout.
  - **High-contrast mode** and dynamic type beyond 20 px base size need their own rules.
  - **Localization:** display names are English defaults; localized labels (e.g. "Bunt", "Nacht", "Wolke") belong to UI localization, not the manifest.
  - **Evidence:** real-device and child usability testing of the themes is still required (MFE acceptance matrix).

## Links
- Issue [#16](https://github.com/pblumer/yapuno/issues/16), related [#5](https://github.com/pblumer/yapuno/issues/5)
- [Skin contract](../planning/skin-contract.md), [skin rendering](../planning/skin-rendering.md)
- [Mobile First Experience ADR draft](draft-mobile-first-experience.md)
