# "Mein Look": theme and figure picker

**Status:** engineering preview for synthetic profiles. Guardians and signed-in children each choose their own look.

- **Guardians** open **Familie → Mein Look** (`/family/look`).
- **Children** tap their figure or **Mein Look ändern** on their start page (`/child/look`), once their device is signed in ([child device sessions](child-device-sessions.md)).

Both choose:

- a **theme** from the first-party skin catalog ([skin contract](skin-contract.md)),
- a **figure** (shape: Kugel, Kissen, Blume, Geist) and its **color** (Lavendel, Limette, Mint, Pfirsich, Weiss).

The picker is one shared renderer (`internal/lookpage`) used by both areas. It works without JavaScript. A CSS `:has()` preview updates theme, shape and color live; browsers without `:has()` show the saved look until the form is submitted. Saving redirects to a confirmation (`role="status"`). The guardian family page and the child's start and chat pages then use the saved theme. `?skin=<id>` on `/family` still previews a skin for one request without saving it.

## Who sees what

- A **theme** is private: it only changes the pages of the person who chose it.
- A **figure** is the profile picture. A child's figure is shown to that child and to **mutually approved contacts** (in their crew list and chat header), and to nobody else. It disappears for a contact when the contact ends. There is no directory in which figures could be browsed.
- A guardian's figure is shown only to that guardian.
- The other child's identifiers are never sent to the browser; the server resolves the figure from the stored contact.

## Security and privacy

- **Identity from the session only.** The stored subject is `guardian` or `child` + tenant + id from the verified server-side session; identity fields in the form are ignored (tested for both areas). Guardian revocation and child device revocation apply on the next request.
- **CSRF**, form-only content type, 4 KiB body limit, exactly one value per field; unknown skins, shapes or colors are rejected (`400`) and nothing is written.
- **Closed vocabularies, no uploads.** A figure is an enum shape and enum color rendered as fixed inline SVG (no links, scripts, styles or external references). There are no photos, so no image moderation, EXIF or face data.
- **Storage:** optional private file (`YAPUNO_APPEARANCE_FILE` / `-appearance`), mode `0600`, atomic replace, fails closed on corrupt content, unknown fields, invalid subjects or insecure permissions. A retired skin id falls back to the default skin on read instead of blocking startup.
- **Data stored per subject:** skin id, figure shape, figure color. Nothing else.
- **Not yet implemented:** deletion when a guardian or child is removed, export, retention rules (#7).

## Open product decisions

- **Guardian limits:** children currently choose freely among all validated themes. Issue #16 asks for optional guardian controls for age-appropriate choices; none exist yet.
- **Deletion:** a stored look is not removed when a child or guardian is removed (#7).
- Localized labels beyond German.

## MFE evidence (headless Chromium, not a physical device)

- 320, 375 and 430 CSS px: no horizontal overflow, no mid-word wrapping of option labels; every option label is at least 44 px tall.
- Radio inputs stay keyboard-focusable; focus is shown on the visible label.
- Reduced motion disables transitions.
- Child flow in headless Chromium at 320 px through the real server: contact's figure in the crew, own figure opens the picker, live preview without script, save, new theme on start and chat pages, look kept after reload; no overflow, 44 px targets.
- **Not verified:** physical iPhone/Android, screen readers, large text settings, landscape.

Related #16, #5, #2.
