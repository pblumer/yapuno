# Roadmap

Status: **initial design**, not production-ready. GitHub issues are the delivery units; ADR approval gates precede implementation. **Mobile First Experience (MFE) is a cross-cutting acceptance rule for every user-facing milestone.**

## Foundation — now
- [x] Public repository created
- [x] Initial README
- [ ] [#1](https://github.com/pblumer/yapuno/issues/1): agent/TDD contract, ADR drafts, Go CI, architecture, security guidelines (in review)
- [ ] [#9](https://github.com/pblumer/yapuno/issues/9): merge-time ADR numbering automation (later; drafts stay unnumbered now)

## First Hello — private text chat
- [ ] [#2](https://github.com/pblumer/yapuno/issues/2): guardian-managed registration, passwordless pairing and device revocation (engineering browser runtime, SMTP delivery and durable pairing core implemented for synthetic families; reviewed onboarding, QR/browser UX, device session and production hardening still open)
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
Group chats/tenants for clubs and schools, stickers, voice snippets, moderated media, age-adaptive themes. No advertising, public profiles or engagement feeds.

**No actual child users until the pilot-readiness gate passes.**
