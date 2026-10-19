import assert from "node:assert/strict";
import { createNodeOrderSync, nodeCardOrderKey, orderNodesBySavedOrder } from "./modules/node-order.js";
const memory = initial => {
  const data = new Map(initial ? [[nodeCardOrderKey, JSON.stringify(initial)]] : []);
  return { getItem: key => data.get(key) ?? null, setItem: (key, value) => data.set(key, value) };
};
const deferred = () => { let resolve; const promise = new Promise(done => { resolve = done; }); return { resolve, promise }; };
const flush = () => new Promise(setImmediate);
let server = { ids: ["a", "b", "c", "d"], revision: 0 }, puts = 0;
let delay = null, fail = false, ambiguous = false;
async function api(_path, options) {
  if (delay) { const wait = delay; delay = null; await wait.promise; }
  if (!options) return structuredClone(server);
  puts++;
  const input = JSON.parse(options.body);
  if (input.initialize && server.revision) return structuredClone(server);
  if (input.revision !== server.revision) throw Object.assign(new Error("conflict"), { status: 409 });
  if (fail) throw new Error("offline");
  const selected = new Set(input.ids), ids = [...input.ids];
  server = { ids: input.initialize ? [...ids, ...server.ids.filter(id => !selected.has(id))] : server.ids.map(id => selected.has(id) ? ids.shift() : id), revision: server.revision + 1 };
  if (ambiguous) { ambiguous = false; throw new Error("response lost"); }
  return structuredClone(server);
}
const storageA = memory(["d", "b", "foreign"]), storageB = memory(["c", "a"]);
const a = createNodeOrderSync(api, storageA), b = createNodeOrderSync(api, storageB);
// Two devices both observe an absent order; the first initializer wins.
await Promise.all([a.load(), b.load()]);
assert.deepEqual(a.ids(), ["d", "b", "a", "c"]);
assert.deepEqual(b.ids(), a.ids());
assert.equal(server.revision, 1);
const fresh = createNodeOrderSync(api, memory());
await fresh.load(); assert.deepEqual(fresh.ids(), a.ids(), "empty browser restores database preference");
const wait = deferred(); delay = wait;
const first = a.save(["b", "d"]), second = a.save(["d", "b"]);
await flush(); wait.resolve(); await Promise.all([first, second]);
assert.deepEqual(a.ids(), ["d", "b", "a", "c"], "partial saves preserve other slots and final gesture wins");
await b.save(["a", "b"]);
assert.deepEqual(b.ids(), ["d", "a", "b", "c"], "stale device rebases its subset after conflict");
await a.load(); assert.deepEqual(a.ids(), b.ids());
fail = true;
await assert.rejects(a.save(["b", "a"]));
assert.deepEqual(a.ids(), server.ids, "failure retains server order"); fail = false;
ambiguous = true; assert.equal(await a.save(["b", "a"]), true, "readback confirms a lost response after commit");
let current = true; const oldStorage = memory();
const old = createNodeOrderSync(api, oldStorage, () => current);
const pending = deferred(); delay = pending;
const stale = old.load(); await flush(); current = false; pending.resolve();
await assert.rejects(stale, { name: "AbortError" });
assert.equal(oldStorage.getItem(nodeCardOrderKey), null, "old request never populates next account cache");
const before = puts;
server = { ids: ["a", "b"], revision: 0 };
const untrusted = createNodeOrderSync(api, memory(["b", "a"]), () => true, false);
await untrusted.load(); assert.equal(puts, before, "ambiguous account cache is never uploaded");
const empty = createNodeOrderSync(api, memory());
await empty.load(); await empty.load(); assert.equal(puts, before, "server cache cannot become a legacy migration");
assert.deepEqual(orderNodesBySavedOrder([{ id: "b" }, { id: "c" }, { id: "a" }], ["a", "b"]).map(n => n.id), ["a", "b", "c"]);
console.log("Account node order queue, migration race, conflict, failure and stale-account smoke passed");
// A lost write response plus an unavailable readback is explicitly uncertain.
{
 let offline = false, reads = 0;
 const uncertain = createNodeOrderSync(async (_path, options) => {
  if (offline) throw new Error("network unavailable");
  if (options) { offline = true; throw new Error("write response lost"); }
  reads++; return { ids: ["a", "b"], revision: 1 };
 }, memory());
 await uncertain.load();
 await assert.rejects(uncertain.save(["b", "a"]), error => error.orderUnconfirmed === true);
 assert.equal(reads, 1);
}
{
 const blocked = { getItem() { throw new Error("storage disabled"); }, setItem() { throw new Error("storage disabled"); } };
 const restored = createNodeOrderSync(async () => ({ ids: ["b", "a"], revision: 4 }), blocked);
 await restored.load(); assert.deepEqual(restored.ids(), ["b", "a"], "database reads survive blocked local storage");
}
