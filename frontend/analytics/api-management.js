import { apiDocsPage } from "./api-docs.js";
import { loadingTable } from "./loading.js";
import { userPermissions } from "../modules/user-model.js";
import { node, button, actions, stack, section, table, badge, field, code, muted, list, pathID, notice, copyText, link } from "./ui.js";
import { date } from "./operations.js";
import { patch } from "./live-dom.js";

function apiKeysPanel(ctx) {
  let resource = { items: [], read_permissions: [], permissions: [] }, sequence = 0, scopeView = null;
  const results = node("div", {}, loadingTable(["名称", "前缀", "状态", "授权范围", "到期时间", "最近使用", "操作"], "API Key"));
  const feedback = notice("正在读取 API Key…", "");
  const create = button("创建 Key", () => edit(), { tone: "primary", disabled: true });
  const retry = button("重新读取", () => void load());
  const recovery = node("div", { className: "a-card-body a-stack" }, feedback, actions(retry)); recovery.hidden = false;
  const labels = { ...Object.fromEntries(userPermissions),
    'agents.grant': '节点转授权', 'certificates.read': '证书查看', 'certificates.manage': '证书管理',
    'certificates.grant': '证书转授权', 'preferences.read': '个人偏好查看', 'preferences.write': '个人偏好编辑',
    'agent-access.read': '共享授权查看', 'agent-access.write': '共享邀请回应', 'secrets.read': '敏感数据读取（含凭据）',
  };
  async function load() {
    const request = ++sequence;
    results.setAttribute("aria-busy", "true");
    try {
      const latest = await ctx.api("/settings/api-keys");
      if (!ctx.current() || request !== sequence) return;
      resource = latest; draw();
      feedback.textContent = ""; recovery.hidden = true; create.disabled = false;
    } catch (error) {
      if (!ctx.current() || request !== sequence || error.name === "AbortError") return;
      feedback.textContent = `无法读取 API Key：${error.message}`; recovery.hidden = false;
      results.firstElementChild?.setReadError?.();
      if (error.status === 401 || error.status === 403) { scopeView?.box.close(); results.replaceChildren(); results.hidden = true; resource = { items: [], read_permissions: [], permissions: [] }; create.disabled = true; }
    } finally { if (request === sequence) results.removeAttribute("aria-busy"); }
  }
  const body = key => ({ name: key.name, permissions: key.permissions, ip_allowlist: key.ip_allowlist,
    mode: key.mode || "read_only", expires_at: key.expires_at, revision: key.revision, disabled: key.disabled });
  function scopeContent(key) {
    return stack(node("p", {}, node("strong", {}, key.name)),
      section("业务权限", node("div", { className: "a-card-body" },
        list(key.permissions).length ? actions(...key.permissions.map(permission => labels[permission] ? badge(labels[permission]) : node("p", { className: "a-muted" }, permission))) : muted("未授予业务权限"))),
      section("来源 IP / CIDR", node("div", { className: "a-card-body a-stack" },
        list(key.ip_allowlist).length ? key.ip_allowlist.map(address => muted(address)) : muted("不限制"))));
  }
  function showScope(id) {
    const key = list(resource.items).find(item => item.id === id);
    if (!ctx.current() || !key) return;
    const content = scopeContent(key), box = ctx.dialog("授权范围", content);
    scopeView = { id, content, box };
    box.addEventListener("close", () => { if (scopeView?.box === box) scopeView = null; }, { once: true });
  }
  function edit(key = null) {
    const available = list(resource.permissions?.length ? resource.permissions : resource.read_permissions);
    const controls = available.map(permission => field(labels[permission] || permission, `permission:${permission}`,
      key ? key.permissions.includes(permission) : false, { type: "checkbox" }));
    const localTime = key?.expires_at ? new Date(new Date(key.expires_at).getTime() - new Date(key.expires_at).getTimezoneOffset() * 60000).toISOString().slice(0, 16) : "";
    const form = ctx.form([
      field("名称", "name", key?.name || "", { required: true, maxlength: 100 }),
      field("模式", "mode", key?.mode || "read_only", { options: [["read_only", "只读"], ["read_write", "读写"]] }),
      field("到期时间", "expires_at", localTime, { type: "datetime-local", hint: "留空为永不过期" }),
      field("来源 IP / CIDR", "ip_allowlist", key?.ip_allowlist?.join("\n") || "", { multiline: 3, hint: "每行一个，留空不限制" }),
      section("业务权限", node("div", { className: "a-card-body a-grid" }, controls)),
    ], key ? "保存授权" : "创建 Key", async (values, element) => {
      const payload = { name: values.name, mode: values.mode, expires_at: values.expires_at === localTime ? key?.expires_at || null : values.expires_at ? new Date(values.expires_at).toISOString() : null,
        ip_allowlist: values.ip_allowlist.split(/[\s,，]+/).filter(Boolean),
        permissions: available.filter(permission => { const control = element.elements.namedItem(`permission:${permission}`); return !control.disabled && control.checked; }),
        ...(key ? { revision: key.revision, disabled: key.disabled } : {}),
      };
      const saved = await ctx.api(key ? `/settings/api-keys/${pathID(key.id)}` : "/settings/api-keys", { method: key ? "PUT" : "POST", body: payload });
      if (!ctx.current() || !box.isConnected || !box.open) { saved.secret = ""; return; }
      box.close();
      if (!key && saved.secret) {
        const secret = code(saved.secret); saved.secret = "";
        secret.setAttribute("data-sensitive", "");
        const revealed = ctx.dialog("API Key 已创建", stack(muted("密钥仅显示一次，请妥善保存。"), secret,
          actions(button("复制 Key", event => void ctx.mutate(event.currentTarget, async () => {
            await copyText(secret.textContent);
            if (ctx.current() && revealed.isConnected) ctx.announce("已复制");
          })))));
        revealed.addEventListener("close", () => { secret.textContent = ""; }, { once: true });
      }
      await load();
      ctx.announce(key ? "授权已保存" : "API Key 已创建");
    });
    function syncMode() {
      const readOnly = form.elements.mode.value === "read_only";
      for (const permission of available) {
        const control = form.elements.namedItem(`permission:${permission}`);
        control.disabled = readOnly && !list(resource.read_permissions).includes(permission);
      }
    }
    form.elements.mode.addEventListener("change", syncMode); syncMode();
    const box = ctx.dialog(key ? "编辑 API Key" : "创建 API Key", form, { wide: true });
  }
  function draw() {
    results.hidden = false;
    const items = list(resource.items);
    const fresh = table(["名称", "前缀", "状态", "授权范围", "到期时间", "最近使用", "操作"], items.map(key => [
      stack(key.name, muted(date(key.created_at))), `${key.prefix}…`, stack(actions(badge(({ active: "有效", expired: "已过期", disabled: "已停用", revoked: "已撤销" })[key.status] || key.status)), muted(key.mode === "read_write" ? "读写" : "只读")),
      stack(button(`${list(key.permissions).length} 项权限`, () => showScope(key.id), { "aria-haspopup": "dialog" }),
        muted(list(key.ip_allowlist).length ? `${key.ip_allowlist.length} 个来源` : "来源 IP 不限制")),
      key.expires_at ? date(key.expires_at) : "永不过期", date(key.last_used_at),
      !key.revoked_at && actions(button("编辑授权", () => edit(key)),
        button(key.disabled ? "启用" : "停用", event => void ctx.mutate(event.currentTarget, async () => {
          await ctx.api(`/settings/api-keys/${pathID(key.id)}`, { method: "PUT", body: { ...body(key), disabled: !key.disabled } });
          await load();
        })),
        button("撤销", async event => {
          const control = event.currentTarget;
          if (!await ctx.confirm(`撤销“${key.name}”后无法恢复，确定撤销？`, "撤销 API Key") || !ctx.current()) return;
          await ctx.mutate(control, async () => {
            await ctx.api(`/settings/api-keys/${pathID(key.id)}`, { method: "DELETE" });
            await load();
          });
        }, { tone: "danger" })),
    ]), { label: "API Key", rowKey: (_, index) => items[index].id });
    if (!results.firstElementChild?.reconcileTable?.(fresh)) results.replaceChildren(fresh);
    if (scopeView) {
      const key = items.find(item => item.id === scopeView.id);
      if (key) patch(scopeView.content, scopeContent(key));
      else scopeView.box.close();
    }
  }
  void load();
  ctx.onRefresh(load);
  return stack(section("接入", node("div", { className: "a-card-body a-stack" },
    code(`${location.origin}/api/v1`), code("Authorization: Bearer <QCH_API_KEY>"))),
    section("API Key", stack(recovery, results), create));
}

