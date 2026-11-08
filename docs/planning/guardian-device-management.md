# Guardian device management — browser preview

**Status:** The guardian-facing part of Magic Pairing is wired to the
existing browser login. This is an engineering preview for synthetic child
profiles. A link-based child WebCrypto and guardian fingerprint approval flow
and local QR rendering are implemented. The native phone camera can open the
generated QR link; optional in-browser scanning uses `BarcodeDetector`.
Child device sessions and real mobile E2E verification are still missing,
so it is **not** a child-safe service.

## Enable the device pages

With the private data directory from
[guardian login setup](guardian-browser-login-runtime.md), add:

```sh
export YAPUNO_PAIRING_FILE=private/pairing.json
go run ./cmd/yapuno-web
```

The file is created with mode `0600` when the first challenge is started.
Keep the data directory private and outside source control. The web server
remains restricted to loopback and must be served through the configured HTTPS
reverse proxy. Without `YAPUNO_PAIRING_FILE`, device pages and links are not
mounted.

After authenticated guardian login, open **Familie** and choose
**Geräte verwalten** for a child. The page lists that child's devices,
including revoked ones, and offers:

- **Pairing-Code erzeugen:** creates a five-minute challenge and displays
  a scannable, locally generated QR code plus copy/share controls. The secret
  appears only in the no-store response, not in the durable store. No remote
  QR service is called. The child opens the link with the native phone camera,
  optional built-in browser scanner (`/pair/scan`), or a shared link.
- **Sperren:** requires a current guardian session, the session CSRF token,
  explicit confirmation and active, child-scoped device-management permission.
  Revocation is checked by subsequent `ActiveDevice` calls. Existing
  future device sessions must also be invalidated in the actual messenger.

All guardian and tenant identifiers are obtained from the authenticated
session, not the request. The backend checks authorization again for list,
start and revoke operations. Unauthenticated access, wrong children, wrong
tenants, forged CSRF tokens and duplicate inputs are denied. Form output is
HTML-escaped and responses disable caches and third-party content.

## Next required delivery

The **link-based child-side** WebCrypto pairing, proof of possession,
fingerprint comparison, guardian approval and nonce-bound completion are
implemented. Local QR rendering, browser scanner (where supported), normal
camera and shared-link fallbacks are coded, but still need actual device tests.
This must have negative security tests, mobile/browser validation, origin and
logging rules for the QR secret, abuse limits, key rotation and recovery.

The JSON persistence is single-process/single-writer only. A transactional
shared backend and revocation propagation are required for multi-instance
operation, including horizontal scale-out. Do not use real child data until
the independent pilot-readiness gate passes.
