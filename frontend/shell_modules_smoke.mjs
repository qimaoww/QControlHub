import assert from "node:assert/strict";
import { createShellAppearance } from "./modules/shell-appearance.js";
import { createShellFeedback } from "./modules/shell-feedback.js";
import { createShellView } from "./modules/shell-view.js";
import { createShellContext } from "./modules/shell-context.js";
import { createLoginPage } from "./modules/login-page.js";
import { createRouteWarmup } from "./modules/route-warmup.js";
import { routeForHash, routeModuleNames } from "./modules/routes.js";
import { createServiceActionGuard, installedEngineCount } from "./modules/agent-service-state.js";

// Factories must not read the DOM, attach listeners, render, or load routes
// while the application is still wiring callbacks.
const state = { data: {}, session: null };
const context = { state };
assert.equal(typeof createShellView(context), "function");
assert.equal(typeof createShellContext(context), "function");
assert.equal(typeof createLoginPage(context), "function");
const appearance = createShellAppearance(state);
const feedback = createShellFeedback(state);
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
  let theme = "dark", storageFails = false;
  const styles = new Map(), attributes = new Map(), icon = {};
  const root = { dataset: {}, style: { setProperty: (key, value) => styles.set(key, value) } };
  setGlobal("localStorage", {
    getItem: () => { if (storageFails) throw new Error("unavailable"); return theme; },
    setItem: (key, value) => { assert.equal(key, "qcontrolhub-color-theme"); if (storageFails) throw new Error("unavailable"); theme = value; },
  });
  setGlobal("matchMedia", () => ({ matches: true }));
  setGlobal("document", {
    documentElement: root,
    querySelectorAll: () => [{ setAttribute: (key, value) => attributes.set(key, value), querySelector: () => icon }],
  });
  state.data.settings = { ui_font_scale: 110 };
  appearance.applyTheme();
  assert.equal(root.dataset.theme, "dark");
  assert.equal(root.style.colorScheme, "dark");
  assert.equal(styles.get("--ui-font-scale"), "1.1");
  assert.equal(attributes.get("aria-label"), "切换为浅色主题");
  appearance.toggleTheme();
  assert.equal(theme, "light");
  assert.equal(icon.textContent, "☾");
  state.data = { settings: { ui_font_scale: 90 } };
  appearance.applyUIFontScale();
  assert.equal(root.dataset.uiFontScale, "90", "appearance reads the new account's settings");
  appearance.applyUIFontScale(200);
  assert.equal(root.dataset.uiFontScale, "100");
  storageFails = true;
  appearance.applyTheme();
  assert.equal(root.dataset.theme, "light", "inaccessible storage falls back to the system theme");
  appearance.toggleTheme();
  assert.equal(root.dataset.theme, "dark", "theme toggles remain usable without storage");

  const nodes = new Map(["message", "accept", "cancel", "title"].map(key => [key, {}]));
  const dialog = Object.assign(new EventTarget(), {
    querySelector: selector => nodes.get(selector.match(/data-confirm-(\w+)/)[1]),
    dataset: {},
    showModal() { this.open = true; },
    close() { this.open = false; },
  });
  setGlobal("document", { querySelector: () => dialog });
  feedback.bindConfirmationDialog();
  feedback.bindConfirmationDialog();
  const confirmed = feedback.confirmAction("确认目标节点", "部署", {title: "部署配置", tone: "primary"});
  assert.equal(dialog.open, true);
  assert.equal(nodes.get("title").textContent, "部署配置");
  assert.equal(dialog.dataset.tone, "primary");
  assert.equal(state.confirmOpen, true);
  assert.equal(nodes.get("message").textContent, "确认目标节点");
  assert.equal(nodes.get("accept").textContent, "部署");
  nodes.get("accept").onclick();
  assert.equal(await confirmed, true);
  assert.equal(state.confirmResolver, null);
  assert.equal(state.confirmOpen, false);
  const canceled = feedback.confirmAction("取消");
  assert.equal(nodes.get("title").textContent, "确认继续？", "普通确认不能残留批量标题");
  assert.equal(dialog.dataset.tone, "danger", "普通确认恢复原有危险操作语义");
  const cancel = new Event("cancel", { cancelable: true });
  dialog.dispatchEvent(cancel);
  assert.equal(cancel.defaultPrevented, true);
  assert.equal(await canceled, false);
  assert.equal(dialog.open, false);
  setGlobal("document", { querySelector: () => null });
  setGlobal("window", { confirm: message => message === "fallback" });
  assert.equal(await feedback.confirmAction("fallback"), true);
  let nativeMessage;
  setGlobal("window", { confirm: message => { nativeMessage = message; return true; } });
  await feedback.confirmAction("", "查询状态", {title: "批量查询状态", details: [["目标内核", "Mihomo"]], targets: ["ALPHA"]});
  assert.match(nativeMessage, /批量查询状态/);
  assert.match(nativeMessage, /Mihomo/);
  assert.match(nativeMessage, /ALPHA/);

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

console.log("Shell composition, appearance, confirmation and navigation warmup smoke passed");
