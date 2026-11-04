# First Hello: family and chat identities

This slice adds pure policy predicates and table-driven tests for:
- Multiple children per guardian and multiple guardians per child (#13).
- Independently scoped device-management and contact-approval capabilities.
- Child/child, adult/child and adult/adult mutually approved contact pairs (#12).
- Denial on wrong guardian, wrong child, cross-tenant, revoked or incomplete grants.

**Not implemented:** persistent family associations, guardian authentication,
child profiles, QR pairing protocol, contact consent workflow, messaging API,
browser/PWA interface, or native app packaging. Callers must never accept
client-declared grants as authority.

All participants, including children, will have complete web access; iOS and
Android apps are additional distribution channels. QR enrollment is planned in
#14 and requires a one-time persisted challenge plus explicit authenticated
guardian approval. Do not deploy this slice as a child-safe service.
