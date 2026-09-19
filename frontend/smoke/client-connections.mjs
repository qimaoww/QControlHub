import assert from "node:assert/strict";
import { connectionQuery, connectionAddress, connectionSourceLabel, defaultConnectionFilters, connectionLocationLabel } from "../modules/client-connection-model.js";
import { createClientConnectionView } from "../modules/client-connection-view.js";
import { installClientConnections } from "../modules/client-connections.js";

export async function run() {
const filters = defaultConnectionFilters(Date.parse("2026-09-19T08:00:00Z"));
const query = connectionQuery({ ...filters, client_ip: "2001:db8::1", port: "443", inbound: "a&b" }, 7);
assert.equal(query.get("client_ip"), "2001:db8::1");
assert.equal(query.get("inbound"), "a&b");
assert.equal(query.get("before"), "7");
assert.equal(query.has("include_non_public"), false);
assert.equal(connectionQuery({ ...filters, include_non_public: "true" }, 7).get("include_non_public"), "true");
assert.throws(() => connectionQuery({ since: "bad", until: "bad" }), /7/);
assert.throws(() => connectionQuery({ since: "2026-01-01", until: "2026-02-01" }), /7/);
assert.equal(connectionLocationLabel({ country_code: "CN", province: "广东" }), "中国 · 广东");
assert.equal(connectionLocationLabel({ country_code: "US", province: "California" }), "美国");
assert.equal(connectionLocationLabel({ country_code: "CN" }), "中国");
assert.equal(connectionLocationLabel({ non_public: true }), "非公网");
assert.equal(connectionLocationLabel({}), "—");
assert.equal(connectionAddress("2001:db8::1", 443), "[2001:db8::1]:443");
assert.match(connectionSourceLabel({}), /升级/);
assert.match(connectionSourceLabel({ updated_at: "2020-01-01" }), /过期/);
const esc = value => String(value).replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll('"', "&quot;");
const view = createClientConnectionView({ esc, engineName: value => value, date: value => value });
const html = view({ ips: 1, flows: 2, records: [{ agent_name: "<script>", inbound: "<img>", client_ip: "2001:db8::1", client_port: 50123, local_ip: "192.0.2.1", local_port: 443, protocol: "vless", engine: "xray", transport: "tcp" }] }, filters, []);
assert.ok(html.includes("&lt;script>"));
assert.ok(!html.includes("<script>"));
assert.match(html, /入站来源 IP/);
assert.match(html, /\[2001:db8::1\]:50123/);

// Construction is inert; stale reads from a previous account/navigation cannot paint.
const oldDocument = globalThis.document;
globalThis.document = { querySelector: () => null };
try {
  const state = { data: {}, navigationEpoch: 1, route: "client-connections" };
  let calls = 0, paints = 0, resolve;
  const load = installClientConnections({ state, esc, engineName: value => value, date: value => value, shell: () => paints++, api: () => { calls++; return new Promise(done => { resolve = done; }); } });
  assert.equal(calls, 0);
  assert.equal(paints, 0);
  const pending = load();
  assert.equal(calls, 1);
  state.data = {}; state.navigationEpoch++;
  resolve({ records: [], sources: [{ agent_id: "old-private-agent" }], timeline: [] });
  await pending;
  assert.equal(paints, 1);
  assert.equal(state.data.connectionSources, undefined);
} finally { globalThis.document = oldDocument; }
console.log("client connection filters, escaping and account lifecycle passed");

}
