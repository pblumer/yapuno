# "Mein Look": theme and figure picker

**Status:** guardian engineering preview. Child use waits for child device sessions (#2).

Authenticated guardians open **Familie → Mein Look** (`/family/look`) and choose:

- a **theme** from the first-party skin catalog ([skin contract](skin-contract.md)),
- a **figure** (shape: Kugel, Kissen, Blume, Geist) and its **color** (Lavendel, Limette, Mint, Pfirsich, Weiss).

The page works without JavaScript. A CSS `:has()` preview updates theme, shape and color live; browsers without `:has()` show the saved look until the form is submitted. Saving redirects to a confirmation (`role="status"`). The family page then uses the saved theme and shows the figure in its header, linked to "Mein Look". `?skin=<id>` on `/family` still previews a skin for one request without saving it.

## Security and privacy

- **Identity from the session only.** The stored subject is `guardian` + tenant + guardian id from the verified server-side session; identity fields in the form are ignored. Live guardian revocation applies (`verifiedSessions`).
- **CSRF**, form-only content type, 4 KiB body limit, exactly one value per field; unknown skins, shapes or colors are rejected (`400`) and nothing is written.
- **Closed vocabularies, no uploads.** A figure is an enum shape and enum color rendered as fixed inline SVG (no links, scripts, styles or external references). There are no photos, so no image moderation, EXIF or face data.
- **Storage:** optional private file (`YAPUNO_APPEARANCE_FILE` / `-appearance`), mode `0600`, atomic replace, fails closed on corrupt content, unknown fields, invalid subjects or insecure permissions. A retired skin id falls back to the default skin on read instead of blocking startup.
- **Data stored per subject:** skin id, figure shape, figure color. Nothing else.
- **Not yet implemented:** deletion when a guardian or child is removed, export, retention rules (#7).

## Designed for children, not yet available to them

`internal/appearance` keys preferences by subject kind (`guardian` or `child`) so the same store, validation and figure rendering serve children once child device sessions exist. Open product decisions before enabling it for children:

- whether guardians may restrict theme choices for younger children (issue #16 asks for optional guardian controls; the current recommendation is free choice within the validated catalog);
- where a child's figure becomes visible: only to mutually approved contacts, never in a public directory;
- localized labels beyond German.

## MFE evidence (headless Chromium, not a physical device)

- 320, 375 and 430 CSS px: no horizontal overflow, no mid-word wrapping of option labels; every option label is at least 44 px tall.
- Radio inputs stay keyboard-focusable; focus is shown on the visible label.
- Reduced motion disables transitions.
- **Not verified:** physical iPhone/Android, screen readers, large text settings, landscape.

Related #16, #5, #2.
