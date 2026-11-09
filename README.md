# Yapuno

**Your crew. Your chat.**

A **Mobile First Experience (MFE)**: Yapuno is a proposed private, parent-approved chat space for children and teens, designed for iPhone and Android with Wi-Fi/internet access. No child-managed passwords, phone numbers, SIMs or public profiles.

> **Early prototype — NOT approved for real children's use.** No production authentication, messaging, moderation or child-safety safeguards are implemented yet. An independent safety, privacy and mobile usability review is required before any real pilot.

## First Hello
The first goal is a simple conversation between two children on approved devices, after **both sides' guardian contact approvals**, with server-enforced tenant boundaries and device revocation.

## Project documents
- [Vision](docs/product/vision.md)
- [Mobile First Experience (MFE) specification](docs/product/mobile-first-experience.md)
- [Roadmap](ROADMAP.md) · [GitHub issues](https://github.com/pblumer/yapuno/issues)
- [Communities & Classrooms (future proposal)](docs/planning/communities-classrooms.md) · [tracking issue #36](https://github.com/pblumer/yapuno/issues/36)
- [Architecture](docs/ARCHITECTURE.md) · [ADR drafts](docs/adr/README.md)
- [Agent development rules / mandatory TDD](AGENTS.md) · [Contribution guide](CONTRIBUTING.md)
- [Responsible security reporting](SECURITY.md)

## Technical direction
Go and standard library first; modular monolith; server-rendered HTML/CSS, small JS for realtime, HTMX only where useful. A phone-first installable PWA is the leading option, subject to real iOS and Android validation. Start with a **fail-closed authorization-policy scaffold**, not an operational messenger.

```bash
go test ./...
go test -race ./...
go build ./...
go vet ./...
```

## Guardian passwordless login (experimental)
An invitation-only email-link flow is available as an **internal component** in `internal/guardianlogin`: expiring single-use challenges, no automatic accounts, and revalidation before issuing a guardian session. It is **not wired to a production login service**: verified guardian directory, real email sender, deployment and safety review are still required. See [draft login ADR](docs/adr/draft-guardian-passwordless-email.md).

## Browser login engineering preview

A startable guardian login and family dashboard are available as an **engineering
preview for synthetic profiles** only. The `cmd/yapuno-provision` offline CLI
provisions a verified guardian and example child, while `cmd/yapuno-web`
connects passwordless email links, mandatory-TLS SMTP, opaque sessions and
live authorization checks to the family UI.

The preview requires a real HTTPS domain behind a trusted local reverse proxy,
a pre-provisioned private data directory, and SMTP credentials. It does not
support public self-registration, child device onboarding, real contacts,
messaging or production-ready security. See the
[setup and safety limitations](docs/planning/guardian-browser-login-runtime.md).

## Guardian device management (preview)

When the optional `YAPUNO_PAIRING_FILE` is configured, authenticated guardians
can view linked devices, start five-minute pairing challenges and revoke devices
for their authorized children. See the [setup and limitations](docs/planning/guardian-device-management.md).
**Browser key-based child enrollment and explicit guardian fingerprint approval are now wired** using a short-lived invitation link. **Locally rendered QR images and a child scanner page** are now included. The scanner supports the browser's BarcodeDetector where available, otherwise the native phone camera or a shared link. Child device sessions, messaging authorization and real iPhone/Android tests are still open. See [the browser pairing flow](docs/planning/browser-device-pairing.md).

## Open source direction
The repository is public. **A distribution license has not yet been selected; public visibility by itself does not confer a license to reuse the code.** We intend to choose and publish an appropriate OSI-approved license before accepting broader contributions or releasing an installable build.
