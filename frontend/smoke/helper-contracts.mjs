import assert from "node:assert/strict";

import { komariCycleRange, geoRegionDetails, komariResetDay } from "../modules/agents.js";

import { renderTrafficAccounting } from "../modules/traffic.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run() {
assert.equal(
  komariCycleRange(27, new Date(2026, 7, 20)),
  "7.27–8.27",
  "Komari cycle before reset uses the previous and current reset dates",
);
assert.equal(
  komariCycleRange(27, new Date(2026, 7, 30)),
  "8.27–9.27",
  "Komari cycle after reset uses the current and next reset dates",
);
assert.equal(
  komariCycleRange(31, new Date(2026, 1, 15)),
  "1.31–2.28",
  "Komari cycle clamps a 31st reset day in a short month",
);
assert.equal(
  komariCycleRange(27, new Date(2026, 0, 20)),
  "12.27–1.27",
  "Komari cycle crosses a year boundary",
);
assert.equal(komariCycleRange(0, new Date(2026, 7, 20)), "", "invalid reset day is unavailable");
assert.equal(
  komariResetDay({ billing_cycle: 30, expired_at: "2026-08-27T00:00:00Z" }),
  27,
  "monthly Komari expiry supplies a reset day when the API omits one",
);
assert.deepEqual(
  geoRegionDetails("TW"),
  { code: "TW", flagCode: "CN", name: "中国台湾" },
  "ISO Taiwan region codes use the same China flag policy",
);
assert.deepEqual(
  geoRegionDetails("SG"),
  { code: "SG", flagCode: "SG", name: "新加坡" },
  "GeoIP country flags preserve non-Taiwan regions",
);
assert.equal(geoRegionDetails("not-a-region"), null, "invalid GeoIP regions are hidden");

const scopeDiagnostic = "dual accounting unavailable: single-protocol policy uses listener-only accounting; dual accounting requires TCP+UDP because core counters and outbound marks are shared";
const accountingHTML = policy => renderTrafficAccounting(policy, value => String(value).replaceAll("<", "&lt;"), value => `${value} B`);
assert.match(accountingHTML({enforcement_error: `; ${scopeDiagnostic}`}), /traffic-accounting-panel limited/);
assert.match(accountingHTML({enforcement_error: `${scopeDiagnostic}; read failed`}), /traffic-accounting-panel bad/);
assert.match(accountingHTML({enforcement_available:false}), /暂未提供详细诊断/);
assert.match(accountingHTML({enforcement_error:"<script>"}), /&lt;script>/);
assert.match(accountingHTML({enforcement_error:"dual accounting unavailable: outbound tags must be present and unique"}), /新版 Agent 可补齐缺失项；重复标签须修改配置，不能合并计数/);
assert.match(accountingHTML({enforcement_error:"dual accounting unavailable: outbounds[1].tag duplicates an earlier outbound; assign distinct tags and update the intended route targets"}), /出口标签重复.*使用不同标签并同步修改路由/);
const dualAccountingHTML = accountingHTML({accounting:{source:"core-api",client_received:1,client_sent:2,target_received:3,target_sent:4}});
assert.match(dualAccountingHTML, /不等同于本月总量/);
assert.match(dualAccountingHTML, /<dl class="traffic-accounting-legs">/);
assert.doesNotMatch(dualAccountingHTML, /<details[^>]*\bopen\b/);
const mihomoAccountingHTML = accountingHTML({engine:"mihomo",accounting:{source:"nft-dual"}});
assert.match(mihomoAccountingHTML, /双链路 · 范围受限/);
assert.match(mihomoAccountingHTML, /入口 \+ 已标记出口/);
assert.match(mihomoAccountingHTML, /traffic-accounting-panel limited/);
assert.match(mihomoAccountingHTML, /不代表已发生漏计/);
assert.match(mihomoAccountingHTML, /非全局单播目标仅计入口，出口不计入、不补算或翻倍/);

// Finish the imported async suites before these checks replace the global DOM.
await import("../client_access_order_smoke.mjs");
await import("../feature_modules_smoke.mjs");
await import("../browser_fixture_smoke.mjs");
await import("../controller_modules_smoke.mjs");
await import("../session_api_smoke.mjs");
await import("../shell_modules_smoke.mjs");

}
