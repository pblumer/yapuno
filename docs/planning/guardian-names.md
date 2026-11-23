# Guardian names

**Status:** engineering preview for synthetic families, optional. A guardian chooses a name for their own children and a name for the guardians of connected families. Decision record: [ADR draft: guardian names](../adr/draft-guardian-names.md).

> Names are chosen freely and checked by nobody. A name says what somebody wants to be called, not who somebody is.

## Enable

```sh
export YAPUNO_GUARDIAN_NAMES_FILE=private/guardian-names.json
```

The file is created empty with mode `0600` at start. It requires `YAPUNO_GUARDIAN_CHAT_FILE`; the server does not start with names but without the guardians' chat. Without the variable none of the pages and fields below exist and the family page lists "Namen für Eltern" as switched off.

## Flow

- **Familie → Mein Name** (`/family/name`) has two fields, **Für deine Kinder** and **Für andere Eltern**. Both are optional; an empty field removes that name. The family page shows the current names on the row that leads there.
- With [invited self-registration](invited-self-registration.md), the registration form has one optional field **Dein Name für andere Eltern**. It is stored when the family is created.

## Who sees what

| Name | Seen by | Where | Without a name |
|---|---|---|---|
| for the children | the guardian's own children | list "Deine Eltern" on the child's start page; heading of that chat | "Dein Elternteil", or "Elternteil 1", "Elternteil 2" |
| for other parents | guardians of families whose children are connected to this guardian's | list "Andere Eltern" and the heading of that chat, always with "Elternteil von …" | "Elternteil von …" |

Nobody else sees a name: not the children of other families, not families without a connection, not the other guardian of the same child. No e-mail contains a name. When the last connection between two families ends, the conversation and the name disappear for the other family.

## Rules

- A guardian changes only their own names; the session decides whose they are.
- At most 40 characters, no angle brackets, no control or formatting characters. Letters of any script and emoji are fine.
- The file holds, per guardian, the family and guardian identifiers and the two names. It is rewritten atomically and refused at start if it is not private, is a symbolic link or is malformed.
- Deleting the family removes the names ([what is deleted](self-service-deletion.md)).

## Known limits

- A name proves nothing. The relation shown next to it ("Elternteil von Bea") is what the application knows.
- Two guardians of a child can choose the same name for that child, and neither sees the other's.
- A changed name changes the heading of old conversations.
- Verified in a real desktop browser at 320, 375 and 430 px; not on real phones.