export function apiPage(ctx) {
  const canManage = ctx.can("settings.manage");
  const requestedDocs = location.hash === "#api-docs" || new URLSearchParams(location.hash.split("?")[1]).get("tab") === "docs";
  let selected = !canManage || requestedDocs ? "docs" : "keys";
  const content = node("div", {}), panels = new Map(), refreshes = new Map();
  const choices = [canManage && ["keys", "密钥"], ["docs", "接口规范"]].filter(Boolean);
  let tabTouched = false;
  const controls = choices.map(([id, label]) => button(label, () => { tabTouched = true; ctx.preferences?.patch('api', { tab: id }); select(id); }, { "aria-pressed": selected === id }));
  function select(id) {
    selected = id;
    ctx.pageActions(id === "docs" ? link("下载规范", "/api/v1/openapi.json") : link("下载管理 Skill", "/api/v1/management-skill"));
    controls.forEach((control, index) => {
      const active = choices[index][0] === id;
      control.classList.toggle("primary", active);
      control.setAttribute("aria-pressed", String(active));
    });
    if (!panels.has(id)) {
      const child = { ...ctx, onRefresh(read) { refreshes.set(id, read); }, pageActions() {} };
      const panel = id === "keys" ? apiKeysPanel(child) : apiDocsPage(child);
      panels.set(id, panel); content.append(panel);
    }
    for (const [name, panel] of panels) panel.hidden = name !== id;
    history.replaceState(null, "", id === "docs" ? "#api?tab=docs" : "#api");
  }
  content.addEventListener('input', () => { tabTouched = true; }, { signal: ctx.signal });
  ctx.onRefresh(() => refreshes.get(selected)?.());
  select(selected);
  void ctx.preferences?.ready().then(() => {
    if (!ctx.current() || tabTouched || requestedDocs) return;
    const saved = ctx.preferences.get('api').tab;
    if (choices.some(([id]) => id === saved)) select(saved);
  });
  return stack(actions(...controls), content);
}
