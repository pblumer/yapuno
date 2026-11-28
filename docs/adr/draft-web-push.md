# ADR-DRAFT: Content-free Web Push notifications for children's devices

- **Status:** Proposed
- **Implementation:** First slice on a branch, optional and off by default: installable web app manifest, service worker, a child's opt-in page, device-bound subscriptions in a private file, content-free pushes to the recipient child's devices after a message is durable (pending review). Guardians receive no pushes yet.
- **Date:** 2026-11-28
- **Deciders:** maintainers; privacy, child-safety and security reviewers required

## Context and problem statement
A child only sees a new message while the chat page is open: the page polls ([messaging preview](../planning/messaging-preview.md)). Nothing tells a child on a locked phone that somebody wrote. People expect the behaviour of common messengers, and the product owner asked whether this requires native iOS and Android apps.

The [Mobile First Experience ADR draft](draft-mobile-first-experience.md) prefers an installable PWA and asks for evidence before relying on push. Since iOS/iPadOS 16.4, Safari delivers Web Push to web apps that were added to the Home Screen; Chrome, Firefox, Edge and Samsung Internet on Android deliver it to web pages and installed web apps. The transport is the standard Web Push protocol (RFC 8030) with VAPID application server identification (RFC 8292). Each browser vendor operates the push service that relays the message to the device (Apple, Google FCM, Mozilla, Microsoft).

A push therefore leaves the Yapuno server and passes a third party. That third party learns at least that some server contacts a given device at a given time. Children's messages, names and relationships must not travel with it.

## Decision drivers
- A child should notice a new message without keeping the page open.
- Privacy by default: no message text, no names and no identifiers in a push, on a lock screen or at a push service.
- Deny by default: a push is delivery. It needs the same current authorization as reading the conversation, and revocation must end it (invariants 1 and 4).
- Durable before acknowledged: a push is sent only for a message that is stored, and it never delays or fails the acknowledgement of a send.
- Do not invent crypto: use standard primitives in standard constructions only.
- No native app, SDK or tracker unless a PWA demonstrably cannot do the job. Standard library only.
- Installations that do not configure the feature keep working as before.

## Considered options
**Delivery channel**
1. Native iOS and Android apps with APNs and FCM. Reliable and familiar, and they would also give hardware-backed device keys, store distribution and calls later. They mean two new client code bases or a cross-platform framework, store review, developer accounts, children's app store policies and a vendor SDK on Android. That is out of proportion for showing that a message arrived.
2. **Standard Web Push to the existing web app (proposed).** No store and no SDK; the same server, pages and authorization. iOS requires the app to be on the Home Screen.
3. E-mail or SMS. Children have neither by design.

**Content of a push**
1. Message text and sender in an encrypted payload (RFC 8291). The notification reads like other messengers. The text leaves the server, even if encrypted to the device, and appears on the lock screen unless the device hides it. Payload encryption needs ECDH, HKDF and AES-GCM in a specific layout. With Go 1.23 that is either a third-party library (an ADR of its own) or a hand-assembled construction, which invariant 9 rules out.
2. **No payload at all (proposed).** The service worker always shows the same fixed text. Nothing about the message, sender or conversation is sent. Only the VAPID signature (ES256, a standard JWT) is needed, which the standard library provides.

**Who may turn it on**
1. **The child, on its own paired device (proposed).** The browser requires the person holding the device to allow it anyway. The child turns it off the same way or in the device settings.
2. The guardian, from the family pages. A guardian cannot grant a browser permission on someone else's device. A guardian-level switch or quiet hours could restrict it later (open question).

**Where subscriptions are kept**
1. Inside the device pairing file. Couples a delivery detail with the identity record.
2. **A private file of their own (proposed),** keyed by tenant, child and device, like the other optional stores.

## Proposed decision outcome
Chosen option (if approved): **standard Web Push without payload, turned on by the child on a paired device, for the recipient child's devices only.** No native app is introduced. A native client needs a new ADR, which should be driven by device-key, distribution or calling requirements rather than by notifications.

