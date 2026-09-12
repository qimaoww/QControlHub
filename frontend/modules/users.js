import { bindEvent } from "./refresh.js";

const GiB = 1024 ** 3;
export const userPermissions = [
  ["overview.read", "总览"], ["agents.read", "节点查看"], ["metrics.read", "性能指标"],
  ["agent-config.read", "配置查看"], ["agent-config.write", "配置编辑"], ["catalogs.read", "内核预设"],
  ["configs.read", "配置存档"], ["configs.write", "存档编辑"], ["configs.restore", "版本恢复"],
  ["configs.delete", "存档删除"], ["deployments.read", "部署记录"], ["tasks.read", "任务查看"],
  ["tasks.execute", "部署与执行"], ["client-access.read", "客户端"], ["traffic.read", "流量查看"],
  ["settings.read", "设置查看"], ["settings.manage", "个人同步 / 设置"],
  ["templates.read", "模板查看"], ["templates.write", "模板编辑"], ["templates.delete", "模板删除"],
  ["agents.manage", "自有主机 / 共享管理"], ["traffic.manage", "自有节点配额"], ["enrollment.manage", "添加自有节点"],
  ["core-logs.read", "自有主机日志"], ["audit.read", "个人审计记录"],
];
const defaultPermissions = userPermissions.map(([permission]) => permission);

export function parseSharedPorts(value) {
  const parts = String(value || "").trim().split(/[\s,，]+/).filter(Boolean);
  const ports = parts.map((part) => /^\d+$/.test(part) ? Number(part) : NaN);
  if (ports.length > 256 || ports.some((port) => !Number.isInteger(port) || port < 1 || port > 65535 || port === 10085 || port === 10086) || new Set(ports).size !== ports.length)
    throw new Error("端口须为 1–65535 的不重复整数，不能使用 10085、10086。");
  return ports.sort((left, right) => left - right);
}

export function sharedLimitBytes(value) {
  const text = String(value ?? "").trim();
  const gib = Number(text);
  const bytes = Math.round(gib * GiB);
  if (!text || !Number.isFinite(gib) || gib < 0 || !Number.isSafeInteger(bytes) || (gib > 0 && bytes === 0))
    throw new Error("请输入有效额度；0 表示不限量。");
  return bytes;
}

export function sharedLimitGiB(bytes) {
  const gib = Number(bytes || 0) / GiB;
  for (let places = 0; places <= 10; places++) {
    const value = Number(gib.toFixed(places));
    if (Math.round(value * GiB) === Number(bytes || 0)) return String(value);
  }
  return String(gib);
}

