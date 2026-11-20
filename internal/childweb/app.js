"use strict";

// Yapuno child login. A paired device proves possession of its
// non-extractable key; the key never leaves the browser and is never logged.
(() => {
  const status = document.getElementById("status");
  const unpaired = document.getElementById("unpaired");
  const retry = document.getElementById("retry");
  const retryReason = document.getElementById("retry-reason");
  const retryButton = document.getElementById("retry-button");
  const sig = globalThis.YapunoSig;
  const GUARD = "yapuno-child-login";

  function say(message) { status.textContent = message; }
  function offerRetry(reason) {
    say("");
    retryReason.textContent = reason;
    retry.hidden = false;
  }
  function guard(action, value) {
    // sessionStorage can be unavailable (private mode, blocked storage).
    try {
      if (action === "get") return Number(sessionStorage.getItem(GUARD) || 0);
      if (action === "set") sessionStorage.setItem(GUARD, String(value));
      if (action === "clear") sessionStorage.removeItem(GUARD);
    } catch (_) { /* treated as no guard */ }
    return 0;
  }
  function openKeyStore() {
    return new Promise((resolve, reject) => {
      const req = indexedDB.open("yapuno-pairing-v1", 1);
      req.onupgradeneeded = () => req.result.createObjectStore("devices");
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error);
    });
  }
  async function pairedDevices() {
    const db = await openKeyStore();
    try {
      const all = await new Promise((resolve, reject) => {
        const req = db.transaction("devices", "readonly").objectStore("devices").getAll();
        req.onsuccess = () => resolve(req.result || []);
        req.onerror = () => reject(req.error);
      });
      return all.filter(d => d && d.privateKey && sig.validDeviceId(d.deviceId));
    } finally {
      db.close();
    }
  }
  async function post(path, payload) {
    return fetch(path, {
      method: "POST", mode: "same-origin", credentials: "same-origin",
      cache: "no-store", redirect: "error",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(payload)
    });
  }
  // Returns true when the server accepted this device, false when it rejected
  // it, and throws when the server could not be reached or answered oddly.
  async function login(device) {
    const challenge = await post("/child/session/challenge", {device_id: device.deviceId});
    if (challenge.status !== 200) throw new Error("challenge " + challenge.status);
    const data = await challenge.json();
    if (!sig.validNonce(data.nonce)) throw new Error("bad challenge");
    const message = new TextEncoder().encode(sig.loginMessage(device.deviceId, data.nonce));
    const raw = await crypto.subtle.sign({name: "ECDSA", hash: "SHA-256"}, device.privateKey, message);
    const answer = await post("/child/session/login", {
      device_id: device.deviceId, nonce: data.nonce, signature: sig.url64(sig.derFromP1363(raw))
    });
    if (answer.status === 204) return true;
    if (answer.status === 401) return false;
    throw new Error("login " + answer.status);
  }
  async function start() {
    retry.hidden = true;
    unpaired.hidden = true;
    if (!globalThis.isSecureContext || !globalThis.crypto || !crypto.subtle || !globalThis.indexedDB || !sig) {
      say("Dieser Browser unterstützt die sichere Anmeldung nicht.");
      return;
    }
    say("Dein Gerät wird geprüft …");
    let devices;
    try {
      devices = await pairedDevices();
    } catch (_) {
      offerRetry("Der sichere Speicher dieses Browsers ist nicht verfügbar.");
      return;
    }
    if (devices.length === 0) {
      say("");
      unpaired.hidden = false;
      return;
    }
    let unreachable = false;
    for (const device of devices) {
      try {
        if (await login(device)) {
          // A second successful login within moments means the session
          // cookie was not kept. Stop instead of reloading in a loop.
          if (Date.now() - guard("get") < 5000) {
            guard("clear");
            offerRetry("Die Anmeldung wurde nicht gespeichert. Sind Cookies für diese Seite erlaubt?");
            return;
          }
          guard("set", Date.now());
          location.replace("/child");
          return;
        }
      } catch (_) {
        unreachable = true;
      }
    }
    if (unreachable) {
      offerRetry("Yapuno ist gerade nicht erreichbar. Prüfe deine Verbindung.");
      return;
    }
    say("Dieses Gerät ist nicht mehr freigegeben.");
    document.getElementById("unpaired-title").textContent = "Dieses Gerät muss neu verbunden werden";
    unpaired.hidden = false;
  }
  retryButton.addEventListener("click", start);
  // After an explicit sign-out, wait for the child instead of signing in again.
  if (new URLSearchParams(location.search).has("signed_out")) {
    history.replaceState(null, "", location.pathname);
    guard("clear");
    say("");
    document.getElementById("retry-title").textContent = "Du bist abgemeldet";
    retryReason.textContent = "Dieses Gerät bleibt mit Yapuno verbunden.";
    retryButton.textContent = "Wieder anmelden";
    retry.hidden = false;
    return;
  }
  start();
})();
