import { requestCoreLogJSON as boundedRead } from "../modules/core-log-read.js";
import { mountTheme } from "../modules/theme-lifecycle.js";
import { createAnalyticsMotion } from "./motion.js";
import { applyAppearance } from "./appearance.js";
import { bindSiteLogo, refreshSiteBranding } from "./site-brand.js";
import { setStorageAccount } from "../modules/account-storage.js";
import { clearConnectionStorage } from "../modules/client-connection-cache.js";
import { accountRequest } from "../modules/account-request.js";
import { effectiveTheme } from "../modules/theme-registry.js";
import { createPermissionChecker } from "../modules/permissions.js";
import { routeForHash } from "../modules/routes.js";
import { createSessionWatch } from "../modules/session-watch.js";
import { node, icon, button, actions, stack, section, notice, empty, field, formData, openDialog } from "./ui.js";
import { createNotifications } from "./notifications.js";
import { createRouteModuleLoader } from "../modules/route-loader.js";
import { createRouteWarmup } from "../modules/route-warmup.js";

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
    ["traffic", "流量", "activity", "traffic.read"],
    ["system-bbr", "TCP 调优", "settings", "system-bbr.read"], ["ip-quality", "IP 质量", "activity", "agents.read"],
  ]],
  ["面板", [
    ["settings", "面板设置", "settings", "settings.read"], ["users", "用户管理", "users", "users.manage"],
    ["my-quota", "共享与额度", "users", "agent-access.read"], ["themes", "主题", "palette", "agent-access.read"],
  ]],
];
const pageModules = {
  dashboard: ['dashboard', 'dashboardPage'], 'node-settings': ['agents', 'nodesPage'], tasks: ['tasks', 'tasksPage'],
  'my-quota': ['quota', 'quotaPage'], 'archive-config': ['archives', 'archivePage'], themes: ['themes', 'themePage'],
  certificates: ['certificates', 'certificatesPage'], 'ip-quality': ['ip-quality', 'ipQualityPage'], users: ['users', 'usersPage'],
  traffic: ['traffic', 'trafficPage'], settings: ['settings', 'settingsPage'], 'substore-sync': ['substore-sync', 'substorePage'],
  'live-config': ['configs', 'liveConfigPage'], 'system-bbr': ['system-bbr', 'tcpPage'],
  'core-logs': ['core-logs', 'logsPage'], 'client-connections': ['client-connections', 'connectionsPage'], 'client-access': ['client-access', 'clientAccessPage'],
};
const pageModuleName = route => pageModules[route]?.[0] || 'dashboard';
const routeModules = createRouteModuleLoader({
  dashboard: () => import('./operations.js'), tasks: () => import('./operations.js'), quota: () => import('./operations.js'),
  agents: () => import('./nodes.js'), archives: () => import('./configs.js'), themes: () => import('./themes.js'),
  certificates: () => import('./certificates.js'), 'ip-quality': () => import('./ip-quality.js'), users: () => import('./users.js'),
  traffic: () => import('./traffic.js'), settings: () => import('./settings.js'), 'substore-sync': () => import('./substore.js'),
  configs: () => import('./live-config.js'), 'system-bbr': () => import('./tcp.js'),
  'core-logs': () => import('./observability.js'), 'client-connections': () => import('./observability.js'), 'client-access': () => import('./client-access.js'),
});

