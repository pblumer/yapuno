# ADR-DRAFT: Guardians as chat participants with their own child and with guardians of connected families

- **Status:** Proposed
- **Implementation:** First slice on a branch: role-aware authorization predicates, a separate message log, guardian chat pages, guardian conversations in the child area (pending review)
- **Date:** 2026-11-22
- **Deciders:** maintainers; security, privacy and child-safety reviewers required

## Context and problem statement
Issue [#12](https://github.com/pblumer/yapuno/issues/12) requires three first-class conversation pairs: child with child, guardian with child, adult with adult. Only the first existed ([message storage ADR draft](draft-message-storage.md)): senders were children on paired devices and every conversation was a stored contact between two children. Guardians administered and could not write.

Adding adults to chat touches *bounded trust* and *privacy by default*: a guardian's authority over a child must not become access to that child's conversations, and adults of different families must not gain a way to find or contact each other, or each other's children.

## Decision drivers
- A guardian's administrative authority and a guardian's own conversations stay separate.
- No new discovery: adults reach only people they are already connected to through decisions both families made.
- Deny by default, one authorization rule per pair type, evaluated for every request from current state.
- No adult reaches a child of another family.
- The existing children's chat, its log format and its tests are not put at risk.
- Durable before acknowledged; standard library only.

## Considered options
**Which pairs**
1. Guardian with own child, and guardians of connected families (proposed).
2. Also an adult with a child of another family. Named in #12; needs the child's guardian's approval and the adult's consent, and is the pair with the highest risk. Deferred.
3. Also guardians of the same family with each other. Harmless; no relationship record exists for it yet. Deferred.

**How two adults of different families become reachable**
1. Derived from a contact between their children that both families approved (proposed). No new workflow, no directory, ends with the contact.
2. A separate opt-in between adults, as #12 sketches. Explicit; needs its own invitation flow and block list.
3. Free contact between registered adults. Rejected: discovery.

**Where messages live**
1. A second, role-aware store with its own log (proposed).
2. Generalizing the children's store to typed participants. The right end state; changes its sender model and log format and ripples through reports and the child pages.

**What the children's block means for the adults**
1. The adults keep their conversation while the children's contact is paused (proposed): that is when they need to talk.
2. A block pauses everything. Simpler; removes the channel exactly when it is useful.

## Proposed decision outcome
Chosen options (product owner decision of 2026-11-22: all three chat kinds in one step): guardian with own child and guardians of connected families; reachability derived from the children's contact; a separate store; a block does not silence the adults.

### Rules
- Participants are tenant-qualified and typed (`authz.Participant`, adult or child). Two predicates decide: `CanUseFamilyChat` and `CanUseParentsChat`. `CanContact` for children is unchanged.
- **Guardian with own child:** same tenant; a current guardian grant for exactly this child (no management capability required); the guardian identity active and not locked; the child profile exists; the child acts through a currently approved device.
- **Guardians of connected families:** different tenants; an approved, not ended contact between one child of each; each guardian currently holds contact authority for their family's child in that contact; both identities active and not locked. A block between the children does not affect it.
- Nothing else is allowed. In particular no adult talks to a child of another family, and no child reads a conversation between adults.
- Conversations are **not stored as records**. For every request the store computes which conversations the actor has right now and admits the request only if the requested one is among them. Ending a contact, revoking a grant, locking a guardian or revoking a device takes effect on the next request.
- Each pair has its own conversation. A second guardian of the same child has a conversation of their own with that child and cannot read the first guardian's. **Guardians cannot read what children write to each other**; these pages only show conversations the guardian takes part in.
- A conversation id is a digest of the two participants. It is not a secret and grants nothing.
- Messages go to a separate private log (`YAPUNO_GUARDIAN_CHAT_FILE`): appended and synced before they are acknowledged, idempotent per sender and client id, server-assigned sequence numbers, at most 20 messages per minute and sender, 2000 per conversation, 30 days. The numbers equal the children's chat and are placeholders.
- When a relationship has ended, its messages are deleted at the next compaction; ending a contact triggers one. A locked guardian's messages are kept, because a lock can be lifted.
- The child sees its guardians under "Deine Eltern" on the same chat page as its contacts, without a block or report function. The guardian sees "Chats" in the guardian area. Both use one shared chat page and script.
- The other family's guardian is described only through the connected children ("Elternteil von Bea") and a self-chosen figure. No address, id or name is shown.

### Consequences
- Positive: guardians and children can talk inside the product; the two families' guardians have a channel that needs no exchange of phone numbers; no discovery and no adult-to-foreign-child path; the children's chat is untouched.
- Negative / accepted trade-offs:
  - Two message stores exist with duplicated log mechanics. They should be unified once the role-aware model has been reviewed.
  - Approving a contact between children now also opens a channel between the guardians, which the approval page does not say yet.
  - Guardians have no display name. A child with two guardians sees "Elternteil 1" and "Elternteil 2" with their figures. (A later draft proposes optional, self-chosen names: [guardian names](draft-guardian-names.md).)
  - A child cannot block or report a guardian. A child who needs protection from a guardian finds no help in the product.
  - No notification, unread marker or read receipt: a message is seen when the page is open.
  - Messages are plaintext on the server, as in the children's chat.
  - A conversation id is stable; if a contact ends and the purge fails, and the families reconnect before the next compaction, old messages reappear.
- Open questions and review gates:
  - **Child protection:** an escalation path for a child at risk from a guardian; whether a child may mute a guardian.
  - **Consent between adults:** whether deriving reachability from the children's contact is acceptable, or an explicit opt-in and a block between adults are required; what the approval page must say.
  - Adult with a child of another family, and co-guardians with each other.
  - Guardian display names and how a child tells its guardians apart.
  - Unifying the two stores; a typed sender model for reports.
  - Retention, deletion on request and evidence for conversations between adults.
  - Usability evidence with children and guardians; real-device tests.

## Links
- Issues [#12](https://github.com/pblumer/yapuno/issues/12), [#3](https://github.com/pblumer/yapuno/issues/3), [#4](https://github.com/pblumer/yapuno/issues/4), [#7](https://github.com/pblumer/yapuno/issues/7)
- [Guardian chat](../planning/guardian-chat.md), [messaging preview](../planning/messaging-preview.md)
- [Guardian contact trust ADR draft](draft-guardian-contact-trust.md), [contact invitations ADR draft](draft-contact-invitations.md), [message storage ADR draft](draft-message-storage.md), [guardian access lock ADR draft](draft-guardian-access-lock.md)
