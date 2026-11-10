import assert from "node:assert/strict";
import { diagnoseTask } from "../modules/task-model.js";

import { geoRegionDetails } from "../modules/region-model.js";

import { trafficAccountingState } from "../modules/traffic-model.js";

// Inert on import. The runner owns ordering and the few shared read-only fixtures.
export async function run() {
const tlsFailure = {status:"failed",action:"deploy",error:"[阶段:证书准备] [原因:网络超时] 检查出站网络；已恢复原反代状态"};
assert.match(diagnoseTask(tlsFailure).title,/证书准备/);
assert.match(diagnoseTask(tlsFailure).advice,/网络超时/);
assert.match(diagnoseTask(tlsFailure).rollback,/已恢复/);
assert.match(diagnoseTask({...tlsFailure,error:tlsFailure.error+"；反代恢复失败"}).rollback,/回滚失败/);

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
assert.equal(trafficAccountingState({enforcement_error: `; ${scopeDiagnostic}`}).tone, "limited");
assert.equal(trafficAccountingState({enforcement_error: `${scopeDiagnostic}; read failed`}).tone, "bad");
assert.match(trafficAccountingState({enforcement_available:false}).hint, /暂未提供详细诊断/);
assert.match(trafficAccountingState({enforcement_error:"dual accounting unavailable: outbound tags must be present and unique"}).hint, /新版 Agent 可补齐缺失项；重复标签须修改配置，不能合并计数/);
assert.match(trafficAccountingState({enforcement_error:"dual accounting unavailable: outbounds[1].tag duplicates an earlier outbound; assign distinct tags and update the intended route targets"}).hint, /出口标签重复.*使用不同标签并同步修改路由/);
assert.equal(trafficAccountingState({engine:"mihomo",accounting:{source:"nft-dual"}}).partialEgress, true);
// Escaping, collapsed diagnostics, dual counters and scope notes are asserted
// on the actual native statistics dialog in browser/traffic-layout.mjs.

// Finish the imported async suites before these checks replace the global DOM.
await import("../client_access_order_smoke.mjs");
await import("../feature_modules_smoke.mjs");
await import("../browser_fixture_smoke.mjs");
await import("../session_api_smoke.mjs");
await import("../shell_modules_smoke.mjs");

}

const unrelatedPhase=diagnoseTask({status:"failed",action:"validate",error:"[阶段:真实内核校验] permission denied"});
assert.doesNotMatch(unrelatedPhase.advice,/ACME|TCP 80/);
assert.match(unrelatedPhase.advice,/实际原因/);
