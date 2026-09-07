import assert from "node:assert/strict";
import { createScopedAPI } from "./modules/requests.js";

const calls = [];
const api = createScopedAPI(async (path, options) => {
  calls.push({ path, options });
  return { items: [path] };
});
api.begin();
const [first, second] = await Promise.all([api.request("/agents"), api.request("/agents")]);
assert.equal(calls.length, 1, "prefetch and page share one read");
first.items.push("changed");
assert.deepEqual(second.items, ["/agents"], "callers cannot mutate each other's responses");
await api.request("/agents");
assert.equal(calls.length, 1, "completed prefetch stays usable until rendering completes");
await api.request("/agents", { signal: new AbortController().signal });
assert.equal(calls.length, 2, "independent cancellation bypasses sharing");
await api.request("/agents", { method: "PUT" });
await api.request("/agents");
assert.equal(calls.length, 4, "writes invalidate prefetched data");
api.end();
await api.request("/agents");
await api.request("/agents");
assert.equal(calls.length, 6, "polling outside rendering always reads fresh data");
api.begin();
await api.request("/agents");
assert.equal(calls.length, 7, "a new navigation cannot reuse old data");
await api.request("/auth/session");
await api.request("/auth/session");
assert.equal(calls.length, 9, "authentication is never cached");
api.end();

// A render can read while a save is in flight. That pre-commit snapshot must
// not be reused by the refresh that follows successful or failed completion.
for (const fails of [false, true]) {
  let version = 1;
  let complete;
  const concurrent = createScopedAPI(async (_path, options) => {
    if (options.method === "PUT") {
      return new Promise((resolve, reject) => {
        complete = () => {
          version = 2;
          if (fails) reject(new Error("response lost after save"));
          else resolve({ version });
        };
      });
    }
    return { version };
  });
  concurrent.begin();
  const saved = concurrent.request("/settings", { method: "PUT" });
  assert.equal((await concurrent.request("/settings")).version, 1);
  complete();
  if (fails) await assert.rejects(saved, /response lost/);
  else await saved;
  assert.equal((await concurrent.request("/settings")).version, 2,
    "write completion invalidates any read cached while the write was pending");
  concurrent.end();
}
