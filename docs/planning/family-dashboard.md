# Family dashboard foundation

The first dashboard slice creates a minimal server-side projection for an authenticated guardian, with child cards and per-child device/contact permissions. The projection filters children by tenant and active grants, excludes message content, and fails closed on conflicting duplicate permissions. It is not a public endpoint.

## Mobile-first interface contract

- Show a child switcher and clear per-child actions: Devices, Contacts, Guardians, Play.
- Present pending requests prominently; do not expose children without an active guardian relationship.
- Render unavailable actions as absent or disabled, but enforce permissions again in every API handler.
- Use semantic components and versioned Yapuno skins. Keep consent and security prompts unskinnable.
- Provide keyboard, screen reader and touch support; do not rely on color alone.
- Child-facing clients cannot mutate guardian policies or create their own grants.

## Next implementation slices

1. Authenticated HTTP handlers, trusted grant repository and authorization integration tests.
2. Responsive, skin-aware HTML dashboard with real child switcher and accessible pending approvals.
3. QR pairing: single-use expiring challenges, explicit guardian confirmation and atomic device binding.
4. Per-child play quota configuration backed by an atomic, server-side ledger.

**Security decision still required:** how multiple guardians resolve conflicting policies, especially quota changes and guardian invitations. No production management actions should be enabled until this is defined.

Related #13 #14 #16 #19.
