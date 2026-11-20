# One-to-one messaging — browser preview

**Status:** engineering preview for synthetic child profiles. Two children whose families approved the contact can exchange text messages from their paired devices. This is the first end-to-end "First Hello" path in code. **It is not safe for real children:** only a first [report and block](report-and-block.md) slice without moderation, no encryption at rest, no rate limiting beyond a per-minute cap, no real-device evidence. Decision record: [ADR draft: message storage](../adr/draft-message-storage.md).

## Enable

```sh
export YAPUNO_PAIRING_FILE=private/pairing.json
export YAPUNO_CONTACTS_FILE=private/contacts.json
export YAPUNO_MESSAGES_FILE=private/messages.log
go run ./cmd/yapuno-web
```

`YAPUNO_MESSAGES_FILE` requires the pairing and contact stores; the server refuses to start otherwise. The log is created with mode `0600`. Without it, chat routes and links do not exist.

## Flow

1. Guardians pair each child's device and connect the two children ([contact approval](guardian-contact-approval.md)).
2. Each child opens `/child`, is signed in with the device key ([child device sessions](child-device-sessions.md)) and taps a name under **Deine Crew**.
3. The chat page shows the last 50 messages. Sending shows **Wird gesendet …**, then **Gesendet · time** once the server has stored the message, or **Nicht gesendet** with **Erneut senden**.
4. The page checks for new messages every 3 seconds while it is visible and slows down when the server cannot be reached.
5. When either family ends the contact, both chats stop on the next request and the messages are deleted from disk.

Without JavaScript the page still works: the form posts, the server redirects back, and a double submit stores one message.

## Security properties (tested)

- **Every send and read is authorized from current state**: the stored contact, both child profiles and the approval of the device in use. Denied alike: outsiders with their own approved device, siblings, pending contacts, unknown conversations, another child's or a revoked device, forged tenant or child, removed profiles.
- **No guardian access:** there is no route to read messages with a guardian session; the test asserts `401` for both guardians.
- **Idempotency:** the same client id and text returns the original message; a different text is a conflict; a retry is authorized again; idempotency survives a restart.
- **Durable before acknowledged:** each acknowledged message was written and then synced; a failed write or sync is not acknowledged, leaves the log unchanged and can be retried once.
- **Crash recovery:** a partial final line is removed on start; any corrupt complete line, unknown field, non-increasing sequence or insecure file prevents start.
- **Revocation races:** after a device revocation or contact removal has returned, no send that starts afterwards succeeds, with concurrent senders running.
- **Content is never markup:** stored as typed, escaped by the server template, placed with `textContent` by the script. The scripts contain no `innerHTML`, `eval` or similar. The chat page allows only same-origin scripts.
- **Responses carry no identifiers:** history says only whether a message is the reader's own.
- Form sends need the session's CSRF token; JSON sends need the exact HTTPS origin and the token in a header.
- 31 store mutations and 14 web mutations (for example authorization removed, device not checked, sync removed, idempotency off, CSRF off, body unescaped) are each detected by the tests. One further web mutation is behaviorally equivalent because the store denies the same request.

## Browser evidence (headless Chromium, not a physical device)

One scripted run through the real server over TLS: both devices paired and signed in inside the browser, chat at 320 px and 390 px, **26 checks passed**, including: confirmation only after the server stored the message; delivery by polling; hostile markup inert in live and server-rendered form; offline send shown as not sent, not delivered, then delivered exactly once after retry; **a lost response followed by a retry leaves exactly one message on both screens and on disk**; draft survives a reload; long and multi-line text without overflow; each child's own theme; form send without JavaScript; identical order on both sides; a message written during a lost session delivered once after renewed sign-in; contact removal disabling the open chat within one poll and deleting the messages from disk; no script errors.

**Not verified:** physical iPhone/Android, on-screen keyboard and safe-area behavior, background and resume, screen readers, large text, landscape, low-data mode, school Wi-Fi.

## Limitations

- **Plaintext on the server and on disk.** No end-to-end encryption, no encryption at rest.
- Children can block a contact and report single messages to their own guardians ([report and block](report-and-block.md)). There is no moderation, escalation or link rule; these are required before a pilot.
- Polling only; no push notifications, no realtime stream.
- "Gesendet" means stored by the server, not delivered or read.
- Retention (30 days, newest 2000 per conversation) and deletion rules are provisional (#7).
- Unsent texts are kept in the browser's session storage of that device until confirmed.
- Single server process; all messages are indexed in memory.

Related #4, #3, #2, #5, #7.
