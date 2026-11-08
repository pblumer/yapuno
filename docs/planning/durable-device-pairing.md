# Durable guardian-approved device pairing core

**Status:** Backend primitive only. Not a runnable QR onboarding experience and
not approved for real child use. Implements the accepted guardian trust ADR
without replacing the separate identity, device-session, messaging or privacy
review gates.

## Protocol (trusted backend / PWA integration contract)

1. The **authenticated guardian** starts a challenge for a child they may
   manage. The backend returns 256 bits of cryptographically random material,
   suitable for a one-time QR payload; only its SHA-256 digest is persisted.
   Challenges expire after five minutes.
2. The child's enrolling browser generates a new **P-256 signing key**. It
   sends the public SPKI DER key and an ECDSA signature of
   `SHA256("yapuno-pair-claim-v1\\x00" || token)` with its device name.
   The backend verifies proof of possession and retains only the public key
   plus its fingerprint. Never export private keys or treat the QR token as
   a long-term device credential.
3. A **currently authorized guardian** explicitly checks the exact fingerprint
   shown on the child device and approves the specific child/tenant challenge.
   Equal verified guardians may do so. Wrong-tenant or revoked grants deny.
4. The enrolling device obtains a second, fresh 256-bit nonce and signs
   `SHA256("yapuno-pair-finish-v1\\x00" || nonce)` with the same key.
   On successful verification and a fresh guardian authorization check, the
   backend durably marks the challenge consumed and creates a device record
   in the *same file transaction*. Replay or wrong private key fails.
5. Guardians may list or revoke that child's devices. `ActiveDevice` checks
   current revocation status and scope; it does **not** authenticate an HTTP
   request or issue a device session.

## What is implemented

- Package `internal/devicepairing`: `Start`, `Claim`, `Approve`,
  `Nonce`, `Finish`, `ListDevices`, `Revoke` and `ActiveDevice`.
- Bounded pending challenges, cryptographic randomness, hashed QR tokens,
  key-bound completion, persistence via atomic rename and fsync, restrictive
  file mode (0600), expiry, replay detection and tenant/guardian checks.
- Regression tests include success across restart, equal guardians, expired
  tokens, attempted cross-tenant approval, invalid key/signature/fingerprint,
  guardian revocation, concurrent finish and persistent device revocation.
  Go race tests run in CI.

## Mandatory next steps and restrictions

- No QR image, camera scanning, HTTP endpoints or browser key lifecycle yet.
  The backend alone cannot be used as a real registration service.
- Build an authenticated guardian web flow with a session-bound CSRF token
  and a *separate*, strictly bounded unauthenticated child-claim API. The
  claim/finish API must not trust client-supplied tenant or child IDs.
- Require a device-signed proof for **every future device session/request**
  and check current child/tenant grants; a public key record alone is not an
  authentication mechanism.
- Persisted JSON is **single-writer and single-process only**. A shared
  transactional challenge/device store with atomic consume, Fencing/locks
  and revocation propagation is required before horizontal scaling.
- Introduce recovery and lost-device safeguards, end-user fingerprint
  comparison UX, key storage threat modelling, abuse controls, key rotation,
  accessibility and Android/iOS camera-fallback tests.
- Do not claim production readiness, secure private messaging, or support
  use with real child data. The project-wide pilot-readiness gate applies.
