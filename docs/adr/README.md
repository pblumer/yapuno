# Architecture Decision Records

Yapuno follows an Atlas-inspired ADR process. An ADR states **why** a choice is made, alternatives, benefits and risks. A proposal is not an approved decision.

- New records use `docs/adr/draft-<slug>.md` with heading `# ADR-DRAFT: ...` and **Status: Proposed**, **Implementation: Not started**.
- Do **not** assign numbers in branches. An automated merge-time numbering procedure and reference/index validation are tracked in [issue #9](https://github.com/pblumer/yapuno/issues/9); they are **not yet implemented**.
- Once accepted and numbered, a record is immutable: supersede it with a new ADR rather than silently rewriting history.
- Status and implementation are independent; `Landed` never means `Proposed`.
- A PR touching a security or MFE invariant must link its ADR and include negative tests.

## Drafts
- [Go-first modular monolith](draft-go-first-modular-monolith.md)
- [Guardian-managed authorization and contact trust](draft-guardian-contact-trust.md)
- [Mobile First Experience](draft-mobile-first-experience.md)
- [Transparent content safety and retention](draft-content-safety-privacy.md)
