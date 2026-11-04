# ADR-DRAFT: Mobile First Experience is the primary delivery contract

- **Status:** Accepted (2026-11-04; awaiting ADR numbering)
- **Implementation:** Not started
- **Date:** 2026-11-03
- **Deciders:** Yapuno maintainers

## Context and problem statement
Children and guardians will use Yapuno predominantly on iPhone and Android. A desktop-first UI made responsive later would likely fail touch ergonomics, setup flows and background messaging expectations.

## Decision drivers
- Usable on a small touchscreen without password or SIM
- Inclusive, age-appropriate and gender-neutral design
- Low data, CPU and battery use
- Observable usability and OS-specific push/PWA limitations
- Reliable session/revocation semantics across app resume

## Considered options
1. Mobile-first PWA and server-rendered HTML/CSS with lean JS; real device acceptance required.
2. Desktop-first web application with a responsive CSS pass later.
3. Native apps for both platforms immediately.

## Accepted decision outcome
Prefer **option 1**. **Mobile First Experience (MFE)** is a cross-cutting gate on every feature. Build/test the narrowest supported screen first; then expand to larger phones, tablets and desktop. Evaluate and document PWA constraints on physical iOS/Android before relying on background/notification features.

### Consequences
- Positive: one initial codebase and rapid iteration, lower operating and maintenance cost.
- Negative: iOS/Android PWA behavior varies and native capabilities may prove insufficient.
- Open questions: target OS/browser baselines, device support, background push reliability, offline behavior; record observed evidence before a child pilot.
- Follow-up: new ADR required if native app is proven necessary.

## Links
- [MFE specification](../product/mobile-first-experience.md)
- [Issue #5](https://github.com/pblumer/yapuno/issues/5)
