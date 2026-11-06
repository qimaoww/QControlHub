import assert from "node:assert/strict";

import { agentClockSyncPresentation, agentTimeZoneEffective, agentTimeZoneStatus } from "../modules/agent-time-zone-model.js";
import { updateAgentTimeZone } from "../modules/agent-time-zone.js";

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

  const values = new Map([
    ["[data-timezone-applied]", { textContent: "" }],
    ["[data-timezone-status]", { textContent: "" }],
    ["[data-clock-sync-status]", { textContent: "" }],
    ["[data-clock-sync-offset]", { textContent: "" }],
  ]);
  globalThis.CSS ||= { escape: (value) => value };
  const section = { querySelector: (selector) => values.get(selector) || null };
  const root = { querySelector: (selector) => selector === '[data-agent-timezone="node-1"]' ? section : null };
  updateAgentTimeZone(root, { id: "node-1", status: "online", metrics: { clock_sync: { status: "synchronized", offset_milliseconds: 0 } }, time_zone: configured });
  assert.equal(values.get("[data-timezone-applied]").textContent, "");
  assert.equal(values.get("[data-timezone-applied]").hidden, true);
  assert.equal(values.get("[data-timezone-status]").textContent, "已生效");
  assert.equal(values.get("[data-clock-sync-status]").textContent, "已同步");
  assert.equal(values.get("[data-clock-sync-offset]").textContent, "+0.000 ms");
  updateAgentTimeZone(root, { id: "node-1", status: "online", time_zone: { ...configured, applied_time_zone: "UTC", status: "applying" } });
  assert.equal(values.get("[data-timezone-applied]").textContent, "当前生效：UTC");
  assert.equal(values.get("[data-timezone-applied]").hidden, false);
  assert.equal(values.get("[data-timezone-status]").textContent, "应用中");

  console.log("Agent time-zone status and display smoke checks passed");
}
