"use strict";

// Yapuno service worker. A push carries no data, and none is read. When one
// arrives, the worker asks its own server what this device was told about
// (three booleans) and shows fixed sentences for them: "Du hast eine neue
// Nachricht.", "Du bist am Zug.", "Du wurdest zu einer Schachpartie
// eingeladen." If the server cannot be asked, it shows a neutral sentence.
// To ask, the device signs in with its own paired key, exactly like the
// login page. The worker caches nothing and handles no requests.
importScripts("/child/sig-utils.js", "/child/push-utils.js");

const TITLE = "Yapuno";
const ASK_TIMEOUT_MS = 8000;

self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (e) => e.waitUntil(self.clients.claim()));

function post(path, payload) {
  const init = {method: "POST", credentials: "same-origin", cache: "no-store", redirect: "error"};
  if (payload !== undefined) {
    init.headers = {"Content-Type": "application/json"};
    init.body = JSON.stringify(payload);
  }
  return fetch(path, init);
}

function pairedDevices() {
  return new Promise((resolve, reject) => {
    const req = indexedDB.open("yapuno-pairing-v1", 1);
    req.onupgradeneeded = () => req.result.createObjectStore("devices");
    req.onerror = () => reject(req.error);
    req.onsuccess = () => {
      const db = req.result;
      const all = db.transaction("devices", "readonly").objectStore("devices").getAll();
      all.onsuccess = () => {
        db.close();
        resolve((all.result || []).filter((d) => d && d.privateKey && self.YapunoSig.validDeviceId(d.deviceId)));
      };
      all.onerror = () => {
        db.close();
        reject(all.error);
      };
    };
  });
}

// Signs in with the first paired key the server accepts.
async function signIn() {
  const sig = self.YapunoSig;
  for (const device of await pairedDevices()) {
    const challenge = await post("/child/session/challenge", {device_id: device.deviceId});
    if (challenge.status !== 200) continue;
    const data = await challenge.json();
    if (!sig.validNonce(data.nonce)) continue;
    const message = new TextEncoder().encode(sig.loginMessage(device.deviceId, data.nonce));
    const raw = await crypto.subtle.sign({name: "ECDSA", hash: "SHA-256"}, device.privateKey, message);
    const answer = await post("/child/session/login", {
      device_id: device.deviceId, nonce: data.nonce, signature: sig.url64(sig.derFromP1363(raw)),
    });
    if (answer.status === 204) return true;
  }
  return false;
}

async function ask() {
  let answer = await post("/child/push/notice");
  if (answer.status === 401 && await signIn()) answer = await post("/child/push/notice");
  if (answer.status !== 200) return null;
  return answer.json();
}

function withTimeout(promise, ms) {
  return Promise.race([promise, new Promise((resolve) => setTimeout(() => resolve(null), ms))]);
}

// Every push must show a notification: browsers end subscriptions whose
// pushes show nothing.
self.addEventListener("push", (e) => {
  e.waitUntil((async () => {
    let kinds = null;
    try {
      kinds = await withTimeout(ask(), ASK_TIMEOUT_MS);
    } catch (_) {
      kinds = null;
    }
    await self.registration.showNotification(TITLE, {
      body: self.YapunoPush.noticeText(kinds),
      tag: "yapuno-new-message",
      renotify: true,
      icon: "/child/icon-192.png",
      lang: "de",
    });
  })());
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
