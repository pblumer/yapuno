# ADR-DRAFT: Go-first modular monolith with minimal dependencies

- **Status:** Accepted (2026-11-04; awaiting ADR numbering)
- **Implementation:** Not started
- **Date:** 2026-11-03
- **Deciders:** Yapuno maintainers

## Context and problem statement
A messaging application for children needs reliable delivery and policy enforcement, but should be cheap to operate, auditable and simple to maintain. Framework sprawl makes security and long-term maintenance harder.

## Decision drivers
- Small deployable footprint, fast operations, low complexity
- Easily testable server-side authorization and data handling
- Accessible, phone-first UI and predictable browser behavior

## Considered options
1. Go modular monolith, standard library first; HTML/CSS + minimal JS + optionally HTMX
2. Service-heavy microservice architecture with event broker
3. Full client-side single-page JavaScript framework and serverless backend

## Accepted decision outcome
Prefer **option 1**. Use standard Go HTTP primitives and explicit package boundaries. Add PostgreSQL or another durable datastore only after a storage ADR; do not invent crypto.

### Consequences
- Positive: easier local testing and review; fewer dependencies; straightforward deployment.
- Negative: manual UI polish and browser lifecycle testing are necessary.
- Open questions: exact datastore, storage migrations, websocket library, push transport, license.

## Links
- [MFE](../product/mobile-first-experience.md)
- [Issue #1](https://github.com/pblumer/yapuno/issues/1)
