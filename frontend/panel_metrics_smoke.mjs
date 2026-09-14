import assert from "node:assert/strict";
import { panelMetricUsage, renderPanelMetrics } from "./modules/panel-metrics.js";

const now = Date.parse("2026-09-14T09:00:00Z");
const metrics = {
  collected_at: new Date(now).toISOString(),
  cpu_available: true, cpu_percent: 23.6,
  memory_available: true, memory_used_bytes: 1024, memory_total_bytes: 4096,
  disk_available: true, disk_used_bytes: 80, disk_total_bytes: 100,
  network_available: true, network_rx_bps: 2048, network_tx_bps: 512,
};
const options = {
  now,
  esc: (value) => String(value).replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll('"', "&quot;"),
  bytes: (value) => `${value} B`,
  rate: (value) => `${value} B/s`,
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
const normal = renderPanelMetrics(metrics, options);
assert.match(normal, /面板主机/);
assert.match(normal, /每 2 秒更新/);
assert.match(normal, /23\.6<small>%/);
assert.match(normal, /width:25\.0%/);
assert.match(normal, /panel-metric elevated/);
assert.match(normal, /2048 B\/s/);
assert.match(normal, /磁盘与网络仅反映容器可见范围/);
assert.doesNotMatch(normal, /NaN|undefined|Infinity/);
assert.match(renderPanelMetrics(undefined, { ...options, pending: true }), /正在采集/);
const unavailable = renderPanelMetrics({}, options);
assert.match(unavailable, /指标暂不可用/);
assert.doesNotMatch(unavailable, />0(?:\.0)?<small>%|>0 B\/s/);
assert.match(renderPanelMetrics(metrics, { ...options, failed: true }), /刷新失败 · 保留上次数据/);
assert.match(renderPanelMetrics(undefined, { ...options, failed: true }), /暂时无法读取/);
assert.match(renderPanelMetrics(metrics, { ...options, now: now + 16000 }), /数据已过期/);
assert.match(renderPanelMetrics({ ...metrics, collected_at: "invalid" }, options), /数据已过期/);
assert.match(renderPanelMetrics({ ...metrics, cpu_percent: 92 }, options), /panel-metric high/);
assert.match(renderPanelMetrics({ ...metrics, cpu_percent: 0 }, options), /0\.0<small>%/);
assert.match(renderPanelMetrics(metrics, { ...options, rate: () => "<img onerror=alert(1)>" }), /&lt;img/);
assert.doesNotMatch(renderPanelMetrics({ ...metrics, network_rx_bps: null }, options), /2048 B\/s/);
console.log("Panel-host metric rendering smoke passed");
