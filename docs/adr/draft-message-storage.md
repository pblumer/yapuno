# ADR-DRAFT: Durable, idempotent one-to-one messages with per-request authorization

- **Status:** Proposed
- **Implementation:** First slice on a branch: message store, HTTP send and history for child sessions, minimal chat page (pending review). No realtime push, no media, no groups.
- **Date:** 2026-11-20
- **Deciders:** maintainers; privacy and child-safety review required before real use

## Context and problem statement
Issue [#4](https://github.com/pblumer/yapuno/issues/4) asks for durable one-to-one text between two mutually approved children: authorization on every send and receive, authoritative ids, idempotency, ordering, acknowledgement only after a durable write, catch-up after reconnect, limits, and nothing delivered to revoked devices.

The pieces now exist: tenant-qualified contacts ([contact invitations](draft-contact-invitations.md)) and key-bound child sessions ([child device sessions](draft-child-device-sessions.md)). `authz.CanSendMessage` still assumed one tenant and therefore denied every cross-family message. The data store is undecided (architecture: "candidate PostgreSQL; ADR pending"), and retention, reporting and encryption are open in issue #7 and the [content safety ADR](draft-content-safety-privacy.md).

## Decision drivers
- Deny by default on every read and write, at the moment of the action (invariants 1 and 2).
- Durable before acknowledged; retries must not duplicate (invariant 8).
- Revocation of a device or a contact must stop both sending and reading.
- No guardian or operator reading feature; minimum data (invariant 5).
- Standard library only until a storage ADR chooses a database.
- No claims the design does not support: no end-to-end encryption, no delivery or read receipts.

## Considered options
1. **Append-only log file with fsync per message, in-memory index** (proposed for the preview).
2. **Rewrite one JSON file per message** like the other preview stores: simplest, but every send rewrites all history.
3. **Introduce PostgreSQL or SQLite now:** the right long-term direction, but a new dependency and operational model that deserves its own ADR and should not be decided inside a feature slice.
4. **End-to-end encrypted messages now:** needs key agreement, multi-device and recovery design and a reviewed library; it also conflicts with the open reporting and safety questions. Not attempted.

## Proposed decision outcome
Chosen option (if approved): **option 1 as a preview store**, with the interface kept narrow so a transactional backend can replace it.

### Authorization
- `authz.ConversationAccess` replaces the single-tenant `MessageAccess`: `Self`, `Peer` (tenant-qualified), both profiles enabled, device approved, and the stored `ContactGrant`. `CanSendMessage` and `CanReadConversation` are separate predicates with the same requirements today.
- A **conversation is the contact record**: its id is the contact's random id. There is no separate membership list to drift.
- For every `Send`, `History` and `Recent`, the store loads the grant for the acting child, checks that both profiles exist and that the acting device is currently approved for that child, and applies the predicate. Unknown conversations, pending contacts and outsiders all get the same `ErrForbidden`.
- The acting child and device come only from a verified child session.

### Message model
- Server-assigned **sequence number per conversation** (1, 2, 3 …) is the authoritative order and the read cursor. Server time is stored for display only.
- The client supplies a random **client id** per message. `(conversation, sender, client id)` is unique: a retry with the same text returns the original message and writes nothing; the same client id with different text is a conflict. A retry is authorized again like any send.
- Text only: 1–1000 characters, valid UTF-8, LF line endings, surrounding whitespace removed. Control characters other than newline and tab, and bidirectional override characters, are rejected. Content is stored as typed; escaping is the renderer's job.
- Limits: 20 messages per minute per sender and conversation.

### Durability
- A message is appended as one JSON line and `fsync`ed before `Send` returns. If the write or sync fails, the log is cut back to its previous length, nothing becomes visible, and the caller gets an error; a retry succeeds exactly once.
- On start, every complete line must parse and be consistent (strictly increasing sequence, valid sender, body and client id, no unknown fields), otherwise the store refuses to open. A partial final line is the trace of a crash during an unacknowledged append and is removed.
- The file must be private (`0600`).

### Retention and deletion (provisional)
- Messages older than **30 days**, and messages beyond the newest **2000** per conversation, are dropped at compaction (on start and when a conversation outgrows its cap).
- When a contact ends, its messages are **inaccessible immediately** and are **deleted from disk** at the next compaction, which the guardian contact pages trigger on removal. Reconnecting creates a new, empty conversation.
- A marker keeps the highest used sequence number when all messages of a conversation have expired, so numbering never restarts and client cursors stay valid.
- These numbers are placeholders until issue #7 decides retention, reporting and incident evidence.

### Transport in this slice
- Authenticated HTTP only: `POST` to send, `GET` with a cursor to read; the chat page polls while visible. This is the "robust fallback" of the MFE spec and needs no dependency.
- **Realtime delivery is not decided here.** WebSocket needs a library or a hand-written implementation (a dependency decision); Server-Sent Events are available in the standard library. Whichever is chosen must call the same read authorization on connect and for every delivered message.

### What this does not provide
- **No end-to-end encryption and no encryption at rest.** Message text is readable by the server process and by anyone with access to the data directory. Encryption at rest is required before any real pilot.
- No delivery or read receipts; "sent" means stored by the server.
- No moderation, link rules or general guardian visibility. Block and single-message report are a separate proposal ([report and block ADR draft](draft-report-and-block.md)).
- No push notifications, media, edits, deletes or groups.

### Consequences
- Positive: every acceptance point of #4 that concerns authorization, idempotency, ordering and durability is implemented and tested without new dependencies; contact or device revocation stops reads and writes on the next call.
- Negative / accepted trade-offs: single process, all messages indexed in memory, compaction rewrites the whole log; polling adds delay and load; plaintext storage.
- Open questions and review gates:
  - Storage ADR (PostgreSQL or SQLite), backups and how expiry reaches them.
  - Retention period, deletion on account removal, export, incident evidence holds (#7).
  - Reporting and blocking by children before a pilot (MFE acceptance matrix): first slice in the [report and block ADR draft](draft-report-and-block.md); evidence with children still required.
  - Realtime transport and its authorization during long-lived streams.
  - Encryption at rest, and whether end-to-end encryption is compatible with the safety model.
  - Abuse limits beyond the per-minute cap; storage quotas per family.

## Links
- Issues [#4](https://github.com/pblumer/yapuno/issues/4), [#3](https://github.com/pblumer/yapuno/issues/3), [#7](https://github.com/pblumer/yapuno/issues/7), [#12](https://github.com/pblumer/yapuno/issues/12)
- [Contact invitations ADR draft](draft-contact-invitations.md), [child device sessions ADR draft](draft-child-device-sessions.md), [content safety ADR](draft-content-safety-privacy.md)
- [Messaging preview (planning)](../planning/messaging-preview.md)
