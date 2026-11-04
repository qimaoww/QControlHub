import { applyAppearance } from "./appearance.js";
import { accountRequest } from "../modules/account-request.js";
import { createPermissionChecker } from "../modules/permissions.js";
import { routeForHash } from "../modules/routes.js";
import { createSessionWatch } from "../modules/session-watch.js";
import { node, icon, button, actions, stack, section, notice, empty, field, formData } from "./ui.js";
import { dashboardPage, nodesPage, tasksPage, quotaPage } from "./operations.js";
import { archivePage } from "./configs.js";
import { themePage } from "./themes.js";
import { logsPage, connectionsPage } from "./observability.js";
import { clientAccessPage } from "./client-access.js";
import { accessControlPage } from "./access-control.js";
import { tcpPage } from "./tcp.js";
import { liveConfigPage } from "./live-config.js";
import { substorePage } from "./substore.js";
import { settingsPage } from "./settings.js";
import { trafficPage } from "./traffic.js";
import { usersPage } from "./users.js";
import { ipQualityPage } from "./ip-quality.js";
import { certificatesPage } from "./certificates.js";

const destinations = [
  ["概览", [["dashboard", "仪表盘", "grid", "overview.read"]]],
  ["节点与配置", [
    ["node-settings", "节点", "server", "agents.read"],
    ["live-config", "实时配置", "file", "agent-config.read"],
    ["archive-config", "配置档案", "file", "configs.read"],
    ["client-access", "客户端接入", "link", "client-access.read"],
    ["substore-sync", "订阅同步", "link", "client-access.read"],
    ["certificates", "证书", "shield", "certificates.read"],
  ]],
  ["运维", [
    ["tasks", "任务", "list", "tasks.read"], ["core-logs", "日志", "list", "core-logs.read"],
    ["client-connections", "客户端连接", "activity", "core-logs.read"],
    ["traffic", "流量", "activity", "traffic.read"], ["access-control", "访问控制", "shield", "agent-config.read"],
    ["system-bbr", "TCP 调优", "settings", "system-bbr.read"], ["ip-quality", "IP 质量", "activity", "agents.read"],
  ]],
  ["面板", [
    ["settings", "面板设置", "settings", "settings.read"], ["users", "用户管理", "users", "users.manage"],
    ["my-quota", "共享与额度", "users", "agent-access.read"], ["themes", "主题", "palette", "agent-access.read"],
  ]],
];
const pages = {
  dashboard: dashboardPage, "node-settings": nodesPage, tasks: tasksPage,
  "my-quota": quotaPage, "archive-config": archivePage, themes: themePage,
  "certificates": certificatesPage, "ip-quality": ipQualityPage, "users": usersPage, "traffic": trafficPage, "settings": settingsPage, "substore-sync": substorePage, "live-config": liveConfigPage, "access-control": accessControlPage, "system-bbr": tcpPage,
  "core-logs": logsPage, "client-connections": connectionsPage, "client-access": clientAccessPage,
};

