"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const {clientId, prepare, outcome, statusText, unseen, clock, pollDelay, MAX_CHARS} = require("./chat-utils.js");

test("client ids match the server pattern and differ per message", () => {
  const a = clientId(new Uint8Array(16).fill(255));
  const b = clientId(new Uint8Array(16));
  assert.match(a, /^[A-Za-z0-9_-]{16,64}$/);
  assert.match(b, /^[A-Za-z0-9_-]{16,64}$/);
  assert.notEqual(a, b);
  assert.throws(() => clientId(new Uint8Array(8)));
  assert.throws(() => clientId("0123456789abcdef"));
});

test("text is prepared like the server normalises it", () => {
  assert.equal(prepare("  Hoi\r\nMia \n"), "Hoi\nMia");
  assert.equal(prepare(""), null);
  assert.equal(prepare(" \n\t "), null);
  assert.equal(prepare(null), null);
  assert.equal(prepare("ä".repeat(MAX_CHARS)).length, MAX_CHARS);
  assert.equal(prepare("ä".repeat(MAX_CHARS + 1)), null);
  // Characters outside the BMP count once, as on the server.
  assert.notEqual(prepare("😀".repeat(MAX_CHARS)), null);
});

test("send outcomes never treat an unknown result as sent", () => {
  assert.equal(outcome(201), "sent");
  assert.equal(outcome(200), "sent");
  assert.equal(outcome(401), "reauth");
  assert.equal(outcome(403), "ended");
  assert.equal(outcome(429), "wait");
  for (const status of [400, 409, 413, 415]) assert.equal(outcome(status), "reject");
  for (const status of [0, 204, 302, 404, 500, 502, 503, 504, undefined]) assert.equal(outcome(status), "retry");
});

test("status lines say what happened in words", () => {
  assert.equal(statusText("sending"), "Wird gesendet …");
  assert.equal(statusText("sent", "16:41"), "Gesendet · 16:41");
  assert.equal(statusText("sent"), "Gesendet");
  assert.match(statusText("retry"), /^Nicht gesendet/);
  assert.match(statusText("wait"), /^Nicht gesendet/);
  assert.match(statusText("reject"), /^Nicht gesendet/);
  assert.equal(statusText("anything else"), "");
});

test("only unseen, well-formed messages are returned in order", () => {
  const known = new Set([1, 2]);
  const {fresh, cursor} = unseen(known, [
    {seq: 4, mine: false, body: "vier", at: "2026-11-20T12:00:04Z"},
    {seq: 2, mine: true, body: "schon da"},
    {seq: 3, mine: "yes", body: "drei"},
    {seq: 3, mine: true, body: "doppelt"},
    {seq: "5", body: "kein Zahlwert"},
    {seq: 0, body: "null"},
    {seq: 6},
    null,
  ]);
  assert.deepEqual(fresh.map(m => m.seq), [3, 4]);
  assert.equal(fresh[0].mine, false, "only a literal true marks an own message");
  assert.equal(fresh[0].body, "drei");
  assert.equal(cursor, 4);
  assert.deepEqual(unseen([1], "not a list"), {fresh: [], cursor: 1});
  assert.deepEqual(unseen(undefined, undefined), {fresh: [], cursor: 0});
});

test("clock and poll delay are defensive", () => {
  assert.match(clock("2026-11-20T12:34:56Z"), /^\d\d:\d\d$/);
  assert.equal(clock(""), "");
  assert.equal(clock("not a date"), "");
  assert.equal(pollDelay(0), 3000);
  assert.equal(pollDelay(1), 6000);
  assert.equal(pollDelay(10), 30000);
  assert.equal(pollDelay(-3), 3000);
  assert.equal(pollDelay("x"), 3000);
});
