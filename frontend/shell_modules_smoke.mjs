import assert from "node:assert/strict";
import { applyAppearance, formatDate } from "./analytics/appearance.js";
import { startAnalytics } from "./analytics/app.js";
import { createRouteWarmup } from "./modules/route-warmup.js";
import { routeForHash, routeModuleNames } from "./modules/routes.js";
import { createServiceActionGuard, installedEngineCount } from "./modules/agent-service-state.js";

// Importing the shipped runtime remains inert before it is mounted.
assert.equal(typeof startAnalytics, "function");
const warmup = createRouteWarmup({
  peek: name => loaded.has(name),
  preload: name => preloaded.push(name),
});
const loaded = new Set(), preloaded = [];

for (const [route, module] of Object.entries(routeModuleNames)) {
  assert.equal(routeForHash(`#${route}?fixture=yes`), route);
  assert.equal(typeof module, "string");
}
for (const [hash, route] of [
  ["#summary", "dashboard"], ["#panel-host", "dashboard"],
  ["#settings-data", "settings"], ["#enrollment", "node-settings"],
  ["#settings-node-a", "node-settings"], ["#node-b", "node-settings"],
  ["#preset-node-a", "live-config"], ["#new-config", "archive-config"],
  ["#config-id", "archive-config"], ["#traffic-agent-a", "traffic"],
  ["#traffic-all", "traffic"], ["#system-bbr-agent-a", "system-bbr"],
  ["#access-control-agent-a", "access-control"], ["#unknown", "dashboard"],
]) assert.equal(routeForHash(hash), route);

let tasks = true, host = true;
const agent = { id: "node" };
const disabled = createServiceActionGuard((permission, target) => {
  if (permission === "tasks.execute") return tasks;
  assert.equal(permission, "host.manage");
  assert.equal(target, agent);
  return host;
});
const check = (action, status, online = true, installed = true) => disabled(action, online, installed, status, agent);
assert.equal(check("start", "inactive"), false);
assert.equal(check("start", "active"), true);
assert.equal(check("stop", "inactive"), true);
assert.equal(check("stop", "active"), false);
assert.equal(check("restart", "activating"), true);
assert.equal(check("restart", "active"), false);
assert.equal(check("status", "active", false), true);
assert.equal(check("status", "active", true, false), true);
host = false;
assert.equal(check("start", "inactive"), true);
assert.equal(check("status", "inactive"), false);
tasks = false;
assert.equal(check("status", "active"), true);
assert.equal(installedEngineCount(), 0);
assert.equal(installedEngineCount({
  capabilities: ["xray", "mihomo"],
  runtime: { xray: { installed: true }, mihomo: { installed: false }, other: { installed: true } },
}), 1);

const originals = new Map(["document", "window", "navigator", "localStorage", "matchMedia"].map(key => [
  key, Object.getOwnPropertyDescriptor(globalThis, key),
]));
const setGlobal = (key, value) => Object.defineProperty(globalThis, key, { configurable: true, value });
try {
  const root = { style: {} };
  setGlobal("document", { documentElement: root });
  for (const [value, expected] of [[90, "90%"], [100, "100%"], ["110", "110%"], [200, "100%"], [null, "100%"]]) {
    applyAppearance({ ui_font_scale: value });
    assert.equal(root.style.fontSize, expected);
  }
  const timestamp = "2026-01-02T04:05:06Z";
  const absolute = zone => new Date(timestamp).toLocaleString("zh-CN", { hour12: false, timeZone: zone });
  applyAppearance({ ui_font_scale: 90, time_zone: "Asia/Shanghai" });
  assert.equal(formatDate(timestamp), absolute("Asia/Shanghai"));
  applyAppearance({ ui_font_scale: 110, time_zone: "UTC", time_display: "absolute-relative" });
  assert.equal(root.style.fontSize, "110%", "appearance must use the current account settings");
  assert.ok(formatDate(timestamp).startsWith(absolute("UTC") + " · "));
  assert.ok(formatDate(new Date(Date.now() + 3600000).toISOString()).includes(" · "), "future relative date was lost");
  applyAppearance({ time_zone: "invalid/timezone" });
  assert.equal(formatDate(timestamp), new Date(timestamp).toLocaleString("zh-CN", { hour12: false }), "invalid zone must remain readable");
  applyAppearance();
  assert.equal(root.style.fontSize, "100%", "logout/account reset retained the previous font scale");
  assert.equal(formatDate(timestamp), new Date(timestamp).toLocaleString("zh-CN", { hour12: false }), "account reset retained the previous time settings");
  for (const value of [undefined, null, "", "invalid", "1960-01-01T00:00:00Z"]) assert.equal(formatDate(value), "—");
  // Real native confirmation acceptance, cancellation, Escape, default focus,
  // target labels and duplicate locks are exercised by batch-layout and users
  // browser modes. The retired shell's native-window fallback and color toggle
  // are not part of the shipped analytics runtime.

  const events = [], timers = new Map(), canceledTimers = [];
  let serial = 0;
  const document = { hidden: false, addEventListener: (...event) => events.push(event) };
  const navigator = { connection: {} };
  const window = {
    requestIdleCallback: (callback, options) => {
      assert.equal(options.timeout, 1500);
      timers.set(++serial, callback);
      return serial;
    },
    cancelIdleCallback: handle => { canceledTimers.push(handle); timers.delete(handle); },
    setTimeout: (callback, delay) => { assert.equal(delay, 600); timers.set(++serial, callback); return serial; },
    clearTimeout: handle => { canceledTimers.push(handle); timers.delete(handle); },
  };
  setGlobal("document", document);
  setGlobal("navigator", navigator);
  setGlobal("window", window);
  warmup.bindNavigationPreload();
  assert.deepEqual(events.map(([type]) => type), ["pointerover", "pointerdown", "focusin"]);
  const link = { contains: value => value === "child", getAttribute: () => "#live-config?agent=node" };
  events[0][1]({ type: "pointerover", target: { closest: () => link }, relatedTarget: "child" });
  assert.deepEqual(preloaded, []);
  events[1][1]({ type: "pointerdown", target: { closest: () => link } });
  assert.deepEqual(preloaded, ["configs"]);
  warmup.scheduleRouteWarmup("dashboard");
  const pending = serial;
  warmup.scheduleRouteWarmup("live-config");
  assert.ok(canceledTimers.includes(pending));
  const callback = timers.get(serial);
  timers.delete(serial);
  callback();
  assert.equal(preloaded.at(-1), "client-access");
  for (const guard of ["hidden", "saveData", "2g", "loaded"]) {
    document.hidden = guard === "hidden";
    navigator.connection = { saveData: guard === "saveData", effectiveType: guard === "2g" ? "slow-2g" : "4g" };
    if (guard === "loaded") loaded.add("agents");
    warmup.scheduleRouteWarmup("dashboard");
    assert.equal(timers.size, 0, `${guard} must suppress background route loads`);
  }
  loaded.clear();
  navigator.connection = {};
  delete window.requestIdleCallback;
  warmup.scheduleRouteWarmup("dashboard");
  assert.equal(timers.size, 1);
  warmup.stopRouteWarmup();
  assert.equal(timers.size, 0, "login/navigation must cancel the timer fallback");
} finally {
  for (const [key, descriptor] of originals) {
    if (descriptor) Object.defineProperty(globalThis, key, descriptor);
    else delete globalThis[key];
  }
}

console.log("Native shell import, appearance, service scope and navigation warmup smoke passed");