// The new runtime never imports the classic shell or its renderers. It owns
// its listeners, dialogs and pending requests for the whole document lifetime.
export function startAnalytics({ app, session }) {
  const state = { session, data: {} };
  const can = createPermissionChecker(state);
  const lifetime = new AbortController();
  let routeAbort = null, routeEpoch = 0, dirty = false, pending = 0;
  let currentHash = location.hash, route = "", dialogSequence = 0, navigation = 0;
  const layout = node("div", { className: "a-layout" });
  const main = node("main", { className: "a-main", id: "content", tabindex: "-1" });
  const status = notice("", "ok");
  const work = node("div", { className: "a-work" });
  let drawer;
  document.body.className = "analytics";
  app.replaceChildren(layout);
  applyAppearance();

  const api = async (path, options = {}) => {
    const bound = state.session;
    const result = await accountRequest(path, {
      ...options, session: bound, signal: options.signal || lifetime.signal,
      isCurrent: () => state.session === bound && !lifetime.signal.aborted,
      onUnauthorized: message => login(message),
    });
    if (path === "/settings" && state.session === bound && !lifetime.signal.aborted) {
      state.data.settings = result;
      applyAppearance(result);
    }
    return result;
  };
  const watch = createSessionWatch({ api, state, onAccessChanged: () => {
    routeAbort?.abort();
    state.data = {};
    applyAppearance();
    dirty = false;
    void render();
  } });
  function announce(message, tone = "ok") {
    status.className = `a-notice ${tone}`;
    status.setAttribute("role", tone === "error" ? "alert" : "status");
    status.textContent = message;
  }
  function dialog(title, content, { wide = false } = {}) {
    const active = document.activeElement;
    const closeButton = button("", () => {
      if (pending) { announce("操作正在提交，请等待结果。", "error"); return; }
      if (box.onRequestClose) void box.onRequestClose();
      else box.close();
    }, { glyph: "close", title: "关闭", className: "a-icon-button" });
    closeButton.setAttribute("aria-label", "关闭");
    const heading = node("h2", { id: `a-dialog-title-${++dialogSequence}` }, title);
    const box = node("dialog", { className: `a-dialog ${wide ? "wide" : ""}`, "aria-labelledby": heading.id },
      node("div", { className: "a-dialog-head" }, heading, closeButton),
      node("div", { className: "a-dialog-body" }, content));
    document.body.append(box);
    box.addEventListener("cancel", event => {
      if (pending || box.onRequestClose) event.preventDefault();
      if (!pending && box.onRequestClose) void box.onRequestClose();
    });
    box.addEventListener("close", () => { box.remove(); if (active?.isConnected) active.focus(); }, { once: true });
    box.showModal();
    return box;
  }
  async function confirm(message, label = "确认") {
    return new Promise(resolve => {
      const box = dialog(label, stack(node("p", {}, message), actions(
        button("取消", () => box.close()),
        button(label, () => { resolve(true); box.close(); }, { tone: "primary" }),
      )));
      box.addEventListener("close", () => resolve(false), { once: true });
    });
  }
  async function mutate(control, run) {
    if (pending || control?.disabled) return;
    const disabled = control?.disabled;
    if (control) { control.disabled = true; control.setAttribute("aria-busy", "true"); }
    pending++;
    try { return await run(); }
    catch (error) {
      if (error.name !== "AbortError") announce(error.message, "error");
    } finally {
      pending--;
      if (control?.isConnected) { control.disabled = disabled; control.removeAttribute("aria-busy"); }
    }
  }
  function form(fields, label, save) {
    const feedback = notice("");
    const submit = node("button", { type: "submit", className: "a-button primary" }, label);
    const controls = node("fieldset", { className: "a-form-fields a-stack" }, fields);
    const element = node("form", { className: "a-stack" }, controls, feedback, actions(submit));
    element.addEventListener("submit", event => {
      event.preventDefault();
      if (pending || submit.disabled) return;
      const values = formData(element);
      controls.disabled = true;
      element.setAttribute("aria-busy", "true");
      void mutate(submit, async () => {
        feedback.textContent = "";
        try { await save(values, element); }
        catch (error) {
          if (error.name === "AbortError") return;
          feedback.textContent = error.message;
          throw error;
        } finally {
          controls.disabled = false;
          element.removeAttribute("aria-busy");
        }
      });
    });
    return element;
  }
  function rail(mobile = false) {
    return node("aside", { className: "a-rail" },
      node("a", { className: "a-brand", href: "#dashboard" }, node("span", { className: "a-mark" }, icon("brand")), "QControlHub"),
      mobile && button("", () => drawer.close(), { glyph: "close", title: "关闭导航", className: "a-icon-button a-drawer-close" }),
      node("nav", { "aria-label": "主导航" }, destinations.map(([group, entries]) => {
        const allowed = entries.filter(([, , , permission]) => can(permission));
        if (!allowed.length) return null;
        return node("div", { className: "a-nav-group" }, node("p", { className: "a-nav-label" }, group),
          allowed.map(([id, title, glyph]) => node("a", {
            className: "a-nav-link", href: `#${id}`, "aria-current": route === id ? "page" : null,
            onclick: () => drawer?.close(),
          }, icon(glyph), title)));
      })),
      node("div", { className: "a-user" }, node("span", { className: "a-avatar" }, (state.session?.username || "Q").slice(0, 2).toUpperCase()),
        node("div", {}, node("strong", {}, state.session?.username || "管理员"), node("span", { className: "a-muted" }, state.session?.role === "admin" ? "管理员" : "用户")),
        button("", () => void logout(), { glyph: "logout", title: "退出登录", className: "a-icon-button" })));
  }
  function shell(title) {
    const toggle = button("", () => {
      if (matchMedia("(max-width: 56rem)").matches) {
        drawer = node("dialog", { className: "a-dialog a-drawer", "aria-label": "导航" }, rail(true));
        const opened = drawer;
        opened.addEventListener("close", () => { opened.remove(); if (drawer === opened) drawer = null; }, { once: true });
        document.body.append(opened);
        opened.showModal();
      } else layout.classList.toggle("a-collapsed");
    }, { glyph: "menu", title: "切换导航", className: "a-icon-button" });
    toggle.setAttribute("aria-label", "切换导航");
    const top = node("header", { className: "a-top" }, node("div", { className: "a-top-inner" },
      toggle, node("span", { className: "a-top-title" }, title),
      actions(button("刷新", () => void refresh(), { glyph: "refresh" }))));
    work.replaceChildren(top, main);
    layout.replaceChildren(node("a", {
      href: "#content", className: "a-button a-skip",
      onclick(event) { event.preventDefault(); main.focus(); main.scrollIntoView(); },
    }, "跳到内容"), rail(), work);
    main.replaceChildren(node("div", { className: "a-heading" }, node("h1", {}, title)), status);
  }
  async function leave() {
    if (pending) { announce("操作正在提交，请等待结果。", "error"); return false; }
    if (dirty && !await confirm("存在未保存的更改，确定离开？", "离开页面")) return false;
    dirty = false;
    return true;
  }
  async function refresh() {
    if (!await leave()) return;
    await render();
  }
  async function logout() {
    if (!await leave()) return;
    await mutate(null, async () => { await api("/auth/logout", { method: "POST", body: {} }); login(); });
  }
  function login(message = "") {
    routeAbort?.abort();
    watch.stop();
    state.session = null;
    state.data = {};
    applyAppearance();
    dirty = false;
    document.querySelectorAll("dialog.a-dialog").forEach(box => box.close());
    const loginForm = form([
      field("用户名", "username", "", { autocomplete: "username", required: true, maxlength: 64 }),
      field("密码 / 管理令牌", "token", "", { type: "password", autocomplete: "current-password", required: true }),
    ], "登录", async values => {
      state.session = await api("/auth/login", { method: "POST", body: values });
      location.reload();
    });
    app.replaceChildren(node("main", { className: "a-login" },
      section("登录 QControlHub", node("div", { className: "a-card-body" }, stack(message && notice(message), loginForm)))));
    document.title = "登录 · QControlHub";
    app.querySelector("input")?.focus();
  }
  async function render() {
    if (!state.session) { login(); return; }
    routeAbort?.abort();
    routeAbort = new AbortController();
    const signal = routeAbort.signal, epoch = ++routeEpoch;
    route = routeForHash(location.hash);
    if (route === "dashboard" && !can("overview.read")) route = "my-quota";
    if (["agents", "agent-config"].includes(route)) route = "live-config";
    const entry = destinations.flatMap(([, entries]) => entries).find(([id]) => id === route);
    const title = entry?.[1] || "控制台";
    status.textContent = "";
    shell(title);
    document.title = `${title} · QControlHub`;
    const stage = node("div", { className: "a-stack", "aria-busy": "true" }, empty("正在加载…"));
    main.append(stage);
    const context = {
      api: (path, options = {}) => api(path, { ...options, signal: options.signal || signal }),
      can, session: state.session, signal, state, dialog, confirm, mutate, form, announce,
      refresh, setDirty(value) { dirty = value; },
      async switchTheme(save) {
        if (pending || !await leave()) return false;
        pending++;
        try { await save(); }
        finally { pending--; }
        return true;
      },
      current: () => !signal.aborted && epoch === routeEpoch && Boolean(state.session),
    };
    try {
      if (entry && !can(entry[3])) { stage.replaceChildren(notice("没有访问此页面的权限。")); return; }
      if (can("settings.read") && !state.data.settings) {
        await context.api("/settings");
        if (!context.current()) return;
      }
      const page = pages[route];
      if (!page) {
        stage.replaceChildren(notice("页面不存在。"));
        return;
      }
      const content = await page(context);
      if (context.current()) stage.replaceChildren(content);
    } catch (error) {
      if (context.current() && error.name !== "AbortError")
        stage.replaceChildren(notice(error.message), actions(button("重试", () => void refresh())));
    } finally {
      if (context.current()) stage.removeAttribute("aria-busy");
    }
    watch.start();
  }
  window.addEventListener("hashchange", async () => {
    const nextHash = location.hash;
    if (nextHash === currentHash) return;
    const attempt = ++navigation;
    const allowed = await leave();
    if (attempt !== navigation) return;
    if (!allowed) { history.replaceState(null, "", currentHash || "#dashboard"); return; }
    currentHash = nextHash;
    document.querySelectorAll("dialog.a-dialog").forEach(box => box.close());
    await render();
  }, { signal: lifetime.signal });
  window.addEventListener("beforeunload", event => {
    if (!dirty && !pending) return;
    event.preventDefault();
    event.returnValue = "";
  }, { signal: lifetime.signal });
  window.addEventListener("pagehide", () => { lifetime.abort(); routeAbort?.abort(); watch.stop(); }, { once: true });
  window.addEventListener("pageshow", event => { if (event.persisted) location.reload(); });
  document.addEventListener("visibilitychange", () => document.hidden ? watch.stop() : void watch.trigger(), { signal: lifetime.signal });
  window.addEventListener("focus", () => void watch.trigger(), { signal: lifetime.signal });
  void render();
}
