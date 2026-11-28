"use strict";

// Yapuno service worker. A push carries no data, and none is read: whatever
// arrives, the same sentence is shown, so nothing about who wrote or what
// can reach the lock screen. The worker caches nothing and handles no
// requests.
const TITLE = "Yapuno";
const BODY = "Du hast eine neue Nachricht.";

self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (e) => e.waitUntil(self.clients.claim()));

// Every push must show a notification: browsers end subscriptions whose
// pushes show nothing.
self.addEventListener("push", (e) => {
  e.waitUntil(self.registration.showNotification(TITLE, {
    body: BODY,
    tag: "yapuno-new-message",
    renotify: true,
    icon: "/child/icon-192.png",
    lang: "de",
  }));
});

self.addEventListener("notificationclick", (e) => {
  e.notification.close();
  e.waitUntil((async () => {
    const windows = await self.clients.matchAll({type: "window", includeUncontrolled: true});
    for (const w of windows) {
      if (new URL(w.url).origin === self.location.origin && "focus" in w) return w.focus();
    }
    return self.clients.openWindow("/child");
  })());
});
