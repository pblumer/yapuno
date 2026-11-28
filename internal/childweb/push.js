"use strict";

// Switches content-free notifications on or off for this device. The server
// stores only the push endpoint, bound to the signed-in device; the payload
// keys of the subscription are never sent.
(() => {
  const u = globalThis.YapunoPush;
  const page = document.querySelector("[data-key]");
  const status = document.getElementById("push-status");
  const onButton = document.getElementById("push-on");
  const offButton = document.getElementById("push-off");
  const install = document.getElementById("push-install");
  if (!u || !page || !status || !onButton || !offButton || !install) return;
  const key = page.dataset.key;
  const csrf = page.dataset.csrf;
  const subscribed = page.dataset.subscribed === "1";
  const apple = /iPhone|iPad|iPod/.test(navigator.userAgent) || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);
  const standalone = navigator.standalone === true || (typeof matchMedia === "function" && matchMedia("(display-mode: standalone)").matches);
  let registration = null;

  function show(state) {
    status.textContent = u.message(state);
    const b = u.buttons(state);
    onButton.hidden = !b.on;
    offButton.hidden = !b.off;
    install.hidden = state !== "install";
  }

  async function tell(path, body) {
    try {
      const response = await fetch(path, {
        method: "POST", mode: "same-origin", credentials: "same-origin", cache: "no-store", redirect: "error",
        headers: {"Content-Type": "application/json", "X-CSRF-Token": csrf},
        body: JSON.stringify(body),
      });
      return response.status;
    } catch (_) {
      return 0;
    }
  }

  async function turnOn() {
    onButton.disabled = true;
    show("working");
    try {
      // subscribe asks for permission itself and must run from the tap.
      const sub = await registration.pushManager.subscribe({userVisibleOnly: true, applicationServerKey: u.keyBytes(key)});
      show(u.outcome(await tell("/child/push/subscription", {endpoint: sub.endpoint}), true));
    } catch (_) {
      show(typeof Notification !== "undefined" && Notification.permission === "denied" ? "denied" : "failed");
    } finally {
      onButton.disabled = false;
    }
  }

  async function turnOff() {
    offButton.disabled = true;
    show("working");
    const state = u.outcome(await tell("/child/push/unsubscribe", {}), false);
    if (state === "off") {
      try {
        const sub = await registration.pushManager.getSubscription();
        if (sub) await sub.unsubscribe();
      } catch (_) { /* the server no longer sends anything */ }
    }
    offButton.disabled = false;
    show(state);
  }

  async function start() {
    const state = u.situation({
      serviceWorker: "serviceWorker" in navigator,
      pushManager: "PushManager" in window,
      notification: "Notification" in window,
      permission: "Notification" in window ? Notification.permission : "default",
      apple, standalone,
    });
    if (state !== "ready") {
      show(state);
      return;
    }
    try {
      await navigator.serviceWorker.register("/child/sw.js", {scope: "/child/"});
      registration = await navigator.serviceWorker.ready;
      const sub = await registration.pushManager.getSubscription();
      if (subscribed && sub) {
        // The browser may have renewed the subscription; store the current one.
        show(u.outcome(await tell("/child/push/subscription", {endpoint: sub.endpoint}), true));
        return;
      }
      if (subscribed && !sub) await tell("/child/push/unsubscribe", {});
      show("off");
    } catch (_) {
      show("unsupported");
    }
  }

  onButton.addEventListener("click", turnOn);
  offButton.addEventListener("click", turnOff);
  start();
})();
