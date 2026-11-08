# Guardian session foundation

`internal/guardiansession` is a single-process session manager that implements
`familyweb.Authenticator`. It supports cryptographically random opaque tokens,
SHA-256 hashed server-side lookup keys, random per-session CSRF tokens, expiry,
revocation, rejection of duplicate cookies and a Secure/HttpOnly/SameSite=Strict
`__Host-` cookie.

**Critical boundary:** `IssueVerified` is strictly a trusted server call made
*after* independently proving guardian identity and tenant membership. It is
NOT a public route, an authentication scheme or an invitation flow. HTTP
headers, query strings and form IDs must never become issuance authority.

## Operational limits

- Session records are held in RAM and discarded when the process restarts;
  neither inter-instance sharing nor durable revocation is implemented.
- Sessions should not be used in a horizontally scaled deployment without
  replacing the registry with a shared, revocable backend.
- HTTPS, correct reverse-proxy configuration, session rotation, account
  recovery, independent passkey/OIDC authentication, anti-abuse controls,
  device pairing and production security review remain open.
- Never wire `IssueVerified` to an unauthenticated request or use this
  prototype to store data belonging to real children.

Tests exercise cookie attributes, untrusted claim rejection, CSRF entropy,
session expiry, duplicate cookie denial, revocation and invalid issuance.
