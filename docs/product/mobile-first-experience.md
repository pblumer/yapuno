# Mobile First Experience (MFE)

**Status:** foundational design requirement, acceptance tests not yet executed.

Yapuno is **mobile first, not merely responsive**. The experience is built and assessed on small touchscreen devices *before* larger screens. The primary product surfaces are a child's chat and a guardian's management/approval portal. Desktop is supported progressively, not the design baseline.

## Principles
- **One-handed and age-appropriate:** prominent conversations, clear compose/send controls, predictable back navigation, actionable errors and friendly microcopy. Do not infantilize teenagers or gender the visual design.
- **Passwordless routine:** guardian pairs/approves device; child taps the Home Screen icon and chats using an authorized session. Renewals/expiration should be understandable, never bypass security silently. Do not promise that a synced passkey is hardware device-bound.
- **Small and fast:** server-rendered HTML/CSS, minimal JavaScript for chat and PWA plumbing. Avoid large client frameworks by default; bundle small, cacheable assets; no embedded tracking/ads.
- **Accessibility:** readable at larger text sizes; screen reader semantics; adequate contrast and hit targets; reduced motion; no color-only safety signals; age-appropriate feedback.
- **Connectivity-aware:** preserve composed text on transient network failure; visible queued/failed/sent states; reconnect and deduplicate; never claim delivery before server confirmation. Wi-Fi only is supported when internet is available.
- **No security degradation for convenience:** all actions server-authorized; locked device/withdrawn consent ends active streams; never leak sensitive previews on lock screen by default.
- **PWA-first, empirically validated:** installable iPhone/Android app shell, proper safe-area layout, verified push notification and background behavior for supported OS/browser combinations. Native code would require a new ADR if a critical feature cannot work in a PWA.

## First Hello acceptance matrix (required evidence)
1. **Small viewport:** start at 320 CSS px width, also test at 375 and 430 CSS px; no horizontal overflow for supported font settings; sensible content wrapping.
2. **Device behaviors:** portrait and landscape, notch/safe area, notch/status bar, on-screen keyboard, focus, screen reader, input zoom and touch targets (target at least 44 × 44 CSS px unless justified).
3. **Child flow:** open, review conversation, type, send, see server-confirmed status, retry/reconnect, block/report, receive clear denied-contact warning.
4. **Guardian flow:** create child profile, pair via single-use QR challenge, approve contact, see device list, revoke and recover; all usable without desktop.
5. **Platform evidence:** at least one recent physical iPhone/Safari Home Screen PWA and one physical Android/Chrome installed PWA; browser-only fallback validated. Real background/push tests required before promising notifications.
6. **Network:** latency and interrupted / offline connection, captive portal or blocked websocket, app resume from background, low-data mode; no unintended duplicate sends.
7. **Privacy:** push/notification previews disabled by default on sensitive content; no stale content after revocation; content expiry reflected in local caches. No hidden analytics.
8. **Performance:** record initial JS/CSS payload, startup time, chat send roundtrip, memory and battery impact on representative entry-level devices. Budgets should be set using measured baselines, not fabricated values.

## Implementation guidance
- CSS layout uses narrow-screen baseline and progressive `min-width` media queries.
- HTML, semantic inputs and accessible focus management first. Use HTMX only where it reduces complexity, primarily guardian flows.
- WebSocket connection for foreground realtime; normal authenticated HTTP for pairing/history/recovery, with robust fallback where possible.
- Avoid unsupported claims of durable offline messaging; distinguish a local unsent draft from a server-accepted message.
- Verify the school-Wi-Fi and low-cost device use case before pilot.

## Delivery policy
Every UI-related issue and PR must state how MFE was evaluated and add device evidence or explain what remains unverified. This is a cross-cutting milestone gate, not an optional workstream. See [issue #5](https://github.com/pblumer/yapuno/issues/5).
