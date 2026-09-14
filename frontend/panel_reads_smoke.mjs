import assert from "node:assert/strict";
import { createPanelReads } from "./modules/panel-reads.js";

let clock = 0;
const reads = [];
const count = (key) => reads.filter((entry) => entry === key).length;
const panelReads = createPanelReads(async (key) => {
  reads.push(key);
  return { key, revision: reads.length };
}, { now: () => clock });

const session = { scope: "session-a" };

// The shell renders on every navigation, but the value it shares only has to be
// read once per TTL; that is the whole point of the cache.
const first = await panelReads.get("overview", session);
assert.equal(reads.length, 1, "the first read reaches the API");
assert.equal(await panelReads.get("overview", session), first, "a later render reuses the cached value");
assert.equal(reads.length, 1, "repeated renders inside the TTL do not refetch");
await panelReads.get("settings", session);
await panelReads.get("settings", session);
assert.equal(count("settings"), 1, "each panel read is cached on its own");

clock = 29_000;
await panelReads.get("overview", session);
assert.equal(count("overview"), 1, "the overview stays cached inside its TTL");
clock = 31_000;
await panelReads.get("overview", session);
assert.equal(count("overview"), 2, "the overview refetches once its TTL expires");
assert.equal(count("settings"), 1, "a shorter TTL does not evict the longer one");

// A route that displays the counter live must not be served the cached copy.
await panelReads.get("overview", { ...session, live: true });
assert.equal(count("overview"), 3, "a live route always reads through the cache");
clock = 31_500;
await panelReads.get("overview", session);
assert.equal(count("overview"), 3, "the live read refreshed the shared entry");

// Saving the panel settings has to reach every other route immediately.
panelReads.invalidate("settings");
await panelReads.get("settings", session);
assert.equal(count("settings"), 2, "a write invalidates the entry it changed");
await panelReads.get("overview", session);
assert.equal(count("overview"), 3, "invalidating one key leaves the others cached");

// Signing in as another account must not inherit the previous account's shell.
await panelReads.get("overview", { scope: "session-b" });
assert.equal(count("overview"), 4, "another account reads its own shell data");
panelReads.invalidate();
clock = 31_600;
await panelReads.get("overview", session);
assert.equal(count("overview"), 5, "a full invalidation clears every account");

// Failures are not cached: the next render retries instead of pinning an error.
let failing = true;
let attempts = 0;
const retrying = createPanelReads(async () => {
  attempts += 1;
  if (failing) throw new Error("temporary failure");
  return { ok: true };
}, { now: () => clock });
await assert.rejects(retrying.get("settings", session), /temporary failure/);
assert.equal(attempts, 1, "a failed read reaches the caller");
failing = false;
assert.deepEqual(await retrying.get("settings", session), { ok: true }, "a failed read retries on the next render");
assert.equal(attempts, 2, "the failure was not cached for the TTL");
assert.throws(() => retrying.get("unknown", session), /unknown panel read/, "only known panel reads are cached");

process.stdout.write("panel read cache smoke passed\n");
