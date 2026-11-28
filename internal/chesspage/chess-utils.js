// Pure helpers for the chess page. They hold no state and touch no DOM, so
// they can be tested outside a browser. The server decides legality; these
// helpers only read the list of legal moves the server sent.
(function (root) {
  "use strict";
  const MOVE = /^[a-h][1-8][a-h][1-8][qrbn]?$/;

  // The legal moves of the page, in UCI notation. Anything else is dropped.
  function parseLegal(text) {
    if (typeof text !== "string") return [];
    return text.split(/\s+/).filter((m) => MOVE.test(m));
  }

  // Squares with at least one legal move.
  function sources(legal) {
    return new Set(legal.map((m) => m.slice(0, 2)));
  }

  // Target square -> legal moves from one square (several for a promotion).
  function targets(legal, from) {
    const out = new Map();
    for (const m of legal) {
      if (m.slice(0, 2) !== from) continue;
      const to = m.slice(2, 4);
      if (!out.has(to)) out.set(to, []);
      out.get(to).push(m);
    }
    return out;
  }

  // The offered move that promotes to the chosen piece, or null.
  function promotionChoice(options, letter) {
    if (!/^[qrbn]$/.test(letter)) return null;
    return options.find((m) => m.length === 5 && m[4] === letter) || null;
  }

  function clockText(ms) {
    const s = Number.isFinite(ms) && ms > 0 ? Math.floor(ms / 1000) : 0;
    return Math.floor(s / 60) + ":" + String(s % 60).padStart(2, "0");
  }

  function pollDelay(base, failures) {
    const b = Number.isFinite(base) && base > 0 ? base : 4000;
    const n = Number.isSafeInteger(failures) && failures > 0 ? failures : 0;
    return Math.min(b * Math.pow(2, n), 30000);
  }

  // What to do after polling the state, by HTTP status (0 = no response).
  function outcome(status) {
    if (status === 200) return "ok";
    if (status === 401) return "signed_out";
    if (status === 403) return "ended";
    return "retry";
  }

  const api = {parseLegal, sources, targets, promotionChoice, clockText, pollDelay, outcome};
  root.YapunoChess = api;
  if (typeof module === "object" && module.exports) module.exports = api;
})(typeof globalThis !== "undefined" ? globalThis : this);
