# Browser device pairing: child key and guardian approval

**Status:** End-to-end browser **link-based** engineering preview for synthetic
child profiles. A QR image, camera scanning and production device sessions are
still **not implemented**. Never use this with real children's data.

## What works in the preview

1. A verified guardian logs into the existing HTTPS Yapuno web preview, opens
   **Familie → Geräte verwalten**, and chooses **Pairing-Code erzeugen**.
2. The page shows a unique, five-minute invitation URL of the form
   `https://configured-yapuno-host/pair#<one-time-token>`. The secret sits in
   the URL *fragment*, not in the HTTP request path or query; the child page
   strips it from the browser address bar immediately on load.
3. Open the link on the **child device** using a current browser over HTTPS.
   The child selects a device name and taps **Gerät vorbereiten**. Browser
   WebCrypto generates a **non-extractable P-256 private key**, stores it in
   IndexedDB for later use, and sends only the public SPKI key plus an ECDSA
   claim signature. The backend stores the fingerprint, not the raw token.
4. The guardian returns to their invitation page and chooses
   **Geräteanfrage prüfen**. The page displays the claimed device name and
   the full 64-digit public-key SHA-256 fingerprint. Both screens must display
   the **same fingerprint**. The guardian explicitly checks confirmation and
   selects **Dieses Gerät freigeben**.
5. The child page polls the challenge status using a token-bound, bounded
   same-origin JSON POST, receives a second nonce only after guardian
   approval, signs it with the same private key and completes the durable,
   one-time device registration.
6. The guardian can see the device in the list and revoke it. The child page
   explicitly says that chat and child login are not yet available.

Parent API (`internal/familyweb`):
- `POST /family/devices/status`: needs verified guardian session, session CSRF
  token, child ID and raw invitation token in the **request body**.
- `POST /family/devices/approve`: also needs the full claimed fingerprint
  and an explicit confirmation checkbox; the backend checks live child/tenant
  grants and the exact fingerprint before storing approval.

Child API (`internal/pairingweb`):
- `GET /pair` and `GET /pair/app.js`: static mobile-first HTML/JS with
  restrictive CSP, no remote JS, and no user content in the script.
- `POST /pair/claim`: one-time 256-bit invitation token, device name,
  public SPKI DER, ECDSA ASN.1 signature of
  SHA-256(`yapuno-pair-claim-v1\\0` + token).
- `POST /pair/nonce`: returns waiting (202) or approved nonce (200).
- `POST /pair/finish`: valid key-bound ECDSA signature of
  SHA-256(`yapuno-pair-finish-v1\\0` + nonce); returns a device ID.

Child requests use JSON bodies (never token-bearing GET URLs), have 4 KiB
request limits, require the canonical HTTPS request Origin and receive no CORS
permission. HTTP responses are no-store and no-referrer. JS fetches omit
cookies. The token does **not** authenticate a child for chat or other APIs.

## Required before a real pilot

- Render a local QR code for the link and implement camera scanning with an
  accessible, guardian-assisted fallback on iOS Safari and Android Chrome.
  Do not rely on remote QR services: the code is a temporary credential.
- Test WebCrypto and IndexedDB on actual phones, private browsing, restored
  tabs and shared family devices; implement safe key recovery, replacement,
  logout, and device-session authentication with every request authorized.
- Bind and expire browser sessions on device revocation; enforce messaging
  membership and contact approvals separately.
- Add shared transactional challenge/session storage and synchronized
  revocation for horizontal scale-out; the current file store is single writer.
- Add IP/network-level abuse controls, rate limits, monitoring without
  sensitive tokens, consent/recovery workflows, and full independent
  child-safety/privacy/security review.

Automated tests cover request-origin rejection, invalid and forged keys,
CSRF and cross-child guardian approval, correct fingerprint, approval before
finish, replay detection and persistence. **Tests are not substitutes for
mobile-browser E2E validation.**
