# ADR-DRAFT: An invitation to register may also connect the two families' children

- **Status:** Proposed
- **Implementation:** Only where invited self-registration and contacts are both switched on: optional child in an invitation, choice in the last step of the registration, direct contact creation, notice by e-mail to the guardians of the inviting child (pending review)
- **Date:** 2026-11-22
- **Deciders:** maintainers; security, privacy and child-safety reviewers required

## Context and problem statement
Two families reach a first chat through two separate hand-overs: an invitation link with which the second family registers ([invited self-registration ADR draft](draft-invited-self-registration.md)), and afterwards a contact code that one family creates, the other redeems and the first confirms ([contact invitations ADR draft](draft-contact-invitations.md)). In trials with synthetic profiles the product owner found this too complicated: the families already talk to each other when the link is passed on, and then have to do it again with a code.

The contact invitations draft considered and did not choose "an invitation whose redemption alone activates the contact", because the inviting family then never sees who redeemed it before the contact is active. Joining the two hand-overs reopens exactly that question and touches the invariant *mutual approval*. It needs a recorded decision before it may be relied on.

## Decision drivers
- A second family should reach the first chat with one hand-over.
- Both guardians must still approve the contact, each for their own child, by an explicit act. Nothing is preselected.
- No approval may be obtained by a crafted request that the approving guardian did not make.
- Authority is checked when the contact is created, not only when the invitation is written.
- The pages must say what is approved and what the other family sees, before the guardian decides.
- Installations without contacts, and invitations without a child, behave exactly as before.

## Considered options
1. **Keep two hand-overs.** Nothing changes; the inviting guardian confirms a named request. Rejected by the product owner as too complicated for the common case.
2. **The invitation names a child; the invited guardian agrees at registration; the contact is active at once** (proposed).
3. **As option 2, but the contact stays pending until the inviting guardian confirms the named child.** Keeps the confirmation of a named counterpart at the price of one more step for the inviting family, who must return to the application after the other family registered.
4. **Connect all children of both families.** Fewer choices; approves contacts nobody looked at. Rejected.

## Proposed decision outcome
Chosen option (product owner decision of 2026-11-22): **option 2**.

### Rules
- When creating an invitation, the guardian must choose: one of the children for whom they hold contact authority, or "only invite". No option is preselected. Naming a child is that guardian's approval of a contact between this child and the first child of whichever family registers with the invitation.
- The invitation stores the child's identifier next to the inviting guardian and tenant. The child's first name is shown to whoever opens the link, and the inviting guardian is told so.
- The invited guardian decides in the last step, after the mailbox is confirmed, by pressing one of two buttons: create the family and connect the children, or only create the family. Only the exact answer "yes" connects; a request with two answers is refused without using up the link.
- The contact is created after the family and the identity are stored. The contact store checks the current contact authority of both guardians for exactly these two children; an existing request or contact between them is left as it is. An invitation of a guardian who is locked or inactive is void as before.
- If the connection fails, the registration stands and the new guardian is told that the children are not connected.
- After a connection every guardian who currently holds contact authority for the inviting child, is active and not locked, is told by e-mail. The mail names only that guardian's own child, never the other family or its child, and carries no link; it says where the new contact can be seen and ended. It is sent in the background and does not delay the registration. A mail that cannot be sent is logged without address or name and changes nothing else. The invite page announces the mail only where it is sent.
- The resulting contact is an ordinary one: either family ends it at any time, and everything that follows from a contact follows from this one too, including the conversation between the guardians of both families where that is switched on. The pages say so before either guardian approves.
- Without a named child, without contacts on the installation, or without the invited guardian's "yes", nothing is connected.

### Consequences
- Positive: one hand-over for the common case; both approvals remain explicit; the contact code remains for families that are already registered.
- Negative / accepted trade-offs:
  - **The inviting guardian approves before knowing who registers.** Whoever obtains the link can register a family and become a contact of the named child without the inviting family confirming a name. With the contact code the inviting family at least sees a self-chosen display name before confirming; here it sees the name only afterwards, on the contact page.
  - **The inviting family learns of the contact only afterwards**, by e-mail and on the contact page. Until a guardian reads the mail and acts, the children can write to each other. A mail can be delayed, filtered or lost, and nothing in the application marks the contact as new. A registration that connects nobody is not announced.
  - The first name of the inviting child is disclosed to everybody who opens the link, before any mailbox is confirmed.
  - Another guardian of the inviting child is not asked. Unlike with the contact code, that guardian is told by the same e-mail. Where guardians can write to each other, the new guardian can also write to that other guardian.
  - Both guardians are self-declared ([invited self-registration ADR draft](draft-invited-self-registration.md)); a contact made this way joins two families of which at most one was checked by the operator.
  - The link is valid for seven days, much longer than a contact code (24 hours).
  - A crash between storing the identity and creating the contact leaves a registered family without the contact; the families then use a contact code.
- Open questions and review gates:
  - Whether the contact should be marked as new in the application until a guardian has looked at it, and whether the children may write before that.
  - Whether option 3 is required for real children, or a shorter validity for invitations that name a child.
  - Whether the inviting child's name may be shown before the mailbox is confirmed.
  - Notification and consent of further guardians of both children.
  - Threat model and privacy review before any real child uses it.

## Links
- Issues [#2](https://github.com/pblumer/yapuno/issues/2), [#3](https://github.com/pblumer/yapuno/issues/3), [#12](https://github.com/pblumer/yapuno/issues/12)
- [Invited self-registration (planning)](../planning/invited-self-registration.md)
- [Invited self-registration ADR draft](draft-invited-self-registration.md), [contact invitations ADR draft](draft-contact-invitations.md), [guardian chat ADR draft](draft-guardian-chat.md)
