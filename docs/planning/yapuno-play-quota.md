# Yapuno Play: first quota policy slice

The first implementation introduces a deny-by-default, per-child gaming quota predicate with game allowlists, allowed weekdays, tenant scoping and a daily time balance. It deliberately does not affect messaging access.

**Not yet implemented:** durable ledger, atomic reservations, concurrent session enforcement, guardian UI, game invitation checks, chess gameplay or real-time timers. A pure balance check alone is insufficient to prevent bypass through concurrent sessions or device switching. Do not expose as a production quota enforcement endpoint.

Before integration: decide the child's authoritative IANA timezone, guardian conflict policy, safe turn completion/grace rules, daily reset semantics and session reservation/idempotency design. Add persistence transaction tests, device switching, reconnect, DST and revocation cases.

Related #19, #3, #13.
