# Equal-guardian provisioning slice

The accepted equal-guardians decision requires multiple verified guardians
with symmetric **child-scoped** baseline administrative rights.

`familystore.BootstrapGuardian` adds a second (or subsequent) verified
guardian to an existing child. It enforces tenant/child identity, rejects
duplicates and rejects partial grants for newly provisioned guardians. The
operation writes through the same durable store.

**This is a privileged provisioning primitive, not a public endpoint.** The
caller must independently establish identity, guardianship/legal authority,
additional high-risk step-up verification, guardian notifications and
anti-lockout controls before this method can be wired to a real application.
Those workflows are **not implemented**. Do not present it as a way for one
unverified user to invite themselves.

The existing `Bootstrap` method and legacy grants are unchanged: existing
partial grants are not silently elevated, pending a reviewed migration.
Tests verify two guardians see the same child, a mutation is shared, data
persists, cross-tenant access is denied, and duplicate or partial grants fail.
