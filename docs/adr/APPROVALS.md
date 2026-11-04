# ADR approval record

On 2026-11-04 the maintainer approved the four foundational ADR proposals:
- Go-first modular monolith
- Guardian-managed devices and mutually approved contacts
- Mobile First Experience
- Transparent content safety and bounded retention

The content-safety ADR approves the **research direction**, not indiscriminate scanning or a production-ready moderation policy. All four are marked Accepted in their existing draft-named files pending issue #9 (merge-time numbering and reference updates). Do not allocate ADR numbers manually.

Implementation of identity, contacts, messaging and PWA remains separate work under issues #2–#5. This branch begins with a pure, fail-closed pairing policy and tests, **not** a usable enrollment API.
