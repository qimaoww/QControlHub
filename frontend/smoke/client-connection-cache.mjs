import assert from "node:assert/strict";
import { createConnectionCache } from "../modules/client-connection-cache.js";
import { connectionQuery, defaultConnectionFilters } from "../modules/client-connection-model.js";

// View/read lifecycle coverage lives in browser/connection-read-lifecycle.mjs.
export async function run() {
    let now = Date.parse("2026-09-21T08:00:00Z");
    const filters = defaultConnectionFilters(now);
    const first = { id: 1, agent_id: "alpha", agent_name: "Alpha", engine: "xray", client_ip: "8.8.8.8", endpoints: [{ local_port: 443 }], location: { country_code: "CN", province: "广东" } };
    const cache = createConnectionCache({ now: () => now, maxEntries: 2, maxBytes: 1500 });
    const result = { records: [first], sources: [], timeline: [] };
    cache.set("a", result); cache.set("b", result); cache.get("a"); cache.set("c", result);
    assert.equal(cache.get("b"), null, "least recently used result is evicted");
    assert.ok(cache.get("a"));
    cache.set("large", { text: "x".repeat(2000) });
    assert.equal(cache.get("large"), null, "oversized payload is not retained");
    now += 301_000;
    assert.equal(cache.get("a"), null, "old snapshots expire");
    const scoped = createConnectionCache({ now: () => now });
    scoped.set(connectionQuery(filters).toString(), result);
    assert.equal(scoped.preview(connectionQuery({ ...filters, date: "2026-09-19" })), null, "different time windows never share preview data");
    assert.equal(scoped.preview(connectionQuery({ ...filters, include_non_public: "true" })), null, "source ranges never share preview data");
    assert.equal(scoped.preview(connectionQuery(filters, "", ["bravo", "alpha"])), null, "a different node order cannot reuse a page preview");
    assert.equal(scoped.get(connectionQuery(filters).toString()).fresh, true);
    now += 31000;
    assert.equal(scoped.get(connectionQuery(filters).toString()).fresh, false, "matching snapshots become stale after 30 seconds");
    assert.equal(scoped.preview(connectionQuery(filters, 'page-2')), null, "a first-page preview must not impersonate a cursor page");
    scoped.clear();
    assert.equal(scoped.get(connectionQuery(filters).toString()), null, "authorization invalidation clears retained snapshots");
    console.log("Connection cache TTL, LRU, byte bounds and preview isolation passed");
}
