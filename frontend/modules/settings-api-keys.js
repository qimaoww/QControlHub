import { bindEvent } from "./refresh.js";
import { bindDialogBackdrop, closeRetiringDialog } from "./popup.js";
import { userPermissions } from "./user-model.js";

const labels = { ...Object.fromEntries(userPermissions), "audit.read": "审计记录", "core-logs.read": "内核日志" };
const statuses = { active: "有效", expired: "已过期", disabled: "已停用", revoked: "已撤销" };
export function apiKeysSection() {
  return `<section class="settings-section api-keys-section" id="settings-api-keys"><header><span class="settings-section-number">10</span><div><h3>API Key</h3><p>供 AI 或脚本只读排查，沿用当前管理员的资源范围。</p></div></header><div class="settings-subsection"><div class="api-keys-toolbar"><p class="settings-hint">密钥仅创建时显示一次。</p><button class="button" type="button" data-api-key-create>创建 Key</button></div><div data-api-key-list aria-live="polite">正在读取…</div></div></section>`;
}
export function bindAPIKeys({ api, state, esc, date, notify, confirmAction }) {
  const root = document.querySelector("#settings-api-keys");
  if (!root) return;
  const account = state.data;
  const current = () => root.isConnected && state.data === account && state.route === "settings";
  let resource = { items: [], read_permissions: [] };
  let loading = 0;
  const time = value => value ? esc(date(value)) : "—";
  const report = error => { if (current() && error.name !== "AbortError") notify(error.message, "error"); };
  async function load() {
    const serial = ++loading;
    try {
      const result = await api("/settings/api-keys");
      if (!current() || serial !== loading) return;
      resource = result;
      const list = root.querySelector("[data-api-key-list]");
      list.innerHTML = result.items.length ? `<ul class="api-key-list">${result.items.map(key => `<li><div class="api-key-summary"><strong>${esc(key.name)}</strong><code>${esc(key.prefix)}…</code><span class="status-label ${key.status === "active" ? "ok" : "muted"}">${esc(statuses[key.status] || key.status)}</span></div><dl class="api-key-meta"><div><dt>创建</dt><dd>${time(key.created_at)}</dd></div><div><dt>到期</dt><dd>${key.expires_at ? time(key.expires_at) : "永不过期"}</dd></div><div><dt>最近使用</dt><dd>${time(key.last_used_at)}</dd></div><div><dt>IP 白名单</dt><dd>${key.ip_allowlist.length ? esc(key.ip_allowlist.join("、")) : "不限制"}</dd></div></dl><div class="api-key-actions"><span class="settings-hint">${key.permissions.length} 项读取权限</span>${key.revoked_at ? "" : `<button type="button" class="button small" data-api-key-edit="${esc(key.id)}">编辑授权</button><button type="button" class="button small" data-api-key-toggle="${esc(key.id)}">${key.disabled ? "启用" : "停用"}</button><button type="button" class="button small danger" data-api-key-revoke="${esc(key.id)}">撤销</button>`}</div></li>`).join("")}</ul>` : `<div class="empty compact"><strong>暂无 API Key</strong><p>创建后可配置读取权限与来源 IP。</p></div>`;
      list.querySelectorAll("[data-api-key-edit]").forEach(button => bindEvent(button, "click", () => edit(resource.items.find(k => k.id === button.dataset.apiKeyEdit))));
      list.querySelectorAll("[data-api-key-toggle]").forEach(button => bindEvent(button, "click", async () => {
        const key = resource.items.find(k => k.id === button.dataset.apiKeyToggle);
        button.disabled = true;
        try { await api(`/settings/api-keys/${encodeURIComponent(key.id)}`, { method: "PUT", body: JSON.stringify({ name: key.name, permissions: key.permissions, ip_allowlist: key.ip_allowlist, expires_at: key.expires_at, revision: key.revision, disabled: !key.disabled }) }); await load(); notify(key.disabled ? "Key 已启用" : "Key 已停用"); } catch (error) { report(error); } finally { button.disabled = false; }
      }));
      list.querySelectorAll("[data-api-key-revoke]").forEach(button => bindEvent(button, "click", async () => {
        const key = resource.items.find(k => k.id === button.dataset.apiKeyRevoke);
        if (!await confirmAction(`撤销“${key.name}”后无法恢复。`, "撤销", { title: "撤销 API Key", danger: true })) return;
        if (!current()) return;
        button.disabled = true;
        try { await api(`/settings/api-keys/${encodeURIComponent(key.id)}`, { method: "DELETE" }); await load(); notify("Key 已撤销"); } catch (error) { report(error); } finally { button.disabled = false; }
      }));
    } catch (error) { if (current()) { root.querySelector("[data-api-key-list]").textContent = "无法读取 API Key"; report(error); } }
  }
  function dialog(title) {
    const node = document.createElement("dialog");
    node.className = "traffic-edit-dialog api-key-dialog";
    node.setAttribute("aria-labelledby", "api-key-dialog-title");
    node.innerHTML = `<header><div><h2 id="api-key-dialog-title">${title}</h2></div><button type="button" class="deploy-command-close" aria-label="关闭" data-key-close>×</button></header>`;
    document.body.append(node);
    let pending = false;
    const close = () => { if (!pending) { const secret = node.querySelector("[data-key-secret]"); if (secret) { secret.value = ""; node.close(); } else closeRetiringDialog(node); } };
    const cleanup = () => { node.remove(); window.removeEventListener("hashchange", navigate); state.routeSignal?.removeEventListener("abort", navigate); };
    const navigate = () => { node.close(); cleanup(); };
    window.addEventListener("hashchange", navigate);
    state.routeSignal?.addEventListener("abort", navigate, { once: true });
    node.addEventListener("close", cleanup, { once: true });
    node.addEventListener("cancel", event => { event.preventDefault(); close(); });
    bindDialogBackdrop(node, close);
    node.addEventListener("click", event => { if (event.target.closest("[data-key-close]")) close(); });
    return { node, setPending: value => { pending = value; }, close };
  }
  function reveal(secret) {
    if (!current()) return;
    const { node } = dialog("保存 API Key");
    node.insertAdjacentHTML("beforeend", `<div class="traffic-edit-body"><p>请立即复制并保存在安全位置。关闭后无法再次查看。</p><label>完整 Key<input data-key-secret readonly autocomplete="off" spellcheck="false"></label><p class="settings-hint">使用 Authorization: Bearer &lt;API_KEY&gt;，请勿放入 URL。</p><div data-key-copy-status role="status"></div></div><footer><button type="button" class="button" data-key-copy>复制 Key</button><button type="button" class="button primary" data-key-close>已保存</button></footer>`);
    const input = node.querySelector("[data-key-secret]"); input.value = secret; secret = "";
    node.addEventListener("close", () => { input.value = ""; }, { once: true });
    node.querySelector("[data-key-copy]").onclick = async () => {
      try { await navigator.clipboard.writeText(input.value); node.querySelector("[data-key-copy-status]").textContent = "已复制"; } catch { input.select(); node.querySelector("[data-key-copy-status]").textContent = "请选择并手动复制"; }
    };
    node.showModal();
  }
  function edit(key) {
    if (!current() || document.querySelector(".api-key-dialog[open]")) return;
    const { node, close, setPending } = dialog(key ? "编辑 API Key" : "创建 API Key");
    const localTime = value => { if (!value) return ""; const d = new Date(value); return new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0,16); };
    const selected = new Set(key?.permissions ?? resource.read_permissions);
    node.insertAdjacentHTML("beforeend", `<form data-key-form><div class="traffic-edit-body"><div class="alert error" data-key-error role="alert" tabindex="-1" hidden></div><label>名称<input name="name" required maxlength="100" autocomplete="off" value="${esc(key?.name || "")}" placeholder="例如：AI 排查"></label><label>到期时间<input name="expires_at" type="datetime-local" value="${esc(localTime(key?.expires_at))}"><small>使用本地时间；留空为永不过期。</small></label><label>IP 白名单<textarea name="ip_allowlist" rows="4" maxlength="5000" spellcheck="false" placeholder="192.0.2.10&#10;2001:db8::/32">${esc(key?.ip_allowlist.join("\n") || "")}</textarea><small>每行一个 IPv4、IPv6 或 CIDR；留空不限制。</small></label><fieldset class="api-key-permissions"><legend>只读排查权限</legend><button class="button small" type="button" data-key-preset>使用只读排查预设</button><div>${resource.read_permissions.map(p => `<label><input type="checkbox" name="permissions" value="${esc(p)}" ${selected.has(p) ? "checked" : ""}>${esc(labels[p] || p)}</label>`).join("")}</div></fieldset></div><footer><button class="button" type="button" data-key-close>取消</button><button class="button primary" type="submit">${key ? "保存授权" : "创建 Key"}</button></footer></form>`);
    const form = node.querySelector("form");
    node.querySelector("[data-key-preset]").onclick = () => form.querySelectorAll('[name="permissions"]').forEach(input => { input.checked = true; });
    form.onsubmit = async event => {
      event.preventDefault();
      const button = form.querySelector('[type="submit"]'); if (button.disabled) return;
      const data = new FormData(form);
      const body = { name: data.get("name"), expires_at: data.get("expires_at") ? new Date(data.get("expires_at")).toISOString() : null, ip_allowlist: String(data.get("ip_allowlist")).split(/\r?\n/).map(s => s.trim()).filter(Boolean), permissions: data.getAll("permissions"), disabled: key?.disabled || false, revision: key?.revision || 0 };
      button.disabled = true; setPending(true);
      try {
        const saved = await api(key ? `/settings/api-keys/${encodeURIComponent(key.id)}` : "/settings/api-keys", { method: key ? "PUT" : "POST", body: JSON.stringify(body) });
        setPending(false); close();
        if (!current()) return;
        if (saved.secret) { reveal(saved.secret); saved.secret = ""; } else notify("授权已保存");
        await load();
      } catch (error) { if (!node.isConnected || !current()) return; const box = node.querySelector("[data-key-error]"); box.hidden = false; box.textContent = error.message; box.focus(); } finally { button.disabled = false; setPending(false); }
    };
    node.showModal();
    form.elements.name.focus();
  }
  bindEvent(root.querySelector("[data-api-key-create]"), "click", () => edit());
  void load();
}
