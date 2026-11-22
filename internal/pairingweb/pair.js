"use strict";

// Yapuno device pairing. The device creates a key that cannot be exported,
// asks the server for a short code and shows it. A guardian enters the code
// in the guardian area; then the device proves its key and is bound.
// The private key never leaves the browser; the token is never logged.
(() => {
  const status = document.getElementById("status");
  const waiting = document.getElementById("waiting");
  const codeBox = document.getElementById("code");
  const completed = document.getElementById("completed");
  const failed = document.getElementById("failed");
  const failedReason = document.getElementById("failed-reason");
  const again = document.getElementById("again");
  const paired = document.getElementById("paired");
  const anyway = document.getElementById("anyway");
  if (!status || !waiting || !codeBox || !completed || !failed || !again || !paired || !anyway) return;

  const POLL_MS = 2500;
  let keyPair = null;
  let storeKey = null;
  let token = null;
  let timer = null;
  let deadline = 0;
  let inFlight = false;

  function say(message) { status.textContent = message; }
  function show(section) {
    for (const s of [waiting, completed, failed, paired]) s.hidden = s !== section;
  }
  function fail(reason) {
    stop();
    token = null;
    say("");
    failedReason.textContent = reason;
    show(failed);
  }
  function stop() {
    if (timer) window.clearInterval(timer);
    timer = null;
  }
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
  // A name the guardian recognizes in the device list. Nothing is typed.
  function deviceName() {
    const ua = navigator.userAgent || "";
    if (/iPhone/.test(ua)) return "iPhone";
    if (/iPad/.test(ua) || (/Macintosh/.test(ua) && navigator.maxTouchPoints > 1)) return "iPad";
    if (/Android/.test(ua)) return /Mobile/.test(ua) ? "Android-Handy" : "Android-Tablet";
    if (/Macintosh/.test(ua)) return "Mac";
    if (/Windows/.test(ua)) return "Windows-PC";
    return "Gerät";
  }
  async function post(path, payload) {
    // No "mode: same-origin" here: with it, and the page's Referrer-Policy
    // no-referrer, the Fetch standard makes Safari and Firefox send
    // "Origin: null". The URL is a fixed same-origin path and redirects are
    // refused, so the request cannot leave this origin either way.
    const response = await fetch(path, {
      method: "POST", credentials: "omit",
      cache: "no-store", redirect: "error",
      headers: {"Content-Type": "application/json"},
      body: JSON.stringify(payload)
    });
    let data = {};
    try { data = await response.json(); } catch (_) { /* no JSON body */ }
    return {status: response.status, data};
  }
  function openKeyStore() {
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
  async function hasPairedDevice() {
    const db = await openKeyStore();
    try {
      const all = await new Promise((resolve, reject) => {
        const req = db.transaction("devices", "readonly").objectStore("devices").getAll();
        req.onsuccess = () => resolve(req.result || []);
        req.onerror = () => reject(req.error);
      });
      return all.some(d => d && d.privateKey && typeof d.deviceId === "string" && d.deviceId !== "");
    } finally {
      db.close();
    }
  }
  async function sign(domain, payload) {
    const message = new TextEncoder().encode(domain + "\0" + payload);
    const raw = await crypto.subtle.sign({name: "ECDSA", hash: "SHA-256"}, keyPair.privateKey, message);
    return url64(fromRawECDSA(raw));
  }

  async function check() {
    if (inFlight || !token) return;
    if (Date.now() > deadline) {
      fail("Der Code ist abgelaufen. Lass dir einen neuen anzeigen.");
      return;
    }
    inFlight = true;
    try {
      const answer = await post("/pair/nonce", {token});
      if (answer.status === 202) return;
      if (answer.status === 410) { fail("Der Code ist abgelaufen. Lass dir einen neuen anzeigen."); return; }
      if (answer.status === 409) { fail("Diese Verbindung wurde abgebrochen. Lass dir einen neuen Code anzeigen."); return; }
      if (answer.status !== 200) return; // keep the code; try again shortly
      if (!/^[A-Za-z0-9_-]{43}$/.test(answer.data.nonce || "")) throw new Error("bad nonce");
      stop();
      say("Deine Eltern haben zugestimmt. Gerät wird verbunden …");
      const done = await post("/pair/finish", {token, signature: await sign("yapuno-pair-finish-v1", answer.data.nonce)});
      if (done.status !== 200 || !done.data.device_id) throw new Error("finish " + done.status);
      await storeDevice(storeKey, {privateKey: keyPair.privateKey, publicKey: keyPair.publicKey, deviceId: done.data.device_id});
      token = null;
      say("");
      show(completed);
    } catch (_) {
      fail("Das Gerät konnte nicht verbunden werden. Lass dir einen neuen Code anzeigen.");
    } finally {
      inFlight = false;
    }
  }

  async function begin() {
    stop();
    show(null);
    say("Dein Gerät wird vorbereitet …");
    try {
      // The private key is non-extractable; only the public key is shared.
      keyPair = await crypto.subtle.generateKey({name: "ECDSA", namedCurve: "P-256"}, false, ["sign", "verify"]);
      const spki = await crypto.subtle.exportKey("spki", keyPair.publicKey);
      storeKey = url64(crypto.getRandomValues(new Uint8Array(16)));
      // Fail closed if the browser cannot keep a non-extractable key.
      await storeDevice(storeKey, {privateKey: keyPair.privateKey, publicKey: keyPair.publicKey, deviceId: null});
      const answer = await post("/pair/request", {name: deviceName(), public_key: url64(spki)});
      if (answer.status === 503) { fail("Gerade verbinden sich sehr viele Geräte. Versuch es in einer Minute nochmal."); return; }
      if (answer.status !== 200 || !/^[0-9]{6}$/.test(answer.data.code || "") || !/^[A-Za-z0-9_-]{43}$/.test(answer.data.token || "")) {
        throw new Error("request " + answer.status);
      }
      token = answer.data.token;
      deadline = Date.now() + (Number(answer.data.expires_in) || 300) * 1000;
      // Shown as text in two groups of three, like a phone code.
      codeBox.textContent = answer.data.code.slice(0, 3) + " " + answer.data.code.slice(3);
      say("");
      show(waiting);
      timer = window.setInterval(check, POLL_MS);
    } catch (_) {
      fail("Das Gerät konnte nicht vorbereitet werden. Prüfe die Internetverbindung und versuch es nochmal.");
    }
  }

  again.addEventListener("click", begin);
  anyway.addEventListener("click", begin);
  document.addEventListener("visibilitychange", () => { if (document.visibilityState === "visible") check(); });

  if (!globalThis.isSecureContext || !globalThis.crypto || !crypto.subtle || !globalThis.indexedDB) {
    fail("Dieser Browser unterstützt die sichere Verbindung nicht. Probier einen aktuellen Browser.");
    again.hidden = true;
    return;
  }
  hasPairedDevice().then(yes => {
    if (yes) { say(""); show(paired); } else { begin(); }
  }).catch(() => {
    fail("Der sichere Speicher dieses Browsers ist nicht verfügbar. Im privaten Modus funktioniert das Verbinden nicht.");
  });
})();
