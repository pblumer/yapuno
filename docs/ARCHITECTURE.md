# Yapuno architecture (proposed)

**Stage:** public prototype; not approved for children's use. [Roadmap](../ROADMAP.md) · [MFE specification](product/mobile-first-experience.md) · [ADRs](adr/).

## Goals
A tiny, resilient, privacy-respecting messaging system for ages roughly 5–16, designed **first for phones**, without requiring phone number, SIM, password, public social account or payments by the child. Guardians control identities, contacts and device access.

## Components (initial design)
```
iOS/Android browser + installable PWA
  child chat: HTML/CSS + small WebSocket JS
  guardian portal: HTML/HTMX where useful
        │ TLS, authenticated sessions / origin controls
        ▼
Go modular monolith
  identity & devices   ─── guardian approvals & provisioning
  authorization        ─── deny-by-default boundaries for every action
  conversations        ─── membership, send, read, realtime delivery
  retention & reports  ─── expiry, incident review and restricted holds
  safety               ─── policy rules; Swiss German and URLs (later)
        │
        ▼
transactional persistence (candidate: PostgreSQL; ADR pending)
  metadata + messages + approvals + durable delivery state
```
No orchestrator, microservices or queue unless evidence requires them. All client-facing behavior is mobile-first and accessible.

## Trust boundaries
1. Guardian identity is independent of child identity. Guardians can only administer their explicitly linked children; shared guardianship requires an explicit policy.
2. Children cannot self-invite strangers, search a global directory or join another tenant using guessed identifiers.
3. A QR code only presents a short-lived pairing challenge. Parent approval, device key verification, one-use consumption and session renewal are separate.
4. Authorization is repeated server-side for writes, reads, history, websocket subscriptions, message fanout, media and notifications, even if UI hides affordances. Device and contact revocation terminates access.
5. A sent message requires current sender/recipient tenant and authorization checks and a committed write before acknowledgement.
6. Safety rules are independent of identity/authorization, may be bypassed by clever language, and are not a substitute for reporting and human review.
7. Logs, metrics, backups, caches and notifications must not silently defeat message expiry or expose sensitive content.

## Privacy / child safety
Encryption in transit and at rest are required for any real pilot; a server that inspects plaintext **cannot claim server-inaccessible E2EE**. Choose privacy and content-safety policy openly with children/guardians and expert review. Human reporting with narrowly scoped evidence retention is the default direction; mass scanning is **not** approved.

## Implementation boundaries
`internal/authz/` initially contains a **pure policy predicate only**; it is not a complete authorization system. All inputs in that package must originate in trusted data access, never directly from the client.

## Status and decisions
Draft ADRs cover Go/dependency choices, guardian trust boundaries, MFE and safety/retention. Data store, E2EE, login guarantees and legal scope are intentionally not settled yet. No service endpoints, frontend or message persistence exist yet.
