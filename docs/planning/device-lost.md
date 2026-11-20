# "Gerät verloren": locking a lost child device

**Status:** engineering preview for synthetic child profiles. A guardian can lock one or all paired devices of a child from a dedicated page. This is server-side revocation, **not** a remote wipe.

## Enable

The page is mounted together with the device pages whenever device pairing is enabled (`YAPUNO_PAIRING_FILE`, see [guardian device management](guardian-device-management.md)). Without pairing, neither the page nor its links exist.

## Flow

1. **Familie → Gerät von … verloren?** (also linked from **Geräte verwalten**) opens `/family/devices/lost?child_id=…`.
2. The page lists the child's **active** devices by the name given during pairing. Each has its own **Dieses Gerät sperren**.
3. **Alle Geräte sperren** is for guardians who are not sure which entry is the lost device. It locks every active device of that child in one durable write and cancels that child's pairings in progress, so a pairing started before the lock cannot produce a fresh device afterwards.
4. Every lock needs a ticked confirmation. The page then confirms the lock (`role="status"`) and offers **Neues Gerät verbinden**.
5. The page states what the lock does and does not do, in the guardian's skin, without JavaScript.

## What a lock does

- The device's session and key stop being accepted: every child request re-checks device approval ([child device sessions](child-device-sessions.md)), so reading, sending and signing in again fail from the next request on.
- An open chat polls every 3 seconds while visible and online and falls back to the sign-in shell, which says that the device is no longer approved. After connection failures the poll backs off to at most 30 seconds.
- Messages and contacts belong to the child, not the device. They stay and are available on a newly paired device.
- A locked device cannot be unlocked. It has to be paired again with a new code.

## What a lock does not do

- **No remote wipe.** The device still holds its (now useless) key and device id. Content already on screen stays visible until the page next reaches the server; an offline device keeps showing it. Received messages are not stored on the device, but the child's own draft and unsent texts live in that tab's session storage.
- **No undo of earlier activity.** Whatever a finder read or sent before the lock stays read and stays in the conversation. There is no marker for "sent while the device was lost".
- **No notification.** Neither the other guardian, nor the child, nor contacts are told, and there is no audit record of who locked which device when.
- **Guardian devices are a separate page.** This page locks child devices only. For a lost guardian device see [guardian account safety](guardian-account-safety.md).

## Security properties (tested)

- Guardian and tenant come from the verified session only; identity fields in the form are ignored. The child is named by the request and authorized by the store against current device-management grants on every call; the page additionally refuses children outside the session's family before asking the store.
- Other families, child sessions and signed-out visitors cannot open the page or lock anything. A device of another child or family named under the guardian's own child is rejected and stays active. Siblings are separated: the page names and lists exactly the requested child.
- A request must be unambiguous: `scope=one` with exactly one well-formed device id, or `scope=all` without one. Anything else, a missing confirmation, a missing or foreign CSRF token, another content type, duplicate fields or an oversized body is rejected and never reaches the store.
- `RevokeAll` is scoped by tenant **and** child (a same-named child id in another family is untouched), leaves the sibling's devices and pairings alone, is idempotent, is persisted, and cannot be raced by a pairing that finishes concurrently.
- Only fixed confirmation texts can be selected by the redirect; query text is never rendered. Device names are HTML-escaped. The page contains no script, loads nothing from other origins and uses skin tokens only.
- 8 store mutations and 15 page and wiring mutations (for example authority check removed, tenant or child ignored, pairings not cancelled, confirmation not required, "one" locking all, locked devices listed, route not mounted) are each detected by the tests.

## Browser evidence (headless Chromium, not a physical device)

One scripted run through the real server over TLS with a guardian, two devices of one child and a device of another family, repeated with the `classic` and `night` skins, 29 checks passed each: navigation from the family overview; no horizontal overflow, no clipped text and 44 px targets at 320, 375 and 430 px; keyboard reaches every control with a visible focus ring; the button does nothing without the ticked confirmation; after locking one device its open chat closed in under 2 seconds and showed the reason while the second device kept sending; an **offline** device kept showing its content, was signed out at the next server contact, and a message written on it never arrived; the other family kept chatting and got no page for the child; a replacement device received the contact and the earlier messages; locking worked with JavaScript disabled; no request left the own origin.

**Not verified:** physical iPhone/Android, Home Screen PWA behaviour of a locked device, screen readers, large text, landscape, a device that is suspended rather than offline.

## Limitations

- Single process, single writer: locks apply to sessions in this process; a multi-instance deployment needs shared revocation.
- No rate limiting and no step-up authentication for locking; a guardian session is enough.
- The older **Geräte verwalten** page keeps its own per-device lock and still lists locked devices.

Related #2, #14.
