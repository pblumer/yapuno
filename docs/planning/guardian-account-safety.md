# Guardian account safety: sign-out, sign-out everywhere, emergency lock

**Status:** engineering preview for synthetic families. A guardian can end their own sessions and, if a device or mailbox may be in someone else's hands, lock their own access. Decision record: [ADR draft: guardian access lock](../adr/draft-guardian-access-lock.md).

## Why three steps

A guardian signs in with a link sent by e-mail, so the mailbox is the credential. What helps depends on what was lost:

| Situation | What helps |
|---|---|
| Shared or borrowed computer | **Abmelden** |
| Device lost, mailbox still safe (other device, mail app locked) | **Überall abmelden** |
| Device **and** mailbox may be in someone else's hands | **Meinen Zugang sperren** |

Signing out everywhere does not help in the third case: whoever reads the mailbox requests a new link. Only a lock that also stops sign-in links closes that path.

## Enable

**Familie → Sicherheit und Abmelden** (`/family/security`) is always available and offers sign-out and sign-out everywhere. The emergency lock needs a private file:

```sh
export YAPUNO_GUARDIAN_LOCKS_FILE=private/guardian-locks.json
```

The file is created empty with mode `0600` at start. Without it the page says that the lock is not set up. The mailer must be able to send lock notices (the built-in SMTP mailer can).

## What each action does

- **Abmelden** ends the session of this browser and removes its cookie.
- **Überall abmelden** (ticked confirmation) ends every session of this guardian, including those of devices on which the guardian [stays signed in](guardian-sessions.md#staying-signed-in-on-a-device-optional), and invalidates sign-in links that were sent but not used. It is not a lock: a new link can be requested.
- **Meinen Zugang sperren** (ticked confirmation):
  1. optionally locks every device of the guardian's children first ([what a device lock does](device-lost.md)); a finder with a guardian session could have paired a device of their own;
  2. stores the lock durably;
  3. ends every session and invalidates unused sign-in links;
  4. sends an e-mail to the other active guardians of the same children, asking them to review devices and contacts. The notice names nobody, contains no address and no link;
  5. shows a confirmation that says who was told, how many child devices were locked and what to do next.

While locked, the guardian gets no sign-in link (the login page answers exactly as for an unknown address) and no session of theirs is accepted, whatever issued it. Children keep chatting with their approved contacts; other guardians keep all their rights.

## Lifting a lock (operator)

Only the operator lifts a lock, after the guardian's device and mailbox are safe again and the guardian has been identified out of band. Remove the guardian's entry from the lock file, keeping the file and its `0600` mode:

```json
{"locks":[]}
```

The file is re-read on every request, so no restart is needed. Replace the file atomically (write a private temporary file, then rename) and not while a lock is being written. **Never delete the file to lift a lock:** a missing, malformed or wrongly permissioned file denies every guardian.

There is deliberately no self-service unlock: a lock that the same mailbox could lift would not protect against a lost mailbox.

## Security properties (tested)

- Every action applies to the guardian of the verified session. No request field names a guardian; identity fields in a form are ignored. A guardian cannot lock, sign out or look up another guardian.
- CSRF token, form-only content type, 4 KiB body limit, exactly one value per field, explicit confirmation for sign-out everywhere and for the lock; rejected requests have no effect.
- The lock is stored (temporary file, fsync, atomic rename, mode `0600`) before any session is ended or any notice is sent. If it cannot be stored, nothing else happens and the guardian keeps the session to try again. The same holds if locking the children's devices fails.
- Mail is best effort and bounded to 10 seconds; a slow or failing mail server cannot prevent or delay the lock. The confirmation says truthfully whether the mail server accepted the notices. Accepted is not read.
- Notices go only to other active, unlocked guardians holding a grant for a child of the locking guardian, in the same tenant: not to other families, not to guardians of unrelated children, not to locked guardians (their mailbox may be compromised), not to the locking guardian.
- Lock store: tenant-qualified, idempotent, survives restart, strict decoding, fails closed on any doubt. Locking twice sends no second notice.
- The login response for a locked address is identical to the one for an unknown address.
- Pages contain no script, load nothing from other origins and use skin tokens only. Only fixed texts can be selected on the login page.
- 74 mutations across sessions, login, family store, lock store, locker, directory, mailer, pages and wiring: 72 are detected by the tests; 2 are equivalent because the property is checked twice (file permissions on path and on the opened file; a missing notifier is also refused by the locker).

## Browser evidence (headless Chromium, not a physical device)

One scripted run through the real server over TLS with three browsers of one guardian, a second guardian of the same child and a paired child device, repeated with the `classic` and `night` skins, 30 checks passed each: navigation from the family overview; no horizontal overflow, no clipped text and 44 px targets at 320, 375 and 430 px; keyboard reaches all seven controls in order with a visible focus ring; sign-out with JavaScript disabled, with the cookie gone and the count of sign-ins dropping; sign-out everywhere does nothing without the tick and then ends every browser of that guardian while the other guardian and the child device continue; sign-in with a fresh e-mail link afterwards; lock with child devices: confirmation with the true outcome and without form, link or address, exactly one notice to the other guardian, child device signed out, other guardian still able to manage; no request left the own origin.

**Not verified:** physical iPhone/Android, screen readers, large text, landscape, a real mail server and real inboxes (rendering, spam classification, delivery time).

## Limitations

- **The guardian must reach a session to lock.** If the only signed-in device and the mailbox are both gone, the guardian cannot lock; only the operator can (disable the entry in the guardian file or add a lock entry). There is no lock by another guardian: see the ADR for why.
- **A finder with a session can lock the guardian out.** That fails safe (access denied, other guardians told) but needs the operator to undo.
- **Earlier actions are not undone.** Contacts approved, children renamed or devices paired before the lock stay, except child devices when the option is ticked.
- **A sole guardian leaves the family unmanaged** until the lock is lifted; the confirmation says so.
- Sessions and pending links live in one process; a multi-instance deployment needs shared revocation. Locks are in a single-writer file.
- No audit trail beyond the lock entry (identifiers and time), no rate limiting, no step-up authentication.
- The sign-in e-mail is still English and the login pages keep their unstyled markup.
- Sign-in notifications and notices about paired devices or approved contacts are not implemented.

Related #2, #8, #14.
