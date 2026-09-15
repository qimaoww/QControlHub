import { sharedEngineNames } from "./engine-capabilities.js";
import { agentShareStatus, sharedPortsLabel, usage } from "./user-model.js";
export function createUserView({ state, esc, shell }, lifecycle) {
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
    lifecycle.activeAllocation?.dialog.close();
    lifecycle.activeAllocation = null;
    lifecycle.captureActive = () => {};
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
    </div>`, "用户", { viewKey: `users-${user?.id || "new"}-${++lifecycle.viewSerial}` });

    return { editable, data, draft, shares };
  }
  return renderUsers;
}
