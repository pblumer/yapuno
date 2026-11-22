# Guardian session foundation

`internal/guardiansession` is a single-process session manager that implements
`familyweb.Authenticator`. It supports cryptographically random opaque tokens,
SHA-256 hashed server-side lookup keys, a CSRF token per session that is derived
from the session secret, expiry, revocation, rejection of duplicate cookies and
a Secure/HttpOnly/SameSite=Strict `__Host-` cookie.

## Staying signed in on a device (optional)

```sh
export YAPUNO_GUARDIAN_SESSIONS_FILE=private/guardian-sessions.json
```

With this file the last step of the sign-in (`/login/verify`) shows an unticked
checkbox **Auf diesem Gerät 30 Tage angemeldet bleiben**. Without the tick the
session lasts 30 minutes as before. With it the session lasts thirty days from
sign-in, is written to the file before the cookie is set, and survives restarts
and deployments. Decision record:
[ADR draft](../adr/draft-guardian-remembered-sessions.md).

- The file holds a digest of each session secret, the guardian and tenant
  identifiers and the expiry; no secret, no address. It is created with mode
  `0600`; a file that is not private, is a symbolic link or is malformed stops
  the start. Protect it like `guardians.json`: whoever can write it can add a
  session.
- **Abmelden**, **Überall abmelden** and the emergency lock end remembered
  sessions too and rewrite the file. If it cannot be rewritten it is removed, so
  that an ended session cannot return; all remembered sessions then end with
  the next restart.
- A deactivated or locked guardian is refused on every request, whatever the
  file says.
- A guardian keeps at most 10 remembered sessions; one more ends the oldest.
- If the session cannot be stored, the guardian is signed in for 30 minutes and
  the family page says so.
- To end every remembered session as the operator: stop the server and delete
  the file.
- Verified in a real desktop browser at 320, 375 and 430 px; not on real phones.

**Critical boundary:** `IssueVerified` is strictly a trusted server call made
*after* independently proving guardian identity and tenant membership. It is
NOT a public route, an authentication scheme or an invitation flow. HTTP
headers, query strings and form IDs must never become issuance authority.

## Operational limits

- Guardians end their own session, or all of them, on the [security page](guardian-account-safety.md).
- Ordinary session records are held in RAM and discarded when the process
  restarts. Remembered sessions are kept in one file of one process; sharing
  between instances is not implemented.
- Sessions should not be used in a horizontally scaled deployment without
  replacing the registry with a shared, revocable backend.
- HTTPS, correct reverse-proxy configuration, session rotation, account
  recovery, independent passkey/OIDC authentication, anti-abuse controls,
  device pairing and production security review remain open.
- Never wire `IssueVerified` to an unauthenticated request or use this
  prototype to store data belonging to real children.

Tests exercise cookie attributes, untrusted claim rejection, CSRF tokens,
session expiry, duplicate cookie denial, revocation, invalid issuance, and for
remembered sessions restarts, durable sign-out, the limit per guardian, unsafe
files and failed writes.
