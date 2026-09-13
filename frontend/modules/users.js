import { bindEvent } from "./refresh.js";
import { sharedEngineChoices, sharedEngineNames } from "./engine-capabilities.js";

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
  const parts = String(value ?? "").trim().replace(/(\d)\s*-\s*(?=\d)/g, "$1-").split(/[\s,，]+/).filter(Boolean);
  if (parts.length === 1 && parts[0] === "0") return [0];
  if (parts.includes("0")) throw new Error("0 表示端口无限制，不能与其他端口或范围混用。");
  const ports = new Set();
  for (const part of parts) {
    const match = /^(\d+)(?:-(\d+))?$/.exec(part);
    if (!match) throw new Error("请填写单个端口或范围，如 21000-21100，多个用逗号分隔。");
    const start = Number(match[1]), end = Number(match[2] ?? match[1]);
    if (!Number.isInteger(start) || !Number.isInteger(end) || start < 1 || end > 65535 || start > end)
      throw new Error("端口须为 1–65535，范围起始端口不能大于结束端口。");
    if (start <= 10086 && end >= 10085) throw new Error("10085、10086 是保留端口，不能分配。");
    // Bound expansion before allocating or iterating through the range.
    if (ports.size + end - start + 1 > 256) throw new Error("最多分配 256 个端口，范围按实际端口数量计算。");
    for (let port = start; port <= end; port++) {
      if (ports.has(port)) throw new Error("端口不能重复，范围不能重叠。");
      ports.add(port);
    }
  }
  return [...ports].sort((left, right) => left - right);
}

export function formatSharedPorts(ports) {
  const sorted = [...(ports || [])].sort((left, right) => left - right);
  const ranges = [];
  for (let index = 0; index < sorted.length; index++) {
    const start = sorted[index];
    let end = start;
    while (sorted[index + 1] === end + 1) end = sorted[++index];
    ranges.push(start === end ? String(start) : `${start}-${end}`);
  }
  return ranges.join(", ");
}

export function sharedPortsLabel(ports) {
  return ports?.length === 1 && ports[0] === 0 ? "无限制" : formatSharedPorts(ports) || "未分配";
}

