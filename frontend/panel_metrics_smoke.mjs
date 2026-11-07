import assert from "node:assert/strict";
import { panelMetricUsage } from "./modules/dashboard-model.js";

const now = Date.parse("2026-09-14T09:00:00Z");
const metrics = {
  collected_at: new Date(now).toISOString(),
  cpu_available: true, cpu_percent: 23.6,
  memory_available: true, memory_used_bytes: 1024, memory_total_bytes: 4096,
  disk_available: true, disk_used_bytes: 80, disk_total_bytes: 100,
  network_available: true, network_rx_bps: 2048, network_tx_bps: 512,
};
assert.equal(panelMetricUsage(metrics, "cpu"), 23.6);
assert.equal(panelMetricUsage(metrics, "memory"), 25);
assert.equal(panelMetricUsage(metrics, "disk"), 80);
assert.equal(panelMetricUsage({ cpu_available: true, cpu_percent: 0 }, "cpu"), 0, "idle CPU is a real zero");
assert.equal(panelMetricUsage({ ...metrics, cpu_percent: 999 }, "cpu"), 100);
assert.equal(panelMetricUsage({ ...metrics, memory_used_bytes: 99999 }, "memory"), 100);
for (const value of [undefined, null, NaN, Infinity, -1, "10"]) {
  assert.equal(panelMetricUsage({ ...metrics, cpu_percent: value }, "cpu"), null);
  assert.equal(panelMetricUsage({ ...metrics, memory_used_bytes: value }, "memory"), null);
  assert.equal(panelMetricUsage({ ...metrics, disk_total_bytes: value }, "disk"), null);
}
assert.equal(panelMetricUsage({ ...metrics, memory_total_bytes: 0 }, "memory"), null);
assert.equal(panelMetricUsage({ ...metrics, cpu_available: false }, "cpu"), null);
assert.equal(panelMetricUsage(null, "cpu"), null);
// Actual native display, partial/missing/stale samples, denial and recovery,
// safe text and production CSP are covered by the dashboard browser modes.
console.log("Panel-host numerical metric validation smoke passed");
