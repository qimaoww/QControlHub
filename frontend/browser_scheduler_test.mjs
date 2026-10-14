import assert from "node:assert/strict";
import test from "node:test";
import { browserShards } from "./browser_scheduler.mjs";
test("one and two process plans cover every scene exactly once, including new scenes", () => {
  const modes = ["motion-cards", "motion-cards-reduced", "logs", "users-mobile", "new-permission-scene"];
  for (const workers of [1, 2]) {
    const shards = browserShards(modes, workers);
    assert.equal(shards.length, workers);
    assert.deepEqual(shards.flat().sort(), [...modes].sort());
  }
  assert.throws(() => browserShards(["logs", "logs"], 2), /duplicate/);
  assert.throws(() => browserShards(modes, 3), /1 or 2/);
});
