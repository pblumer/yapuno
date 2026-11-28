"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const {parseLegal, targets, sources, clockText, pollDelay, outcome, promotionChoice} = require("./chess-utils.js");

test("legal moves are read strictly", () => {
  assert.deepEqual(parseLegal("e2e4 g1f3  e7e8q"), ["e2e4", "g1f3", "e7e8q"]);
  assert.deepEqual(parseLegal(""), []);
  assert.deepEqual(parseLegal(undefined), []);
  // Anything that is not a move is dropped, never passed on.
  assert.deepEqual(parseLegal("e2e4 <b> e9e1 e2e4x a7a8Q"), ["e2e4"]);
});

test("targets group promotion choices by square", () => {
  const legal = parseLegal("e7e8q e7e8r e7e8b e7e8n e7d8q e7d8r e7d8b e7d8n g1f3");
  const fromPawn = targets(legal, "e7");
  assert.deepEqual([...fromPawn.keys()].sort(), ["d8", "e8"]);
  assert.equal(fromPawn.get("e8").length, 4);
  assert.deepEqual(targets(legal, "g1").get("f3"), ["g1f3"]);
  assert.equal(targets(legal, "a1").size, 0);
  assert.deepEqual([...sources(legal)].sort(), ["e7", "g1"]);
});

test("a promotion is chosen only among offered moves", () => {
  const options = ["e7e8q", "e7e8r", "e7e8b", "e7e8n"];
  assert.equal(promotionChoice(options, "q"), "e7e8q");
  assert.equal(promotionChoice(options, "n"), "e7e8n");
  assert.equal(promotionChoice(options, ""), null);
  assert.equal(promotionChoice(options, "k"), null);
  assert.equal(promotionChoice(["g1f3"], "q"), null);
});

test("clocks show minutes and seconds, rounded down, never negative", () => {
  assert.equal(clockText(300000), "5:00");
  assert.equal(clockText(293999), "4:53");
  assert.equal(clockText(9500), "0:09");
  assert.equal(clockText(0), "0:00");
  assert.equal(clockText(-5), "0:00");
  assert.equal(clockText(NaN), "0:00");
});

test("polling slows down after failures and is bounded", () => {
  assert.equal(pollDelay(1000, 0), 1000);
  assert.equal(pollDelay(1000, 2), 4000);
  assert.equal(pollDelay(4000, 10), 30000);
  assert.equal(pollDelay(undefined, 0), 4000);
  assert.equal(pollDelay(1000, -1), 1000);
});

test("state answers map to one action each", () => {
  assert.equal(outcome(200), "ok");
  assert.equal(outcome(401), "signed_out");
  assert.equal(outcome(403), "ended");
  assert.equal(outcome(0), "retry");
  assert.equal(outcome(503), "retry");
});
