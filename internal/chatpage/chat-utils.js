// Pure helpers for the chat page. They hold no state and touch no DOM, so
// they can be tested outside a browser.
(function (root) {
  "use strict";
  const MAX_CHARS = 1000;

  // A fresh idempotency key for one message. bytes must be 16 random bytes.
  function clientId(bytes) {
    if (!(bytes instanceof Uint8Array) || bytes.length !== 16) throw new Error("16 random bytes required");
    let binary = "";
    for (const b of bytes) binary += String.fromCharCode(b);
    return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/g, "");
  }

  // Mirrors the server's normalisation closely enough to avoid sending text
  // the server would reject. The server remains the authority.
  function prepare(text) {
    if (typeof text !== "string") return null;
    const body = text.replace(/\r\n/g, "\n").trim();
    if (body === "" || Array.from(body).length > MAX_CHARS) return null;
    return body;
  }

  // What to do after a send attempt, by HTTP status (0 = no response).
  //   sent    stored by the server
  //   retry   not stored or unknown; the same client id may be sent again
  //   wait    rate limited; retry later with the same client id
  //   reauth  session ended; sign in again, then retry
  //   ended   this conversation is no longer allowed
  //   reject  the server refused this text; retrying cannot help
  function outcome(status) {
    if (status === 200 || status === 201) return "sent";
    if (status === 401) return "reauth";
    if (status === 403) return "ended";
    if (status === 429) return "wait";
    if (status === 400 || status === 409 || status === 413 || status === 415) return "reject";
    return "retry";
  }

  // Status line shown under an own message. Never colour alone.
  function statusText(state, time) {
    switch (state) {
      case "sending": return "Wird gesendet …";
      case "sent": return time ? "Gesendet · " + time : "Gesendet";
      case "retry": return "Nicht gesendet";
      case "wait": return "Nicht gesendet · zu viele Nachrichten, warte kurz";
      case "reject": return "Nicht gesendet · diese Nachricht geht so nicht";
      default: return "";
    }
  }

  // Returns the messages not seen yet, in sequence order, and the new cursor.
  // Anything malformed is ignored rather than rendered.
  function unseen(known, incoming) {
    const seen = known instanceof Set ? known : new Set(known || []);
    const fresh = [];
    for (const m of Array.isArray(incoming) ? incoming : []) {
      if (!m || !Number.isSafeInteger(m.seq) || m.seq < 1 || typeof m.body !== "string" || seen.has(m.seq)) continue;
      seen.add(m.seq);
      fresh.push({seq: m.seq, mine: m.mine === true, body: m.body, at: typeof m.at === "string" ? m.at : ""});
    }
    fresh.sort((a, b) => a.seq - b.seq);
    let cursor = 0;
    for (const seq of seen) if (seq > cursor) cursor = seq;
    return {fresh, cursor};
  }

  function clock(iso) {
    const date = new Date(iso);
    if (!iso || Number.isNaN(date.getTime())) return "";
    return String(date.getHours()).padStart(2, "0") + ":" + String(date.getMinutes()).padStart(2, "0");
  }

  // Next polling delay in milliseconds: quick while healthy, slower after
  // consecutive failures, capped.
  function pollDelay(failures) {
    const n = Number.isSafeInteger(failures) && failures > 0 ? failures : 0;
    return Math.min(3000 * Math.pow(2, n), 30000);
  }

  const api = {MAX_CHARS, clientId, prepare, outcome, statusText, unseen, clock, pollDelay};
  root.YapunoChat = api;
  if (typeof module === "object" && module.exports) module.exports = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
