import assert from "node:assert/strict";
import { createConnectionCache } from "../modules/client-connection-cache.js";
import { installClientConnections } from "../modules/client-connections.js";
import { connectionQuery, defaultConnectionFilters } from "../modules/client-connection-model.js";

export async function run() {
  const originalDocument = globalThis.document, originalNow = Date.now;
  let now = Date.parse("2026-09-21T08:00:00Z");
  Date.now = () => now;
  globalThis.document = { querySelector: () => null, querySelectorAll: () => [] };
  try {
    const filters = defaultConnectionFilters(now);
    const first = { id: 1, agent_id: "alpha", agent_name: "Alpha", engine: "xray", client_ip: "8.8.8.8", endpoints: [{ local_port: 443 }], location: { country_code: "CN", province: "广东" } };
    const second = { ...first, id: 2, agent_id: "bravo", agent_name: "Bravo", engine: "sing-box", client_ip: "1.1.1.1" };
    const state = { data: { connectionFilters: filters }, navigationEpoch: 1, route: "client-connections" };
    let paints = [], reads = [], pending, failure;
    const load = installClientConnections({ state, esc: value => String(value ?? ""), engineName: value => value, date: value => value,
      shell: markup => paints.push(markup),
      api: async (path, { signal }) => {
        reads.push({ path, signal });
        const query = new URL(path, "http://test").searchParams;
        if (pending) return pending;
        if (failure) throw failure;
        const records = (query.has("cursor") ? [second] : [first, second]).filter(row => (!query.get("agent_id") || row.agent_id === query.get("agent_id")) && (!query.get("engine") || row.engine === query.get("engine")) && (!query.get("client_ip") || row.client_ip === query.get("client_ip")));
        return { records, ips: records.length, flows: 10, sources: [], timeline: [], next_cursor: query.has("cursor") ? "" : "page-2" };
      },
    });
    await load();
    assert.equal(reads.length, 1);
    state.data.connectionFilters = { ...filters, agent_id: "alpha" };
    await load();
    assert.equal(reads.length, 2);
    const alphaPaints = paints.slice(-2);
    assert.ok(alphaPaints[0].includes("8.8.8.8") && !alphaPaints[0].includes("1.1.1.1"), "cold narrower filter paints only matching cached records");
    assert.ok(alphaPaints[0].includes("观测连接</span> <strong>—"), "partial preview cannot claim broader totals");
    state.data.connectionFilters = filters;
    await load();
    assert.equal(reads.length, 2, "switching back to a fresh scope makes no read");
    assert.ok(paints.at(-1).includes("1.1.1.1"));
    await load({ cursor: "page-2", cursors: [""] });
    assert.equal(reads.length, 3);
    await load();
    await load({ cursor: "page-2", cursors: [""] });
    assert.equal(reads.length, 3, "previous and next page both reuse cache");
    now += 31_000;
    let finish;
    pending = new Promise(resolve => { finish = resolve; });
    const staleRead = load();
    assert.equal(reads.length, 4);
    assert.ok(paints.at(-1).includes("8.8.8.8"), "stale refresh retains matching rows");
    assert.ok(!paints.at(-1).includes('type="submit" disabled'), "filter remains usable while reading");
    finish({ records: [first], ips: 1, flows: 1, sources: [], timeline: [] });
    await staleRead;
    pending = null;
    await load();
    assert.equal(reads.length, 4, "updated result becomes fresh");
    failure = new Error("offline");
    await load({ force: true });
    assert.equal(reads.length, 5, "explicit refresh bypasses the cache");
    assert.ok(paints.at(-1).includes("8.8.8.8") && paints.at(-1).includes("保留上次结果"));
    failure = null;
    await load();
    failure = Object.assign(new Error("forbidden"), { status: 403 });
    await load({ force: true });
    assert.ok(!paints.at(-1).includes("8.8.8.8"), "authorization denial clears visible history");
    assert.equal(state.data.connectionCache.get(connectionQuery(filters).toString()), null);
    failure = null;
    state.data = { connectionFilters: filters };
    const beforeAccount = reads.length;
    await load();
    assert.equal(reads.length, beforeAccount + 1, "account replacement cannot reuse prior history");

    const beforeFilters = reads.length;
    for (const changed of [{ engine: "xray" }, { engine: "sing-box" }, { client_ip: "8.8.8.8" }]) {
      state.data.connectionFilters = { ...filters, ...changed };
      await load();
    }
    assert.equal(reads.length, beforeFilters + 3);
    for (const changed of [{ engine: "xray" }, { engine: "sing-box" }, { client_ip: "8.8.8.8" }]) {
      state.data.connectionFilters = { ...filters, ...changed };
      await load();
      const expected = changed.engine === "sing-box" ? "1.1.1.1" : "8.8.8.8";
      assert.ok(paints.at(-1).includes(expected));
      assert.ok(!paints.at(-1).includes(expected === "8.8.8.8" ? "1.1.1.1" : "8.8.8.8"));
    }
    assert.equal(reads.length, beforeFilters + 3, "engine and IP filters each retain their own cache");
    const geoState = { data: { connectionFilters: filters }, navigationEpoch: 1, route: "client-connections" };
    let geoReads = 0;
    const geoLoad = installClientConnections({ state: geoState, esc: String, engineName: String, date: String, shell: () => {}, api: async () => {
      geoReads++;
      return { records: [{ ...first, location: {} }], sources: [], timeline: [] };
    } });
    await geoLoad();
    await Promise.resolve();
    await geoLoad();
    assert.equal(geoReads, 2, "an unknown geography result does not retrigger lookup on every cache hit");
    const chinaState = { data: { connectionFilters: filters }, navigationEpoch: 1, route: "client-connections" };
    let chinaReads = 0, chinaMarkup = "";
    const chinaLoad = installClientConnections({ state: chinaState, esc: String, engineName: String, date: String,
      shell: markup => { chinaMarkup = markup; },
      api: async path => {
        chinaReads++;
        const location = path.includes("locations=only") ? { country_code: "CN", province: "江苏" } : { country_code: "CN" };
        return { records: [{ ...first, location }], sources: [], timeline: [] };
      },
    });
    await chinaLoad();
    await Promise.resolve();
    assert.equal(chinaReads, 2, "a cached China country without province still requests enrichment");
    assert.ok(chinaMarkup.includes("中国 · 江苏"));

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
    console.log("Connection query cache, pagination, refresh and account isolation passed");
  } finally { globalThis.document = originalDocument; Date.now = originalNow; }
}
