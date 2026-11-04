# Yapuno skin contract (first implementation slice)

Skins are declarative appearance data, never executable plugins. This initial Go contract validates version, stable ID, named palette tokens, density and shape; invalid user preferences resolve to a vetted fallback.

All users, including children, retain full browser/PWA functionality. Future iOS/Android clients should consume the same semantic tokens and presentation contracts.

## Next slices
- CSS custom property mapping and semantic component slots for radically different layouts
- Two complete first-party skins, mobile-first and accessible
- Per-participant persisted preferences, guardian age-appropriate restrictions and previews
- Contrast testing, reduced-motion, font scaling, CSP and trusted asset pipeline
- Schema migrations and skin version compatibility

Never permit a skin to modify authorization, consent, contact approval, or privacy UI obligations. This slice does not implement UI, persistence, CSS or skin distribution. Related #16.
