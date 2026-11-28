// Pure helpers for the notifications page. They hold no state and touch no
// DOM, so they can be tested outside a browser.
(function (root) {
  "use strict";

  // The server's public VAPID key as the bytes PushManager.subscribe wants:
  // an uncompressed P-256 point of 65 bytes.
  function keyBytes(b64url) {
    if (typeof b64url !== "string" || !/^[A-Za-z0-9_-]+$/.test(b64url)) throw new Error("invalid key");
    const b64 = b64url.replace(/-/g, "+").replace(/_/g, "/") + "===".slice((b64url.length + 3) % 4);
    const binary = atob(b64);
    const out = new Uint8Array(binary.length);
    for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i);
    if (out.length !== 65 || out[0] !== 4) throw new Error("invalid key");
    return out;
  }

  // What this device can do, from what the browser reports.
  //   ready        push can be switched on and off here
  //   install      an iPhone or iPad outside the Home Screen app
  //   unsupported  this browser has no Web Push
  //   denied       the person refused notifications for Yapuno
  function situation(env) {
    const e = env || {};
    if (e.permission === "denied") return "denied";
    if (!e.serviceWorker || !e.pushManager || !e.notification) return e.apple && !e.standalone ? "install" : "unsupported";
    return "ready";
  }

  // The state after the server answered a subscribe or unsubscribe request,
  // by HTTP status (0 = no response).
  function outcome(status, turningOn) {
    if (status === 204) return turningOn ? "on" : "off";
    if (status === 401) return "signed_out";
    return turningOn ? "failed" : "failed_off";
  }

  const MESSAGES = {
    on: "Benachrichtigungen sind auf diesem Gerät eingeschaltet.",
    off: "Benachrichtigungen sind auf diesem Gerät ausgeschaltet.",
    working: "Einen Moment …",
    install: "Auf diesem Gerät geht das nur, wenn Yapuno auf dem Home-Bildschirm ist.",
    unsupported: "Dieser Browser kann keine Benachrichtigungen anzeigen.",
    denied: "Du hast Benachrichtigungen für Yapuno abgelehnt. Du kannst das in den Einstellungen deines Geräts ändern.",
    failed: "Das hat nicht geklappt. Benachrichtigungen sind ausgeschaltet. Versuch es nochmal.",
    failed_off: "Das hat nicht geklappt. Versuch es nochmal.",
    signed_out: "Du bist abgemeldet. Öffne Yapuno neu und versuch es dann nochmal.",
  };

  function message(state) {
    return MESSAGES[state] || MESSAGES.failed;
  }

  // Which buttons the page shows in a state.
  function buttons(state) {
    return {on: state === "off" || state === "failed", off: state === "on" || state === "failed_off"};
  }

  // What a notification says. The server answers which kinds were
  // delivered to this device; only true booleans count, and the sentences
  // are fixed here, so no text from anywhere reaches the lock screen.
  const NEUTRAL = "Bei Yapuno gibt es etwas Neues für dich.";
  const SENTENCES = [
    ["message", "Du hast eine neue Nachricht."],
    ["turn", "Du bist am Zug."],
    ["invite", "Du wurdest zu einer Schachpartie eingeladen."],
  ];
  function noticeText(kinds) {
    if (!kinds || typeof kinds !== "object") return NEUTRAL;
    const parts = SENTENCES.filter(([key]) => kinds[key] === true).map(([, text]) => text);
    return parts.length > 0 ? parts.join(" ") : NEUTRAL;
  }

  const api = {keyBytes, situation, outcome, message, buttons, noticeText, NEUTRAL};
  root.YapunoPush = api;
  if (typeof module === "object" && module.exports) module.exports = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
