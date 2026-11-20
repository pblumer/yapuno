"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const crypto = require("node:crypto");
const {url64, derFromP1363, validDeviceId, validNonce, loginMessage} = require("./sig-utils.js");

test("P1363 signatures convert to DER that a strict verifier accepts", () => {
  const {privateKey, publicKey} = crypto.generateKeyPairSync("ec", {namedCurve: "P-256"});
  // Many signatures, so short and high-bit r/s values are exercised.
  for (let i = 0; i < 300; i++) {
    const message = Buffer.from("yapuno-device-login-v1\0device." + i);
    const raw = crypto.sign("sha256", message, {key: privateKey, dsaEncoding: "ieee-p1363"});
    const der = Buffer.from(derFromP1363(new Uint8Array(raw)));
    assert.equal(crypto.verify("sha256", message, {key: publicKey, dsaEncoding: "der"}, der), true);
    assert.equal(crypto.verify("sha256", Buffer.from("other"), {key: publicKey, dsaEncoding: "der"}, der), false);
  }
});

test("DER integers are minimal and positive", () => {
  const raw = new Uint8Array(64);
  raw[31] = 1;            // r = 1 with 31 leading zero bytes
  raw[32] = 0x80;         // s has its high bit set and needs a 0x00 prefix
  const der = Array.from(derFromP1363(raw));
  assert.deepEqual(der.slice(0, 5), [0x30, 3 + 35, 0x02, 0x01, 0x01]);
  assert.deepEqual(der.slice(5, 9), [0x02, 33, 0x00, 0x80]);
  assert.throws(() => derFromP1363(new Uint8Array(63)));
  assert.throws(() => derFromP1363(new Uint8Array(65)));
});

test("url64 is unpadded base64url", () => {
  assert.equal(url64(new Uint8Array([0xfb, 0xff, 0xfe])), "-__-");
  assert.equal(url64(new Uint8Array([1])), "AQ");
  assert.equal(url64(new Uint8Array([])), "");
});

test("login message is fixed and only built from well-formed values", () => {
  const device = "A".repeat(22);
  const nonce = "B".repeat(43);
  assert.equal(loginMessage(device, nonce), "yapuno-device-login-v1\0" + device + "." + nonce);
  assert.equal(validDeviceId(device), true);
  assert.equal(validNonce(nonce), true);
  for (const bad of ["", "A".repeat(21), "A".repeat(23), "../" + "A".repeat(19), null, 5]) {
    assert.equal(validDeviceId(bad), false);
    assert.throws(() => loginMessage(bad, nonce));
  }
  assert.throws(() => loginMessage(device, "short"));
  assert.throws(() => loginMessage(device, nonce + "."));
});
