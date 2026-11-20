# Yapuno

**Your crew. Your chat.**

A **Mobile First Experience (MFE)**: Yapuno is a proposed private, parent-approved chat space for children and teens, designed for iPhone and Android with Wi-Fi/internet access. No child-managed passwords, phone numbers, SIMs or public profiles.

> **Early prototype — NOT approved for real children's use.** Authentication and messaging exist only as engineering previews for synthetic profiles; no moderation, reporting or production child-safety safeguards are implemented. An independent safety, privacy and mobile usability review is required before any real pilot.

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
**Browser key-based child enrollment and explicit guardian fingerprint approval are now wired** using a short-lived invitation link. **Locally rendered QR images and a child scanner page** are now included. The scanner supports the browser's BarcodeDetector where available, otherwise the native phone camera or a shared link. Messaging authorization and real iPhone/Android tests are still open. See [the browser pairing flow](docs/planning/browser-device-pairing.md).

## Messaging (preview)

With `YAPUNO_MESSAGES_FILE` (plus pairing and contacts), two children whose
families approved the contact can exchange text messages from their paired
devices. Every send and read is authorized from current contact, profile and
device state; retries are idempotent; a message is confirmed only after it is
on disk. Guardians cannot read messages. **Messages are stored in plaintext,
there is no reporting or blocking yet, and this must not be used with real
children.** See [the preview and its limits](docs/planning/messaging-preview.md)
and the [proposed ADR](docs/adr/draft-message-storage.md).

## Child device sessions (preview)

A paired device signs in at `/child` without a password by proving possession
of the key bound during pairing, and receives a 12-hour session that ends when
a guardian revokes the device. The child sees a greeting and the contacts both
families approved. The key is non-extractable
but not hardware-backed. See [the flow and limitations](docs/planning/child-device-sessions.md)
and the [proposed ADR](docs/adr/draft-child-device-sessions.md).

## Guardian contact approval (preview)

When the optional `YAPUNO_CONTACTS_FILE` is configured, guardians of two
families can connect two children: one family creates a one-time code, the
other redeems it for their child, and the first family confirms the named
request. Only then is the contact active; either family can end it. There is
no directory or search. See [the flow and limitations](docs/planning/guardian-contact-approval.md)
and the [proposed ADR](docs/adr/draft-contact-invitations.md).

## Mein Look (preview)

When the optional `YAPUNO_APPEARANCE_FILE` is configured, guardians (`/family/look`)
and signed-in children (`/child/look`) each choose a theme and a photo-free
figure. A theme is private; a child's figure is shown to mutually approved
contacts only. See [Mein Look](docs/planning/mein-look.md).

## Open source direction
The repository is public. **A distribution license has not yet been selected; public visibility by itself does not confer a license to reuse the code.** We intend to choose and publish an appropriate OSI-approved license before accepting broader contributions or releasing an installable build.
