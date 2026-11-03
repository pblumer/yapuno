# ADR-DRAFT: Transparent content safety and bounded retention

- **Status:** Proposed
- **Implementation:** Not started
- **Date:** 2026-11-03
- **Deciders:** Yapuno maintainers; privacy and child-safety reviewers required

## Context and problem statement
Yapuno may need to discourage harmful links and repeated harassment, including dialect-rich Swiss German. Automatic scanning, parent access, message expiry, and end-to-end encryption have conflicting privacy and evidentiary implications.

## Decision drivers
- Trust and reasonable privacy for children
- Clear, age-appropriate, non-secret safety disclosures
- No constant cloud-AI/token spending
- Low false-positive rate for dialect and joking between friends
- Reports and narrow evidence retention for actual harm
- Verifiable deletion across persistence, logs, cache and backup

## Considered options
1. Closed contacts, transparent optional rule-based nudges, child reports and restricted human escalation
2. Server-wide indiscriminate inspection and permanent parent transcript access
3. End-to-end encryption and no server-side content moderation; focus solely on client-side and reports

## Proposed decision outcome
Prefer **option 1** as a *research direction*, not final approval of content scanning. Link allowlists for younger children, locally executed classifiers only after measured evidence, and human decisions for serious cases. No unsupported bullying verdicts. Any architecture that requires server plaintext inspection must not claim server-inaccessible E2EE.

### Consequences
- Positive: explicit rules, limited scope, lower processing cost and better reviewability.
- Negative: false alarms and missed harm; server processing may undermine privacy if not constrained.
- Open questions: legal basis, effective age/guardian rules, opt-in versus opt-out, moderation/incident procedures, retention and security review.
- **Mandatory gate:** complete child-rights and Swiss/EU privacy assessment before using real children's messages.

## Links
- [Issue #6](https://github.com/pblumer/yapuno/issues/6)
- [Issue #7](https://github.com/pblumer/yapuno/issues/7)
- [Issue #8](https://github.com/pblumer/yapuno/issues/8)
