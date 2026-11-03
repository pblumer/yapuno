# Contributing to Yapuno

Yapuno is experimental open-source-in-intent software and **not ready for use by children**. Welcome thoughtful contributions, including usability and privacy review.

Read [AGENTS.md](AGENTS.md), [ROADMAP.md](ROADMAP.md), [the architecture](docs/ARCHITECTURE.md), the [MFE spec](docs/product/mobile-first-experience.md), and related ADR drafts.

- Use English for public development artifacts. User-visible interface copy should be localizable.
- File an issue for non-trivial changes; split implementation into small PRs.
- TDD is mandatory for behavior: red → green → refactor. Confirm authorization and tenant-isolation negative tests for all endpoint and storage changes.
- Run `go build ./...`, `go test ./...`, `go test -race ./...`, `go vet ./...` and formatting checks.
- For UI changes, validate on a narrow touchscreen first, then a larger phone; record iOS/Android, keyboard, screen reader and connectivity behavior where affected.
- Include threat-model impact, tests, docs and user-visible limitations in PR descriptions.
- An ADR draft is needed for major architecture/security changes. Never overwrite a decided ADR without a superseding decision.
- Disclose vulnerabilities privately according to [SECURITY.md](SECURITY.md); do not post attacks or child data in a public issue.

**License pending.** The repository is public but no reuse rights are granted solely by public visibility; do not submit third-party code that cannot be redistributed under a future compatible open-source license.
