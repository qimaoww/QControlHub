import assert from "node:assert/strict";
import { createDisplayHelpers } from "./modules/formatting.js";

const state = { data: { settings: { time_zone: "UTC" } } };
const display = createDisplayHelpers(state);
const timestamp = "2026-09-15T12:00:00Z";

assert.equal(display.esc(`<script a="'">&`), "&lt;script a=&quot;&#39;&quot;&gt;&amp;");
assert.equal(display.esc(0), "0");
assert.equal(display.esc(false), "false");
assert.equal(display.esc(null), "");
assert.equal(display.bytes(0), "0 B");
assert.equal(display.bytes(1024), "1.0 KB");
assert.equal(display.bytes(1024 ** 2), "1.0 MB");
assert.equal(display.rate(1024), "1.0 KB/s");
assert.equal(display.percent(-1, 10), 0);
assert.equal(display.percent(11, 10), 100);
assert.equal(display.percent(10, 0), 0);
assert.equal(display.engineName("sing-box"), "sing-box");
assert.equal(display.engineName("future-core"), "future-core");
assert.equal(display.actionName("read-managed-config"), "读取 QAgent 配置");
assert.equal(display.actionName("new_action"), "new action");
assert.equal(display.statusName("canceled"), "已取消");
assert.equal(display.serviceStatusName(""), "未知");
assert.equal(display.statusTone("active"), "ok");
assert.equal(display.statusTone("activating"), "warn");
assert.equal(display.statusTone("failed"), "bad");
assert.equal(display.statusTone("unknown"), "muted");
assert.equal(display.conciseVersion("xray", "Xray 26.3.27 (release)"), "Xray 内核 v26.3.27");
assert.equal(display.conciseVersion("mihomo", ""), "Mihomo 内核版本未知");
assert.equal(display.date(""), "-");
assert.equal(display.date(timestamp), new Date(timestamp).toLocaleString(undefined, { timeZone: "UTC" }));

state.data = { settings: { time_zone: "Asia/Shanghai", time_display: "absolute" } };
assert.equal(display.date(timestamp), new Date(timestamp).toLocaleString(undefined, { timeZone: "Asia/Shanghai" }));
assert.equal(display.ago(timestamp), display.date(timestamp), "helpers must read the current account's settings");
assert.equal(display.heartbeat(timestamp), `心跳 ${display.date(timestamp)}`);
assert.equal(display.heartbeat(null), "尚未心跳");

state.data.settings = { time_zone: "browser" };
assert.equal(display.date(timestamp), new Date(timestamp).toLocaleString());
assert.equal(display.ago("not-a-date"), "未知");
assert.equal(display.renderConfigDiff("same\r\n", "same\n"), "");
const diff = display.renderConfigDiff("shared\n<new>\ntail\n", "shared\n<old>\ntail\n");
assert.match(diff, /diff-remove[^]*&lt;old&gt;/);
assert.match(diff, /diff-add[^]*&lt;new&gt;/);
assert.doesNotMatch(diff, /<old>|<new>/);
assert.equal(display.trafficChart([]), "");
assert.equal(display.trafficChart([{ rx_rate_bps: 1, tx_rate_bps: 2 }]), "");
assert.match(display.trafficChart([
  { rx_rate_bps: 0, tx_rate_bps: 512 },
  { rx_rate_bps: 1024, tx_rate_bps: 2048 },
]), /1\.0 KB\/s[^]*2\.0 KB\/s/);

console.log("Extracted display helper smoke passed");
