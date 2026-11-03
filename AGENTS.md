# AGENTS.md — Yapuno engineering contract

Read this file, [ROADMAP.md](ROADMAP.md), [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md), and relevant [ADRs](docs/adr/) **before editing code**.

## Purpose
Yapuno ("Your crew. Your chat.") is an early-stage, mobile-first, private messaging project for children and teenagers, with guardians managing access. **It is not certified or safe for real children yet.** Never claim otherwise.

## Non-negotiable invariants
1. **Deny by default.** Every server-side read, write, websocket subscription, push fanout and media retrieval must authenticate, authorize the device/principal and check current room/contact permissions. A hidden UI control is not security.
2. **Bounded trust.** Tenant, family, child, guardian, device and conversation membership must never be inferred from a client-provided ID or token alone. Confirm against authoritative server-side records at the moment of action. A guardian may administer only children they are authorized to represent.
3. **Mutual approval.** A child's contact requires the explicitly defined guardian approvals on both sides; membership and revocation affect subsequent reads and sends. No public directory, discoverability, anonymous contact or open invitations.
4. **Secure by design.** Pairing links and QR tokens must be short-lived, single-use, bound to a server-side challenge and parent confirmation. Revocation must invalidate sessions and delivery credentials. Never commit secrets or children's data.
5. **Privacy by default.** No advertising SDKs or trackers. Collect minimum necessary data; configure expiry, deletion and incident evidence holds transparently. Do not quietly grant parents unrestricted transcript access.
6. **Mobile First Experience (MFE).** Treat iPhone and Android touch use as the default experience, not a desktop port. Every user-facing change needs small-screen, touch, keyboard, accessibility and connectivity validation. See [MFE spec](docs/product/mobile-first-experience.md).
7. **No unsupported safety claims.** Filtering is fallible, dialect-sensitive and context-dependent. No automatic bullying verdict, guaranteed anti-circumvention or E2EE claims without design and tests. Serious safety decisions require a review path.
8. **Durable before acknowledged.** A message send must not be acknowledged as stored or delivered before the corresponding data is durably committed; retries must be idempotent.
9. **Do not invent crypto.** Use mature, maintained standards/libraries for WebAuthn, passwordless login and encryption. Treat device-bound keys and synced passkeys as different guarantees.

Changes to invariants need a proposed ADR and review before implementation.

## Architecture and dependencies
- **Go first; standard library first.** Build a modular monolith with server-rendered HTML/CSS and minimal JavaScript. HTMX is optional for parent/admin forms; websocket JS is acceptable for realtime chat.
- No frameworks, ORMs, brokers, microservices, analytics or AI services by default. A dependency needs a concrete problem, lifecycle review, license check, and ADR if architecturally significant.
- Keep modules/package boundaries explicit and interfaces small. Inject clock and storage in tests.
- **MFE is a delivery gate:** functional success without mobile ergonomics is not done.

## TDD: required default
1. **Red:** write a meaningful failing behavior/security test and observe the expected failure.
2. **Green:** smallest implementation that passes.
3. **Refactor:** retain tests; add authorization, revocation, malicious client and boundary tests.
- Bug fixes begin with a reproducing regression test.
- Exceptions: documentation-only, mechanical-only changes; disclose exceptions honestly in the PR.
- Do not report tests as run unless observed.

## Local checks (run before requesting merge)
```sh
go build ./...
go test ./...
go test -race ./...
go vet ./...
test -z "$(gofmt -l .)"
```
CI must be green. Later add device E2E tests and retention/incident gates before a child pilot.

## Work protocol (Atlas-inspired)
- Start from an issue and current roadmap milestone; one scoped change per PR.
- Read relevant draft/accepted ADR before coding. Architectural changes require a new `docs/adr/draft-<slug>.md` with an **ADR-DRAFT** heading; never pick a number on a branch. See [ADR index](docs/adr/README.md). A numbering automation is a separate issue (#9), not currently implemented.
- Use English for committed docs/code/issues/PRs, except user-facing localized copy. Use Swiss German/Standard German where appropriate in product locale tests.
- Update docs, tests, roadmaps and acceptance evidence in the **same PR**. Reference the issue with `Fixes #N` only if fully complete.
- Do not merge with failing checks. Do not mark security/child-readiness as complete until threat model, privacy review and real-device tests are evidenced.
- Private vulnerability reports: [SECURITY.md](SECURITY.md). Do not disclose exploit details in public issues.
