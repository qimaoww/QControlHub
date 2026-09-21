import assert from "node:assert/strict";
import { connectionQuery, connectionAddress, connectionSourceLabel, connectionSourceDetail, defaultConnectionFilters, connectionLocationLabel } from "../modules/client-connection-model.js";
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
assert.equal(connectionAddress("", 0), "—");
assert.equal(connectionAddress("0.0.0.0", 0), "—");
assert.match(connectionSourceLabel({}), /等待内核日志/);
assert.equal(connectionSourceLabel({ updated_at: "2020-01-01", status: "ok" }), "已读取内核日志");
assert.match(connectionSourceDetail({ updated_at: "2020-01-01", status: "ok" }), /面板保存的内核日志/);
assert.ok(!connectionSourceDetail({}).includes("Agent"));

const esc = value => String(value).replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll('"', "&quot;");
const view = createClientConnectionView({ esc, engineName: value => value, date: value => value });
const html = view({ ips: 1, flows: 2, records: [{ agent_name: "<script>", inbound: "<img>", client_ip: "2001:db8::1", client_port: 50123, local_ip: "192.0.2.1", local_port: 443, protocol: "vless", engine: "xray", transport: "tcp" }] }, filters, []);
assert.ok(html.includes("&lt;script>"));
assert.ok(!html.includes("<script>"));
assert.match(html, /来源 IP/);
assert.match(html, /\[2001:db8::1\]:50123/);

// Construction is inert; stale reads from a previous account/navigation cannot paint.
const oldDocument = globalThis.document;
globalThis.document = { querySelector: () => null, querySelectorAll: () => [] };
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

  // A slow location response cannot block history or overwrite a newer query.
  const liveState = { data: {}, navigationEpoch: 1, route: "client-connections" };
  let resolveLocation, painted = [], requestSignals = [];
  const activeLoad = installClientConnections({ state: liveState, esc, engineName: value => value, date: value => value,
    shell: markup => painted.push(markup),
    api: (path, { signal }) => {
      requestSignals.push(signal);
      if (path.includes("locations=only")) return new Promise(done => { resolveLocation = done; });
      return Promise.resolve({ ips: 1, flows: 1, records: [{ id: 1, agent_name: "current", transport: "tcp", location: {} }], sources: [], timeline: [] });
    },
  });
  await activeLoad();
  assert.ok(painted.at(-1).includes("current"), "history paints without awaiting locations");
  assert.ok(liveState.data.connectionCache);
  const oldLocation = resolveLocation;
  await activeLoad();
  assert.equal(requestSignals[0].aborted, true, "superseded request is canceled");
  const paintCount = painted.length;
  oldLocation({ records: [{ id: 1, location: { country_code: "CN", province: "广东" } }] });
  await Promise.resolve();
  assert.equal(painted.length, paintCount, "old enrichment must not repaint");
  liveState.data = {}; liveState.navigationEpoch++;
  resolveLocation({ records: [] });
  await Promise.resolve();
  assert.equal(liveState.data.connectionCache, undefined, "enrichment cannot restore another account's data");
} finally { globalThis.document = oldDocument; }
console.log("client connection filters, escaping and account lifecycle passed");

}
