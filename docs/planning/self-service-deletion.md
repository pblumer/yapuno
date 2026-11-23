# Deleting a child or a family

**Status:** engineering preview for synthetic families, available only where [invited self-registration](invited-self-registration.md) is switched on. A guardian deletes a child profile or their whole family in the application. Decision record: [ADR draft: self-service deletion](../adr/draft-self-service-deletion.md).

> Deletion is immediate and cannot be undone. One guardian decides for all guardians of the child or the family. Do not use it with real children.

## Enable

Nothing separate: the pages exist while `YAPUNO_SIGNUP_FILE` is set. Without it none of the routes below exist.

## Flow

**A child**

1. On **Familie**, the card of a child the guardian fully answers for has a link **Profil von … löschen** (`/family/children/delete?child_id=…`).
2. The page lists what is deleted and what is not, and asks the guardian to type the child's name.
3. After the deletion the overview confirms it.

The only child of a family cannot be deleted on its own; the page says so and leads to the family deletion.

**A family**

1. **Sicherheit und Abmelden** has a section **Familie löschen** that leads to `/family/delete`.
2. The page lists the children by name, what is deleted and what is not, and asks the guardian to type **LÖSCHEN**.
3. The guardian is signed out and lands on the sign-in page, which confirms the deletion.

The last family of an installation cannot be deleted; the page says so.

## Who may delete

- A child: a guardian with full authority (devices and contacts) for exactly that child. Another guardian of the child is neither asked nor told.
- A family: a guardian with full authority for every child of the family. A guardian who answers for one of several children cannot.
- Guardian and family come from the session. Another family cannot be named.

## What is deleted

| Child | Family |
|---|---|
| the profile and all grants for it | all profiles and all grants |
| its devices and open pairings; the devices are signed out at once | the same for every child |
| its contacts, open requests and contact codes; the other child loses exactly this contact | the same for every child |
| the chats with those contacts and with the child's guardians | every chat of the family, also between guardians of connected families |
| the reports the child filed | the same for every child |
| its look | the looks of all children and guardians |
| its mention in invitations (they still register a family, without the offer to connect) | open invitations (their links stop working) and the record that the family joined |
| | the guardians' identities: no sign-in link is sent any more, and the addresses can register again with a new invitation |
| | all sessions of the guardians, remembered ones included, and unused sign-in links |
| | the guardians' emergency locks |

## What is not deleted

- A report that a child of **another** family filed about a message of the deleted child. It stays with that family until they close it or it expires, with the message, the name at that time and the deleted child's identifiers.
- Used invitations with which the deleted family invited others. They hold identifiers only.
- Anything outside the application: backups, proxy logs, e-mails already sent.

## If something goes wrong

- A failure before the profile is removed stops the deletion. The page says that it was interrupted; the profile still exists, and repeating the deletion completes it. What was already removed (for example the devices) stays removed.
- A failure after the profile is removed does not stop the rest. For a child the overview says that some chat messages are only removed with the next clean-up of the server; nobody can read them any more. For a family the sign-in page says that something could not be removed and that the operator has to look. The guardians are signed out in every case.
- The server logs which part failed, without identifiers, names or addresses.

## Operator notes

- If the log says that the guardians' identities could not be removed, the family is gone but its addresses are still in `guardians.json` and cannot register again. Stop the server and remove those entries. Their emergency locks were kept on purpose; remove them from `guardian-locks.json` with the entries.
- A family that the operator provisioned with fixed identifiers can be deleted like any other when self-registration is on. Provisioning it again later with the same identifiers is safe only if its deletion completed without the message above.
- If every family but one is deleted, that one cannot be deleted in the application. To start over, stop the server and remove the data files.
- An operator command for deletion does not exist yet.

## Known limits

- One guardian decides for all, without notice to the others.
- Typing a name or a word protects against a slip, not against somebody who holds an unlocked device with a guardian session.
- No grace period, no undo, no export.
- Children are not told beforehand.
- No transaction across the stores; see "If something goes wrong".
- Verified in a real desktop browser at 320, 375 and 430 px; not on real phones.
