# Yapuno skin contract (schema v2)

Skins are declarative appearance data, never executable plugins. Decision record: [ADR draft: semantic skin token contract](../adr/draft-skin-token-contract.md).

A skin assigns values to fixed semantic roles:

| Group | Values |
|---|---|
| Colors (16 roles) | background, surface, text, text_muted, line, accent, on_accent, accent_text, own/other bubble and their text, badge, on_badge, danger_text, focus — `#rrggbb` only |
| Typography | heading and body font from `system`, `jakarta`, `bricolage`, `nunito`, `mono`; base size 16–20 px |
| Shape | radius `square`/`soft`/`round`; bubble `flat`/`outlined`/`raised` |
| Wallpaper | `none`, `dots`, `lines` (generated gradients, never `url()`) |
| Avatar | `initials`, `figure` |
| Density / motion | `comfortable`/`compact`; `calm`/`standard` (reduced motion always wins) |

`Validate` fails closed on unknown versions or vocabulary values, malformed colors, and contrast below 4.5:1 for every text pairing (3:1 for the focus indicator). Invalid preferences resolve to a vetted fallback.

All users, including children, retain full browser/PWA functionality. Future iOS/Android clients should consume the same semantic tokens.

Never permit a skin to modify authorization, consent, contact approval, privacy UI obligations or message status wording.

## Next slices
- Self-hosted, license-checked font files (until then system fonts render)
- Guardian age-appropriate restrictions and child access to the picker (guardian preview of persisted preferences and the "Mein Look" picker: see [Mein Look](mein-look.md))
- Enumerated component slots for layout variants
- High-contrast mode, dynamic type beyond 20 px, responsive visual regression
- Manifest import/export and schema migration tooling

Related #16.
