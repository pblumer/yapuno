# Notifications for children's devices (Web Push)

**Status:** engineering preview for synthetic families, optional and off by default. A child switches content-free notifications on for its own paired device. When somebody writes to the child, the device shows "Du hast eine neue Nachricht." Decision record: [ADR draft: Web Push](../adr/draft-web-push.md).

> Nobody may be promised notifications before they have been tested on a real iPhone and a real Android phone (see "Not verified").

## Enable

Create the private signing key once, offline, in the private directory (mode `0700`):

```sh
go run ./cmd/yapuno-pushkey private/push-key.pem
```

The command writes a new file with mode `0600`, refuses to overwrite an existing one and prints the public key, which is not secret. Keep the private key out of version control and back it up: a new key invalidates every subscription, and children then have to switch notifications on again.

```sh
export YAPUNO_PUSH_KEY_FILE=private/push-key.pem
export YAPUNO_PUSH_SUBSCRIPTIONS_FILE=private/push.json
```

Both variables go together. They require `YAPUNO_PAIRING_FILE` and at least one chat (`YAPUNO_MESSAGES_FILE` or `YAPUNO_GUARDIAN_CHAT_FILE`). The server needs outbound HTTPS to the push services (`*.push.apple.com`, `fcm.googleapis.com`, `*.push.services.mozilla.com`, `*.notify.windows.com`). Without the variables there is no settings page, worker or subscription route, and the family page lists "Benachrichtigungen" as switched off.

## Flow

1. The child opens **Benachrichtigungen** on its start page (`/child/notifications`) and taps **Einschalten**. The browser asks for permission.
2. The page sends only the endpoint of the browser's subscription. The server stores it for the device of the session.
3. A message is stored. Then the server sends an empty push to every subscribed device of the recipient child, after checking that the device is still approved and the child may read the conversation now.
4. The device shows "Yapuno – Du hast eine neue Nachricht." A tap opens Yapuno.

| Message from | to | Notice to |
|---|---|---|
| a child | a connected child | the recipient child's devices |
| a guardian | their own child | the child's devices |
| a child | its guardian | nobody (guardians have no device binding yet) |
| a guardian | a guardian of another family | nobody |

### iPhone and iPad

Safari delivers notifications only to Yapuno opened from the Home Screen: **Teilen → Zum Home-Bildschirm → Hinzufügen**, then open it from the new icon. The page explains this when it is opened in Safari. **The Home Screen app does not share storage with Safari, so the device has to be paired again inside the app.** The guardians then see two devices for the child and can remove the Safari one.

### Android

Chrome, Firefox, Edge and Samsung Internet deliver notifications without installation. Installing from the browser menu still gives the app its own icon.

## What is sent and stored

- **Push:** an empty `POST` to the endpoint with a VAPID signature, `TTL: 43200`, `Urgency: high` and `Topic: yapuno-new-message`. No text, no name, no identifier of a child, device or conversation.
- **File:** one line per device: family, child and device identifiers and the endpoint URL. No payload keys, nothing about messages.
- **Logs:** that a push failed and the status code, never an endpoint or identifier.

## Ends

- **Ausschalten** on the page, or signing out on the device: the subscription is removed at once.
- A device a guardian removes or reports lost: gets nothing from that moment on; its subscription is removed at the next delivery attempt and at server start.
- Deleting the child or the family: removed with the rest ([what is deleted](self-service-deletion.md)).
- The push service reports the subscription gone (`404`, `410`): removed.

## Verified

Automated tests for signing, endpoint rules, the subscription file, authorization at delivery (contact removed, blocked, device revoked), merging, the bounded queue, sign-out and deletion. One scripted run in headless Chromium against the real handler over TLS, with the browser's push service replaced by a stand-in: settings page at 320 and 390 px and at 200 % text without horizontal overflow, buttons at least 44 × 44 px, worker registered for `/child/`, switching on and off stored on the server, an iPhone in Safari shown the installation steps, a push delivered to the real worker showing the fixed sentence and ignoring a payload, no script errors.

## Not verified

- Real iPhone (Home Screen app) and real Android phone, including that **a push without payload is shown on iOS**, the locked screen, Focus modes, Screen Time restrictions on a child's device, and battery optimisation of several Android vendors.
- Delivery through the real push services of Apple, Google, Mozilla and Microsoft.
- Screen readers on the settings page.

## Known limits

- The notice never says who wrote or what.
- Delivery is best effort: no retry, and a full queue drops notices.
- A renewed browser subscription is stored only when the child opens the settings page again.
- A child is notified even while the chat is open on the same device.
- Guardians get no notifications. Quiet hours and guardian control are open questions in the ADR.
