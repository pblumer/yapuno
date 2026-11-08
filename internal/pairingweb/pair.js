"use strict";

// Yapuno child pairing prototype. Never export private keys or log invite tokens.
(() => {
  const status = document.getElementById("status");
  const form = document.getElementById("join");
  const nameField = document.getElementById("device-name");
  const button = document.getElementById("join-button");
  const confirmation = document.getElementById("confirmation");
  const fingerprint = document.getElementById("fingerprint");
  const check = document.getElementById("check");
  const completed = document.getElementById("completed");

  // The invite is in the URL *fragment*, which browsers do not send in HTTP
  // requests. Remove it from the visible URL immediately after reading it.
  const token = location.hash.startsWith("#") ? location.hash.slice(1) : "";
  history.replaceState(null, "", location.pathname);
  const validToken = /^[A-Za-z0-9_-]{43}$/.test(token);
  let keyPair = null;
  let storeKey = null;
  let pollTimer = null;
  let inFlight = false;

  function display(message) { status.textContent = message; }
  function url64(bytes) {
    const view = bytes instanceof Uint8Array ? bytes : new Uint8Array(bytes);
    let binary = "";
    for (const byte of view) binary += String.fromCharCode(byte);
    return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
  }
  function fromRawECDSA(raw) {
    // Browser WebCrypto returns P-256 ECDSA as IEEE-P1363 (r || s).
    // Go's ecdsa.VerifyASN1 expects strict DER SEQUENCE(INTEGER r, INTEGER s).
    const bytes = new Uint8Array(raw);
    if (bytes.length !== 64) throw new Error("Unexpected device signature");
    function integer(part) {
      let start = 0;
      while (start < part.length - 1 && part[start] === 0) start++;
      let v = Array.from(part.slice(start));
      if (v[0] & 128) v.unshift(0);
      return [2, v.length, ...v];
    }
    const r = integer(bytes.slice(0, 32));
    const s = integer(bytes.slice(32, 64));
    return new Uint8Array([48, r.length + s.length, ...r, ...s]);
  }
  async function request(path, payload) {
    const response = await fetch(path, {
      method: "POST", mode: "same-origin", credentials: "omit",
      cache: "no-store", redirect: "error",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(payload)
    });
    let data = {};
    try { data = await response.json(); } catch (_) { /* generic error */ }
    if (response.status !== 200 && response.status !== 202) {
      throw new Error("Server hat die Kopplung abgelehnt (HTTP " + response.status + ").");
    }
    return {response, data};
  }
  async function openKeyStore() {
    return new Promise((resolve, reject) => {
      const req = indexedDB.open("yapuno-pairing-v1", 1);
      req.onupgradeneeded = () => req.result.createObjectStore("devices");
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error);
    });
  }
  async function storeDevice(key, value) {
    const db = await openKeyStore();
    try {
      await new Promise((resolve, reject) => {
        const transaction = db.transaction("devices", "readwrite");
        transaction.objectStore("devices").put(value, key);
        transaction.oncomplete = resolve;
        transaction.onerror = () => reject(transaction.error);
        transaction.onabort = () => reject(transaction.error);
      });
    } finally {
      db.close();
    }
  }
  async function signature(domain, payload) {
    const message = new TextEncoder().encode(domain + "\0" + payload);
    const raw = await crypto.subtle.sign({name: "ECDSA", hash: "SHA-256"}, keyPair.privateKey, message);
    return url64(fromRawECDSA(raw));
  }
  function stopPolling() {
    if (pollTimer) window.clearInterval(pollTimer);
    pollTimer = null;
  }
  async function checkApproval() {
    if (inFlight || !keyPair || !token) return;
    inFlight = true;
    try {
      const {response, data} = await request("/pair/nonce", {token});
      if (response.status === 202) {
        display("Warte auf die Freigabe deiner Eltern …");
        return;
      }
      if (!data.nonce || !/^[A-Za-z0-9_-]{43}$/.test(data.nonce)) {
        throw new Error("Ungültige Antwort vom Server.");
      }
      stopPolling();
      display("Freigabe erhalten! Gerät wird verknüpft …");
      const proof = await signature("yapuno-pair-finish-v1", data.nonce);
      const result = await request("/pair/finish", {token, signature: proof});
      if (!result.data.device_id) throw new Error("Gerätekennung fehlt.");
      await storeDevice(storeKey, {privateKey: keyPair.privateKey, publicKey: keyPair.publicKey, deviceId: result.data.device_id});
      confirmation.hidden = true;
      completed.hidden = false;
      display("Gerät erfolgreich verbunden.");
    } catch (error) {
      stopPolling();
      display(error.message || "Gerät konnte nicht verbunden werden.");
    } finally {
      inFlight = false;
    }
  }

  if (!validToken || !globalThis.isSecureContext || !crypto.subtle || !globalThis.indexedDB) {
    display("Dieser Einladungslink ist ungültig oder dein Browser unterstützt die sichere Kopplung nicht.");
    return;
  }
  form.hidden = false;
  display("Einladung bereit. Gib dem Gerät einen Namen.");

  form.addEventListener("submit", async event => {
    event.preventDefault();
    if (keyPair) return;
    const name = nameField.value.trim();
    if (!name || name.length > 64 || /[<>\r\n]/.test(name)) {
      display("Bitte einen gültigen Gerätenamen eingeben.");
      return;
    }
    button.disabled = true;
    display("Ein sicherer Geräteschlüssel wird erzeugt …");
    try {
      // The private key is non-extractable; the public key is shared.
      keyPair = await crypto.subtle.generateKey({name: "ECDSA", namedCurve: "P-256"}, false, ["sign", "verify"]);
      const spki = await crypto.subtle.exportKey("spki", keyPair.publicKey);
      const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(token));
      storeKey = url64(digest);
      // Fail closed if non-extractable CryptoKey storage is unavailable.
      await storeDevice(storeKey, {privateKey: keyPair.privateKey, publicKey: keyPair.publicKey, deviceId: null});
      const proof = await signature("yapuno-pair-claim-v1", token);
      const result = await request("/pair/claim", {
        token, name, public_key: url64(spki), signature: proof
      });
      if (!/^[a-f0-9]{64}$/.test(result.data.fingerprint || "")) {
        throw new Error("Ungültiger Geräte-Fingerabdruck.");
      }
      fingerprint.textContent = result.data.fingerprint.match(/.{1,8}/g).join(" ");
      form.hidden = true;
      confirmation.hidden = false;
      display("Bitte deine Eltern den Fingerabdruck prüfen lassen.");
      pollTimer = window.setInterval(checkApproval, 3000);
    } catch (error) {
      keyPair = null;
      button.disabled = false;
      display(error.message || "Kopplung fehlgeschlagen.");
    }
  });
  check.addEventListener("click", checkApproval);
})();