### Rules
- The feature exists only when both `YAPUNO_PUSH_KEY_FILE` (the private VAPID key, mode `0600`) and `YAPUNO_PUSH_SUBSCRIPTIONS_FILE` are configured. It also requires device pairing and at least one chat. Without it no settings page, subscription route or worker is served, and the family overview lists "Benachrichtigungen" as switched off. The VAPID key is created offline with `yapuno-pushkey` and never committed.
- The web app manifest and its icons are public, contain no personal data and are served whenever the child pages are, so that the app can be installed. For that, the content security policy of the child pages allows the manifest and same-origin images. The worker and the settings page belong to the feature; only the settings page may register the worker (`worker-src 'self'`), whose scope is `/child/`.
- A subscription is accepted only from an authenticated child session, with the exact HTTPS origin and the session's CSRF token. It is stored for the tenant, child and device of that session, never for identifiers the client names. There is one subscription per device; a new one replaces the old one. The same endpoint is never stored for two devices.
- Only the endpoint URL is stored. The payload keys (`p256dh`, `auth`) are not requested, not accepted and not stored, because no payload is sent.
- The endpoint must be an `https` URL on the default port, with a lower-case host name, without user information or fragment, and without a query except the single `token` parameter of the Windows service, on a host of a known push service: `*.push.apple.com`, `fcm.googleapis.com`, `*.push.services.mozilla.com`, `*.notify.windows.com`. Anything else is refused. This stops the server from being used to send requests to arbitrary hosts (SSRF).
- A push is triggered only after a message has been stored: by a child to another child, or by a guardian to their own child. Duplicate retries do not trigger a push. A child writing to a guardian triggers nothing in this slice. A push is also triggered when a game makes a child the one waited for: invited to a game, or to move after the other player moved or after an invitation was accepted. A child is never told about its own action, guardians are never told (no device binding), and draw offers, resignations and the end of a game tell nobody.
- Before each push, the server checks again, for each subscribed device of the recipient, that the device is still approved and that this child on this device may read the conversation now (the same predicates as for reading). A device that is no longer approved loses its subscription. A blocked or removed contact, a locked guardian or a deleted profile therefore sends nothing.
- The push is a `POST` with an empty body, `TTL: 43200` (12 hours), `Urgency: high` and `Topic: yapuno-new-message`, so that a push service keeps at most one undelivered notice per device. The VAPID token is signed per request, expires after one hour and names the server origin as `sub`; no e-mail address is sent.
- Pushes are sent in the background from a bounded queue. Pending notices for the same child are merged. A full queue drops the notice and logs that it did so. Delivery is best effort: there is no retry. A `404` or `410` from the push service removes the subscription. Redirects are not followed. Logs never contain endpoints, identifiers or names.
- The service worker ignores any payload. When a push arrives, it asks its own server `POST /child/push/notice` which kinds of notices were delivered to this device: three booleans, `message`, `turn` and `invite`, which the server forgets once asked. To ask, it uses the child session; without one, it signs in with the paired device key exactly like the login page (challenge, signature with the non-extractable key from the same IndexedDB, login). It then shows fixed sentences for the true kinds: "Du hast eine neue Nachricht.", "Du bist am Zug.", "Du wurdest zu einer Schachpartie eingeladen." If the server cannot be asked within eight seconds, it shows "Bei Yapuno gibt es etwas Neues für dich." The title is always "Yapuno"; a tap opens `/child`. No text from the server reaches the lock screen, and nothing about the reason leaves the server through the push service.
- The kinds a device was told are kept in memory per device for at most twelve hours (the push lifetime) and recorded before the push is sent. The answer goes only to a verified session of that very device, with the exact HTTPS origin; it holds no identifiers or names. For this, the worker's content security policy allows same-origin scripts (its own helpers) and same-origin connections, and nothing else.
- Before a game notice is sent, the server checks again, for each subscribed device, that this child on this device may play in that game now and is still waited for there (`games.Store.Waiting`); switching chess off, blocking, ending the contact or having moved on another device sends nothing.
- Signing out on the device and deleting the child or the family remove the subscription at once. A device that a guardian removes or reports lost gets nothing from that moment on; its stored subscription is removed at the next delivery attempt for that child and when the server starts.
- The settings page says on every device what is sent and what is not. On an iPhone that is not running Yapuno from the Home Screen, it explains how to add it.

### Consequences
- Positive: children notice new messages without a native app; no message content, name or identifier leaves the server through push; the existing authorization decides every delivery; revocation stops delivery at the next attempt at the latest; no new dependency.
- Negative / accepted trade-offs:
  - **The notification says nothing about who wrote or what.** This is less convenient than common messengers, and it is deliberate.
  - **A third party learns timing.** Apple, Google, Mozilla or Microsoft see that the Yapuno server sends a push to a device, when and how often. They do not learn which child, from whom or about what. The endpoint itself is an identifier issued by that service.
  - **iOS needs the Home Screen.** Push works on iPhone only after "Teilen → Zum Home-Bildschirm" and opening Yapuno from there. **A Home Screen web app on iOS does not share storage with Safari, so the device has to be paired again inside the installed app.** The previous Safari pairing stays listed for the guardians until they remove it.
  - Delivery is best effort. A lost push or a full queue is not retried. The chat itself remains the source of truth.
  - When the browser rotates a subscription (`pushsubscriptionchange`), notifications stop until the child opens the settings page again, which stores the new one.
  - A child gets a notification even while the chat is open on the same device. iOS revokes subscriptions whose pushes show nothing, so the worker always shows one.
  - Subscriptions live in a single-process file store, like the other preview stores.
- Open questions and review gates:
  - **Quiet hours and guardian control:** whether guardians may restrict notifications (night, school time) and how that respects the child's privacy.
  - Pushes to guardians, which need a guardian device binding that does not exist yet.
  - Whether an encrypted payload with the sender's name should be offered as an opt-in. That needs its own ADR and a reviewed library.
  - Suppressing a push while the recipient is reading the conversation or looking at the game.
  - Whether the service worker's own sign-in (to ask what a push was about) works on a real iPhone Home Screen app: Safari's storage and cookie behaviour for workers is unverified. If it fails, the neutral sentence is shown.
  - Retries with backoff and a durable queue.
  - **Real-device evidence** on a recent iPhone (Home Screen app) and an Android phone (Chrome), including a push without payload on iOS, a locked screen, Focus modes, Screen Time restrictions on a child's device, and the Android battery optimisation of several vendors. Until then nobody may be promised notifications.
  - Threat model and privacy review before any real child uses it.

## Links
- Issues [#5](https://github.com/pblumer/yapuno/issues/5), [#8](https://github.com/pblumer/yapuno/issues/8)
- [Notifications (planning)](../planning/web-push.md), [Mobile First Experience](../product/mobile-first-experience.md)
- [Mobile First Experience ADR draft](draft-mobile-first-experience.md), [message storage ADR draft](draft-message-storage.md), [child device sessions ADR draft](draft-child-device-sessions.md), [guardian chat ADR draft](draft-guardian-chat.md), [self-service deletion ADR draft](draft-self-service-deletion.md)
- RFC 8030 (Generic Event Delivery Using HTTP Push), RFC 8292 (VAPID), RFC 8291 (Message Encryption for Web Push, not used)
