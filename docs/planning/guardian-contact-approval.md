# Guardian contact approval — browser preview

**Status:** engineering preview for synthetic child profiles. Guardians of two families can connect two children through a one-time code and both families' approval. Children see approved contacts on their own device and, when the message store is enabled, can write to them ([messaging preview](messaging-preview.md)). Decision record: [ADR draft: contact invitations](../adr/draft-contact-invitations.md).

## Enable the contact pages

With the private data directory from [guardian login setup](guardian-browser-login-runtime.md), add:

```sh
export YAPUNO_CONTACTS_FILE=private/contacts.json
go run ./cmd/yapuno-web
```

The file is created with mode `0600` on first use. Without `YAPUNO_CONTACTS_FILE` the contact pages and links are not mounted.

## Flow

1. Guardian A opens **Familie → Kontakte von … verwalten** and chooses **Einladungscode erstellen**. The code is shown once and is valid for 24 hours and a single use.
2. Guardian A passes the code to the other family in person or by phone.
3. Guardian B opens the contact page of their own child and enters the code under **Code der anderen Familie**. This is family B's approval.
4. Guardian A sees the request with the other child's display name under **Wartet auf eure Zustimmung**, ticks the confirmation and chooses **Freigeben**. Only now is the contact active.
5. Either family can decline, withdraw or remove at any time. Reconnecting needs a new code.

## Security properties (tested)

- Guardian and tenant come only from the verified session; forged identity fields in forms are ignored. The child named in a request is authorized against current grants on every call, and operator revocation of a guardian blocks all pages on the next request.
- One-sided approval never allows contact; the redeeming family cannot confirm its own request; a guardian cannot act through a sibling or another family's child.
- Codes are single use, expire, are stored only as digests, and are limited to 5 open codes per child. A rejected redeem does not consume the code; a second redeemer is told it was already used.
- The other family's tenant, child and guardian identifiers never reach the page; only a display name and a random request id do. Failed redeems do not reveal who is behind a code.
- CSRF token, form-only content type, 4 KiB body limit, exactly one value per field, explicit confirmation field for confirm and remove.
- The store fails closed on corrupt content, unknown fields, duplicate relationships, self-links and files readable by others.
- A removed child profile ends its contacts; concurrent redeems have exactly one winner; a removal is never lost against a concurrent confirmation.

## Limitations

- A display name is not proof of identity; see the ADR for the interception scenario and mitigations.
- No notification to co-guardians, no audit history, no rate limiting, single server process.
- Deleted records leave no trace; retention and incident evidence are open (#7).
- Guardian ↔ child and adult ↔ adult contacts (#12), groups and child-side block/report are not implemented.
- Messaging authorizes every send and read from the stored grant (`contacts.Store.Grant`) together with device and profile state; see the [messaging preview](messaging-preview.md).

## MFE evidence (headless Chromium, not a physical device)

- Pending, active, invitation and error states at 320, 375 and 430 CSS px: no horizontal overflow, including a 39-character child name; all buttons, links, inputs and checkbox rows at least 44 px.
- Works without JavaScript; uses the guardian's saved skin (classic and night reviewed); status and error messages use `role="status"` / `role="alert"` and text, not color alone.
- **Not verified:** physical iPhone/Android, screen readers, large text settings, landscape, on-screen keyboard behavior for the code field.

Related #3, #12, #4, #5.
