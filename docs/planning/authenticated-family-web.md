# Family web: authenticated read and rename slice

## Scope

`internal/familyweb` provides `GET /family` and `POST /family/rename`.
The handler accepts **only a trusted authenticator** and the durable `familystore`.
No authentication method is selected or shipped by this PR, and there is no
public server exposing this handler yet. Do not attach a resolver that trusts
user-supplied `guardian_id`, `tenant_id`, `X-User`, or other client claims.

The guardian session contains the server-verified guardian and tenant identity
plus a random, per-session CSRF token. Authentication and session lifecycle,
secure HttpOnly/SameSite cookies, revocation and HTTPS remain integration work.

## Server-side safeguards

- Deny missing/invalid sessions before any read or write.
- Data access is always filtered by guardian and tenant through
  `familystore.Cards` and `familystore.RenameChild`.
- POST requires form encoding, one value per expected field and a bounded body.
- CSRF token comparison uses constant-time byte comparison.
- Response disables caching, framing, referrer disclosure and scripts.
- Child name and identifiers are escaped by Go templates. The embedded dashboard
  fragment comes exclusively from the trusted `familydashboard.RenderHTML`.
- The form is shown only when both current legacy management grants are present;
  the store rechecks authorization on submission. Uniform guardian rights from
  the accepted equal-guardians ADR require an explicit grant migration before
  real profiles are enabled.

## Acceptance and remaining gaps

Tests cover missing authentication, tenant isolation, cross-child mutations,
valid rename, CSRF rejection and duplicate parameters. The implementation
is **not** child-pilot ready: guardian enrollment/login, session storage,
step-up operations, QR device pairing, real messaging, privacy/security review
and mobile-device testing are not yet available. Never use demonstration identity
adapters or synthetic account values for real children.
