# Guardian browser login runtime (engineering preview)

This slice composes the existing `guardianlogin`, `guardiansession`,
`familyweb` and `familystore` components into a startable Go web server
with an operator-provisioned guardian identity directory and implicit-TLS SMTP.

**Not approved for real children, production traffic or a public pilot.**
This is an authenticated *synthetic-family* engineering preview, not a
complete onboarding or parenting app.

## First-time test setup

1. Create a private directory *outside any tracked repository data*.
   The directory must have mode `0700`.
2. Independently verify the adult's email and legal authority before
   provisioning. For this preview use **synthetic** child profiles only.
3. Provision the *first* family and guardian with the offline admin command:

```sh
mkdir -p private
chmod 700 private
go run ./cmd/yapuno-provision \
  -verified \
  -families private/family.json \
  -guardians private/guardians.json \
  -tenant trial-tenant \
  -guardian guardian-a \
  -email YOUR_VERIFIED_EMAIL@example.org \
  -child demo-child \
  -name Demo
```

Both files are created with `0600` permissions; existing files are
never overwritten. The command is for **first** bootstrap only and runs
offline. There is no untrusted user-facing guardian registration endpoint.

4. Configure a real SMTP service that supports **implicit TLS** on port
   465 and provide a canonical, publicly resolvable **HTTPS** origin.
   Configure credentials via the environment, not command-line arguments:

```sh
export YAPUNO_ORIGIN=https://yapuno.example.org
export YAPUNO_FAMILIES_FILE=private/family.json
export YAPUNO_GUARDIANS_FILE=private/guardians.json
export YAPUNO_SMTP_HOST=smtp.example.org
export YAPUNO_SMTP_USER=YOUR_SMTP_USERNAME
export YAPUNO_SMTP_PASSWORD=YOUR_SECRET
export YAPUNO_SMTP_FROM=login@example.org
go run ./cmd/yapuno-web
```

The server binds to `127.0.0.1:8086` only. A trusted local reverse proxy
MUST terminate TLS for the configured host, preserve the original `Host`,
and prevent direct remote access to port 8086. Example Caddy deployment
with a valid publicly resolvable domain:

```caddyfile
yapuno.example.org {
    reverse_proxy 127.0.0.1:8086
}
```

Open `https://yapuno.example.org/login`, submit the provisioned email,
follow the link received by email and confirm the login. After authentication,
`/family` renders the family and lets the authorized guardian rename their
synthetic child profile. Do **not** attempt to use the loopback `http://`
endpoint as a browser login: the `__Host-` session cookie requires HTTPS.

## Safety boundaries

- Identity directory is an operator-provisioned `0600` JSON file,
  reloaded on every lookup. Symlinks, unsafe permissions, malformed records,
  duplicate emails and duplicate guardian/tenant pairs fail closed.
- Each identity is valid only with a currently active, tenant- and child-scoped
  family-store grant. Disabling the identity immediately blocks family
  requests even when the session cookie has not yet expired.
- The operator MUST verify guardianship independently. Email possession
  alone is not legal or family authority. Multi-tenant email selection is not
  implemented and ambiguous email mappings are rejected.
- SMTP is authenticated with TLS 1.2+ and certificate verification; plaintext
  or opportunistic STARTTLS transport is not supported by this implementation.
- Session cookies are Secure/HttpOnly/SameSite=Strict; login links are
  short-lived and single-use. HTTPS host matching rejects host-header poisoning.
- HTTP routes remain limited to guardian login and family profile operations.
  There are no child accounts, device credentials, contact approvals or
  messaging endpoints in this runtime.

## Remaining blockers

- Security review and threat model for email login, including login-CSRF,
  origin verification at ingress, enumeration via timing, account takeover
  and abuse controls for outbound email.
- Production-grade email queue/transport verification and retry strategy.
- Shared, atomic login-challenge and session backends; in-memory state is not
  horizontally scalable and restart invalidates sessions and pending links.
- Transactional, multi-writer family persistence replacing the single-process
  JSON store; secure backup, retention and recovery procedures.
- Guardian account recovery, independent step-up for high-risk changes,
  custody checks, robust guardian revocation, audit trail and notifications.
- Real mobile/desktop accessibility and usability validation; QR pairing,
  mutual contact approvals, durable messaging and a child safety review.
