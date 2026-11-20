# ADR-DRAFT: Child device sessions through proof of possession of the paired key

- **Status:** Proposed
- **Implementation:** First slice on a branch: challenge-response login, opaque server-side sessions, child entry page (pending review). No messaging, no push, no native apps.
- **Date:** 2026-11-20
- **Deciders:** maintainers

## Context and problem statement
Pairing ([guardian contact trust ADR](draft-guardian-contact-trust.md), issue [#2](https://github.com/pblumer/yapuno/issues/2)) binds a guardian-approved device to a non-extractable P-256 key held in the browser. Until now that binding granted nothing: a paired device had no session, so no child-facing page could exist. Messaging (#4) needs an authenticated child on every read, send and stream.

Children must not manage passwords, phone numbers or email addresses. A guardian must be able to cut off a device immediately. The project forbids inventing cryptography and prefers the Go standard library.

## Decision drivers
- No child-managed secret; the daily routine is "open the app".
- A stolen session must be short-lived and useless for renewal without the device.
- Revocation takes effect on the next request (invariants 1, 2 and 4).
- Reuse what pairing already established instead of adding a second credential.
- Standard primitives only (ECDSA P-256, SHA-256, CSPRNG) from the standard library.
- Honest claims: a browser-held key is not hardware-backed attestation.

## Considered options
1. **Long-lived bearer cookie issued at pairing.** Simplest; a copied cookie is the device until someone notices, and expiry forces re-pairing.
2. **Challenge-response with the paired device key, then a short opaque session** (proposed).
3. **WebAuthn / passkeys.** A reviewed standard with platform authenticators, but needs a maintained server library (new dependency and ADR) or hand-written CBOR/COSE verification (inventing crypto plumbing); synced passkeys are not device-bound; children's devices often lack a configured screen lock.
4. **Sign every request with the device key (no session).** Strongest binding; needs JavaScript on every navigation, cannot protect plain page loads or WebSocket upgrades simply, and has a larger attack surface for replay handling.

## Proposed decision outcome
Chosen option (if approved): **option 2**, keeping WebAuthn as a later evaluation for platforms where it adds real hardware binding.

### Protocol
1. `POST /child/session/challenge {device_id}` → `{nonce}`. The nonce is 256 random bits, valid 60 s, single use, bound to the device id, stored only as a digest. Unknown but well-formed ids receive a nonce too, so the endpoint does not reveal which ids exist. At most 3 pending challenges per device.
2. The device signs `"yapuno-device-login-v1\0" + device_id + "." + nonce` with ECDSA P-256 / SHA-256 using the key stored during pairing.
3. `POST /child/session/login {device_id, nonce, signature}`. The server consumes the nonce on **any** attempt, loads the active device by id, verifies the ASN.1 signature against the paired public key, confirms the child profile exists and sets a session cookie. Every failure returns the same `401`.
4. The session is an opaque 256-bit token in `__Host-yapuno_child` (`Secure`, `HttpOnly`, `SameSite=Strict`, `Path=/`), stored server-side as a digest with tenant, child, device and a separate CSRF token. Lifetime 12 h; at most 5 sessions per device.
5. **Every request** re-checks that the device is still active for the same tenant and child and that the child profile exists. Revocation therefore ends access on the next request.
6. Renewal is the same proof of possession, performed silently by the entry page. Sign-out ends the session; the device stays paired and signs in again with one tap.

The login domain string differs from the pairing domains and the signed message covers the device id, so pairing signatures and challenges for other devices cannot be replayed.

Both JSON endpoints require the exact HTTPS `Origin`, JSON content type, a 4 KiB body, no unknown fields and no trailing data. The authenticated home page carries no script (`default-src 'none'` without `script-src`).

### What this does and does not guarantee
- A copied session cookie works for at most 12 hours and cannot be renewed without the device key.
- The private key is created non-extractable by WebCrypto and never leaves the browser profile. **It is not hardware-backed or attested.** Malware or a compromised browser on the device, or script injected into the origin, can use the key while it runs. Anyone holding the unlocked device is the child as far as Yapuno can tell. A guardian ends that by locking the device on the ["Gerät verloren" page](../planning/device-lost.md); this is server-side revocation, not a remote wipe.
- Clearing site data, private browsing or some OS storage eviction deletes the key; the device must then be paired again.
- A browser profile that pairs several children signs in as the first device the server accepts; switching profiles is not designed yet.

### Consequences
- Positive: no password or contact detail for children; one credential from pairing to login; immediate guardian revocation; short-lived sessions; standard library only.
- Negative / accepted trade-offs: JavaScript is required for login; sessions and challenges live in one process and are lost on restart (devices sign in again silently); no rate limiting yet; origin-wide script injection defeats key isolation, so CSP and the no-third-party-script rule are load-bearing.
- Open questions and review gates:
  - Independent review of this protocol before any real child uses it; compare against WebAuthn where platform authenticators are available.
  - Real-device evidence: iOS Safari Home Screen PWA and Android Chrome for IndexedDB key persistence, storage eviction, cookie lifetime and background resume.
  - Rate limiting and abuse monitoring without logging identifiers; shared session storage for more than one server process.
  - Session lifetime (12 h) and whether guardians can see or end active sessions.
  - Multiple children on one shared device; key rotation and recovery; suspension when a child has no active guardian.
  - WebSocket authentication for realtime messaging must reuse `Authenticate` on upgrade and re-check during the stream.

## Links
- Issues [#2](https://github.com/pblumer/yapuno/issues/2), [#14](https://github.com/pblumer/yapuno/issues/14), [#4](https://github.com/pblumer/yapuno/issues/4), [#5](https://github.com/pblumer/yapuno/issues/5)
- [Browser device pairing](../planning/browser-device-pairing.md), [child device sessions (planning)](../planning/child-device-sessions.md)
- [Guardian contact trust ADR](draft-guardian-contact-trust.md), [Mobile First Experience ADR](draft-mobile-first-experience.md)
