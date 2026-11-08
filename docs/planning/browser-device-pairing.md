# Browser device pairing: child key and guardian approval

**Status:** End-to-end QR-or-link browser engineering preview for synthetic
child profiles. The guardian QR image is created locally; children can scan
using the native phone camera or the optional in-browser scanner. Browser camera
recognition depends on `BarcodeDetector` support. Production device sessions
and real-device compatibility validation remain **not implemented**. Never use
this with real children's data.

## What works in the preview

1. A verified guardian logs into the existing HTTPS Yapuno web preview, opens
   **Familie → Geräte verwalten**, and chooses **Pairing-Code erzeugen**.
2. The page shows a locally generated **QR code**, copy/share buttons and a
   unique, five-minute invitation URL of the form
   `https://configured-yapuno-host/pair#<one-time-token>`. No third-party QR
   service receives the link. The secret sits in the URL *fragment*, not in
   HTTP request paths or queries; the child page strips it from the browser
   address bar immediately on load.
3. On the **child device**, scan the parent QR with the native phone camera
   (recommended fallback) or open `/pair/scan` and choose **Kamera starten**.
   Where `BarcodeDetector` is unavailable, the page explains how to use the
   phone's normal camera or paste a link shared by the guardian. The scanner
   accepts **only same-origin** Yapuno `/pair#...` URLs. The child then
   selects a device name and taps **Gerät vorbereiten**. Browser
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

- **Implemented in code:** offline QR creation (vendored Kazuhiko Arase
  QR generator, MIT), accessible scanner page, optional in-browser camera
  detection using `BarcodeDetector`, native camera app and shared-link
  fallbacks. **Still required:** test the QR scan path on actual iOS Safari,
  Android Chrome, and camera-denied browsers; ensure accessibility and
  reliable sharing, including when no `BarcodeDetector` is present.
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
CSRF and cross-child guardian approval, fingerprint verification, replay and
persistence, locally generated QR SVG matrix structure, and rejection of
phishing/cross-origin/token-query scan links. **Tests are not substitutes for
mobile-browser E2E validation.**
