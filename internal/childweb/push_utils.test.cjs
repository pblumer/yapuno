"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const {keyBytes, situation, outcome, message, buttons} = require("./push-utils.js");

test("the public key becomes the 65 bytes of an uncompressed P-256 point", () => {
  const raw = new Uint8Array(65).fill(7);
  raw[0] = 4;
  const b64url = Buffer.from(raw).toString("base64url");
  assert.deepEqual(keyBytes(b64url), raw);
  assert.throws(() => keyBytes(""));
  assert.throws(() => keyBytes("not+base64url/"));
  assert.throws(() => keyBytes(Buffer.from(new Uint8Array(64)).toString("base64url")));
  const compressed = new Uint8Array(65);
  compressed[0] = 2;
  assert.throws(() => keyBytes(Buffer.from(compressed).toString("base64url")));
  assert.throws(() => keyBytes(undefined));
});

test("an iPhone outside the Home Screen is told how to install", () => {
  const none = {serviceWorker: true, pushManager: false, notification: false};
  assert.equal(situation({...none, apple: true, standalone: false}), "install");
  assert.equal(situation({...none, apple: false}), "unsupported");
  assert.equal(situation({...none, apple: true, standalone: true}), "unsupported");
  assert.equal(situation({serviceWorker: true, pushManager: true, notification: true, permission: "default"}), "ready");
  assert.equal(situation({serviceWorker: true, pushManager: true, notification: true, permission: "denied"}), "denied");
  assert.equal(situation(undefined), "unsupported");
});

test("only a stored subscription counts as switched on", () => {
  assert.equal(outcome(204, true), "on");
  assert.equal(outcome(204, false), "off");
  assert.equal(outcome(401, true), "signed_out");
  for (const status of [0, 400, 403, 409, 503]) {
    assert.equal(outcome(status, true), "failed");
    assert.equal(outcome(status, false), "failed_off");
  }
});

test("every state has a German message and sensible buttons", () => {
  for (const state of ["on", "off", "working", "install", "unsupported", "denied", "failed", "failed_off", "signed_out"]) {
    assert.ok(message(state).length > 10, state);
  }
  assert.equal(message("unknown"), message("failed"));
  assert.deepEqual(buttons("off"), {on: true, off: false});
  assert.deepEqual(buttons("on"), {on: false, off: true});
  assert.deepEqual(buttons("failed_off"), {on: false, off: true});
  for (const state of ["working", "install", "unsupported", "denied", "signed_out"]) {
    assert.deepEqual(buttons(state), {on: false, off: false}, state);
  }
});
