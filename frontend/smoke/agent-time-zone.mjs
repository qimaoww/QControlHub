import assert from "node:assert/strict";

import { agentClockSyncPresentation, agentTimeZoneEffective, agentTimeZoneStatus } from "../modules/agent-time-zone-model.js";

const configured = { mode: "custom", time_zone: "America/New_York", applied_time_zone: "America/New_York", status: "applied" };

export function run() {
  assert.equal(agentTimeZoneEffective(configured), "");
  assert.equal(agentTimeZoneEffective({ ...configured, applied_time_zone: "UTC" }), "当前生效：UTC");
  assert.equal(agentTimeZoneEffective({ ...configured, applied_time_zone: "" }), "");
  assert.equal(agentTimeZoneStatus({ status: "online", time_zone: { status: "applying" } }), "应用中");
  assert.equal(agentTimeZoneStatus({ status: "offline", time_zone: { status: "pending" } }), "离线，待生效");
  assert.equal(agentTimeZoneStatus({ status: "online", time_zone: { mode: "auto", status: "pending" } }), "待检测");
  assert.equal(agentTimeZoneStatus({ status: "online", time_zone: { mode: "custom", status: "pending" } }), "待生效");
  assert.equal(agentTimeZoneStatus({ status: "online", time_zone: { status: "failed", error: "定位失败" } }), "定位失败");
  assert.deepEqual(agentClockSyncPresentation({ status: "synchronized", offset_milliseconds: 1.25 }), { status: "已同步", offset: "+1.250 ms" });
  assert.deepEqual(agentClockSyncPresentation({ status: "unsynchronized", offset_milliseconds: -0.5 }), { status: "未同步", offset: "-0.500 ms" });
  assert.deepEqual(agentClockSyncPresentation({ status: "unavailable" }), { status: "不可用", offset: "" });
  assert.deepEqual(agentClockSyncPresentation({ status: "synchronized", offset_milliseconds: null }), { status: "已同步", offset: "偏差不可用" });

  assert.deepEqual(agentClockSyncPresentation({ status: "synchronized", offset_milliseconds: 0 }), { status: "已同步", offset: "+0.000 ms" });
  // Native form refresh, effective value, clock, draft/CAS and stale results
  // are exercised in browser/agent-time-zone-layout.mjs on the actual page.

  console.log("Agent time-zone status and display smoke checks passed");
}
