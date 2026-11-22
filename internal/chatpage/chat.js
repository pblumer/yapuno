"use strict";

// Yapuno chat page enhancement. The page works without this script (form
// post and reload). With it: visible sending states, safe retries with the
// same client id, catch-up polling and a draft that survives a reload.
// Message text is only ever placed with textContent.
(() => {
  const chat = globalThis.YapunoChat;
  const root = document.getElementById("yp-chat");
  const list = document.getElementById("yp-messages");
  const form = document.getElementById("yp-compose");
  const field = document.getElementById("yp-body");
  const banner = document.getElementById("yp-banner");
  if (!chat || !root || !list || !form || !field || !banner) return;

  const conversation = root.dataset.conversation;
  const csrf = root.dataset.csrf;
  // The page says where its messages live and where "home" is, so the same
  // script serves the child area and the guardian area.
  const base = root.dataset.base || "/child/chat/" + conversation + "/messages";
  const home = root.dataset.home || "/child";
  const draftKey = "yapuno-draft-" + conversation;
  const pendingKey = "yapuno-pending-" + conversation;
  const known = new Set();
  let cursor = Number(root.dataset.lastSeq) || 0;
  let failures = 0;
  let ended = false;
  let timer = null;

  function store(action, key, value) {
    // Storage can be blocked; the chat still works without it.
    try {
      if (action === "get") return sessionStorage.getItem(key);
      if (action === "set") sessionStorage.setItem(key, value);
      if (action === "del") sessionStorage.removeItem(key);
    } catch (_) { /* ignore */ }
    return null;
  }
  function say(message) {
    banner.textContent = message;
    banner.hidden = message === "";
  }
  function toEnd() { list.scrollTop = list.scrollHeight; window.scrollTo(0, document.body.scrollHeight); }

  function bubble(mine, body) {
    const empty = list.querySelector(".yp-empty");
    if (empty) empty.remove();
    const item = document.createElement("li");
    item.className = "yp-msg " + (mine ? "yp-mine" : "yp-theirs");
    const text = document.createElement("p");
    text.className = "yp-text";
    text.textContent = body;
    const meta = document.createElement("p");
    meta.className = "yp-meta";
    item.append(text, meta);
    list.append(item);
    return {item, meta};
  }
  function showIncoming(messages) {
    const {fresh, cursor: next} = chat.unseen(known, messages);
    for (const m of fresh) {
      const {item, meta} = bubble(m.mine, m.body);
      item.dataset.seq = String(m.seq);
      meta.textContent = m.mine ? chat.statusText("sent", chat.clock(m.at)) : chat.clock(m.at);
    }
    if (next > cursor) cursor = next;
    if (fresh.length) toEnd();
  }

  // Unsent messages are remembered until the server confirms them, so a
  // reload or a renewed sign-in does not lose what the child wrote.
  function pending() {
    try { return JSON.parse(store("get", pendingKey) || "[]").filter(p => p && typeof p.id === "string" && typeof p.body === "string"); } catch (_) { return []; }
  }
  function remember(id, body) { store("set", pendingKey, JSON.stringify(pending().filter(p => p.id !== id).concat([{id, body}]))); }
  function forget(id) { store("set", pendingKey, JSON.stringify(pending().filter(p => p.id !== id))); }

  function endConversation() {
    ended = true;
    if (timer) clearTimeout(timer);
    field.disabled = true;
    form.querySelector("button").disabled = true;
    say("Ihr seid nicht mehr verbunden. Nachrichten sind hier nicht mehr möglich.");
  }

  async function deliver(id, body, view) {
    view.meta.textContent = chat.statusText("sending");
    view.item.setAttribute("aria-busy", "true");
    const old = view.item.querySelector("button");
    if (old) old.remove();
    let status = 0;
    let data = null;
    try {
      // No "mode: same-origin" here: with it, and the page's Referrer-Policy
      // no-referrer, the Fetch standard makes Safari and Firefox send
      // "Origin: null". The URL is a fixed same-origin path and redirects are
      // refused, so the request cannot leave this origin either way.
      const response = await fetch(base, {
        method: "POST", credentials: "same-origin", cache: "no-store", redirect: "error",
        headers: {"Content-Type": "application/json", "X-CSRF-Token": csrf},
        body: JSON.stringify({client_id: id, body})
      });
      status = response.status;
      if (status === 200 || status === 201) data = await response.json();
    } catch (_) { status = 0; }
    view.item.removeAttribute("aria-busy");
    const result = chat.outcome(status);
    if (result === "sent" && data && Number.isSafeInteger(data.seq)) {
      forget(id);
      known.add(data.seq);
      if (data.seq > cursor) cursor = data.seq;
      say("");
      // The same message may already be on screen: rendered by the server
      // before a reload, or fetched by polling before this answer arrived.
      const shown = list.querySelector('li[data-seq="' + data.seq + '"]');
      if (shown && shown !== view.item) {
        view.item.remove();
        return;
      }
      view.item.dataset.seq = String(data.seq);
      view.meta.textContent = chat.statusText("sent", chat.clock(data.at));
      return;
    }
    if (result === "reauth") {
      // The message stays remembered and is offered again after sign-in.
      location.replace(home);
      return;
    }
    if (result === "ended") {
      forget(id);
      view.meta.textContent = chat.statusText("retry");
      endConversation();
      return;
    }
    if (result === "reject") {
      forget(id);
      view.meta.textContent = chat.statusText("reject");
      return;
    }
    view.meta.textContent = chat.statusText(result === "wait" ? "wait" : "retry");
    const again = document.createElement("button");
    again.type = "button";
    again.className = "yp-retry";
    again.textContent = "Erneut senden";
    again.addEventListener("click", () => deliver(id, body, view));
    view.item.append(again);
    if (result === "retry") say("Keine Verbindung. Deine Nachricht wurde noch nicht gesendet.");
  }

  form.addEventListener("submit", event => {
    event.preventDefault();
    if (ended) return;
    const body = chat.prepare(field.value);
    if (body === null) {
      say(field.value.trim() === "" ? "" : "Diese Nachricht ist zu lang.");
      return;
    }
    const id = chat.clientId(crypto.getRandomValues(new Uint8Array(16)));
    remember(id, body);
    field.value = "";
    store("del", draftKey);
    const view = bubble(true, body);
    toEnd();
    deliver(id, body, view);
    field.focus();
  });
  field.addEventListener("input", () => store("set", draftKey, field.value));

  async function poll() {
    if (ended) return;
    if (document.visibilityState === "visible") {
      try {
        const response = await fetch(base + "?after=" + cursor, {mode: "same-origin", credentials: "same-origin", cache: "no-store", redirect: "error"});
        if (response.status === 200) {
          showIncoming((await response.json()).messages);
          if (failures > 0) say("");
          failures = 0;
        } else if (response.status === 401) {
          location.replace(home);
          return;
        } else if (response.status === 403) {
          endConversation();
          return;
        } else {
          failures++;
        }
      } catch (_) {
        failures++;
        say("Keine Verbindung. Neue Nachrichten erscheinen, sobald du wieder online bist.");
      }
    }
    timer = setTimeout(poll, chat.pollDelay(failures));
  }

  // Start-up: adopt server-rendered messages, restore draft and unsent texts.
  for (const item of list.querySelectorAll("li[data-seq]")) {
    const seq = Number(item.dataset.seq);
    if (Number.isSafeInteger(seq)) known.add(seq);
    const time = item.querySelector("time");
    const local = time ? chat.clock(time.getAttribute("datetime")) : "";
    if (time && local) time.textContent = local;
  }
  if (!field.value) field.value = store("get", draftKey) || "";
  for (const p of pending()) {
    const view = bubble(true, p.body);
    deliver(p.id, p.body, view);
  }
  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible" && !ended) { if (timer) clearTimeout(timer); poll(); }
  });
  toEnd();
  timer = setTimeout(poll, chat.pollDelay(0));
})();
