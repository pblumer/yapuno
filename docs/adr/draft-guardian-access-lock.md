# ADR-DRAFT: Emergency lock of a guardian's own access

- **Status:** Proposed
- **Implementation:** First slice on a branch: sign-out, sign-out everywhere, durable self-lock with notices to co-guardians, operator-only release (pending review)
- **Date:** 2026-11-20
- **Deciders:** maintainers

## Context and problem statement
Guardians sign in with a one-time link sent by e-mail ([passwordless e-mail ADR draft](draft-guardian-passwordless-email.md)). A guardian session can pair child devices, approve contacts and lock devices. Until now a guardian could not even sign out, and nothing could be done about a lost guardian device except waiting for the 30-minute session to expire.

The mailbox is the credential. If a lost device also gives access to the mailbox, ending sessions is useless: the holder requests a new link. Issue [#2](https://github.com/pblumer/yapuno/issues/2) asks for revocation; the accepted [equal guardian rights decision](guardian-equal-rights.md) requires that high-risk operations come with notification and anti-lockout safeguards and never create a primary guardian.

## Decision drivers
- Cut off a holder of device and mailbox, not only of a session cookie.
- No guardian may gain power over another guardian of the same child.
- Deny by default; durable before acknowledged; no new third party; standard library only.
- Do not reveal to an outsider whether an address is locked.
- The second guardian is the only person who can inspect the family while the first is locked out.

## Considered options
1. **Sign-out everywhere only.** Simple, in memory. Does not stop a new sign-in link, so it fails exactly in the case that matters.
2. **Self-lock, released by the operator** (proposed). The guardian locks their own access from any session; sign-in links stop; only the operator lifts the lock after out-of-band identification.
3. **Lock by a co-guardian.** Works even when the affected guardian cannot reach a session. Gives each guardian the power to lock the other out, which is a weapon in separation conflicts and contradicts the anti-lockout requirement of the equal-rights decision unless paired with an independent release process that does not exist yet.
4. **Self-service unlock by e-mail or after a waiting period.** Convenient; an unlock the same mailbox can perform protects against nothing, and a timer only delays the holder.
5. **A second factor for guardians (passkey, TOTP).** The right long-term answer to "mailbox is the credential"; a larger change with its own recovery problem, not a replacement for an emergency stop.

## Proposed decision outcome
Chosen option (if approved): **option 2**, together with option 1 for the harmless cases. Product owner decisions of 2026-11-20: only the guardian themself can lock; other guardians of the same children are notified by e-mail only when a lock happens.

### Rules
- All three actions act on the guardian of the verified session. No request field can name a guardian.
- The lock is written durably before sessions end or mail is sent. It is tenant-qualified and idempotent.
- A locked guardian resolves neither for sign-in nor for an existing session; the directory checks the lock on every lookup. The login response does not differ from the one for an unknown address.
- The lock store fails closed: if it cannot be read, every guardian is treated as locked.
- Release is an operator action on the private lock file. The application has no unlock path.
- Notices go to the other active, unlocked guardians who share a child with the locking guardian. They name nobody and contain no address and no link. Mail is best effort and never a condition of the lock.
- The guardian may lock all devices of their children in the same step, because a session holder could have paired a device.

### Consequences
- Positive: the lost-mailbox case has a remedy inside the product; no guardian gains authority over another; co-guardians learn that they should review the family; an unreadable store cannot silently unlock anyone.
- Negative / accepted trade-offs: a guardian who has lost every session cannot lock; a session holder can lock the guardian out (fail-safe, but operator work); a sole guardian's family is unmanaged while locked; release depends on an operator process that is not yet defined; a broken lock file takes all guardians offline until repaired.
- Open questions and review gates:
  - **Operator release process:** how the operator identifies a guardian before lifting a lock, and who the operator is for a hosted service.
  - **Lock without a session:** whether an operator hotline or a pre-registered recovery code should allow locking when nothing is signed in. Lock by co-guardian stays rejected until an independent, symmetric release process exists.
  - **Second factor** for guardians and its recovery.
  - **Further notices:** sign-ins from a new browser, paired devices, approved contacts. Each makes guardians visible to each other and needs its own decision.
  - **Audit trail** of locks and releases, and its retention.
  - **Legal review** of notifying one guardian about another in families with custody restrictions.

## Links
- Issues [#2](https://github.com/pblumer/yapuno/issues/2), [#8](https://github.com/pblumer/yapuno/issues/8), [#14](https://github.com/pblumer/yapuno/issues/14)
- [Guardian account safety](../planning/guardian-account-safety.md), ["Gerät verloren"](../planning/device-lost.md)
- [Equal guardian administration rights](guardian-equal-rights.md), [passwordless e-mail ADR draft](draft-guardian-passwordless-email.md)
