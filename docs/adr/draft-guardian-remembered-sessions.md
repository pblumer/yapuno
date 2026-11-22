# ADR-DRAFT: A guardian may stay signed in on a device for thirty days

- **Status:** Proposed
- **Implementation:** Optional, off by default: remembered sessions in a private file, a choice in the last step of the sign-in, restyled sign-in pages (pending review)
- **Date:** 2026-11-22
- **Deciders:** maintainers; security and privacy reviewers required

## Context and problem statement
A guardian session lasts 30 minutes and lives in the memory of one process ([guardian sessions](../planning/guardian-sessions.md)). Every sign-in needs a link from the mailbox ([guardian sign-in ADR draft](draft-guardian-passwordless-email.md)). In the first trial on a real installation the product owner found that too laborious: a guardian who wants to approve a contact or connect a device has to fetch a new link almost every time, and every deployment signs everybody out.

A longer session is a longer window for whoever holds the device. A guardian session can connect a device as a child, approve contacts, read reported messages and write to children and other guardians. The lifetime of that authority is a security decision and is recorded here before it may be relied on.

## Decision drivers
- A guardian should not need the mailbox for every visit on their own device.
- The short session stays the default; a longer one is an explicit choice per device.
- Ending a session must be reliable: sign-out, sign-out everywhere and the emergency lock end remembered sessions too, and an ended session never returns.
- A stolen copy of the server's files must not be usable as a session.
- A session is acknowledged only after it is stored (durable before acknowledged).
- Installations that do not configure the feature behave exactly as before. Standard library only.

## Considered options
**How long**
1. Stay at 30 minutes for everybody. Safest window; the reason for this change.
2. Choice per sign-in: 30 minutes, or 30 days on this device (proposed).
3. 30 days for everybody, or no expiry until sign-out. One step fewer; a shared or borrowed device stays signed in without anybody having decided that.

**Where the choice is made**
1. On the page that asks for the address. The link is often opened on another device than the one that asked.
2. On the page that confirms the link (proposed): that browser receives the cookie, so "this device" is true.

**Where a remembered session lives**
1. In memory only. Every restart and deployment would end it, which defeats the purpose.
2. In a private file with a digest of the session secret (proposed).
3. In a signed, self-contained cookie without server state. No file; but a single session cannot be ended before it expires, which contradicts the lock.

**Absolute or sliding expiry**
1. Thirty days from sign-in, not extended by use (proposed). Predictable; the guardian needs the mailbox once a month.
2. Extended by every visit. A device in daily use would never ask again, and a session in the wrong hands never ends by itself.

## Proposed decision outcome
Chosen options (product owner decision of 2026-11-22): a choice per sign-in on the confirming device; a private file; thirty days from sign-in.

### Rules
- The feature exists only while `YAPUNO_GUARDIAN_SESSIONS_FILE` is configured. Without it the page offers nothing, and a crafted `remember=yes` yields an ordinary session.
- The last step of the sign-in shows an unticked checkbox "Auf diesem Gerät 30 Tage angemeldet bleiben" and says: only on a device nobody else uses, what happens without the tick, and where to sign out everywhere. Only the exact value `yes` counts; two answers are refused without using up the link.
- A remembered session differs from an ordinary one in two things only: it expires thirty days after sign-in, and it is written to the file. Cookie attributes are the same (`__Host-` prefix, `Secure`, `HttpOnly`, `SameSite=Strict`). Every request still checks that the guardian is active and not locked.
- The file holds, per session, a SHA-256 digest of the session secret, the guardian and tenant identifiers and the expiry. It holds no secret, no address and no CSRF token. The CSRF token of every session is derived from the session secret by a domain-separated hash, so it needs no storage and survives a restart.
- The session is written (temporary file, fsync, atomic rename, mode `0600`) before the cookie is set. If that fails, the guardian is signed in with an ordinary session and told that staying signed in did not work.
- Sign-out ends the session of that browser, sign-out everywhere and the emergency lock end all sessions of the guardian, remembered ones included, and rewrite the file. If the file cannot be rewritten it is removed: an ended session must not return with a restart, at the price that all remembered sessions then end with the next restart.
- A guardian keeps at most 10 remembered sessions; a further one ends the oldest. An installation keeps at most 10 000.
- At start, entries that have expired are dropped, and so are entries that would outlive the configured time, so a shortened policy or an edited file grants no more than a new sign-in would. A file that is not private, is a symbolic link or is malformed stops the start.
- Sessions issued after a registration are ordinary ones.

### Consequences
- Positive: one mail a month on a guardian's own device; deployments no longer sign such guardians out; ending sessions is durable; the sign-in pages are in the current design and lead a child's device to pairing.
- Negative / accepted trade-offs:
  - **Whoever holds an unlocked device with a remembered session is that guardian for up to thirty days.** Nothing asks again before a device is connected as a child or a contact is approved. The tick is the guardian's own decision, and the page cannot know whether the device is shared.
  - The guardian does not see which devices are signed in, only how many sessions exist, and can end them only all at once or on the device itself.
  - The file now holds live session records. Digests cannot be used as sessions, but whoever can write the file can add a session for any guardian. It needs the same protection as the guardian directory.
  - A failed write while ending a session ends everybody's remembered sessions at the next restart.
  - A guardian whose sign-in at the limit ends the oldest session is not told which device that was.
  - Ordinary sessions still end with every restart.
  - One process only: a second instance would neither see nor end these sessions.
- Open questions and review gates:
  - Whether actions with lasting effect (connecting a device, approving a contact, inviting a family) should ask for a fresh sign-in when the session is older than some time.
  - A list of signed-in devices with a way to end one of them, and a notice by e-mail about a new remembered sign-in.
  - Whether thirty days is the right length, and whether it should be shorter where guardianship is self-declared.
  - Binding the session to the device (for example a device-bound key) so that a copied cookie is useless.
  - Shared, transactional storage for more than one server process.
  - Threat model review before any real child uses it.

## Links
- Issues [#2](https://github.com/pblumer/yapuno/issues/2), [#5](https://github.com/pblumer/yapuno/issues/5)
- [Guardian sessions (planning)](../planning/guardian-sessions.md), [guardian account safety](../planning/guardian-account-safety.md)
- [Guardian sign-in ADR draft](draft-guardian-passwordless-email.md), [guardian access lock ADR draft](draft-guardian-access-lock.md)
