# ADR-DRAFT: Communities and Classrooms trust and membership boundaries

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-11-09
- **Deciders:** Yapuno maintainers

## Context and problem statement
Yapuno's First Hello is a guardian-approved, closed contact network. Families may later want a private class community; a school may eventually operate verified classroom rooms. Simply adding every classmate as an approved contact would create an unsafe graph of direct messaging and would weaken the current tenant boundary. Equally, using a family tenant for a multi-family classroom would mix unrelated guardians' administrative authority.

The capability must work for other opt-in communities (e.g. clubs) without turning Yapuno into a school administration system.

## Decision drivers
- Keep the First Hello family/guardian and mutual-contact model authoritative and unchanged.
- Class membership must never imply a one-to-one contact approval or parental access to private conversations.
- Cross-family resource access must be explicit, resource-scoped, revocable and server-authorized, not a generic cross-tenant bypass.
- Adult, school and teacher claims require real-world verification, not merely an email domain, a name or possession of an invitation.
- Protect child dignity, minimal personal information, and bounded retention.
- Mobile-first, Go-first, deny-by-default and independently reviewed before use by children.

## Considered options
1. **Ordinary group chat as a set of mutually approved personal contacts.** Simple but grants excessive relationships and poorly separates roles; reject.
2. **A generic Community > Cohort > Room model with independent memberships, role grants and optional verified organization.** More complex but explicit trust boundaries; preferred.
3. **One shared family tenant for a whole class.** Reuses tenant tooling but incorrectly mixes unrelated families, custody and administrators; reject.
4. **A school LMS / classroom directory integration first.** Creates unnecessary dependencies, imports sensitive data and blocks private parent-led groups; defer.

## Proposed decision outcome
**Prefer option 2, subject to security, privacy and product review.** This is a design proposal, not authorization to implement or deploy it.

Conceptual entities:
- **Principal**: existing adult or child identity; its home family/tenant authority does not move to a class.
- **Community**: bounded opt-in space; subtype `guardian_organised` or `verified_organisation`. A private group must never display an official school verification badge.
- **Organisation** (optional): verified institutional operator and delegated administrators; no claim of verification from a self-reported name.
- **Cohort**: school class/group with a defined period (e.g. academic year), with no automatic carry-over.
- **Room**: chat or announcement space with explicit capabilities and membership policy.
- **Membership/grant**: server-owned, time-scoped relationship between principal and specific community/cohort/room, with provenance, status, approver, revocation and audit references.
- **Guardian approval**: independent, authenticated authorization for a child to join; co-guardian and contested custody cases fail closed pending policy resolution.
- **Personal contact grant**: continues to be separate from and unaffected by every class grant.

A single guardian may organize a **private community** but cannot impersonate the school, enroll other children without applicable approvals, or thereby gain guardianship. A **verified school community** is a different operating mode requiring validated institutional authority, separate responsibility, explicit teacher/staff role grants and legal review. Ordinary guardians do not inherit school-administrator rights.

### Authorization and privacy invariants
1. Every read, write, roster view, invitation, history access, websocket subscription, notification and media request checks current principal, device, community, room, membership, role and policy **on the server**. Client-declared organization or cohort IDs are not authority.
2. A community-scoped grant is a narrow exception to cross-family isolation for that resource only. No generic cross-tenant reads, home-family traversal, user search or contact creation.
3. Membership **never** creates private one-to-one contact grants, friend recommendations, public discoverability or blanket access to private messages.
4. Children cannot create unrestricted invitation links or self-join from guessed codes. Invitations expire and are bound to an approval workflow, not a bearer-only entitlement.
5. Staff roles are verified independently. Teachers cannot initiate unsolicited private child messages through classroom membership; any exceptional one-to-one communication requires a separately reviewed protocol.
6. Admins can manage authorized rosters/rooms, not silently view members' other chats. Moderators only see policy-approved content or narrowly scoped reports; no general surveillance or indiscriminate scanning is approved.
7. Revocation/expiry immediately blocks further sends, reads, history, media, websocket delivery and push fanout; test cache and race windows. Roster visibility is minimized.
8. Closing a cohort or academic year revokes active access without silently preserving membership; retention and lawful incident evidence holds remain explicitly scoped and reviewed.
9. No bulk import of student identities, official directory or automated school verification before an approved data-protection and legal design.

## Consequences
- Positive: enables private family-organized classrooms first and verified schools later, with reusable rooms for clubs.
- Positive: provides explicit denial test boundaries without broadening First Hello's contact graph.
- Trade-off: additional entity relationships, lifecycle, audit and policy complexity.
- Trade-off: institution verification and guardian authority are operational processes, not purely technical checks.
- Open questions: valid guardian approval rule with joint custody; school controller/processor agreements and jurisdiction; minimal roster fields; teacher emergency workflows; age-specific moderation and child rights; membership offboarding; storage boundary and future scalability.

## Review and implementation gates
- **CC0**: threat-model update, role/capability matrix, authorisation decision table, data minimisation, retention and failure-mode designs, reviewed ADR.
- **CC1**: only after secure First Hello authorization and reporting foundations; private guardian-organized groups with negative access/revocation tests and MFE validation.
- **CC2**: requires separately verified school authority, child-privacy/legal assessment (including whether a DPIA is needed), operating agreements and an independent safety review.
- **No real child pilot** before the existing mandatory [pre-pilot gate](https://github.com/pblumer/yapuno/issues/8).

## Links
- [Capability plan](../planning/communities-classrooms.md)
- [Tracking issue #36](https://github.com/pblumer/yapuno/issues/36)
- [Guardian/contact ADR](draft-guardian-contact-trust.md)
- [Roadmap](../../ROADMAP.md)