export function selectedSharedEngines(row) {
  const engines = [...row.querySelectorAll('[name="engines"]:checked')].map(input => input.value);
  if (row.querySelector('[name="enabled"]').checked && !engines.length)
    throw new Error("请至少分配一个内核。");
  return engines;
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

export function agentShareStatus(share) {
  if (share.reinvite || (!share.id && !share.user_id)) return "待发送";
  if (!share.enabled) return "已撤销";
  return { pending: "待接受", accepted: "已接受", rejected: "已拒绝" }[share.status] || "待接受";
}

export function mergeUserAllocation(shares, allocation) {
  // The API replaces the full allocation set. Keep every untouched share,
  // including revoked reservations, without replaying invitation actions.
  const rows = (shares || []).map(share => ({
    agent_id: share.agent_id, enabled: share.enabled,
    engines: [...(share.engines || [])], ports: [...(share.ports || [])],
    limit_bytes: share.limit_bytes || 0,
  }));
  const index = rows.findIndex(row => row.agent_id === allocation.agent_id);
  if (index < 0) rows.push(allocation);
  else rows[index] = { ...rows[index], ...allocation };
  return rows;
}

export function installUsers(ctx) {
  const { api, state, esc, shell, notify, confirmAction } = ctx;
  let serial = 0, viewSerial = 0, captureActive = () => {};
  let activeInvitation = null;
  let activeAllocation = null;
  const captureDraft = () => captureActive();
  const hasUnsavedChanges = () => Boolean(state.data.userDrafts?.size || state.data.userAccessSaves?.size || state.data.userDeletions?.size || state.data.agentShareResponse);
  const formValues = (form) => ({
    isolated: true,
    rows: [...form.querySelectorAll("[data-share-row]")].map((row) => ({
      agent_id: row.dataset.shareRow,
      enabled: row.querySelector('[name="enabled"]').checked,
      engines: [...row.querySelectorAll('[name="engines"]:checked')].map(input => input.value),
      limit_gib: row.querySelector('[name="limit_gib"]').value,
      unlimited: row.querySelector('[name="quota_mode"]:checked').value === "unlimited",
      ports_text: row.querySelector('[name="ports"]').value,
      reinvite: row.dataset.reinvite === "true",
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
    const name = agent?.name || share.agent_name || share.agent_id;
    const supported = ["shared-traffic-v1", "shared-engines-v1", "independent-egress-v1"].every(feature => agent?.features?.includes(feature));
    const statusClass = !share.enabled ? "muted" : share.status === "accepted" ? "ok" : "warn";
    return `<article class="user-allocation-row" data-allocation-agent="${esc(share.agent_id)}">
      <div class="user-allocation-node"><strong>${esc(name)}</strong><span class="status-label ${statusClass}" data-share-status>${agentShareStatus(share)}</span>${supported ? "" : `<small>${agent ? "Agent 需升级" : "节点不可用"}</small>`}</div>
      <dl class="user-allocation-terms"><div><dt>内核</dt><dd>${sharedEngineNames(share.engines)}</dd></div><div><dt>端口</dt><dd>${esc(sharedPortsLabel(share.ports))}</dd></div><div><dt>总额度</dt><dd>${share.limit_bytes ? usage(share.limit_bytes) : "不限量"}<small>已用 ${usage(share.used_bytes)}</small></dd></div></dl>
      <div class="user-allocation-row-actions"><button class="button small" type="button" data-allocation-edit="${esc(share.agent_id)}" aria-label="编辑 ${esc(name)} 的分配">编辑</button>${!share.enabled || share.status === "rejected" ? `<button class="button small" type="button" data-allocation-invite="${esc(share.agent_id)}" aria-label="重新邀请使用 ${esc(name)}">重新邀请</button>` : ""}${share.enabled ? `<button class="button small danger-button" type="button" data-allocation-revoke="${esc(share.agent_id)}" aria-label="撤销 ${esc(name)} 的共享">撤销</button>` : ""}</div>
    </article>`;
  }

  function renderUsers(items, agents, user, access) {
    const editable = user?.role !== "admin";
    const data = state.data;
    data.userDrafts ||= new Map();
    data.userAccessSaves ||= new Map();
    data.userDeletions ||= new Set();
    if (!editable) data.userDrafts.delete(user?.id);
    const draft = data.userDrafts.get(user?.id);
    const shares = access?.shares || [];
    const ownedIDs = new Set(access?.owned_agent_ids || []);
    const ownedAgents = agents.filter((agent) => ownedIDs.has(agent.id));
    const available = agents.filter((agent) => !ownedIDs.has(agent.id) && !shares.some((share) => share.agent_id === agent.id));
    activeAllocation?.dialog.close();
    activeAllocation = null;
    captureActive = () => {};
    shell(`<div class="settings-workspace users-workspace user-admin-workspace">
      <header class="users-toolbar"><div class="users-title"><h2>${user ? esc(user.display_name || user.username) : "用户"}</h2>${user ? `<span class="user-account-state">${user.disabled ? "已停用" : user.role === "admin" ? "管理员" : "普通用户"}</span>` : ""}</div><div class="users-toolbar-actions">${user ? '<button class="button small" type="button" data-user-edit>编辑账号</button>' : ""}${user && editable && user.id !== state.session?.user_id ? `<button class="button small danger-button" type="button" data-user-delete ${data.userDeletions.has(user.id) ? "disabled" : ""}>删除账号</button>` : ""}<button class="button primary small" type="button" data-user-create>新增用户</button></div></header>
      ${items.length ? `<select class="users-mobile-select" data-user-mobile-select aria-label="选择用户">${items.map((item) => `<option value="${esc(item.id)}" ${item.id === user?.id ? "selected" : ""}>${esc(item.display_name && item.display_name !== item.username ? `${item.display_name} · ${item.username}` : item.username)}</option>`).join("")}</select>` : ""}
      ${user ? `<section class="settings-section user-allocation-panel" data-user-allocations aria-labelledby="user-allocation-title">
        <header class="user-allocation-head"><div class="user-allocation-title"><h3 id="user-allocation-title">共享节点</h3><span class="user-allocation-count" data-share-count>${shares.length}</span></div><div class="user-allocation-tools"><button class="button small" type="button" data-user-reload>刷新</button>${editable ? `<button class="button primary small" type="button" data-allocation-add ${available.length ? "" : 'disabled title="没有可分配的节点"'}>分配节点</button>` : ""}</div></header>
        ${editable ? `<div data-allocation-list>${shares.map((share) => shareRow(share, agents)).join("") || '<p class="user-share-empty" data-share-empty>尚未分配共享节点</p>'}</div>` : '<p class="settings-hint user-allocation-readonly">管理员按自身权限访问节点，无需分配。</p>'}
        <div class="alert error user-allocation-error" data-user-error role="alert" hidden></div>
      </section>${ownedAgents.length ? `<details class="user-owned-nodes"><summary><span>自有节点 <small>${ownedAgents.length}</small></span><svg viewBox="0 0 24 24" aria-hidden="true"><path d="m6 9 6 6 6-6"/></svg></summary><ul>${ownedAgents.map(agent => `<li>${esc(agent.name)}</li>`).join("")}</ul></details>` : ""}` : '<div class="empty large"><strong>尚无用户</strong></div>'}
      <dialog class="traffic-edit-dialog user-allocation-dialog" data-allocation-dialog aria-labelledby="user-allocation-dialog-title"></dialog>
      <dialog class="traffic-edit-dialog user-edit-dialog" data-user-dialog aria-labelledby="user-dialog-title"></dialog>
    </div>`, "用户", { viewKey: `users-${user?.id || "new"}-${++viewSerial}` });

    const selectUser = async (id) => {
      if (id === state.data.userID) return;
      captureDraft();
      state.data.userID = id;
      const previousForm = document.querySelector("[data-user-access-form]") || document.querySelector("[data-user-allocations]");
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
    bindEvent(document.querySelector("[data-user-delete]"), "click", async (event) => {
      const button = event.currentTarget;
      const label = user.display_name || user.username;
      const currentUser = () => data === state.data && state.route === "users" && data.userID === user.id && button.isConnected;
      if (button.disabled || !currentUser() || data.userDeletions.has(user.id) || data.userAccessSaves.has(user.id)) return;
      data.userDeletions.add(user.id);
      button.disabled = true;
      try {
        if (!await confirmAction(`删除账号“${label}”后无法恢复：节点（含隐藏节点）、配置与模板将移交给管理员；旧安装凭据失效，未完成任务作废，共享与个人设置删除，流量历史保留。`, "继续删除")) return;
        if (!currentUser()) return;
        if (!await confirmAction(`再次确认删除账号“${label}”（${user.username}）？`, "永久删除")) return;
        if (!currentUser()) return;
        await api(`/users/${encodeURIComponent(user.id)}/purge`, { method: "POST" });
        if (data !== state.data) return;
        data.userDrafts?.delete(user.id);
        data.userAccessSaves?.delete(user.id);
        data.users = data.users?.filter((item) => item.id !== user.id);
        if (state.route === "users" && data.userID === user.id) {
          // users() captures the visible form before refreshing. It must
          // not resurrect an unsaved draft for the just-deleted account.
          captureActive = () => {};
          data.userID = "";
          await users();
        }
        notify(`账号“${label}”已删除`);
      } catch (error) {
        if (error.name !== "AbortError") notify(error.message, "error");
      } finally {
        data.userDeletions.delete(user.id);
        button.disabled = false;
        if (data === state.data && state.route === "users" && data.userID === user.id) {
          const activeButton = document.querySelector("[data-user-delete]");
          if (activeButton) activeButton.disabled = false;
        }
      }
    });
    const scope = () => data === state.data && state.route === "users" && data.userID === user?.id;
    const busy = () => !scope() || document.querySelector("[data-user-allocations]")?.inert || data.userAccessSaves.has(user.id) || data.userDeletions.has(user.id);
    bindEvent(document.querySelector("[data-user-reload]"), "click", async (event) => {
      const button = event.currentTarget;
      if (busy() || button.disabled) return;
      const panel = document.querySelector("[data-user-allocations]");
      button.disabled = true;
      panel.inert = true;
      panel.setAttribute("aria-busy", "true");
      try { await users(); } catch (error) { if (scope()) report(error, panel); }
      finally {
        button.disabled = false;
        panel.inert = false;
        panel.removeAttribute("aria-busy");
      }
    });
    if (!user || !editable) return;
    bindEvent(document.querySelector("[data-allocation-add]"), "click", (event) => {
      if (!busy()) openAllocation(user, agents, access, { mode: "add", trigger: event.currentTarget });
    });
    document.querySelectorAll("[data-allocation-edit], [data-allocation-invite]").forEach(button => bindEvent(button, "click", () => {
      if (!busy()) openAllocation(user, agents, access, {
        mode: button.hasAttribute("data-allocation-invite") ? "invite" : "edit",
        agentID: button.dataset.allocationEdit || button.dataset.allocationInvite, trigger: button,
      });
    }));
    document.querySelectorAll("[data-allocation-revoke]").forEach(button => bindEvent(button, "click", () => {
      if (busy()) return;
      const share = shares.find(item => item.agent_id === button.dataset.allocationRevoke);
      const name = agents.find(agent => agent.id === share.agent_id)?.name || share.agent_name || share.agent_id;
      void saveAllocation(user, agents, access, { agent_id: share.agent_id, enabled: false }, {
        data, trigger: button, notice: "共享已撤销",
        confirmation: `撤销“${name}”对“${user.display_name || user.username}”的共享？已用流量和端口预留会保留。`,
      });
    }));
    if (draft) openAllocation(user, agents, access, { mode: draft.mode, draft });
    const pending = data.userAccessSaves.get(user.id);
    if (pending) lockAllocationControls(data, user.id, pending);
  }

  function lockAllocationControls(data, userID, saving) {
    if (data !== state.data || state.route !== "users" || data.userID !== userID) return;
    const dialog = document.querySelector("[data-allocation-dialog]");
    const controls = [...document.querySelectorAll("[data-allocation-add], [data-allocation-edit], [data-allocation-invite], [data-allocation-revoke], [data-user-reload], [data-user-delete], [data-user-edit]"), ...(dialog?.querySelector("form")?.elements || []), ...(dialog?.querySelectorAll("[data-allocation-close]") || [])];
    for (const control of controls) {
      if (!saving.controls.has(control)) saving.controls.set(control, control.disabled);
      control.disabled = true;
    }
  }

  function closeAllocation() {
    if (!activeAllocation) return;
    const previous = activeAllocation;
    if (previous.data.userAccessSaves.has(previous.userID)) return;
    previous.data.userDrafts.delete(previous.userID);
    activeAllocation = null;
    captureActive = () => {};
    previous.dialog.close();
    previous.dialog.innerHTML = "";
    if (previous.trigger?.isConnected) previous.trigger.focus();
  }

  function allocationFields(share, agent) {
    const limit = share.limit_gib ?? sharedLimitGiB(share.limit_bytes);
    const unlimited = share.unlimited ?? Number(limit) === 0;
    return `${sharedEngineChoices(share.engines, agent?.supported_capabilities ?? agent?.capabilities)}
      <label class="settings-field"><span>可用端口</span><input name="ports" value="${esc(share.ports_text ?? formatSharedPorts(share.ports))}" placeholder="21000-21100, 22000" autocomplete="off"><small>留空未分配，0 无限制；最多指定 256 个端口</small></label>
      <fieldset class="user-allocation-quota"><legend>流量额度</legend><div class="user-quota-options"><label><input type="radio" name="quota_mode" value="unlimited" ${unlimited ? "checked" : ""}>不限量</label><label><input type="radio" name="quota_mode" value="limited" ${unlimited ? "" : "checked"}>设置总额度</label></div><label class="settings-field" data-quota-amount ${unlimited ? "hidden" : ""}><span>累计总额度（GiB）</span><input name="limit_gib" type="number" min="0" max="8388607" step="any" required value="${esc(limit)}" placeholder="例如 100" ${unlimited ? "disabled" : ""}></label><small>已用 ${usage(share.used_bytes)}</small></fieldset>`;
  }

  function openAllocation(user, agents, latest, options) {
    const data = state.data, draft = options.draft;
    const access = draft?.access || latest;
    let mode = options.mode || "edit";
    const value = draft?.values.rows[0];
    let agentID = value?.agent_id || options.agentID || "";
    const shares = access.shares || [];
    const available = agents.filter(item => !(access.owned_agent_ids || []).includes(item.id) && !shares.some(row => row.agent_id === item.id));
    if (mode === "add" && !available.some(item => item.id === agentID)) agentID = "";
    const original = shares.find(share => share.agent_id === agentID);
    if (mode !== "add" && !original) return;
    if (mode === "invite" && original.enabled && original.status !== "rejected") mode = "edit";
    const share = { enabled: true, ...original, ...value };
    if (mode === "invite") { share.enabled = true; share.reinvite = original.status === "rejected"; }
    const agent = agents.find(item => item.id === agentID);
    const dialog = document.querySelector("[data-allocation-dialog]");
    const title = mode === "add" ? "分配节点" : mode === "invite" ? "重新邀请" : "编辑分配";
    dialog.innerHTML = `<header><div><h2 id="user-allocation-dialog-title">${title}</h2><p>接收用户 · ${esc(user.display_name || user.username)}${user.display_name && user.display_name !== user.username ? `（${esc(user.username)}）` : ""}</p></div><button type="button" class="deploy-command-close" data-allocation-close aria-label="关闭分配弹窗">×</button></header>
      <form data-user-access-form data-user-form><div class="traffic-edit-body" data-share-row="${esc(agentID)}" data-reinvite="${Boolean(share.reinvite)}">
        <input type="checkbox" name="enabled" hidden ${share.enabled ? "checked" : ""}>
        ${mode === "add" ? `<label class="settings-field"><span>节点</span><select name="agent_id" data-share-agent required autofocus><option value="">选择节点</option>${available.map(item => `<option value="${esc(item.id)}" ${item.id === agentID ? "selected" : ""}>${esc(item.name)}</option>`).join("")}</select></label>` : `<div class="user-allocation-target"><span>节点</span><div><strong>${esc(agent?.name || original?.agent_name || agentID)}</strong><small>${agentShareStatus(original)}</small></div></div>`}
        <fieldset class="user-allocation-fields" data-allocation-fields ${agentID ? "" : "hidden disabled"}>${allocationFields(share, agent)}</fieldset>
        <p class="user-allocation-consent" data-allocation-consent></p>
        <div class="user-allocation-failure" hidden><p class="alert error" data-user-error role="alert" hidden></p><button type="button" class="button small" data-allocation-reload>重新读取</button></div>
      </div><footer><button class="button" type="button" data-allocation-close>取消</button><button class="button primary" type="submit" ${agentID ? "" : "disabled"}>${mode === "edit" ? "保存修改" : "发送邀请"}</button></footer></form>`;
    const form = dialog.querySelector("form");
    const initial = formValues(form);
    if (mode === "invite") {
      // Restoring a revoked share changes enabled even without a reinvite flag.
      initial.rows[0].enabled = original.enabled;
      initial.rows[0].reinvite = false;
    }
    const baseline = draft?.baseline || JSON.stringify(initial);
    const capture = () => {
      if (!form.isConnected || data !== state.data) return;
      const values = formValues(form), serialized = JSON.stringify(values);
      const previous = data.userDrafts.get(user.id);
      if (serialized === baseline) data.userDrafts.delete(user.id);
      else if (previous?.access.revision !== access.revision || previous.baseline !== baseline || JSON.stringify(previous.values) !== serialized)
        data.userDrafts.set(user.id, { access, values, baseline, mode });
    };
    const editor = { dialog, userID: user.id, data, trigger: options.trigger, reloading: false };
    const isCurrent = () => activeAllocation === editor && data === state.data && state.route === "users" && data.userID === user.id;
    const updateConsent = () => {
      const message = form.querySelector("[data-allocation-consent]");
      const addedEngines = [...form.querySelectorAll('[name="engines"]:checked')].some(input => !original?.engines?.includes(input.value));
      message.textContent = mode !== "edit" ? "用户接受邀请后才能使用节点。"
        : !original.enabled ? "保存修改不会恢复共享。"
        : original.status === "rejected" ? "保存修改不会重新发送邀请。"
        : original.status === "accepted" && addedEngines ? "增加内核后，用户需重新接受。"
        : "";
      message.hidden = !message.textContent || !form.querySelector("[data-share-row]").dataset.shareRow;
    };
    activeAllocation = editor;
    captureActive = capture;
    if (!dialog.open) dialog.showModal();
    updateConsent();
    capture();
    bindEvent(form, "input", capture);
    bindEvent(form, "change", (event) => {
      if (event.target.matches("[data-share-agent]")) {
        const id = event.target.value;
        form.querySelector("[data-share-row]").dataset.shareRow = id;
        const fields = form.querySelector("[data-allocation-fields]");
        fields.innerHTML = allocationFields({}, agents.find(item => item.id === id));
        fields.disabled = !id;
        fields.hidden = !id;
        form.querySelector('[type="submit"]').disabled = !id;
      }
      if (event.target.name === "quota_mode") {
        const unlimited = event.target.value === "unlimited";
        form.querySelector("[data-quota-amount]").hidden = unlimited;
        form.elements.limit_gib.disabled = unlimited;
        if (!unlimited && Number(form.elements.limit_gib.value) === 0) form.elements.limit_gib.value = "";
      }
      updateConsent();
      capture();
    });
    dialog.querySelectorAll("[data-allocation-close]").forEach(button => bindEvent(button, "click", () => closeAllocation()));
    bindEvent(dialog, "cancel", event => { event.preventDefault(); closeAllocation(); });
    bindEvent(form.querySelector("[data-allocation-reload]"), "click", async () => {
      if (!isCurrent() || editor.reloading || data.userAccessSaves.has(user.id) || data.userDeletions.has(user.id)) return;
      editor.reloading = true;
      const controls = [...form.elements].filter(control => !control.hasAttribute("data-allocation-close")).map(control => [control, control.disabled]);
      controls.forEach(([control]) => { control.disabled = true; });
      try {
        if (!await confirmAction("重新读取会丢弃未保存的分配。", "重新读取") || !isCurrent()) return;
        const selectedID = formValues(form).rows[0].agent_id;
        const refreshed = await api(`/users/${encodeURIComponent(user.id)}/agent-access`);
        if (!isCurrent()) return;
        data.userDrafts.delete(user.id);
        ++serial;
        renderUsers(data.users, agents, user, refreshed);
        openAllocation(user, agents, refreshed, { mode, agentID: selectedID, trigger: document.querySelector(mode === "add" ? "[data-allocation-add]" : `[data-allocation-edit="${CSS.escape(selectedID)}"]`) });
      } catch (error) { if (isCurrent()) reportAllocationError(error, data, user.id); }
      finally {
        editor.reloading = false;
        controls.forEach(([control, disabled]) => { control.disabled = disabled; });
      }
    });
    bindEvent(form, "submit", async (event) => {
      event.preventDefault();
      if (!isCurrent() || editor.reloading || form.inert || data.userAccessSaves.has(user.id) || data.userDeletions.has(user.id)) return;
      try {
        const row = form.querySelector("[data-share-row]"), values = formValues(form).rows[0];
        if (!values.agent_id) throw new Error("请选择节点。");
        const limit = values.unlimited ? 0 : sharedLimitBytes(values.limit_gib);
        if (!values.unlimited && limit === 0) throw new Error("请输入大于 0 的总额度，或选择不限量。");
        const allocation = { agent_id: values.agent_id, enabled: values.enabled, engines: selectedSharedEngines(row), ports: parseSharedPorts(values.ports_text), limit_bytes: limit, reinvite: values.reinvite };
        capture();
        await saveAllocation(user, agents, access, allocation, { data, trigger: form, notice: mode === "edit" ? "分配已保存" : "共享邀请已发送" });
      } catch (error) { reportAllocationError(error, data, user.id); }
    });
    const pending = data.userAccessSaves.get(user.id);
    if (pending) lockAllocationControls(data, user.id, pending);
  }

  function reportAllocationError(error, data, userID) {
    if (error.name === "AbortError" || data !== state.data) return;
    if (activeAllocation?.data === data && activeAllocation.userID === userID && activeAllocation.dialog.isConnected) {
      activeAllocation.dialog.querySelector(".user-allocation-failure").hidden = false;
      report(error, activeAllocation.dialog);
    } else if (state.route === "users" && data.userID === userID) report(error, document.querySelector("[data-user-allocations]"));
    else notify(error.message, "error");
  }

  async function saveAllocation(user, agents, access, allocation, { data, trigger, notice, confirmation }) {
    const currentUser = () => data === state.data && state.route === "users" && data.userID === user.id && trigger.isConnected;
    if (!currentUser() || data.userAccessSaves.has(user.id) || data.userDeletions.has(user.id)) return;
    const submitted = data.userDrafts.get(user.id), saving = { controls: new Map() };
    data.userAccessSaves.set(user.id, saving);
    lockAllocationControls(data, user.id, saving);
    try {
      if (confirmation && (!await confirmAction(confirmation, "撤销共享") || !currentUser())) return;
      const saved = await api(`/users/${encodeURIComponent(user.id)}/agent-access`, {
        method: "PUT", body: JSON.stringify({ isolated: true, shares: mergeUserAllocation(access.shares, allocation), revision: access.revision }),
      });
      if (data !== state.data) return;
      if (data.userDrafts.get(user.id) === submitted) data.userDrafts.delete(user.id);
      data.userAccessSaves.delete(user.id);
      if (state.route === "users" && data.userID === user.id) {
        ++serial;
        captureActive = () => {};
        renderUsers(data.users, agents, data.users.find(item => item.id === user.id) || user, saved);
      }
      notify(notice);
    } catch (error) { reportAllocationError(error, data, user.id); }
    finally {
      if (data.userAccessSaves.get(user.id) === saving) data.userAccessSaves.delete(user.id);
      saving.controls.forEach((disabled, control) => { control.disabled = disabled; });
    }
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
        <details class="user-permissions" ${user?.role === "admin" ? "hidden" : ""}><summary>操作权限</summary><div>${userPermissions.map(([key, label]) => `<label><input type="checkbox" name="permission" value="${key}" ${selected.has(key) ? "checked" : ""}>${label}</label>`).join("")}</div><p class="settings-hint">共享节点仅限已分配内核、端口与额度。</p></details>
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
    renderQuota(access, data);
  }

  function closeInvitation() {
    if (!activeInvitation) return;
    const { dialog, trigger } = activeInvitation;
    activeInvitation = null;
    dialog.close();
    dialog.remove();
    if (trigger?.isConnected) trigger.focus();
  }

  const lockResponse = (saving) => {
    document.querySelectorAll("[data-share-decision], [data-share-open], [data-quota-refresh], [data-invitation-reload]").forEach((control) => {
      if (!saving.controls.has(control)) saving.controls.set(control, control.disabled);
      control.disabled = true;
    });
  };

  function openInvitation(share, trigger) {
    if (!share?.enabled || share.status !== "pending" || state.route !== "my-quota") return;
    closeInvitation();
    const data = state.data;
    const dialog = document.createElement("dialog");
    dialog.className = "traffic-edit-dialog agent-invitation-dialog";
    dialog.setAttribute("aria-labelledby", "agent-invitation-title");
    dialog.setAttribute("aria-describedby", "agent-invitation-origin");
    dialog.innerHTML = `<header><div><h2 id="agent-invitation-title">共享邀请</h2><p id="agent-invitation-origin">所有者 · ${esc(share.owner_username || "管理员")}</p></div><button type="button" class="deploy-command-close" data-invitation-close aria-label="关闭邀请">×</button></header>
      <div class="traffic-edit-body">
        <div class="agent-invitation-node"><span>Agent</span><h3>${esc(share.agent_name)}</h3></div>
        <dl class="agent-invitation-terms"><div><dt>内核</dt><dd>${sharedEngineNames(share.engines)}</dd></div><div><dt>端口</dt><dd>${esc(sharedPortsLabel(share.ports))}</dd></div><div><dt>总额度</dt><dd>${share.limit_bytes ? usage(share.limit_bytes) : "不限量"}<small>已用 ${usage(share.used_bytes)}</small></dd></div></dl>
        <div class="agent-invitation-error" data-invitation-error hidden><p class="alert error" role="alert"></p><button type="button" class="button small" data-invitation-reload>刷新邀请</button></div>
      </div><footer><button type="button" class="button" data-share-decision="reject">拒绝</button><button type="button" class="button primary" data-share-decision="accept" ${share.engines?.length ? "" : 'disabled title="等待所有者分配内核"'}>接受</button></footer>`;
    activeInvitation = { dialog, data, share, trigger };
    document.body.append(dialog);
    dialog.showModal();
    bindEvent(dialog.querySelector("[data-invitation-close]"), "click", closeInvitation);
    bindEvent(dialog, "cancel", (event) => { event.preventDefault(); closeInvitation(); });
    dialog.querySelectorAll("[data-share-decision]").forEach((button) => bindEvent(button, "click", () => {
      if (activeInvitation?.dialog !== dialog || data !== state.data) return;
      void respondToShare(share, button.dataset.shareDecision, data);
    }));
    bindEvent(dialog.querySelector("[data-invitation-reload]"), "click", async (event) => {
      if (activeInvitation?.dialog !== dialog || data !== state.data || data.agentShareResponse || activeInvitation.loading) return;
      const button = event.currentTarget, previous = activeInvitation;
      previous.loading = true;
      button.disabled = true;
      const decisions = [...dialog.querySelectorAll("[data-share-decision]")].map(control => [control, control.disabled]);
      decisions.forEach(([control]) => { control.disabled = true; });
      try {
        const latest = await api("/agent-access");
        if (activeInvitation !== previous || data !== state.data || state.route !== "my-quota") return;
        ++serial;
        renderQuota(latest, data);
      } catch (error) {
        if (activeInvitation === previous && data === state.data && error.name !== "AbortError")
          dialog.querySelector("[role=alert]").textContent = error.message;
      } finally {
        previous.loading = false;
        button.disabled = false;
        decisions.forEach(([control, disabled]) => { control.disabled = disabled; });
      }
    });
    if (data.agentShareResponse) lockResponse(data.agentShareResponse);
  }

  async function respondToShare(share, decision, data) {
    if (data !== state.data || data.agentShareResponse || state.route !== "my-quota" || activeInvitation?.loading) return;
    const saving = { controls: new Map() };
    data.agentShareResponse = saving;
    lockResponse(saving);
    try {
      const saved = await api(`/agent-access/${encodeURIComponent(share.id)}/response`, {
        method: "POST", body: JSON.stringify({ revision: share.invitation_revision, decision }),
      });
      if (data !== state.data) return;
      delete data.agentShareResponse;
      if (activeInvitation?.data === data && activeInvitation.share.id === share.id) closeInvitation();
      if (state.route === "my-quota") {
        ++serial; // Discard a read begun before the response committed.
        renderQuota(saved, data);
      }
      notify(decision === "accept" ? "已接受共享" : share.status === "accepted" ? "已退出共享" : "已拒绝共享");
    } catch (error) {
      if (data !== state.data || error.name === "AbortError") return;
      if (activeInvitation?.data === data && activeInvitation.share.id === share.id) {
        const output = activeInvitation.dialog.querySelector("[data-invitation-error]");
        output.hidden = false;
        output.querySelector("[role=alert]").textContent = error.message;
      } else {
        const output = state.route === "my-quota" && document.querySelector("[data-quota-error]");
        if (output) { output.textContent = error.message; output.hidden = false; }
        else notify(error.message, "error");
      }
    } finally {
      if (data.agentShareResponse === saving) delete data.agentShareResponse;
      saving.controls.forEach((disabled, control) => { control.disabled = disabled; });
    }
  }

  function renderQuota(access, data) {
    const openedID = activeInvitation?.data === data ? activeInvitation.share.id : null;
    closeInvitation();
    data.agentAccess = access;
    shell(`<div class="settings-workspace users-workspace">
      <header class="users-toolbar"><h2>共享与额度</h2><button class="button small" type="button" data-quota-refresh>刷新</button></header>
      <p class="alert error" role="alert" data-quota-error hidden></p>
      ${access.isolated ? `<section class="user-quota-grid">${(access.shares || []).map((share) => {
        const exhausted = share.limit_bytes > 0 && share.used_bytes >= share.limit_bytes;
        const accepted = share.enabled && share.status === "accepted";
        const pending = share.enabled && share.status === "pending";
        const status = accepted && exhausted ? "额度已用完" : agentShareStatus(share);
        return `<article class="workspace-panel user-quota-card" data-quota-share="${esc(share.id)}"><header><div><h3>${esc(share.agent_name)}</h3><small>所有者 · ${esc(share.owner_username || "管理员")}</small></div><span class="status-label ${accepted && !exhausted ? "ok" : "warn"}">${status}</span></header><div><strong>${usage(share.used_bytes)}</strong><span> / ${share.limit_bytes ? usage(share.limit_bytes) : "不限量"}</span>${share.limit_bytes ? `<progress max="100" value="${Math.min(100, share.used_bytes / share.limit_bytes * 100)}" aria-label="已用额度"></progress>` : ""}<small>内核 ${sharedEngineNames(share.engines)}</small><small>端口 ${esc(sharedPortsLabel(share.ports))}</small></div>
          ${pending || accepted ? `<footer class="user-quota-actions">${pending ? '<button class="button primary small" type="button" data-share-open>查看邀请</button>' : '<button class="button small" type="button" data-share-decision="reject">退出共享</button>'}</footer>` : ""}</article>`;
      }).join("") || '<div class="empty large"><strong>暂无共享邀请</strong></div>'}</section>` : '<section class="workspace-panel"><div class="empty compact"><strong>按账号权限访问节点</strong></div></section>'}
    </div>`, "共享与额度", { viewKey: `my-quota-${++viewSerial}` });
    const selectedShare = button => access.shares.find(share => share.id === button.closest("[data-quota-share]").dataset.quotaShare);
    document.querySelectorAll("[data-share-open]").forEach((button) => bindEvent(button, "click", () => {
      if (data !== state.data || data.agentShareResponse || state.route !== "my-quota") return;
      openInvitation(selectedShare(button), button);
    }));
    if (openedID) {
      const share = access.shares.find(share => share.id === openedID);
      const trigger = [...document.querySelectorAll("[data-share-open]")].find(button => selectedShare(button)?.id === openedID);
      if (share) openInvitation(share, trigger);
    }
    if (data.agentShareResponse) lockResponse(data.agentShareResponse);
    document.querySelectorAll(".user-quota-card [data-share-decision]").forEach((button) => bindEvent(button, "click", async () => {
      if (data !== state.data || data.agentShareResponse || state.route !== "my-quota") return;
      const share = selectedShare(button);
      if (!share) return;
      if (!await confirmAction("退出后将失去此节点的访问权限。", "退出共享")) return;
      if (data !== state.data || data.agentShareResponse || state.route !== "my-quota" || !button.isConnected) return;
      await respondToShare(share, "reject", data);
    }));
    bindEvent(document.querySelector("[data-quota-refresh]"), "click", async (event) => {
      if (data !== state.data || data.agentShareResponse) return;
      const button = event.currentTarget;
      button.disabled = true;
      try { await myQuota(); } catch (error) { if (error.name !== "AbortError") notify(error.message, "error"); }
      finally { button.disabled = false; }
    });
  }
  return { users, myQuota, closeInvitation, captureDraft, hasUnsavedChanges };
}
