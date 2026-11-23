# ADR-DRAFT: Guardians choose a name for their children and a name for other parents

- **Status:** Proposed
- **Implementation:** Optional, off by default: a private store of names, a guardian page, an optional field at registration, display in both chats (pending review)
- **Date:** 2026-11-23
- **Deciders:** maintainers; privacy and child-safety reviewers required

## Context and problem statement
Guardians had no name in the application ([guardian chat ADR draft](draft-guardian-chat.md)). A child saw "Dein Elternteil", or "Elternteil 1" and "Elternteil 2", and a guardian of a connected family saw "Elternteil von Bea". That is safe, because the application only says what it knows, but in a trial it reads like a form: nobody could tell which of two guardians had written, and the product owner asked for real names.

A name is new personal data about an adult, and it is shown to other people. Whoever chooses it can choose anything. This draft records who sees which name and what a name is allowed to mean.

## Decision drivers
- People should recognise each other in a chat.
- What a child calls a guardian ("Mami") is not what another adult should call them, and the other way round.
- A name must not replace what the application knows. A self-chosen name alone could claim anything.
- Nothing is shown to anybody who has no relationship with the guardian. No directory, no search.
- Privacy by default: no name unless the guardian gives one, and it can be removed.
- Installations that do not configure the feature behave exactly as before. Standard library only.

## Considered options
**How many names**
1. One name for everybody. Simplest; either the children read a first name or other adults read "Mami".
2. Two names: one for the guardian's own children, one for guardians of connected families (proposed).
3. Only a name for other adults. Children keep the neutral label.

**When a name is given**
1. Optional, at registration and at any time afterwards (proposed).
2. Required at registration. Every guardian is named; forces people to disclose something to use the product, and guardians who exist already have none anyway.
3. Only afterwards on the family page. Keeps registration short; the first conversation with the inviting family starts without a name.

**How a name is shown to other adults**
1. The name alone. Reads best; "Yapuno Support" or the name of another parent would stand there unchallenged.
2. The name together with the relation the application derives itself, "Elternteil von Bea" (proposed).

**Where the names are kept**
1. In the guardian directory, next to the address. No new file; mixes what decides who may sign in with what somebody likes to be called, and the provisioning tool rewrites that file.
2. In a store of their own (proposed).

## Proposed decision outcome
Chosen options (product owner decisions of 2026-11-23): two names; optional at registration and changeable at any time. Showing the relation next to the name and the separate store are the implementer's proposals.

### Rules
- The feature exists only while `YAPUNO_GUARDIAN_NAMES_FILE` is configured, and it requires the guardians' chat, the only place where names are shown. Without it no page, field or link of this feature is served.
- A guardian has a name **for their own children** and a name **for other parents**. Both are optional and independent. An empty field removes the name.
- The name for the children is shown to the guardian's own children: in the list "Deine Eltern" and as the heading of that chat. Without it the neutral label stays.
- The name for other parents is shown to guardians with whom a conversation exists, that is guardians of families whose children are connected to this guardian's. It always appears together with "Elternteil von …" and the names of the connecting children. When the last connection ends, the conversation and with it the name disappear for that family.
- Nobody else sees either name: no child of another family, no family without a connection, and the names appear in no e-mail.
- A guardian sets only their own names. Guardian and family come from the session; a form cannot name somebody else.
- A name has at most 40 characters and contains no angle brackets and no control or formatting characters (which could hide or reorder text). Surrounding spaces are removed. Pages render names as text.
- The registration form has one optional field, the name for other parents. It is kept with the unconfirmed registration in memory and stored once the family exists. If it cannot be stored the registration stands.
- The names are stored per guardian in a private file (mode `0600`, temporary file, fsync, atomic rename), fail closed on a file that is not private or malformed, and are removed when the family is deleted ([self-service deletion ADR draft](draft-self-service-deletion.md)).
- The pages say that names are not checked.

### Consequences
- Positive: guardians and children recognise who writes; a child with two guardians can tell them apart; another family reads a name next to a relation it can check with its own child; nothing is disclosed that the guardian did not type.
- Negative / accepted trade-offs:
  - **A name proves nothing.** A guardian can call themselves anything, including the name of another parent or of an institution. The relation next to the name limits this towards adults. Towards the guardian's own children nothing limits it: two guardians of a child can both be "Mami", and a guardian can take the other's name.
  - The name for other parents is shown as soon as the children are connected, which with an invitation that connects the children is the moment of registration. The guardian is told on the form who will see it.
  - Guardians of one family do not see each other's names anywhere, so one guardian cannot notice what name the other shows to their child.
  - A name stays visible in the heading of a conversation as it is now, not as it was when a message was written. A changed name changes how old messages read.
  - Children still have one name for everybody.
  - Names are plaintext on the server.
- Open questions and review gates:
  - Whether a child's guardians should see each other's name for that child, and whether duplicates should be refused.
  - Whether changing a name should be visible to the other side, or limited in frequency.
  - Names in e-mail notices, which so far name nobody but the recipient's own child.
  - Showing the guardian's name on the contact page of the other family.
  - Verified identity for guardians, of which a self-chosen name is no part.
  - Threat model and privacy review before any real child uses it.

## Links
- Issues [#12](https://github.com/pblumer/yapuno/issues/12), [#13](https://github.com/pblumer/yapuno/issues/13)
- [Guardian names (planning)](../planning/guardian-names.md), [guardian chat](../planning/guardian-chat.md)
- [Guardian chat ADR draft](draft-guardian-chat.md), [invited self-registration ADR draft](draft-invited-self-registration.md), [self-service deletion ADR draft](draft-self-service-deletion.md)
