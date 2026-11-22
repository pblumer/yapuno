# Guardian chat and the family overview

**Status:** engineering preview for synthetic families. Guardians write with their own children and with the guardians of families whose children are connected to theirs. Decision record: [ADR draft: guardian chat](../adr/draft-guardian-chat.md).

## Enable

```sh
export YAPUNO_GUARDIAN_CHAT_FILE=private/guardian-chat.log
```

The log is created with mode `0600`. It works on its own; with `YAPUNO_PAIRING_FILE` children take part from their paired devices, and with `YAPUNO_CONTACTS_FILE` guardians of connected families reach each other.

## Who talks to whom

| Pair | Allowed when |
|---|---|
| Child with child | both families approved the contact (unchanged, `YAPUNO_MESSAGES_FILE`) |
| Guardian with own child | the guardian holds a current grant for the child; the child uses an approved device |
| Guardians of two families | a child of each is connected by an approved contact for which each guardian holds contact authority |

Everything else is refused: an adult never writes to a child of another family, a child never reads what adults write, and a second guardian of the same child has a conversation of their own.

**Guardians cannot read their children's chats with other children.** "Chats" only lists conversations the guardian takes part in.

## Where it is

- Guardian: **Familie → Chats** (`/family/chat`), or **Mit … schreiben** on the child's card. Other guardians appear as "Elternteil von …" as soon as the children are connected.
- Child: **Deine Eltern** on the start page (`/child`). With two guardians they are shown as "Elternteil 1" and "Elternteil 2".
- Both sides use the same chat page as the children's chat: messages appear without reloading, sending shows a state and can be retried, and the page works without JavaScript by form post.

## What ends a conversation

Every request is authorized again, so each of these applies at once:

- the contact between the children ends: the guardians' conversation ends and its messages are deleted;
- a guardian locks their access or is deactivated: nobody writes to or as that guardian (messages are kept);
- a child's device is locked: that device no longer reads or writes;
- a child blocks another child: the children's chat pauses, **the guardians keep theirs**.

Messages are deleted after 30 days; at most 2000 are kept per conversation and 20 per minute and sender are accepted.

## The family overview

`/family` was a placeholder: an English dashboard that filled the screen and said nothing could be edited, with the real functions as unstyled links below it. It is now the start page of the guardian area:

- one card per child with every action the guardian's grant allows (devices, contacts with the number of open reports, chat, lost device, rename);
- a "Familie" card (chats, further child, invite a family, look, security);
- a four-step guide to the first chat that names only what this installation offers;
- a line that lists functions that are not switched on, and the build version when the binary was built with `-ldflags "-X main.version=…"`.

## Known limits

- Guardians have no display name.
- No notification, unread marker or read receipt.
- A child cannot block or report a guardian.
- Messages are plaintext on the server and there is no moderation.
- Two message stores exist side by side; see the ADR draft.
- Verified in a real desktop browser at 320, 375 and 430 px, including the whole path from one family to three kinds of chat; not yet on real phones or with a screen reader.
