# Roadmap

Status: **initial design**, not production-ready. GitHub issues are the delivery units; ADR approval gates precede implementation. **Mobile First Experience (MFE) is a cross-cutting acceptance rule for every user-facing milestone.**

## Foundation — now
- [x] Public repository created
- [x] Initial README
- [ ] [#1](https://github.com/pblumer/yapuno/issues/1): agent/TDD contract, ADR drafts, Go CI, architecture, security guidelines (in review)
- [ ] [#9](https://github.com/pblumer/yapuno/issues/9): merge-time ADR numbering automation (later; drafts stay unnumbered now)

## First Hello — private text chat
- [ ] [#2](https://github.com/pblumer/yapuno/issues/2): guardian-managed registration, passwordless pairing and device revocation (engineering browser runtime, SMTP delivery and durable pairing core implemented for synthetic families; reviewed onboarding, link-based child WebCrypto claim, guardian fingerprint approval and key-bound finish implemented as synthetic browser preview; local QR rendering and optional browser camera scanner added with native-camera/shared-link fallback; real iOS/Android compatibility testing, child device sessions, production security and shared storage still open)
- [ ] [#3](https://github.com/pblumer/yapuno/issues/3): cross-tenant isolation, mutual guardian contact approval, closed circles
- [ ] [#4](https://github.com/pblumer/yapuno/issues/4): durable, idempotent one-to-one text chat and reconnection
- [ ] [#5](https://github.com/pblumer/yapuno/issues/5): **Mobile First Experience**, installable PWA, accessible child interface and parent portal
- Gate: two test accounts chat on real iPhone/Android after explicit approvals, with negative auth and network tests.

## Safety & Trust
- [ ] [#6](https://github.com/pblumer/yapuno/issues/6): transparent URL policy and Swiss German language hints
- [ ] [#7](https://github.com/pblumer/yapuno/issues/7): privacy, reporting, deletion and incident evidence retention

## Pilot readiness — mandatory, not optional
- [ ] [#8](https://github.com/pblumer/yapuno/issues/8): independently assessed security, child-privacy legal review, guardian/child understanding, real-device accessibility and push behavior; operational recovery tests

## Future options, not commitments
[Communities & Classrooms (#36)](https://github.com/pblumer/yapuno/issues/36) is a **proposed future capability, not part of First Hello**. Design: [capability plan](docs/planning/communities-classrooms.md), [ADR proposal](docs/adr/draft-communities-classrooms.md).

- **CC0 — foundation**: define community/cohort/room entity model, independent membership vs. direct-contact authorization, role grants, privacy and threat review; ADR approval required before implementation.
- **CC1 — private class communities**: guardian-organized opt-in class rooms with separately approved children, announcements and guardian spaces; no school verification claim.
- **CC2 — official schools**: verified school/staff roles, institutional governance and legal/privacy gates; explicitly later than CC1.

Dependencies: #2–#5, #7 and #8; no real child pilot before the independent safety gate. Other future options: stickers, voice snippets, moderated media, age-adaptive themes. No advertising, public profiles or engagement feeds.

**No actual child users until the pilot-readiness gate passes.**
