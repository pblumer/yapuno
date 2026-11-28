"use strict";

// Yapuno chess page enhancement. The page works without this script (a
// typed move is posted with a form). With it: tap a piece, see where it may
// go, tap the target; a ticking clock; and a reload as soon as the game
// changes. Only moves the server listed as legal can be chosen, and the
// server checks every move again.
(() => {
  const util = globalThis.YapunoChess;
  const root = document.getElementById("yp-play");
  const board = document.getElementById("yp-board");
  if (!util || !root || !board) return;
  const form = document.getElementById("yp-move-form");
  const input = document.getElementById("yp-move");
  const promo = document.getElementById("yp-promo");
  const announce = document.getElementById("yp-announce");
  const squares = new Map();
  for (const b of board.querySelectorAll("[data-square]")) {
    b.dataset.label = b.getAttribute("aria-label");
    squares.set(b.dataset.square, b);
  }
  const legal = util.parseLegal(board.dataset.legal);
  const from = util.sources(legal);
  let selected = null;
  let options = new Map();
  let sending = false;

  function say(text) { if (announce) announce.textContent = text; }

  function clear() {
    for (const b of squares.values()) {
      b.classList.remove("yp-selected", "yp-target", "yp-capture");
      b.setAttribute("aria-label", b.dataset.label);
      b.removeAttribute("aria-pressed");
    }
    selected = null;
    options = new Map();
    if (promo) promo.hidden = true;
  }

  function select(square) {
    clear();
    selected = square;
    options = util.targets(legal, square);
    const b = squares.get(square);
    b.classList.add("yp-selected");
    b.setAttribute("aria-pressed", "true");
    for (const to of options.keys()) {
      const t = squares.get(to);
      t.classList.add("yp-target");
      if (t.classList.contains("yp-w") || t.classList.contains("yp-b")) t.classList.add("yp-capture");
      t.setAttribute("aria-label", t.dataset.label + ", hierhin ziehen");
    }
    say(b.dataset.label + " gewählt. " + options.size + (options.size === 1 ? " mögliches Feld." : " mögliche Felder."));
  }

  function send(move) {
    if (!form || !input || sending) return;
    sending = true;
    input.value = move;
    for (const b of squares.values()) b.disabled = true;
    say("Zug wird gesendet …");
    form.submit();
  }

  if (legal.length > 0 && form && input) {
    for (const b of squares.values()) b.disabled = false;
    board.addEventListener("click", (event) => {
      const b = event.target.closest("[data-square]");
      if (!b || sending) return;
      const square = b.dataset.square;
      if (selected && options.has(square)) {
        const moves = options.get(square);
        if (moves.length === 1) { send(moves[0]); return; }
        if (promo) {
          promo.hidden = false;
          promo.dataset.to = square;
          const first = promo.querySelector("button");
          if (first) first.focus();
          say("Der Bauer wird umgewandelt. Wähle eine Figur.");
        }
        return;
      }
      if (from.has(square) && square !== selected) { select(square); return; }
      clear();
      say("");
    });
    if (promo) {
      promo.addEventListener("click", (event) => {
        const b = event.target.closest("[data-promo]");
        if (!b) return;
        const move = util.promotionChoice(options.get(promo.dataset.to) || [], b.dataset.promo);
        if (move) { send(move); return; }
        clear();
        say("Abgebrochen.");
      });
    }
  }

  // Clock: only the running side's clock ticks, from the server's value.
  const started = performance.now();
  let flagged = false;
  function tick() {
    for (const id of ["yp-clock-you", "yp-clock-them"]) {
      const c = document.getElementById(id);
      if (!c || c.dataset.running !== "true") continue;
      const left = Number(c.dataset.ms) - (performance.now() - started);
      const time = c.querySelector("time");
      if (time) time.textContent = util.clockText(left);
      c.classList.toggle("yp-low", left < 20000);
      if (left <= 0 && !flagged) { flagged = true; poll(); }
    }
  }
  if (document.querySelector('[data-running="true"]')) setInterval(tick, 250);

  // Polling: reload when anything the page shows has changed.
  // A page that cannot change by itself (practice) does not poll.
  const base = Number(root.dataset.poll);
  if (!(base > 0)) return;
  let failures = 0;
  let timer = null;
  async function poll() {
    if (timer) { clearTimeout(timer); timer = null; }
    if (document.visibilityState !== "visible" || sending) { schedule(); return; }
    let status = 0;
    let body = null;
    try {
      const r = await fetch(root.dataset.state, {credentials: "same-origin", cache: "no-store", headers: {"Accept": "application/json"}});
      status = r.status;
      if (status === 200) body = await r.json();
    } catch (_) { status = 0; }
    switch (util.outcome(status)) {
      case "ok":
        failures = 0;
        if (body && typeof body.sig === "string" && body.sig !== root.dataset.sig) { location.reload(); return; }
        break;
      case "signed_out":
      case "ended":
        location.href = root.dataset.home || "/child";
        return;
      default:
        failures++;
    }
    schedule();
  }
  function schedule() { timer = setTimeout(poll, util.pollDelay(base, failures)); }
  document.addEventListener("visibilitychange", () => { if (document.visibilityState === "visible") poll(); });
  schedule();
})();
