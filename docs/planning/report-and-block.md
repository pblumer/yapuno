# Report and block (child side) — browser preview

**Status:** engineering preview for synthetic child profiles. A child can block a contact and can report one message to their own guardians. This is a first slice of issue [#7](https://github.com/pblumer/yapuno/issues/7), **not** moderation and not a reason to use Yapuno with real children. Decision record: [ADR draft: report and block](../adr/draft-report-and-block.md).

## Enable

Blocking is available wherever chats are ([messaging preview](messaging-preview.md)). Reporting needs a private file:

```sh
export YAPUNO_REPORTS_FILE=private/reports.json
```

It requires `YAPUNO_MESSAGES_FILE`. Without it the child page offers blocking only and says "Blockieren" instead of "Melden".

## Child flow

The chat header has a **Melden** button that opens **Melden oder blockieren** for that conversation. The page works without JavaScript.

- **Eine Nachricht melden:** the child picks one of the other child's recent messages (newest first, up to 20) and may tick **Ausserdem … blockieren**. Before sending, the page says who will see what: the child's own guardians see exactly this one message and that it was reported; nothing else from the chat; the other child is not told.
- **Blockieren:** needs a ticked confirmation. The page says that the chat disappears, that only the guardians can undo it, and that the guardians see the block but no messages.
- After a report the page confirms it and marks the message as **Schon gemeldet**. After a block the start page confirms it and lists the blocked contact under **Blockiert**.
- If a report cannot be stored (limit reached, storage failure) the child is told to talk to their guardians directly. If the child also asked to block, the block still happens and the confirmation says that the report was not stored.

## What a block does

- A block suspends the contact for **both** children: neither can read, send or open the chat, and neither sees the other's figure. It is one more condition in the same predicate that guards every send and read.
- The **blocked** child is not told who did what. The contact disappears from their crew list and an open chat ends with the same neutral text as when a guardian ends a contact ("Ihr seid nicht mehr verbunden"). A child cannot find out from any page that they were blocked, though they may guess.
- The **blocking** child sees the contact under **Blockiert**. Only a guardian of the blocking child can lift the block (**Blockierung aufheben** on the contact page). The child cannot lift it, so nobody can pressure the child into unblocking.
- Guardians of the blocking child see **Von … blockiert**. Guardians of the other child see **Pausierte Kontakte**: the contact is paused, without a reason, and they cannot lift it. Either family can still end the contact.
- If both children block, each family lifts only its own block.
- The contact and its messages are kept while blocked, so lifting the block restores the conversation (within message retention).

## What a report does

- The server copies exactly the reported message (text, time, sender's display name as known then) into the report store. The child's right to report is the child's right to read: the message is fetched with the child's own session, so contact, profile and device state are checked at that moment. A child cannot report their own message, a message of another conversation, or a message that no longer exists.
- Only guardians with current contact authority for the **reporting** child see the report: on that child's contact page, under **Meldungen von …**. The family overview shows the number of open reports, never their content.
- The other family is not informed and cannot see anything.
- A guardian deletes a report with **Erledigt** (ticked confirmation); the copy of the message is removed from disk. Otherwise a report is deleted after 30 days. The child can have at most 20 open reports.
- A report outlives the contact: if the contact is ended and its messages are purged, the guardians still see what was reported until they dismiss it.
- **Guardians still cannot read chats.** A report is the only path by which a message reaches a guardian, and the child chooses it knowingly, one message at a time.

## Notice to guardians

A report is of little use if nobody looks. When the mailer can send notices (the built-in SMTP mailer can), a newly filed report triggers an e-mail to the guardians who may see it.

- **Recipients:** guardians with current contact authority for the reporting child, active in the guardian directory and not [locked](guardian-account-safety.md). Not the other family, not a guardian who only manages devices or only a sibling.
- **Content:** the text names the reporting child and nobody else. No message text, no other child, no link. The subject, which shows on lock screens, names nobody. An unusual child name (control characters, over-long) is replaced by "Dein Kind".
- **At most one e-mail per hour and child.** Further reports within the hour are visible in Yapuno but send nothing. Reporting the same message again is not a new report. If the mail server accepts none of the notices, the hour is not used up and the next report tries again.
- **In the background:** the child's request returns at once; sending is limited to 15 seconds.
- **The child is told** on the page, before reporting, that the guardians get an e-mail, that they are reminded if they do not look, and that the message is not in the e-mail. Without a mailer that can send notices the page does not promise any of this. Blocking alone sends no e-mail.

### Reminders while nobody has looked

- A report counts as **seen** when a guardian with contact authority for the reporting child opens that child's contact page, where the report is shown. The store records the time, not who looked. The family overview with its count does not count as seeing; neither does listing or counting through the store.
- While a report is unseen, the guardians are reminded **3 hours and 24 hours** after it was filed, then no more. Seeing or dismissing it ends the reminders. An expired report is never reminded.
- A reminder is one e-mail per child, not per report, with the same rules as the first notice (names only the reporting child, no link). Its subject differs ("wartet noch"), and the text says that the report has not been looked at and that there are at most two reminders.
- Reminders and first notices **share the hourly limit**: no guardian gets more than one e-mail per hour and child. A reminder that falls into that hour waits for the next pass; a report filed right after a reminder is not announced separately.
- A timer checks every 5 minutes. A reminder is recorded only after a mail server accepted it, so a failed attempt is tried again at the next pass. Reminder state is stored with the report and survives restarts. A report that was unseen for a long time (for example while the server was down) gets one reminder, not one per missed time.

## Security and privacy properties (tested)

- Identity comes from the verified child or guardian session only; identity fields in forms are ignored. CSRF token, form-only content type, 4 KiB body limit, exactly one value per field, explicit confirmation for blocking, lifting a block and deleting a report.
- Block: only a party of an active contact can block it; pending requests, siblings, strangers and unknown ids are refused with the same answer; blocking twice is harmless; the block is durable and ends with the relationship; a new contact does not inherit an old block.
- Lifting: only a guardian with current authority for the blocking child; not the other family, not a guardian of a sibling only, not a revoked guardian; one family cannot lift the other family's block.
- Reports: visible to and deletable by guardians of the reporting child only (tenant- and child-scoped, checked on every call); the store holds the reported text and nothing else of the conversation; reporting the same message twice keeps one report; a neighbouring message is never substituted for a missing one; a failed write stores nothing and is reported as failure; the store fails closed on corrupt or insecure files.
- Nothing on the reported child's pages changes after a report.
- Pages for children and guardians contain no script for these functions, load nothing from other origins and use skin tokens only. Message text is rendered as text. Only fixed confirmation texts can be selected by a redirect.
- Notices: recipients are exactly the guardians who may see the report (tested with an equal second guardian, a helper without contact authority, a guardian of a sibling only, a locked guardian and the other family); only a new report is announced; one notice per hour and child also under concurrent reports; a failed attempt does not use up the hour; a slow mail server does not delay the child; the mail is 7-bit clean, marked as automatic, without link or address, and cannot be bent by a crafted child name.
- 71 mutations across the predicate, both stores, child pages, guardian pages and wiring: 69 are detected by the tests; 2 are equivalent because the property is checked twice (sequence number zero; a missing session on the safety page). 28 further mutations of the notice code and 33 of the reminder code are all detected.
- Reminders: due exactly at 3 and 24 hours and not a second earlier; never a third; per child and per report; not for seen, dismissed or expired reports; only a guardian with authority for that child can mark its reports as seen (not the other family, not a guardian of a sibling only); a view that shows no report writes nothing; a repeated view rewrites nothing; a failed write changes no state; the page still shows reports when the seen mark cannot be stored.

## Browser evidence (headless Chromium, not a physical device)

One scripted run through the real server over TLS with two children and both guardians, repeated with the `classic` and `night` guardian skins, 35 checks passed each: the header button is a 44 px target without overflow at 320 px; the page offers only the other child's messages, newest first; hostile message text stays inert on the child page and on the guardian page; no overflow, no clipped text and 44 px targets at 320, 375 and 430 px on the child page and the guardian contact page; keyboard reaches every control with a visible focus ring; nothing happens without a chosen message or a ticked confirmation; the guardian sees exactly the reported message and the other family nothing; after a block the other child's open chat ended in about 2 seconds with the neutral text and their start page showed no hint; the other family saw a neutral pause without a way to lift it; the blocking child's guardian lifted the block and the conversation returned with its history; report and block in one step with JavaScript disabled; no request left the own origin.

A second scripted run for the notice, 10 checks passed: the page tells the child about the e-mail and what it does not contain; no overflow, no clipped text and 44 px targets at 320, 375 and 430 px with the added sentence; no mail before a report; one notice to the reporting child's guardian naming only that child; the child's request took well under a second; a second report within the hour sent no second mail while both reports were visible to the guardian.

A third scripted run for the reminder, 11 checks passed: the page tells the child about the reminder; no overflow, no clipped text and 44 px targets at 320, 375 and 430 px with the longer sentence; a fresh report is stored as unseen; the family overview and the other family's page leave it unseen; opening the contact page with the report records a time and no person; opening it again changes nothing. The timing itself is covered by tests with a controlled clock, not by the browser run.

**Not verified:** physical iPhone/Android, screen readers, large text, landscape, a real mail server and real inboxes (delivery time, spam classification, rendering), and above all **whether children understand the page and would use it** — that needs testing with children and guardians.

## Limitations

- **Notice and reminders are best effort.** Accepted by the mail server is not read; mail can be delayed, filtered or missed. The child is advised to speak to the guardians as well. There is no push notice.
- **"Seen" means the page was opened, not that anybody read or acted.** A guardian who opens the contact page for another reason ends the reminders. Seen by one guardian ends them for all.
- **The child does not learn whether the guardians looked.**
- **The hourly limit is kept in memory.** After a restart the next report or reminder goes out even if a mail was sent shortly before. A second, more serious report within the hour sends no mail of its own; it is covered by the reminder 3 hours after it was filed, if still unseen.
- **Reminders ignore the time of day.** A report filed in the evening can cause a mail at night.
- **The report file has new fields** (`seen_at`, `reminders`). An older binary refuses a file that contains them, so a rollback needs these fields removed or the file set aside.
- **The e-mail leaves Yapuno.** The mail provider learns that this address belongs to a guardian of a child with that first name who reported something.
- **No operator or moderator sees a report.** There is no escalation path for serious cases, no evidence hold beyond the guardians' own view and no process for the reported child's family.
- **A blocked child can still guess** that they were blocked, and both children can still meet outside Yapuno.
- Only the 20 most recent messages of the other child can be picked. There is no free-text reason and no report of a whole conversation or of a contact as such.
- A guardian lifting a block is asked to confirm that the child agrees; the software cannot check that.
- Reported text is stored in plaintext like messages. Report retention (30 days) and the limit of 20 are placeholders until #7 decides.
- Reports are not deleted when a child profile or guardian is removed.
- Single process, single-writer files.

Related #7, #5, #4, #3.