// The new runtime never imports the classic shell or its renderers. It owns
// its listeners, dialogs and pending requests for the whole document lifetime.
export function startAnalytics({ app, session }) {
  const presentation = mountTheme("analytics", createAnalyticsMotion);
  const motion = presentation.motion;
  presentation.own(() => document.documentElement.style.removeProperty("--a-modal-scrollbar-gap"));
  const state = { session, data: {} };
  setStorageAccount(session);
  const can = createPermissionChecker(state);
  const lifetime = new AbortController();
  const warmup = createRouteWarmup(routeModules, { moduleName: pageModuleName, signal: lifetime.signal });
  warmup.bindNavigationPreload();
  let routeAbort = null, routeEpoch = 0, dirty = false, pending = 0;
  let currentHash = location.hash, route = "", dialogSequence = 0, navigation = 0;
  const layout = node("div", { className: "a-layout" });
  const main = node("main", { className: "a-main", id: "content", tabindex: "-1" });
  const notifications = createNotifications(presentation);
  const work = node("div", { className: "a-work" });
  let drawer, shellTitle, navigationToggle, pageActions, refreshButton, pageRefresh;
  document.body.className = "analytics";
  app.replaceChildren(layout);
  applyAppearance();

  const api = async (path, options = {}) => {
    const bound = state.session;
    const data = state.data;
    const writing = !['GET', 'HEAD', 'OPTIONS'].includes(String(options.method || 'GET').toUpperCase());
    if (writing) delete data.analyticsTrafficSnapshot;
    const result = await accountRequest(path, {
      ...options, session: bound, signal: options.signal || lifetime.signal,
      isCurrent: () => state.session === bound && !lifetime.signal.aborted,
      onUnauthorized: message => login(message),
    }).finally(() => { if (writing) delete data.analyticsTrafficSnapshot; });
    if (path === "/settings" && state.session === bound && !lifetime.signal.aborted) {
      state.data.settings = result;
      applyAppearance(result);
    }
    return result;
  };
  const watch = createSessionWatch({ api, state, onAccessChanged: () => {
    routeAbort?.abort();
    state.data.analyticsConnectionCache?.clear();
    clearConnectionStorage();
    document.querySelectorAll("dialog.a-dialog").forEach(box => box.close());
    setStorageAccount(state.session);
    state.data = {};
    applyAppearance();
    dirty = false;
    shellTitle = null;
    void render();
  } });
  const announce = notifications.show;
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
    box.addEventListener("close", () => {
      const restore = document.activeElement === document.body || box.contains(document.activeElement);
      notifications.position(); box.remove();
      if (restore && active?.isConnected) active.focus();
    }, { once: true });
    openDialog(box);
    notifications.position();
    return box;
  }
  async function confirm(message, label = "确认") {
    return new Promise(resolve => {
      const box = dialog(label, stack(node("p", {}, message), actions(
        button("取消", () => box.close(), { autofocus: true }),
        button(label, () => { resolve(true); box.close(); }, { tone: "primary" }),
      )));
      box.addEventListener("close", () => resolve(false), { once: true });
    });
  }
  async function mutate(control, run) {
    if (pending) { announce("操作正在提交，请等待结果。", "error"); return; }
    if (control?.disabled) return;
    const disabled = control?.disabled;
    if (control) { control.disabled = true; control.setAttribute("aria-busy", "true"); }
    pending++;
    // A write may replace its form or table cell before its readback finishes.
    // Keep pending state on the stable app root through the whole operation.
    app.setAttribute("aria-busy", "true");
    try { return await run(); }
    catch (error) {
      if (error.name !== "AbortError") announce(error.message, "error");
    } finally {
      pending--;
      if (!pending) app.removeAttribute("aria-busy");
      if (control?.isConnected) { control.disabled = disabled; control.removeAttribute("aria-busy"); control.dispatchEvent(new Event("mutationend")); }
    }
  }
  function form(fields, label, save) {
    const feedback = notice("");
    const submit = node("button", { type: "submit", className: "a-button primary" }, label);
    const controls = node("fieldset", { className: "a-form-fields a-stack" }, fields);
    const element = node("form", { className: "a-stack" }, controls, feedback, actions(submit));
    element.addEventListener("invalid", event => {
      event.preventDefault();
      for (let parent = event.target.parentElement; parent && parent !== element; parent = parent.parentElement)
        if (parent.tagName === "DETAILS") parent.open = true;
      const first = [...element.elements].find(control => control.willValidate && !control.validity.valid);
      if (first === event.target) {
        first.focus();
        const inline = document.getElementById(first.getAttribute("aria-errormessage"));
        feedback.textContent = inline?.matches(".a-validation") ? "" : first.validity.customError ? first.validationMessage : first.validity.valueMissing ? "请填写此项。" : "请检查此项的格式或取值范围。";
      }
    }, true);
    element.addEventListener("submit", event => {
      event.preventDefault();
      if (pending || submit.disabled) return;
      const values = formData(element);
      controls.disabled = true;
      element.setAttribute("aria-busy", "true");
      void mutate(submit, async () => {
        feedback.textContent = "";
        let invalid;
        try { await save(values, element); }
        catch (error) {
          if (error.name === "AbortError") return;
          feedback.textContent = error.message;
          invalid = error.field && element.elements.namedItem(error.field);
        } finally {
          controls.disabled = false;
          element.removeAttribute("aria-busy");
          if (invalid && !invalid.disabled) {
            for (let parent = invalid.parentElement; parent && parent !== element; parent = parent.parentElement)
              if (parent.tagName === "DETAILS") parent.open = true;
            invalid.focus();
          }
        }
      });
    });
    return element;
  }
  function rail(mobile = false) {
    return node("aside", { className: "a-rail" },
      node("a", { className: "a-brand", href: "#dashboard" }, bindSiteLogo(node("span", { className: "a-mark" }, icon("brand"))), "QControlHub"),
      mobile && button("", () => drawer.close(), { glyph: "close", title: "关闭导航", className: "a-icon-button a-drawer-close" }),
      node("nav", { "aria-label": "主导航" }, destinations.map(([group, entries]) => {
        const allowed = entries.filter(([id, , , permission]) => id === "themes" || can(permission));
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
    // Keep navigation and header nodes alive across routes and refreshes.
    // Scroll, keyboard focus and collapse state belong to this session's shell.
    if (!shellTitle) {
      navigationToggle = button("", () => {
        if (matchMedia("(max-width: 56rem)").matches) {
          drawer = node("dialog", { className: "a-dialog a-drawer", "aria-label": "导航" }, rail(true));
          const opened = drawer;
          opened.addEventListener("close", () => {
            notifications.position();
            opened.remove();
            if (drawer === opened) { drawer = null; navigationToggle?.setAttribute("aria-expanded", "false"); }
          }, { once: true });
          document.body.append(opened);
          navigationToggle.focus({ preventScroll: true });
          openDialog(opened);
          notifications.position();
          navigationToggle.setAttribute("aria-expanded", "true");
        } else {
          layout.classList.toggle("a-collapsed");
          motion?.navigation(layout);
          navigationToggle.setAttribute("aria-expanded", String(!layout.classList.contains("a-collapsed")));
        }
      }, { glyph: "menu", title: "切换导航", className: "a-icon-button" });
      navigationToggle.setAttribute("aria-label", "切换导航");
      navigationToggle.setAttribute("aria-expanded", String(!matchMedia("(max-width: 56rem)").matches && !layout.classList.contains("a-collapsed")));
      shellTitle = node("h1", { className: "a-top-title" }, title);
      const top = node("header", { className: "a-top" }, node("div", { className: "a-top-inner" },
        navigationToggle, shellTitle, actions(pageActions = actions(), refreshButton = button("刷新", () => void refresh(), { glyph: "refresh" }))));
      work.replaceChildren(top, main);
      layout.replaceChildren(node("a", {
        href: "#content", className: "a-button a-skip",
        onclick(event) { event.preventDefault(); main.focus(); main.scrollIntoView(); },
      }, "跳到内容"), rail(), work);
    }
    shellTitle.textContent = title;
    layout.querySelectorAll('.a-nav-link').forEach(link => {
      if (link.hash === `#${route}`) link.setAttribute("aria-current", "page");
      else link.removeAttribute("aria-current");
    });
    pageActions.replaceChildren();
    main.replaceChildren();
  }
  async function leave() {
    if (pending) { announce("操作正在提交，请等待结果。", "error"); return false; }
    if (dirty && !await confirm("存在未保存的更改，确定离开？", "离开页面")) return false;
    dirty = false;
    return true;
  }
  async function refresh() {
    if (pageRefresh) {
      if (pending || refreshButton.disabled) return;
      const epoch = routeEpoch, read = pageRefresh;
      refreshButton.disabled = true;
      try { await read(); }
      catch (error) { if (epoch === routeEpoch && error.name !== 'AbortError') announce(error.message, 'error'); }
      finally { if (epoch === routeEpoch) refreshButton.disabled = false; }
      return;
    }
    if (!await leave()) return;
    await render();
  }
  async function logout() {
    if (!await leave()) return;
    await mutate(null, async () => { await api("/auth/logout", { method: "POST", body: {} }); login(); });
  }
  function login(message = "") {
    void refreshSiteBranding();
    routeAbort?.abort();
    state.data.analyticsConnectionCache?.clear();
    clearConnectionStorage();
    watch.stop();
    const loginURL = new URL(location.href);
    loginURL.searchParams.delete("theme");
    history.replaceState(null, "", loginURL);
    notifications.clear();
    state.session = null;
    setStorageAccount(null);
    state.data = {};
    shellTitle = navigationToggle = null;
    layout.classList.remove("a-collapsed");
    applyAppearance();
    dirty = false;
    document.querySelectorAll("dialog.a-dialog").forEach(box => box.close());
    const loginForm = form([
      field("用户名", "username", "", { autocomplete: "username", required: true, maxlength: 64, pattern: "(?:[A-Za-z0-9._]|-)+", autocapitalize: "none", spellcheck: false }),
      field("密码 / 管理令牌", "token", "", { type: "password", autocomplete: "current-password", required: true }),
    ], "登录", async values => {
      const authenticated = await api("/auth/login", { method: "POST", body: values });
      const preference = await accountRequest("/preferences/theme", {
        session: authenticated, signal: lifetime.signal,
        isCurrent: () => loginForm.isConnected && !state.session && !lifetime.signal.aborted,
      });
      state.session = authenticated;
      if (effectiveTheme(preference.theme_id) !== "analytics") {
        // Authentication has completed. Release the old runtime before the
        // deliberate theme boundary; its pending login is not an editor draft.
        lifetime.abort();
        routeAbort?.abort();
        watch.stop();
        presentation.dispose();
        location.reload();
        return;
      }
      setStorageAccount(state.session);
      dirty = false;
      app.replaceChildren(layout);
      await render();
    });
    loginForm.setAttribute("aria-label", "登录控制台");
    app.replaceChildren(node("main", { className: "a-login" },
      node("div", { className: "a-login-brand" }, bindSiteLogo(node("span", { className: "a-mark" }, icon("brand"))), node("strong", {}, "QControlHub")),
      node("section", { className: "a-login-card", "aria-labelledby": "a-login-heading" },
        node("header", { className: "a-login-heading" }, node("h1", { id: "a-login-heading" }, "登录控制台")),
        message && notice(message), loginForm)));
    document.title = "登录 · QControlHub";
    app.querySelector("input")?.focus();
    motion?.page(app);
  }
  async function render() {
    if (!state.session) { login(); return; }
    routeAbort?.abort();
    routeAbort = new AbortController();
    const signal = routeAbort.signal, epoch = ++routeEpoch;
    pageRefresh = null;
    const previousRoute = route;
    route = routeForHash(location.hash);
    if (route === "dashboard" && !can("overview.read")) route = "my-quota";
    if (["agents", "agent-config", "access-control"].includes(route)) route = "live-config";
    const entry = destinations.flatMap(([, entries]) => entries).find(([id]) => id === route);
    const title = entry?.[1] || "控制台";
    shell(title);
    refreshButton.hidden = false; refreshButton.disabled = false;
    document.title = `${title} · QControlHub`;
    const stage = node("div", { className: "a-stack", "aria-busy": "true" }, empty("正在加载…"));
    main.append(stage);
    let stagedActions = [];
    const context = {
      pageActions(...items) { stagedActions = items; },
      onRefresh(read, { local = false } = {}) { if (!signal.aborted && epoch === routeEpoch) { pageRefresh = read; refreshButton.hidden = local; } },
      api: (path, options = {}) => api(path, { ...options, signal: options.signal || signal }),
      can, session: state.session, signal, state, dialog, confirm, mutate, form, announce,
      motion,
      refresh, visible: (...args) => motion ? motion.visible(...args) : (args[0].hidden = !args[1]), setDirty(value) { dirty = value; },
      async switchTheme(save) {
        if (pending || !await leave()) return false;
        pending++;
        try { await save(); presentation.dispose(); }
        finally { pending--; }
        return true;
      },
      current: () => !signal.aborted && epoch === routeEpoch && Boolean(state.session),
    };
    try {
      if (entry && route !== "themes" && !can(entry[3])) { stage.replaceChildren(notice("没有访问此页面的权限。")); return; }
      const descriptor = pageModules[route];
      if (!descriptor) {
        stage.replaceChildren(notice("页面不存在。"));
        return;
      }
      // Ordering is independent of the page module and appearance settings,
      // but must be known before the first connection query creates a cursor.
      if (route === "client-connections") {
        context.connectionNodeOrder = boundedRead(context.api, "/preferences/node-order", { signal: context.signal });
        void context.connectionNodeOrder.catch(() => {});
        context.connectionScope = boundedRead(context.api, "/client-connections?scope_only=true", { signal: context.signal });
        void context.connectionScope.catch(() => {});
      }
      const [module] = await Promise.all([
        routeModules.load(descriptor[0]),
        can('settings.read') && !state.data.settings ? context.api('/settings') : null,
      ]);
      if (!context.current()) return;
      const page = module[descriptor[1]];
      const content = await page(context);
      if (context.current()) {
        pageActions.replaceChildren(...stagedActions);
        stage.replaceChildren(content);
        warmup.scheduleRouteWarmup(route);
        if (previousRoute !== route) motion?.page(stage);
      }
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
    const targetControl = document.activeElement?.matches('.a-target-form select[name]') ? document.activeElement.name : "";
    const allowed = await leave();
    if (attempt !== navigation) return;
    if (!allowed) { history.replaceState(null, "", currentHash || "#dashboard"); return; }
    currentHash = nextHash;
    document.querySelectorAll("dialog.a-dialog").forEach(box => box.close());
    await render();
    if (attempt === navigation && targetControl)
      main.querySelector(`.a-target-form select[name="${targetControl}"]`)?.focus({ preventScroll: true });
  }, { signal: lifetime.signal });
  window.addEventListener("beforeunload", event => {
    if (!state.session || (!dirty && !pending)) return;
    event.preventDefault();
    event.returnValue = "";
  }, { signal: lifetime.signal });
  window.addEventListener("pagehide", () => { state.data.analyticsConnectionCache?.flush(); lifetime.abort(); routeAbort?.abort(); watch.stop(); }, { once: true });
  window.addEventListener("pageshow", event => { if (event.persisted) location.reload(); });
  document.addEventListener("visibilitychange", () => document.hidden ? watch.stop() : void watch.trigger(), { signal: lifetime.signal });
  window.addEventListener("focus", () => void watch.trigger(), { signal: lifetime.signal });
  void render();
}
