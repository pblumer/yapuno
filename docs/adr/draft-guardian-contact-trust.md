# ADR-DRAFT: Guardian-managed devices and mutually approved contacts

- **Status:** Accepted (2026-11-04; awaiting ADR numbering)
- **Implementation:** Partial (durable pairing core and authenticated guardian device pages; child-side QR/approval completion and device sessions open)
- **Date:** 2026-11-03
- **Deciders:** Yapuno maintainers

## Context and problem statement
Children should not manage passwords or invite arbitrary internet strangers. Merely hiding invitation UI or checking a single bearer token is insufficient to prevent unauthorized messaging.

## Decision drivers
- No phone number or email required for child accounts
- Explicit authorization, even for media, history, notifications and websocket feeds
- Guardian-approved enrollment and mutually approved contact relationships
- Immediate revocation and recovery

## Considered options
1. Guardians enroll children and approve short-lived QR device pairing; backend checks explicit mutual contact and tenant grants for each action.
2. Children register publicly, then use optional parental restrictions.
3. Trust possession of QR or a client-declared tenant/contact ID.

## Accepted decision outcome
Prefer **option 1**: closed, deny-by-default contact graph and server-backed guardian/device permissions. Evaluate WebAuthn/passkeys for authentication, understanding passkey synchronization; pairing requires independent guardian authorization and a one-time server-side challenge.

### Consequences
- Positive: understandable, testable contact boundaries without a global directory.
- Negative: significant complexity in joint guardian authority, lost devices and family disputes.
- Open questions: co-guardians and custody, invitation protocol, threat model for compromise, key binding per platform.

## Links
- [Issue #2](https://github.com/pblumer/yapuno/issues/2)
- [Issue #3](https://github.com/pblumer/yapuno/issues/3)
