# ADR-DRAFT: Pairing a child device with a short code the guardian reads off the device

- **Status:** Proposed
- **Implementation:** On a branch: device-initiated requests with a six-digit code, guardian entry of the code, removal of the QR and fingerprint flow (pending review)
- **Date:** 2026-11-22
- **Deciders:** maintainers; security and usability reviewers required

## Context and problem statement
A child has no password and no e-mail address. A device is bound to a child by a key that never leaves the device, and a guardian decides which device that is ([child device sessions ADR draft](draft-child-device-sessions.md), [guardian contact trust ADR draft](draft-guardian-contact-trust.md)).

The first flow had five steps on two devices: the guardian created a QR link, the child's device opened it and was given a name, the guardian compared a 64-character key fingerprint and approved, the device finished. In the first trial on a real installation the product owner found it too complicated. A comparison of 64 characters is also a check that people skip, and a skipped check protects nothing.

## Decision drivers
- As few steps and concepts as possible; nothing to scan, compare or name.
- The guardian's decision must still be tied to exactly the device in their hand.
- A mistake must fail safely: a typo must not bind some other device.
- No new secret that travels in a link, a photo or a message.
- Deny by default, durable before acknowledged, standard library only.

## Considered options
1. **The device shows a short code, the guardian types it** (proposed). Like pairing a television.
2. Keep the QR link, replace the fingerprint by one tap. Fewer changes; a photographed QR code still lets a stranger's device ask first, and the tap confirms a device the guardian cannot tell apart.
3. Keep the flow and only restyle it. Does not address the complaint.
4. The guardian signs in on the child's device and binds it there. One device, no code; puts a guardian session, which can administer the whole family, on the child's device.

## Proposed decision outcome
Chosen option 1 (product owner decision of 2026-11-22).

### Rules
- A device that opens `/pair` creates a non-extractable P-256 key and asks for a code. Nobody is signed in. The server stores the public key and answers with a secret token for the device and a code to show.
- A code is five random digits and a Damm check digit. Every single wrong digit and every swap of two neighbouring digits fails the check, so a typo is never another device's code.
- A code lives five minutes. At most 200 devices wait at once; a waiting device's code is unique among them.
- A signed-in guardian with current device authority for a child enters the code on "Geräte von …". That binds the waiting device with this code to that child. Nothing is bound before.
- The device then proves possession of its key by signing a fresh nonce; only then does a device record exist. The guardian's authority is checked again at that moment.
- A code that fails its check digit costs nothing. A well-formed code that no waiting device carries counts; after five of those in ten minutes a guardian is refused for the rest of the window, so codes cannot be guessed to capture another family's waiting device.
- The device names itself from its browser ("iPhone", "Android-Handy"); nothing is typed. The name is shown in the guardian's device list and is never trusted for anything.
- The QR link, the scanner page, the fingerprint comparison and the vendored QR library are removed.

### Consequences
- Positive: three steps instead of five; nothing to scan or compare; no bearer secret in a link or image; a mistyped code cannot bind a foreign device; about 2700 lines of pairing and QR code removed.
- Negative / accepted trade-offs:
  - **Whoever supplies the code decides which device is bound.** A guardian who types a code that somebody else dictates, on the phone or in a message, binds that person's device as their child. The page says so; nothing enforces it.
  - A code has about 100 000 values. The limits make guessing impractical, they do not make it impossible.
  - Anybody can make a device wait and thereby occupy one of 200 places. The reverse proxy should rate-limit `POST /pair/request`.
  - A guardian can bind a stranger's waiting device to their own child by guessing its code. The stranger's device would then show the wrong child's name; no data of the stranger is exposed.
  - The key is non-extractable but not hardware-backed, as before.
- Open questions and review gates:
  - Whether the confirmation should show something only the real device knows, as a second check against a dictated code.
  - Whether a guardian should name or rename devices.
  - Real-device tests on iOS and Android, including a device in private mode and storage eviction.
  - Evidence that children and guardians complete the flow without help.

## Links
- Issues [#2](https://github.com/pblumer/yapuno/issues/2), [#5](https://github.com/pblumer/yapuno/issues/5)
- [Device pairing](../planning/device-pairing.md)
- [Child device sessions ADR draft](draft-child-device-sessions.md), [guardian contact trust ADR draft](draft-guardian-contact-trust.md)
