# Communities & Classrooms — capability proposal

**Status:** Proposed future capability; **not implemented**. Tracking: [#36](https://github.com/pblumer/yapuno/issues/36). Architecture decision: [proposed ADR](../adr/draft-communities-classrooms.md).

## Product intent
Enable a safe, opt-in class community with separated spaces for children, guardians and, eventually, verified educators. Extend the model later to clubs, teams and camps, without a public people directory or a learning-management system. **First Hello remains the priority.**

### Two operating modes
| Mode | Who starts it? | Verification and scope | Release order |
| --- | --- | --- | --- |
| Private class community | Existing authenticated guardian | Guardian can propose a class label but **not** claim school endorsement; other families opt in individually; guardian authority is only over their own children | CC1 |
| Official school community | Verified school/institutional administrator | Independent organization and educator verification, delegated school roles, separate policy, retention and legal agreements | CC2 |

A private class named “5b – Example School” is **not** a school-verified class. A future official-badge workflow must prevent impersonation.

### Example spaces (all opt-in/policy-controlled)
- **Classroom chat:** approved child members only, age-appropriate moderation/reporting; classmates do not become direct contacts.
- **Announcements:** authorized organizers/educators publish, recipients can read; audience, replies and attachments are explicit policies.
- **Guardian room:** approved guardian participants, with no inferred access to children's private conversations.
- **Activities and games:** future optional features; gaming time quotas continue to be governed independently by each child’s guardian policy.
- No whole-school directory, student search, school grades, attendance, assignments, sensitive pupil records or emergency-contact system in scope.

## Conceptual authorization model
```text
Principal (adult/child) --belongs to--> Home family/tenant
     |                                       |
     +-- explicit, time-bound membership ---> Community
                                             |-- optional verified Organisation
                                             +-- Cohort (class, school year)
                                                  +-- Room (chat/announcements/guardian)
                                                       +-- scoped role/capability grants

Personal contact approval = independent relation; NEVER inherited from membership.
```

- Membership must carry server-verifiable origin, status, issuer/approvals, effective period, revocation metadata and applicable policy version.
- Family guardianship and device authorization remain authoritative in home-family scope. A community resource grant does not permit cross-family administration.
- Every room membership and capability is assessed server-side. Do not rely on cosmetic client checks, an email domain, teacher claim or a guessable invite code.
- Suggested roles (not finalized): `child_member`, `guardian_member`, `community_organizer`, `verified_educator`, `school_admin`, `safety_reviewer`. Roles and capabilities require independent, least-privilege grants. No role gets unrestricted transcript access.
- Membership roster should disclose the minimum information necessary for safe group participation, never a child’s address, email, guardian contact details, or family graph.

## Illustrative permission matrix (proposal, deny by default)
| Action | Child member | Guardian member | Private organizer | Verified educator | School admin |
| --- | --- | --- | --- | --- | --- |
| Read/write approved child chat | Only if explicit room grant | No by default | No by default | No by default | No by default |
| Read guardian room | No | With room grant | With room grant | No by default | No by default |
| Publish announcements | No by default | No by default | With scoped grant | With scoped grant | With scoped grant |
| Invite a child | No | Request only; own child approval separately | Initiate approval workflow | Only if delegated | Only if delegated |
| Approve child enrollment | No | For own child when policy permits | Never for unrelated children | Never as substitute for guardian | Never as substitute for guardian |
| Create or grant direct-message contact | Separate mutual contact flow only | Separate mutual contact flow only | No via class role | No via class role | No via class role |
| View private chat outside community | No | No | No | No | No |
| Inspect incident evidence | No | No by default | No by default | Only via scoped safety process | Only via scoped safety process |

A room can further restrict all entries. Report handling is distinct from casual administrator powers.

## Membership, governance and lifecycle
1. Authenticated organizer creates a private space with an unverified descriptive label (not school branding). Official mode requires institutional vetting and a different creation flow.
2. Organizer proposes invitation; no public invite URL or self-join. Expiring, single-use invitations create only a pending request.
3. Child's valid guardian(s) approve; existing guardianship/co-guardian policy controls conflicts. The child receives understandable membership information and an appropriate choice/reporting path.
4. Server persists explicit membership/room grants after successful checks; no implicit personal contact approvals. Each participant sees only permitted room/roster fields.
5. Organizers may remove a member within scope; guardians may revoke their children's membership. Membership state changes close or reject active subscriptions and queued notifications.
6. Cohort end-of-year: lock invites, revoke access, apply documented message-retention rules, and require new approvals for rollover. Do not migrate pupils silently.
7. Reporting and retention: clear child-friendly reporting; minimise exposure; bound automatic deletion and restrict incident evidence to authorized reviewers, consistent with existing safety and privacy ADR.

## Threats and mitigations to validate
- **Fake parent / fake teacher / fake school**: identity and authority proofing outside the classroom claim; explicit labels and no self-verification.
- **Bulk harvesting of child identities**: no search or directory; limit roster views, profile detail, enumeration, exports, rate of requests and side-channel responses.
- **Grooming via classroom affiliation**: membership does not enable DMs; adult messaging requires separately approved contact and safety constraints.
- **Disputed custody / revoked guardianship**: policy and authority revalidation, conflict state fail-closed, auditable approval source and prompt revocation.
- **Cross-tenant leakage**: community/resource-scoped grants only; deny read/write/media/history/push outside allowed room even for an authenticated class member.
- **Bullying or harmful content**: reporting, blocking where meaningful, trained review with safeguards, short retention and realistic moderation limitations; avoid unvalidated algorithmic enforcement.
- **Stale school-year memberships**: explicit expiry, no rollover by default and negative expiry tests.
- **School impersonation / branded invite phishing**: verified badges available only through institutional verification; no untrusted redirect/invitation URLs.

## Delivery plan and gates
### CC0 — Foundation and review (documentation phase)
- [x] Track capability as #36 with three scoped stages.
- [x] Propose ADR and initial entity/permissions architecture.
- [ ] Product/security/privacy review and **explicit ADR approval**.
- [ ] Define age-dependent guardian approval semantics, custody disputes, minimum roster visibility, retention schedule and school verification method.
- [ ] Threat model, service/permission contracts, authorization matrix and test plan reviewed before implementation.

### CC1 — Private class communities (later implementation)
- [ ] Dependent on secure #2/#3/#4 contact and messaging work, #5 MFE, #7 reporting/deletion, and safety gate #8.
- [ ] Implement minimal community/cohort/room storage with explicit multi-family grants and tenant-scoped checks.
- [ ] Guardian-only controlled invitation and consent; do not allow teacher identity claims.
- [ ] Child chat, announcements and guardian-only room via role-scoped messages and accessible mobile UI.
- [ ] Auditable revoke, leave, group archive, school-year expiry and deletion.
- [ ] Negative tests: unauthorized reader/sender, fake organizer/guardian, wrong tenant, forged membership, guessed invite, child self-invite, teacher DM bypass, expired role, revoked device, websocket/push/media leak, membership race, old-school-year access.
- [ ] Independent security/privacy checks and real-device validation before any real children.

### CC2 — Verified school organizations (separate go/no-go)
- [ ] Institution and educator verification with renewal/offboarding and clear roles.
- [ ] School-approved processing purpose, applicable cantonal/legal assessment, agreements and DPIA determination, data residency and operator obligations.
- [ ] School-specific governance, approved retention and independent incident/audit pathway.
- [ ] New pilot approval; no silent migration of existing private communities.

## Non-goals / architecture constraints
No public recruitment, global directory, automatic contact expansion, school administration, broad school-controlled private-chat visibility, unverified “official” branding or automatic class roster import. Reuse Go-first modular-monolith services and MFE; no need for new microservices. Preserve family-tenant integrity and independent contact approvals.

## Definition of done for this planning PR
The capability is traceable from roadmap, architecture, ADR index and tracking issue. All decisions are labelled **proposed**, not implemented or safety-certified. This documentation-only change does not run code tests.
