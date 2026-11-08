"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const qrcode = require("./qrcode.vendor.js");

test("local QR renderer produces a self-contained scannable SVG", () => {
  const invite = "https://app.example.org/pair#" + "A".repeat(43);
  const qr = qrcode(0, "M");
  qr.addData(invite, "Byte");
  qr.make();

  assert.ok(qr.getModuleCount() >= 21);
  assert.equal(typeof qr.isDark(0, 0), "boolean");

  const svg = qr.createSvgTag({cellSize: 7, margin: 28});
  assert.match(svg, /<svg\b/);
  assert.match(svg, /<rect\b/);
  assert.match(svg, /<\/svg>/);
  assert.doesNotMatch(svg, /<script|<image|xlink:href=|<foreignObject/i);
});

test("QR library handles maximum-length canonical invitation hostname", () => {
  const invite = "https://preview.yapuno.app/pair#" + "AbCd012_-".repeat(4) + "AbCd012";
  assert.equal(invite.slice(invite.indexOf("#") + 1).length, 43);
  const qr = qrcode(0, "M");
  qr.addData(invite, "Byte");
  assert.doesNotThrow(() => qr.make());
  assert.ok(qr.getModuleCount() >= 21);
});
