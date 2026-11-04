# Decision: Equal guardian administration rights

Status: Accepted (product decision, 2026-11-04)

## Context

A child may have multiple guardians and a guardian may manage multiple children. The product owner decided that guardians of the same child have equal administrative rights. No primary guardian role or hierarchy is introduced.

## Decision

- Every verified, active guardian of a given child has the same **baseline administrative capabilities** for that child, including device management, contact approvals and play policy.
- Grants remain child-scoped and tenant-scoped. Rights for one child never imply rights for siblings or access to private messages.
- The backend, not the UI, must enforce these capabilities. Revocation takes effect for subsequent operations.
- High-risk operations (adding/removing guardians, account recovery, device resets) require separate step-up verification, notification, and anti-lockout safeguards, applied symmetrically to all guardians. These controls do not create a primary guardian.
- If there is a legal restriction or verified custody constraint, access must be suspended or limited through an explicit safety/legal process, not an implicit primary-guardian role.

## Implementation consequences

The current `GuardianGrant` flags are a legacy policy scaffold. Future persistence and enrollment must establish uniform baseline rights for verified guardians, while keeping high-risk operations separately authorized. Migrate and test old grants before enabling actual administration. Never silently elevate existing accounts or treat chat-contact consent as guardian authority.

## Verification

Tests must cover multiple equal guardians, separate children, cross-tenant isolation, revocation, concurrent edits, and protected guardian removal/recovery.
