import { sharedEngineNames } from "./engine-capabilities.js";
import { agentShareStatus, sharedPortsLabel, usage } from "./user-model.js";
export function createUserQuotaView({ esc, shell }, lifecycle) {
  function renderQuotaView(access) {
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
    </div>`, "共享与额度", { viewKey: `my-quota-${++lifecycle.viewSerial}` });

  }
  function renderInvitationDialog(dialog, share) {
    dialog.innerHTML = `<header><div><h2 id="agent-invitation-title">共享邀请</h2><p id="agent-invitation-origin">所有者 · ${esc(share.owner_username || "管理员")}</p></div><button type="button" class="deploy-command-close" data-invitation-close aria-label="关闭邀请">×</button></header>
      <div class="traffic-edit-body">
        <div class="agent-invitation-node"><span>Agent</span><h3>${esc(share.agent_name)}</h3></div>
        <dl class="agent-invitation-terms"><div><dt>内核</dt><dd>${sharedEngineNames(share.engines)}</dd></div><div><dt>端口</dt><dd>${esc(sharedPortsLabel(share.ports))}</dd></div><div><dt>总额度</dt><dd>${share.limit_bytes ? usage(share.limit_bytes) : "不限量"}<small>已用 ${usage(share.used_bytes)}</small></dd></div></dl>
        <div class="agent-invitation-error" data-invitation-error hidden><p class="alert error" role="alert"></p><button type="button" class="button small" data-invitation-reload>刷新邀请</button></div>
      </div><footer><button type="button" class="button" data-share-decision="reject">拒绝</button><button type="button" class="button primary" data-share-decision="accept" ${share.engines?.length ? "" : 'disabled title="等待所有者分配内核"'}>接受</button></footer>`;

  }
  return { renderQuotaView, renderInvitationDialog };
}
