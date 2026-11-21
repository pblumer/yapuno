# ADR-DRAFT: Child-initiated block and single-message report to the child's own guardians

- **Status:** Proposed
- **Implementation:** First slice on a branch: block on the contact record, report store with a copy of one message, child page and guardian views (pending review)
- **Date:** 2026-11-20
- **Deciders:** maintainers; privacy and child-safety reviewers required

## Context and problem statement
Messaging between approved contacts exists ([message storage ADR draft](draft-message-storage.md)). A child had no way to stop a contact or to ask for help inside the product; the Mobile First Experience acceptance matrix and issue [#7](https://github.com/pblumer/yapuno/issues/7) require block and report before any pilot.

The accepted direction ([content safety ADR](draft-content-safety-privacy.md)) is closed contacts plus child reports and restricted human escalation, with **no blanket parental access** to messages. Guardians cannot read chats today, and AGENTS.md forbids silent transcript access. Any report function therefore opens a narrow, deliberate path from a message to an adult and must not become a general one.

## Decision drivers
- A child must be able to protect themselves immediately, without an adult being online.
- A child must not be pressured into undoing that protection.
- Guardians see message content only when the child chooses to show it, and then as little as possible.
- No retaliation by design: the reported or blocked child is not told who did what.
- No false statements in the UI, including to the blocked child.
- Deny by default; one authorization rule for all paths; standard library only.
- No operator moderation process exists yet; the design must not pretend one does.

## Considered options
**Who blocks and who lifts**
1. Child blocks, own guardians lift (proposed).
2. Child blocks and lifts alone. Full autonomy; the child can be pressured to unblock, and guardians never learn of a conflict.
3. Blocking ends the contact. Simple; destroys the conversation and any basis for a later report, and reconnecting needs both families again.

**Who receives a report**
1. Own guardians only (proposed).
2. Own guardians and the operator. Needed for a hosted service with moderation; requires roles, access logs and a process that do not exist.
3. Both families. Transparent; invites retaliation and takes the decision away from the reporting child's guardians.

**What a report contains**
1. Exactly the reported message (proposed).
2. The message with surrounding context. Easier to judge; exposes messages the child did not choose to show, including their own.
3. No content. Maximum privacy; no evidence once messages expire or the contact ends.

**What the blocked child experiences**
- Telling them is honest but points at the other child. Pretending that messages are still delivered ("shadow block") is a false status and is rejected. Proposed: the conversation ends for both with the same neutral state as a contact ended by guardians.

## Proposed decision outcome
Chosen options (if approved; product owner decisions of 2026-11-20): child blocks and own guardians lift; reports go to the reporting child's own guardians; a report holds exactly the one message.

### Rules
- `authz.ContactGrant` gains `Blocked`. `CanContact` denies while it is set, so sending, reading, listing and figures all follow from one predicate. A block suspends the contact for both children and does not end the consent.
- Each side records its own block on the contact. Only a guardian with current contact authority for the blocking child lifts that side's block. The other family sees a neutral pause.
- A report is created through the reporting child's own authorized read of that message, sent by the other child, at that moment. The report store keeps a copy of that message, its time, and the sender's display name as known then.
- Only guardians with current contact authority for the reporting child can list or delete reports. Nobody else, including the other family and the operator interface (there is none), sees them.
- The child is told before sending what the guardians will see. Nothing on the other child's pages changes.
- A guardian closes a report with "Erledigt": the message copy and the sender's name are deleted and guardians no longer see it; a marker without content stays until 30 days after the report. Otherwise a report is deleted after 30 days. A child can hold at most 20 open reports; markers do not count. Both numbers are placeholders.
- The reporting child sees, on the report page only and next to the reported message, "Deine Eltern haben die Meldung gesehen" once a guardian has seen or closed the report (product owner decisions of 2026-11-21). The start page and the chat show no report status. Guardians are told that the child sees this.
- A failed report never prevents a block the child asked for in the same step.
- A new report is announced by e-mail to the guardians who may see it (product owner decisions of 2026-11-20): the mail names only the reporting child, carries no message text, no other child and no link; at most one mail per hour and child; blocking alone sends none. The child is told on the page. Locked guardians are not mailed. Sending never delays or fails the report.
- A report is **seen** when a guardian with authority for the reporting child opens the page that shows it; the store records the time only. While unseen, the guardians are reminded 3 and 24 hours after the report, then no more (product owner decisions of 2026-11-21). Reminders follow the content rules of the first notice and share its limit of one mail per hour and child.

### Consequences
- Positive: immediate self-protection; no pressure to unblock; guardians learn of a problem through the child's own choice; minimal disclosure; no new way to read chats; the blocked child is not confronted.
- Negative / accepted trade-offs: the notice is best effort, discloses to the mail provider that a named child reported something, and a second report within the hour sends none, so guardians may still see a report late; serious cases have no escalation beyond the family; a single message can lack context; the blocked child's family is left with an unexplained pause; guardians can lift a block against the child's wish (the UI asks them to talk first, nothing enforces it); report copies are plaintext and outlive the messages they came from.
- Open questions and review gates:
  - **Notification:** push or in-app notice as a second channel, quiet hours for reminders, and whether the hourly limit should let an urgent second report through.
  - **Feedback to the child:** whether "seen" should be split from "talked about it", and whether the child may withdraw a report.
  - **Escalation:** operator or moderator role, evidence hold, access logs, appeal for the reported family, and legal duties for serious content (#7).
  - **Retention** of reports and whether the reporting child may withdraw one.
  - **Context:** whether a child may add neighbouring messages or a reason, and how to prevent that from becoming transcript access.
  - **Age rules:** whether older children may lift their own blocks.
  - **Usability evidence** with children and guardians; wording review by child-safety experts.
  - **Privacy and child-rights assessment** before real messages are reported.

## Links
- Issues [#7](https://github.com/pblumer/yapuno/issues/7), [#5](https://github.com/pblumer/yapuno/issues/5), [#4](https://github.com/pblumer/yapuno/issues/4), [#3](https://github.com/pblumer/yapuno/issues/3)
- [Report and block](../planning/report-and-block.md), [messaging preview](../planning/messaging-preview.md)
- [Content safety ADR](draft-content-safety-privacy.md), [message storage ADR draft](draft-message-storage.md), [contact invitations ADR draft](draft-contact-invitations.md)
