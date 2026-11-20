# ADR-DRAFT: Cross-family contacts through one-time guardian invitations

- **Status:** Proposed
- **Implementation:** First slice on a branch: tenant-qualified contact predicate, durable contact store and guardian pages (pending review). No messaging, child sessions or child-facing UI.
- **Date:** 2026-11-20
- **Deciders:** maintainers

## Context and problem statement
The accepted [guardian contact trust ADR](draft-guardian-contact-trust.md) requires a closed, deny-by-default contact graph with mutual guardian approval and lists the **invitation protocol** as an open question. Issue [#3](https://github.com/pblumer/yapuno/issues/3) asks for an explicitly defined pairwise consent model.

Two facts collide in the existing scaffold:

- A tenant is a **family** (vision, architecture, family store).
- The contact predicate (`authz.ContactGrant`, `CanContact`) carried a single `TenantID`, so a contact could only exist **inside one tenant**. First Hello is two children of two families; under that predicate they could never be contacts.

A decision is needed on how a relationship crosses the family boundary without weakening it, and on how two families find each other without a directory.

## Decision drivers
- No public directory, search, discoverability or child self-invitation (invariant 3).
- Both families explicitly approve the **specific** counterpart; one-sided approval grants nothing.
- Tenant, guardian and child are never inferred from client input; authority is re-checked at the moment of action (invariants 1 and 2).
- Revocation by either family takes effect on the next check.
- Minimum data: the other family learns a child's display name and nothing else.
- Usable on a phone without both families being in the same room.

## Considered options
1. **Shared tenant per friendship or class.** Reuses same-tenant checks, but mixes unrelated families' administrative authority; already rejected for classrooms.
2. **Directory or search by name, email or phone.** Convenient; violates the no-discovery invariant and leaks who uses the service.
3. **One-time invitation code, redeemed by the other family, then confirmed by the inviting family** (proposed). The contact record is the only cross-family relationship and names exactly two tenant-qualified children.
4. **Invitation whose redemption alone activates the contact.** One step fewer; anyone who obtains the code becomes a contact without the inviting family ever seeing who redeemed it.
5. **Guardian-to-guardian link first, then children.** Models adult relationships explicitly; larger scope and depends on adult participants (#12).

## Proposed decision outcome
Chosen option (if approved): **option 3**.

### Model
- `authz.Participant` is tenant-qualified (`TenantID`, `ID`, `Kind`). `authz.ContactGrant` names exactly two participants, who may belong to different tenants, plus both approvals and an active flag. `CanContact` matches the exact pair in either direction and denies everything else. **This replaces the single-tenant `ContactGrant`.**
- A contact grants nothing beyond that pair: no tenant-wide access, no access to siblings, no guardian authority, no discovery of further contacts.
- Siblings need an explicit contact too; nothing is implied by sharing a family.

### Protocol
1. **Invite.** A guardian with current contact authority for child A creates a code: 128 random bits, base32, shown once, stored only as a domain-separated SHA-256 digest. Valid 24 hours, single use, at most 5 open codes per child.
2. **Redeem.** A guardian with current contact authority for child B enters the code for B. This is family B's approval. The code is consumed and a pending request is recorded. Rejected attempts (no authority, own child, existing relationship) do not consume the code. A second redeemer is told the code was already used, which exposes interception.
3. **Confirm.** A guardian of child A sees B's display name and explicitly confirms. Any equal guardian of A may confirm ([equal guardian rights](guardian-equal-rights.md)); the redeeming family cannot. Unconfirmed requests expire after 7 days.
4. **End.** A guardian of either child can decline a pending request or remove an active contact at any time. The record is deleted; reconnecting needs a new code and both approvals again.

`contacts.Store.Allowed(a, b)` is the authoritative relationship check for later messaging: the exact pair must be active and both child profiles must still exist.

### Not decided here
- `authz.CanSendMessage` still requires one tenant for sender and recipient and therefore denies cross-family messages. It stays fail-closed until messaging (#4) revises it together with device and session checks.
- Adult participants and adult–child contacts (#12) reuse `Participant` and `ContactGrant` but need their own consent rules.
- Group membership and circles.

### Consequences
- Positive: cross-family contact without relaxing tenant isolation; every step is server-authorized and testable; no directory; guardians see only a display name.
- Negative / accepted trade-offs:
  - Three steps across two families instead of one tap.
  - A display name does not prove identity. If a stranger intercepts and redeems a code first, the inviting family sees a request under a name the stranger chose. Mitigations: single use (the intended family is told the code was used), explicit confirmation with a prompt to check with the other family, either side can remove the contact. There is no cryptographic binding to the intended family.
  - One guardian per side is sufficient. A second guardian of the same child is not asked and is not yet notified.
  - A 24-hour code is longer-lived than the 5-minute pairing challenge because families are usually not together; the 128-bit code makes guessing infeasible, but there is no rate limiting or lockout yet.
- Open questions and review gates:
  - Notification and audit trail for co-guardians; disputed custody.
  - Verification phrase or in-person QR as optional stronger binding.
  - Blocking and reporting by children, and how a child-initiated block relates to the guardian-approved contact.
  - Retention: deleted records leave no history; incident evidence rules belong to #7.
  - Suspension when a child has no active guardian left.
  - Rate limiting of redeem attempts; shared transactional storage for more than one server process.
  - Threat model and privacy review before any real child uses it.

## Links
- Issues [#3](https://github.com/pblumer/yapuno/issues/3), [#12](https://github.com/pblumer/yapuno/issues/12), [#4](https://github.com/pblumer/yapuno/issues/4)
- [Guardian contact trust ADR](draft-guardian-contact-trust.md), [equal guardian rights](guardian-equal-rights.md), [Communities & Classrooms draft](draft-communities-classrooms.md)
- [Guardian contact approval (planning)](../planning/guardian-contact-approval.md)