export function installUsers(ctx) {
  const { api, state, esc, shell, notify, confirmAction } = ctx;
  let serial = 0, viewSerial = 0, captureActive = () => {};
  const captureDraft = () => captureActive();
  const hasUnsavedChanges = () => Boolean(state.data.userDrafts?.size || state.data.userAccessSaves?.size);
  const lockForm = (form, saving) => {
    const controls = [...form.elements].map((control) => [control, control.disabled]);
    saving.controls.push(...controls);
    controls.forEach(([control]) => { control.disabled = true; });
  };
  const formValues = (form) => ({
    isolated: true,
    rows: [...form.querySelectorAll("[data-share-row]")].map((row) => ({
      agent_id: row.dataset.shareRow,
      enabled: row.querySelector('[name="enabled"]').checked,
      limit_gib: row.querySelector('[name="limit_gib"]').value,
      ports_text: row.querySelector('[name="ports"]').value,
    })),
  });
  const usage = (bytes) => `${(Number(bytes || 0) / GiB).toLocaleString("zh-CN", { maximumFractionDigits: 2 })} GiB`;
  const current = (request, data, route) => request === serial && data === state.data && state.route === route;
  const report = (error, element) => {
    if (error?.name === "AbortError" || !element?.isConnected) return;
    const output = element.querySelector("[data-user-error]");
    if (output) { output.textContent = error.message; output.hidden = false; }
    else notify(error.message, "error");
  };

  async function users() {
    if (state.session?.role !== "admin") return;
    captureDraft();
    const request = ++serial, data = state.data;
    const [items, agents] = await Promise.all([api("/users"), api("/agents")]);
    if (!current(request, data, "users")) return;
    data.users = items;
    data.userID = items.some((user) => user.id === data.userID) ? data.userID : items[0]?.id || "";
    const user = items.find((item) => item.id === data.userID);
    const access = user ? await api(`/users/${encodeURIComponent(user.id)}/agent-access`) : null;
    if (!current(request, data, "users")) return;
    renderUsers(items, agents, user, access);
  }

  function shareRow(share, agents) {
    const agent = agents.find((item) => item.id === share.agent_id);
    const supported = agent?.features?.includes("shared-traffic-v1");
    return `<div class="user-share-row" data-share-row="${esc(share.agent_id)}">
      <label class="user-share-agent"><input type="checkbox" name="enabled" ${share.enabled ? "checked" : ""}><span><b>${esc(agent?.name || share.agent_name || share.agent_id)}</b><small>${usage(share.used_bytes)} 已用${supported ? "" : " · Agent 需升级"}</small></span></label>
      <label class="settings-field"><span>总额度（GiB）</span><input name="limit_gib" type="number" min="0" max="8388607" step="any" required value="${esc(share.limit_gib ?? sharedLimitGiB(share.limit_bytes))}"></label>
      <label class="settings-field"><span>可用端口</span><input name="ports" value="${esc(share.ports_text ?? (share.ports || []).join(", "))}" placeholder="21001, 21002" autocomplete="off"></label>
    </div>`;
  }

  function renderUsers(items, agents, user, access) {
    const editable = user?.role !== "admin";
    const data = state.data;
    data.userDrafts ||= new Map();
    data.userAccessSaves ||= new Map();
    if (!editable) data.userDrafts.delete(user?.id);
    const draft = data.userDrafts.get(user?.id);
    const latestShares = access?.shares || [];
    if (draft) access = draft.access; // Retain the revision the edit was based on.
    const shares = draft ? draft.values.rows.map((row) => ({ ...latestShares.find((share) => share.agent_id === row.agent_id), ...row })) : latestShares;
    const ownedIDs = new Set(access?.owned_agent_ids || []);
    const ownedAgents = agents.filter((agent) => ownedIDs.has(agent.id));
    const available = agents.filter((agent) => !ownedIDs.has(agent.id) && !shares.some((share) => share.agent_id === agent.id));
    shell(`<div class="settings-workspace users-workspace">
      <header class="users-toolbar"><h2>${user ? esc(user.display_name || user.username) : "用户"}</h2><div>${user ? '<button class="button small" type="button" data-user-edit>编辑账号</button>' : ""}<button class="button primary small" type="button" data-user-create>新增用户</button></div></header>
      ${items.length ? `<select class="users-mobile-select" data-user-mobile-select aria-label="选择用户">${items.map((item) => `<option value="${esc(item.id)}" ${item.id === user?.id ? "selected" : ""}>${esc(item.display_name || item.username)} · ${esc(item.username)}</option>`).join("")}</select>` : ""}
      ${user ? `<form class="settings-form" data-user-form data-user-access-form>
        <section class="settings-section">
          <header><span class="settings-section-number">01</span><div><h3>Agent 分配</h3><p>${esc(user.username)} · ${user.disabled ? "已停用" : user.role === "admin" ? "管理员" : "普通用户"}</p></div></header>
          ${editable ? `<div class="settings-hint user-isolation"><b>账号资源始终独立</b><p>可自行添加 Agent，并使用明确共享的节点。自有节点 ${ownedAgents.length} 个${ownedAgents.length ? `：${ownedAgents.map((agent) => esc(agent.name)).join("、")}` : ""}。</p></div>
            <div data-share-editor>
              <div data-share-rows>${shares.map((share) => shareRow(share, agents)).join("")}</div>
              <div class="user-share-add"><select data-share-agent aria-label="选择 Agent"><option value="">选择 Agent</option>${available.map((agent) => `<option value="${esc(agent.id)}">${esc(agent.name)}</option>`).join("")}</select><button class="button small" type="button" data-share-add>添加分配</button></div>
              <p class="settings-hint">累计额度，0 不限量；关闭勾选即撤销访问。1 GiB = 1024³ 字节。</p>
            </div>
          ` : '<p class="settings-hint">管理员可访问所有 Agent。</p>'}
        </section>
        ${editable ? '<div class="alert error" data-user-error role="alert" hidden></div><footer class="settings-savebar"><button class="button" type="button" data-user-reload>重新读取</button><button class="button primary" type="submit">保存分配</button></footer>' : ""}
      </form>` : '<div class="empty large"><strong>尚无用户</strong></div>'}
      <dialog class="traffic-edit-dialog user-edit-dialog" data-user-dialog aria-labelledby="user-dialog-title"></dialog>
    </div>`, "用户", { viewKey: `users-${user?.id || "new"}-${++viewSerial}` });

    const selectUser = async (id) => {
      if (id === state.data.userID) return;
      captureDraft();
      state.data.userID = id;
      const previousForm = document.querySelector("[data-user-access-form]");
      if (previousForm) { previousForm.inert = true; previousForm.setAttribute("aria-busy", "true"); }
      const loading = users(), selectionRequest = serial;
      try { await loading; } catch (error) {
        if (!current(selectionRequest, data, "users")) return;
        data.userID = user?.id || "";
        const select = document.querySelector("[data-user-mobile-select]");
        if (select) select.value = data.userID;
        if (error.name !== "AbortError") notify(error.message, "error");
      } finally {
        if (previousForm?.isConnected && current(selectionRequest, data, "users")) {
          previousForm.inert = false;
          previousForm.removeAttribute("aria-busy");
        }
      }
    };
    document.querySelectorAll("[data-user-select]").forEach((link) => bindEvent(link, "click", (event) => {
      event.preventDefault();
      void selectUser(link.dataset.userSelect);
    }));
    bindEvent(document.querySelector("[data-user-mobile-select]"), "change", (event) => { void selectUser(event.target.value); });
    bindEvent(document.querySelector("[data-user-create]"), "click", () => editUser(null));
    bindEvent(document.querySelector("[data-user-edit]"), "click", () => editUser(user));
    const form = document.querySelector("[data-user-access-form]");
    captureActive = () => {};
    if (!form || !editable) return;
    const baseline = draft?.baseline || JSON.stringify(formValues(form));
    const capture = () => {
      if (!form.isConnected || data !== state.data) return;
      const values = formValues(form);
      const serialized = JSON.stringify(values);
      const previous = data.userDrafts.get(user.id);
      if (serialized === baseline) data.userDrafts.delete(user.id);
      else if (previous?.access.revision !== access.revision || previous.baseline !== baseline || JSON.stringify(previous.values) !== serialized)
        data.userDrafts.set(user.id, { access, values, baseline });
    };
    captureActive = capture;
    if (data.userAccessSaves.has(user.id)) lockForm(form, data.userAccessSaves.get(user.id));
    bindEvent(form, "input", capture);
    bindEvent(form, "change", capture);
    bindEvent(form.querySelector("[data-share-add]"), "click", () => {
      const select = form.querySelector("[data-share-agent]");
      if (!select.value) return;
      form.querySelector("[data-share-rows]").insertAdjacentHTML("beforeend", shareRow({ agent_id: select.value, enabled: true, ports: [] }, agents));
      select.selectedOptions[0].remove();
      select.value = "";
      capture();
    });
    bindEvent(form.querySelector("[data-user-reload]"), "click", async () => {
      if (await confirmAction("重新读取会丢弃未保存的分配。", "重新读取")) {
        data.userDrafts.delete(user.id);
        captureActive = () => {};
        try { await users(); } catch (error) { captureActive = capture; capture(); report(error, form); }
      }
    });
    bindEvent(form, "submit", async (event) => {
      event.preventDefault();
      const button = form.querySelector('button[type="submit"]');
      if (button.disabled || form.inert || data.userAccessSaves.has(user.id) || data !== state.data || state.route !== "users" || data.userID !== user.id) return;
      let saving;
      try {
        if (data.userAccessSaves.has(user.id) || !form.isConnected || data !== state.data) return;
        const allocations = [...form.querySelectorAll("[data-share-row]")].map((row) => ({
          agent_id: row.dataset.shareRow,
          enabled: row.querySelector('[name="enabled"]').checked,
          ports: parseSharedPorts(row.querySelector('[name="ports"]').value),
          limit_bytes: sharedLimitBytes(row.querySelector('[name="limit_gib"]').value),
        }));
        capture();
        const submitted = data.userDrafts.get(user.id);
        saving = { controls: [] };
        data.userAccessSaves.set(user.id, saving);
        lockForm(form, saving);
        const saved = await api(`/users/${encodeURIComponent(user.id)}/agent-access`, {
          method: "PUT", body: JSON.stringify({ isolated: true, shares: allocations, revision: access.revision }),
        });
        if (data !== state.data) return;
        if (data.userDrafts.get(user.id) === submitted) data.userDrafts.delete(user.id);
        data.userAccessSaves.delete(user.id);
        if (state.route === "users" && data.userID === user.id) {
          ++serial;
          const latestItems = data.users || items;
          renderUsers(latestItems, agents, latestItems.find((item) => item.id === user.id) || user, saved);
        }
        notify("分配已保存");
      } catch (error) {
        if (data !== state.data || error.name === "AbortError") return;
        const activeForm = state.route === "users" && data.userID === user.id && document.querySelector("[data-user-access-form]");
        if (activeForm) report(error, activeForm);
        else notify(error.message, "error");
      } finally {
        if (data.userAccessSaves.get(user.id) === saving) data.userAccessSaves.delete(user.id);
        saving?.controls.forEach(([control, disabled]) => { control.disabled = disabled; });
      }
    });
  }

  function editUser(user) {
    const dialog = document.querySelector("[data-user-dialog]");
    const selected = new Set(user ? user.permissions || [] : defaultPermissions);
    dialog.innerHTML = `<header><span class="traffic-edit-icon" aria-hidden="true">＋</span><div><h2 id="user-dialog-title">${user ? "编辑账号" : "新增用户"}</h2></div><button class="deploy-command-close" type="button" data-user-close aria-label="关闭">×</button></header>
      <form data-user-form><div class="traffic-edit-body">
        <label>用户名<input name="username" required maxlength="64" autocomplete="off" value="${esc(user?.username || "")}" ${user ? "disabled" : ""}></label>
        <label>显示名称<input name="display_name" maxlength="100" value="${esc(user?.display_name || "")}"></label>
        <label>${user ? "新密码（留空不改）" : "密码"}<input name="password" type="password" autocomplete="new-password" minlength="12" maxlength="72" ${user ? "" : "required"}></label>
        <label>角色<select name="role"><option value="user" ${user?.role !== "admin" ? "selected" : ""}>普通用户</option><option value="admin" ${user?.role === "admin" ? "selected" : ""}>管理员</option></select></label>
        ${user ? `<label class="settings-toggle"><span><b>启用账号</b></span><input name="enabled" type="checkbox" ${user.disabled ? "" : "checked"} ${user.id === state.session.user_id ? "disabled" : ""}></label>` : '<p class="settings-hint" data-user-default-isolation>普通用户资源始终独立，可自行添加 Agent，或使用获共享的 Agent。</p>'}
        <details class="user-permissions" ${user?.role === "admin" ? "hidden" : ""}><summary>操作权限</summary><div>${userPermissions.map(([key, label]) => `<label><input type="checkbox" name="permission" value="${key}" ${selected.has(key) ? "checked" : ""}>${label}</label>`).join("")}</div><p class="settings-hint">主机管理、接入凭据和日志仅限自有 Agent；借用节点只能按分配端口和额度部署个人配置。</p></details>
        <div class="alert error" data-user-error role="alert" hidden></div>
      </div><footer><button class="button" type="button" data-user-close>取消</button><button class="button primary" type="submit">保存账号</button></footer></form>`;
    dialog.querySelectorAll("[data-user-close]").forEach((button) => bindEvent(button, "click", () => dialog.close()));
    const form = dialog.querySelector("form");
    bindEvent(form.elements.role, "change", () => {
      dialog.querySelector(".user-permissions").hidden = form.elements.role.value === "admin";
      const hint = dialog.querySelector("[data-user-default-isolation]");
      if (hint) hint.hidden = form.elements.role.value === "admin";
    });
    bindEvent(form, "submit", async (event) => {
      event.preventDefault();
      const button = form.querySelector('button[type="submit"]');
      if (button.disabled) return;
      const data = new FormData(form);
      const body = { display_name: data.get("display_name"), role: data.get("role"), permissions: data.getAll("permission") };
      if (data.get("password")) body.password = data.get("password");
      if (user) body.disabled = user.id === state.session.user_id ? user.disabled : !data.has("enabled");
      else Object.assign(body, { username: data.get("username"), agent_isolation: body.role !== "admin" });
      button.disabled = true;
      try {
        const saved = await api(user ? `/users/${encodeURIComponent(user.id)}` : "/users", { method: user ? "PUT" : "POST", body: JSON.stringify(body) });
        if (!dialog.isConnected || state.route !== "users") return;
        dialog.close();
        state.data.userID = saved.id;
        await users();
        notify("账号已保存");
      } catch (error) { report(error, form); }
      finally { button.disabled = false; }
    });
    dialog.showModal();
  }

  async function myQuota() {
    const request = ++serial, data = state.data;
    const access = await api("/agent-access");
    if (!current(request, data, "my-quota")) return;
    data.agentAccess = access;
    shell(`<div class="settings-workspace users-workspace">
      <header class="users-toolbar"><h2>我的额度</h2><button class="button small" type="button" data-quota-refresh>刷新</button></header>
      ${access.isolated ? `<section class="user-quota-grid">${(access.shares || []).map((share) => {
        const exhausted = share.limit_bytes > 0 && share.used_bytes >= share.limit_bytes;
        const status = !share.enabled ? "已撤销" : exhausted ? "额度已用完" : "可用";
        return `<article class="workspace-panel user-quota-card"><header><h3>${esc(share.agent_name)}</h3><span class="status-label ${!share.enabled || exhausted ? "warn" : "ok"}">${status}</span></header><div><strong>${usage(share.used_bytes)}</strong><span> / ${share.limit_bytes ? usage(share.limit_bytes) : "不限量"}</span>${share.limit_bytes ? `<progress max="100" value="${Math.min(100, share.used_bytes / share.limit_bytes * 100)}" aria-label="已用额度"></progress>` : ""}<small>端口 ${(share.ports || []).join(", ") || "未分配"}</small></div></article>`;
      }).join("") || '<div class="empty large"><strong>暂无 Agent 分配</strong></div>'}</section><p class="settings-hint">监听端口上传 + 下载累计计费，不按月清零。</p>` : '<section class="workspace-panel"><div class="empty compact"><strong>未启用 Agent 隔离</strong><p>按账号权限访问节点。</p></div></section>'}
    </div>`, "我的额度");
    bindEvent(document.querySelector("[data-quota-refresh]"), "click", async (event) => {
      const button = event.currentTarget;
      button.disabled = true;
      try { await myQuota(); } catch (error) { if (error.name !== "AbortError") notify(error.message, "error"); }
      finally { button.disabled = false; }
    });
  }
  return { users, myQuota, captureDraft, hasUnsavedChanges };
}
