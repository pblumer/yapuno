# ADR-DRAFT: Invitation-only guardian passwordless sign-in

Status: **Draft** — authentication method and production provider selection are
not yet approved. This slice does not grant identities or enable a child pilot.

## Context

Guardians need low-friction access to family administration without children
owning passwords, SIM cards or email accounts. Existing guardian sessions can
only be issued after independent authentication. A mail-link adapter offers an
initial web-first option but is not equivalent to passkeys or verified custody.

## Proposed decision

Implement a backend-agnostic, **invitation-only email link** flow for guardians
who have already been independently verified and provisioned. The email link
authenticates possession of that mailbox, not guardianship. Children still pair
devices via guardian-approved QR challenges, never email login.

- Issue 256-bit random, ten-minute, single-use links; store only SHA-256
  digests of login tokens. Use a locked atomic consume operation.
- An email preview GET must not consume a link: only explicit POST confirms it.
- Re-check the trusted guardian directory before issuing the existing secure
  session; deny after guardian revocation or tenant/account changes.
- Use a canonical configured HTTPS origin, never a request Host header.
- Return the same generic user-facing response for unknown and known accounts.
- Limit link requests and bound in-memory registries; further edge/IP controls
  and asynchronous delivery are mandatory before exposure.
- Require independently verified guardian identities, approved email ownership,
  and tenant-scoped grants **before** provisioning the directory. No
  client-supplied guardian ID or tenant ID may establish a session.

## Alternatives and risks

- **WebAuthn/passkeys:** preferred future high-assurance option, but requires
  mature, reviewed library and device-browser rollout testing.
- **OIDC:** viable if a suitable provider and identity linking are available.
- **Email links:** simplest initial browser path but depend on mailbox security;
  phishing, mail forwarding, URL leakage, login-CSRF and provider outages need
  reviewed mitigations. Synchronous delivery may leak account existence via
  timing despite identical response text. The current rate limit is per email,
  not a substitute for trusted-edge abuse controls.
- **Never use homemade challenge-response cryptography or SMS for children.**

## Implementation scope / rollout gate

This PR only adds `internal/guardianlogin`: HTTP form, challenge lifecycle,
trust interfaces and negative tests. It does **not** ship SMTP/transactional
email delivery, independently verified guardian directory, real login server,
durable/shared challenge storage, durable guardian sessions, user onboarding,
logout or audit/abuse monitoring.

Before deployment, settle provider and email-delivery policies, implement
verified provisioning and a shared atomic challenge/session store, audit
security/privacy, test iOS/Android and ensure revocation affects *existing*
sessions. The acceptance gate for real children remains unchanged.
