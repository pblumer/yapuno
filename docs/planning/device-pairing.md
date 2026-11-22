# Device pairing

**Status:** engineering preview for synthetic families. A child's device is bound to the child with a short code. Decision record: [ADR draft: pairing by code](../adr/draft-device-pairing-by-code.md). This replaces the QR and fingerprint flow described in the older notes on [durable pairing](durable-device-pairing.md), [guardian device management](guardian-device-management.md) and [browser pairing](browser-device-pairing.md).

## Enable

```sh
export YAPUNO_PAIRING_FILE=private/pairing.json
```

## Flow

1. On the child's device, open `<host>/pair`. A device that opens `/child` without being paired is led there by a button.
2. The device shows a code of six digits, valid for five minutes.
3. The guardian signs in, opens **Familie → Geräte von …** and types the code.
4. The device finishes by itself and offers **Yapuno öffnen**.

Nothing is scanned, compared or named. The device calls itself "iPhone", "Android-Handy" and so on.

**Read the code off the device yourself.** Whoever supplies the code decides which device becomes the child's. The page tells guardians not to enter a code that somebody dictates.

## What happens underneath

| Step | Request | Effect |
|---|---|---|
| Device asks | `POST /pair/request` with a name and a public key | stores the key; returns a secret token (stays on the device) and the code |
| Device waits | `POST /pair/nonce` with the token | `202` until a guardian entered the code |
| Guardian enters the code | `POST /family/devices/connect` | binds the waiting device to the child, creates a nonce |
| Device finishes | `POST /pair/finish` with a signature over the nonce | creates the device record |

- The private key is created in the browser as non-extractable and stored in IndexedDB. It is never sent.
- The token is stored as a digest only. The code is five random digits and a check digit; a typo fails the check instead of matching another device.
- Device requests are unauthenticated JSON calls of at most 4 KiB without unknown fields. They must come from a page of the same HTTPS origin, decided by `internal/sameorigin`: the `Origin` header names exactly this origin, or, where a browser sends `Origin: null`, `Sec-Fetch-Site` says `same-origin`. WebKit (every browser on an iPhone) and Firefox send `Origin: null` for a request in same-origin mode from a no-referrer page, which is why the scripts post in the default mode.
- Responses are `no-store` and `no-referrer`. The pairing token never authenticates a child.

## Limits

- 200 devices can wait at once; each code is unique among them.
- Five unknown codes in ten minutes lock that guardian out of entering codes for the rest of the window. Typos do not count.
- **Put the reverse proxy's rate limit on `POST /pair/request`.**

## Locking a device

**Geräte von …** lists the devices with a lock for each; **Gerät verloren?** locks one or all ([details](device-lost.md)).

## Known limits

- Somebody who talks a guardian into typing their code binds their own device.
- The key is not hardware-backed; clearing the browser's storage unpairs the device.
- A device in private mode cannot be paired.
- Verified in a real desktop browser at 320, 375 and 430 px, including the case where the server receives `Origin: null`; **not yet on a real iPhone or Android phone**.
