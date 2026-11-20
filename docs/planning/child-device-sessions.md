# Child device sessions — browser preview

**Status:** engineering preview for synthetic child profiles. A paired device can sign in without a password and sees a minimal home page with the child's name, figure and approved contacts. With the message store enabled, each contact opens a chat ([messaging preview](messaging-preview.md)). Decision record: [ADR draft: child device sessions](../adr/draft-child-device-sessions.md).

## Enable

Child pages are mounted whenever device pairing is enabled (`YAPUNO_PAIRING_FILE`, see [guardian device management](guardian-device-management.md)). The crew list needs `YAPUNO_CONTACTS_FILE`; the child's skin and figure need `YAPUNO_APPEARANCE_FILE`. Without pairing, `/child` does not exist.

## Flow

1. A guardian pairs the child's device as before. The final pairing screen now offers **Yapuno öffnen**.
2. `/child` shows a login shell without any child data. Its script finds the device key stored during pairing, requests a challenge, signs it and receives a session cookie.
3. The page reloads into the home page: greeting, figure, **Deine Crew** (mutually approved contacts only) and a sign-out button. The home page contains no script.
4. When the session ends (12 hours, server restart), the shell signs in again silently. After an explicit sign-out it waits for **Wieder anmelden**.
5. When a guardian revokes the device, the next request falls back to the shell, which reports that the device is no longer approved.

## Security properties (tested)

- Login requires a fresh, single-use, device-bound challenge signed by the paired key. Rejected: other keys, another paired device's key, signatures over another nonce, pairing-domain signatures, messages not covering the device id, challenges of another device, replays, expired challenges, self-chosen nonces, unknown and malformed device ids, revoked devices, removed child profiles. A failed attempt burns the challenge. All failures return the same response.
- Session cookie: `__Host-` prefix, `Secure`, `HttpOnly`, `SameSite=Strict`. Exactly one well-formed cookie is accepted; a guardian cookie is not a child session and vice versa.
- Every request re-checks device approval and child existence; sessions expire; sign-out ends only that session and needs that session's CSRF token.
- The home page shows only the authenticated child's own active contacts by display name: no pending requests, no other family's identifiers, no device id, no session secret.
- JSON endpoints reject cross-site and non-HTTPS origins, other content types, unknown fields, trailing data and oversized bodies.
- 19 session-manager mutations and 9 web mutations (for example signature check removed, nonce reusable, revocation not re-checked, CSRF off, origin check off) are each detected by the tests.

## Browser evidence (headless Chromium 320 px, not a physical device)

One scripted run through the real server over TLS with two browser contexts (guardian and child), 17 checks passed: pairing with matching fingerprints, key login from IndexedDB, cookie attributes as seen by the browser, script cannot read the cookie, **the private key cannot be exported**, reload keeps the session, sign-out stays signed out, one-tap sign-in, a lost cookie stops instead of reloading in a loop, silent renewal, loss of access after guardian revocation, no console errors, no horizontal overflow and 44 px targets on the home page.

A unit test with 300 random signatures checks that the browser's IEEE-P1363 signatures convert to DER accepted by a strict verifier.

**Not verified:** physical iPhone/Android, Home Screen PWA storage persistence and eviction, screen readers, large text, landscape, slow or interrupted networks.

## Limitations

- The device key is non-extractable but **not hardware-backed**; see the ADR for what that means.
- Sessions and challenges are in memory in one process; no rate limiting.
- The login shell uses fixed colors because no child is known yet; the home page uses skin tokens.
- Several children paired in one browser profile, key rotation, recovery and guardian-visible session lists are not implemented.
- Block/report is a separate step. Children choose their own look in ["Mein Look"](mein-look.md).

Related #2, #14, #4, #5.
