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

## Open source direction
The repository is public. **A distribution license has not yet been selected; public visibility by itself does not confer a license to reuse the code.** We intend to choose and publish an appropriate OSI-approved license before accepting broader contributions or releasing an installable build.
