"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const {validInvite} = require("./scan-utils.js");
const origin = "https://app.example.org";
const token = "A".repeat(43);

test("only canonical same-origin QR fragment invites are accepted", () => {
  assert.equal(validInvite(origin + "/pair#" + token, origin), "/pair#" + token);
  assert.equal(validInvite("/pair#" + token, origin), "/pair#" + token);
  assert.equal(validInvite(origin + "/pair#bad", origin), null);
  assert.equal(validInvite(origin + "/pair?token=" + token, origin), null);
  assert.equal(validInvite(origin + "/other#" + token, origin), null);
  assert.equal(validInvite(origin + "/pair#" + token + "%2F", origin), null);
});

test("phishing domains, credentials and unsafe protocols are denied", () => {
  for (const value of [
    "https://evil.example.org/pair#" + token,
    "https://app.example.org.evil.test/pair#" + token,
    "http://app.example.org/pair#" + token,
    "javascript:alert(1)",
    "data:text/html,test",
    "https://attacker@app.example.org/pair#" + token,
    "https://app.example.org:8443/pair#" + token,
    "https://app.example.org/pair#bad#"+token,
    "not-a-url",
    "x".repeat(3000)
  ]) {
    assert.equal(validInvite(value, origin), null, value);
  }
});

test("QR invitation must not contain spaces, query strings or an empty secret", () => {
  assert.equal(validInvite(origin + "/pair#", origin), null);
  assert.equal(validInvite(origin + "/pair#"+token.slice(1)+" ", origin), null);
  assert.equal(validInvite(origin + "/pair?next=%2Fpair#" + token, origin), null);
});
