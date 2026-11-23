# ADR-DRAFT: Guardians delete a child profile or their whole family themselves

- **Status:** Proposed
- **Implementation:** Only where invited self-registration is switched on: removal in the family store and the guardian directory, purge in every store that holds records about a child or a guardian, a coordinator that fixes the order, two guardian pages (pending review)
- **Date:** 2026-11-23
- **Deciders:** maintainers; security, privacy and child-safety reviewers required

## Context and problem statement
Nothing in the application could be deleted except a contact and a single report. A family, once registered, stayed; a child profile, once created, stayed. Removing one meant editing up to ten files while the server was stopped. In trials with synthetic profiles the product owner asked for deletion in the application, because test families pile up and addresses cannot be used twice.

Deletion is also what a guardian may expect of a service that holds data about their child ([privacy, deletion and incident process](https://github.com/pblumer/yapuno/issues/7)). This draft does not settle that process. It records how the first, self-service form of deletion works and what it deliberately leaves out, so that the trial feature is not mistaken for a reviewed deletion concept.

What a family leaves behind is spread over many stores without a common transaction: profiles and grants, identities, devices and pairings, contacts and contact codes, two message logs, reports, looks, emergency locks, invitations and remembered sessions.

## Decision drivers
- A guardian can remove a test child or a test family without the operator.
- Deletion means deletion: afterwards no file of the installation names the child or the family, with exceptions that are stated to the guardian beforehand.
- Nobody deletes what they do not answer for (deny by default), and nothing is deleted by accident.
- An interrupted deletion must not leave a state from which the guardian cannot go on.
- Ending access must win over tidiness: after a family is gone, nobody of it is signed in, whatever else failed.
- Records that protect (an emergency lock) must not disappear while the thing they protect against still exists.
- Installations with operator-provisioned families behave exactly as before. Standard library only.

## Considered options
**Who deletes**
1. Guardians in the application (proposed).
2. Only the operator, with a command on the server. Safest; the reason for this change.
3. Both. More to build; an operator command remains open.

**What "delete the guardians" means**
1. The whole family: every child, every guardian (proposed).
2. Only the guardian's own access; the family goes with the last guardian. Needs rules for what a remaining guardian inherits.

**How a deletion is confirmed**
1. A checkbox, as for removing a contact. Fastest; one slip of a thumb.
2. Typing the child's name, or the word LÖSCHEN (proposed). Slower than a slip, still quick.
3. A fresh sign-in by e-mail link. Protects against somebody holding an unlocked device on which the guardian stays signed in for thirty days ([remembered sessions ADR draft](draft-guardian-remembered-sessions.md)); slow for the purpose at hand.

**How consistency is reached without a transaction**
1. Remove the profile first, then purge the rest. Access ends at once; an interruption leaves records nobody can reach and nobody can ask to delete again.
2. Purge what depends on the profile first, then the profile (proposed). An interruption leaves the profile with fewer attachments, and the guardian repeats the deletion.
3. A journal of pending deletions that is replayed at start. Covers every interruption; one more file and replay logic.
4. Purge by existence at every start ("whatever refers to a profile that is not there"). Simple; an operator who restores an older family file would silently lose the data of every family missing from it.

## Proposed decision outcome
Chosen options (product owner decisions of 2026-11-23): guardians delete in the application; deleting the guardians means deleting the whole family; confirmation by typing. Order by dependency (option 2) is the implementer's proposal.

### Rules
- The pages exist only while `YAPUNO_SIGNUP_FILE` is configured, that is where guardians also create families and profiles themselves. Where the operator provisions families, the operator's configuration stays the source of truth.
- **A child** is deleted by a guardian who holds full authority for exactly that child. Other guardians of the child are neither asked nor told. The last child of a family is not deleted on its own, because a family without a child cannot be signed in to; it goes with the family.
- **A family** is deleted by a guardian who holds full authority for every child of it. The last family of an installation is not deleted: somebody must be left who can sign in and invite.
- Guardian and family always come from the session. The request names the child, and the store decides whether the session's guardian answers for it.
- The guardian types the child's name, or the word LÖSCHEN, compared without regard to case and surrounding spaces. The page lists beforehand what is deleted and what is not.
- Order of a child's deletion: check authority; purge devices and pairings, contacts and contact codes, reports the child filed, the look, and the child's mention in invitations; remove the profile and its grants; compact both message logs, which drop every conversation whose relationship no longer exists.
- Order of a family's deletion: the same for all its children; then remove the family; then the guardians' identities, their sessions (remembered ones included) and unused sign-in links; then the family's open invitations, the record that it once joined, and its emergency locks; then compact the logs.
- If a step before the profile fails, the deletion stops and says so; the profile still exists and the guardian tries again. If a step after it fails, the remaining steps still run and the guardian is told that something was left for the operator. Guardians of a deleted family are signed out in every case.
- An emergency lock is only removed if the identities were removed. Otherwise it stays.
- Deletions run one at a time.
- Failures are logged with the name of the part that failed, never with an identifier, a name or an address.

### What is not deleted
- **A report another family's child filed about a message of the deleted child** stays with that family until they close it or it expires. It holds the message, the name as that family knew it and the identifiers of the deleted child. The evidence of the child who reported must not vanish because the reported child's family deleted itself.
- **Used invitations with which a deleted family invited others** stay as the record of how families that still exist came in. They hold identifiers of the deleted guardian and family, no name and no address.
- Copies outside the application (backups, logs of a reverse proxy, e-mails already sent) are not reached.

### Consequences
- Positive: test families and addresses can be reused; a guardian can remove their family's data without the operator; an ended family leaves no session, device or contact behind; the other family of a contact loses exactly that contact.
- Negative / accepted trade-offs:
  - **One guardian deletes for all.** A second guardian of a child loses the child, and every other guardian of a family loses their access, without being asked or told. Where guardians are in conflict this is a way to destroy the other's access and the children's history.
  - **Whoever holds an unlocked device with a guardian session can delete the family.** Typing a word stops an accident, not a person. With remembered sessions that window is up to thirty days.
  - Deletion is immediate and final. There is no grace period, no undo and no export beforehand.
  - Children are not asked and not told beforehand; their devices are simply signed out, and the other child's chat disappears.
  - Without a transaction an interruption between two steps is possible. Before the profile is removed it is repaired by repeating the deletion. After it, records can remain that nobody can reach and only the operator can remove; in the worst case the guardians' addresses stay registered and cannot register again.
  - If identifiers are reused (provisioned families use fixed ones), leftovers of an interrupted deletion could attach to the new family. Self-registered families get random identifiers.
  - The reporting family keeps a message of a deleted child (see above).
  - There is no deletion of a single guardian, and none by the operator through the application.
- Open questions and review gates:
  - The deletion concept as a whole ([issue 7](https://github.com/pblumer/yapuno/issues/7)): retention, legal holds for incidents, what a guardian may demand, and what must be kept against their wish.
  - Consent or at least notice for other guardians, and whether a child's deletion needs every guardian of that child.
  - A fresh sign-in, a waiting period, or both, before a family is deleted.
  - What children are told, and whether they get their messages before the family goes.
  - A journal that completes an interrupted deletion at start.
  - Whether a report about a deleted child may keep that child's identifiers, or only the message and the name.
  - Deletion by the operator, and of a single guardian.
  - Threat model and privacy review before any real child uses it.

## Links
- Issues [#7](https://github.com/pblumer/yapuno/issues/7), [#2](https://github.com/pblumer/yapuno/issues/2), [#13](https://github.com/pblumer/yapuno/issues/13)
- [Deleting a child or a family (planning)](../planning/self-service-deletion.md)
- [Invited self-registration ADR draft](draft-invited-self-registration.md), [guardian access lock ADR draft](draft-guardian-access-lock.md), [remembered sessions ADR draft](draft-guardian-remembered-sessions.md), [report and block ADR draft](draft-report-and-block.md), [guardian equal rights](guardian-equal-rights.md)
