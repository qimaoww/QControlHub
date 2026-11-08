import assert from "node:assert/strict";
import { connectionQuery, connectionSourceLabel, connectionSourceDetail, defaultConnectionFilters, connectionLocationLabel } from "../modules/client-connection-model.js";

export async function run() {
// Calendar queries use local midnight and the following midnight, including DST.
const previousTZ = process.env.TZ;
try {
  process.env.TZ = "America/New_York";
  for (const [day, start, end] of [
    ["2026-03-08", "2026-03-08T05:00:00.000Z", "2026-03-09T04:00:00.000Z"],
    ["2026-11-01", "2026-11-01T04:00:00.000Z", "2026-11-02T05:00:00.000Z"],
  ]) {
    const q = connectionQuery({ date: day });
    assert.equal(q.get("since"), start);
    assert.equal(q.get("until"), end);
  }
  process.env.TZ = "Europe/Berlin";
  const october = connectionQuery({ date: "2026-10-25", period: "month" });
  assert.equal(october.get("since"), "2026-09-30T22:00:00.000Z");
  assert.equal(october.get("until"), "2026-10-31T23:00:00.000Z");
  assert.equal(october.get("bucket"), "day");
  process.env.TZ = "Asia/Shanghai";
  for (const [date, end] of [["2026-01-31", "2026-01-31T16:00:00.000Z"], ["2024-02-29", "2024-02-29T16:00:00.000Z"], ["2026-12-31", "2026-12-31T16:00:00.000Z"]]) {
    assert.equal(connectionQuery({ date, period: "month" }).get("until"), end);
  }
  assert.equal(defaultConnectionFilters().period, "day");
  assert.equal(defaultConnectionFilters(Date.parse("2026-09-19T20:00:00Z")).date, "2026-09-20");
  assert.equal(connectionQuery({ date: "2026-09-20" }).get("since"), "2026-09-19T16:00:00.000Z");
  for (const date of ["", "bad", "2026-02-30", "2026-13-01"]) assert.throws(() => connectionQuery({ date }), /日期/);
} finally {
  if (previousTZ === undefined) delete process.env.TZ;
  else process.env.TZ = previousTZ;
}
const filters = defaultConnectionFilters(Date.parse("2026-09-19T08:00:00Z"));
const query = connectionQuery({ ...filters, client_ip: "2001:db8::1", country_code: "CN", port: "443", inbound: "a&b" }, 7);
assert.equal(query.get("group_by"), "ip");
assert.equal(query.get("timeline"), "false");
assert.equal(query.get("client_ip"), "2001:db8::1");
assert.equal(query.get("country_code"), "CN");
assert.equal(query.has("inbound"), false);
assert.equal(query.has("port"), false);
assert.equal(query.get("cursor"), "7");
assert.deepEqual(connectionQuery(filters, "", ["node-b", "node-a"]).getAll("node_order"), ["node-b", "node-a"]);
assert.equal(query.has("include_non_public"), false);
assert.equal(connectionQuery({ ...filters, include_non_public: "true" }, 7).get("include_non_public"), "true");
assert.throws(() => connectionQuery({ since: "bad", until: "bad" }), /32/);
assert.throws(() => connectionQuery({ since: "2026-01-01", until: "2026-03-01" }), /32/);
assert.equal(connectionLocationLabel({ country_code: "CN", province: "广东" }), "中国 · 广东");
assert.equal(connectionLocationLabel({ country_code: "US", province: "California" }), "美国");
assert.equal(connectionLocationLabel({ country_code: "CN" }), "中国");
assert.equal(connectionLocationLabel({ non_public: true }), "非公网");
assert.equal(connectionLocationLabel({}), "—");
assert.match(connectionSourceLabel({}), /等待内核日志/);
assert.equal(connectionSourceLabel({ updated_at: "2020-01-01", status: "ok" }), "已读取内核日志");
assert.match(connectionSourceDetail({ updated_at: "2020-01-01", status: "ok" }), /入库.*按日期查询/);
assert.ok(!connectionSourceDetail({}).includes("Agent"));

// Native rendering, escaping, node order, cursor/cache behavior and late
// account/location responses run in browser/client-connections.mjs and
// browser/connection-read-lifecycle.mjs against the authenticated native app.
console.log("Client connection calendar, query and source models passed");
}
