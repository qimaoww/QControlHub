import { bindEvent } from "./refresh.js";
import { agentShareStatus, parseSharedPorts, sharedLimitBytes, sharedLimitGiB } from "./users.js";

export function createAgentSharing(ctx, interactions) {
  const { api, state, can, esc, notify, confirmAction, bytes = value => `${sharedLimitGiB(value)} GiB` } = ctx;
  let serial = 0, active = null;
  const hasUnsavedChanges = () => Boolean(state.data.agentSharingDrafts?.size || state.data.agentSharingSaves?.size);
  const values = (form) => [...form.querySelectorAll("[data-recipient]")].map((row) => ({
    username: row.querySelector('[name="username"]').value,
    enabled: row.querySelector('[name="enabled"]').checked,
    ports_text: row.querySelector('[name="ports"]').value,
    limit_gib: row.querySelector('[name="limit_gib"]').value,
    reinvite: row.dataset.reinvite === "true",
  }));
  const rowMarkup = (share) => `<div class="agent-share-recipient" data-recipient data-reinvite="${Boolean(share.reinvite)}">
    <div class="agent-share-recipient-head">
      <label class="settings-field"><span>用户名</span><input name="username" required maxlength="64" autocomplete="off" placeholder="准确用户名" value="${esc(share.username || "")}" ${share.user_id ? "readonly" : ""}></label>
      <div class="agent-share-recipient-actions"><label class="agent-share-enabled"><input type="checkbox" name="enabled" ${share.enabled !== false ? "checked" : ""}><span>启用</span></label>${share.user_id ? "" : '<button type="button" class="deploy-command-close" data-recipient-remove aria-label="移除未保存的用户">×</button>'}</div>
    </div>
    <div class="agent-share-fields">
      <label class="settings-field"><span>端口</span><input name="ports" autocomplete="off" placeholder="21001, 21002" value="${esc(share.ports_text ?? (share.ports || []).join(", "))}"></label>
      <label class="settings-field"><span title="累计总额度；0 表示不限量">总额度 · GiB</span><input name="limit_gib" type="number" required min="0" max="8388607" step="any" title="0 表示不限量" value="${esc(share.limit_gib ?? sharedLimitGiB(share.limit_bytes))}"></label>
    </div>
    <div class="agent-share-meta"><small class="agent-share-usage"><span data-share-status>${agentShareStatus(share)}</span> · 已用 ${esc(bytes(share.used_bytes || 0))}</small>${share.status === "rejected" ? `<button type="button" class="button small" data-recipient-reinvite ${share.reinvite ? "disabled" : ""}>重新邀请</button>` : ""}</div>
  </div>`;
  const close = () => {
    ++serial; // Also discard a pending read after navigation/sign-out.
    if (!active) return;
    const previous = active;
    active = null;
    previous.capture();
    previous.dialog.close();
    previous.dialog.remove();
    previous.release();
  };

  async function open(agent) {
    if (!agent || agent.can_manage === false || !can("agents.manage", agent)) return;
    close();
    const request = ++serial, data = state.data, epoch = state.navigationEpoch;
    data.agentSharingDrafts ||= new Map();
    data.agentSharingSaves ||= new Map();
    try {
      const sharing = await api(`/agents/${encodeURIComponent(agent.id)}/sharing`);
      if (request !== serial || data !== state.data || epoch !== state.navigationEpoch) return;
      show(agent, sharing, data);
    } catch (error) {
      if (request === serial && data === state.data && error.name !== "AbortError") notify(error.message, "error");
    }
  }

  function show(agent, sharing, data) {
    const draft = data.agentSharingDrafts.get(agent.id);
    const revision = draft?.revision ?? sharing.revision;
    const rows = draft
      ? draft.rows.map((row) => ({ ...sharing.shares.find((share) => share.username === row.username), ...row }))
      : sharing.shares;
    const dialog = document.createElement("dialog");
    dialog.className = "traffic-edit-dialog agent-sharing-dialog";
    dialog.setAttribute("aria-labelledby", "agent-sharing-title");
    dialog.innerHTML = `<header><div><h2 id="agent-sharing-title">共享 Agent</h2><p>${esc(agent.name)}</p></div><button type="button" class="deploy-command-close" data-sharing-close aria-label="关闭共享设置">×</button></header>
      <form><div class="traffic-edit-body">
        ${agent.features?.includes("shared-traffic-v1") ? "" : '<p class="alert error">请先升级 Agent，再启用共享。</p>'}
        <div data-recipients>${rows.map(rowMarkup).join("")}</div>
        <button type="button" class="button small" data-recipient-add>添加用户</button>
        <p class="alert error" role="alert" data-sharing-error hidden></p>
      </div><footer><div class="agent-sharing-secondary"><button type="button" class="button small" data-sharing-reload>刷新</button><a href="https://github.com/qimaoww/QControlHub/blob/main/docs/agent-sharing.md" target="_blank" rel="noopener noreferrer">共享规则 ↗</a></div><button type="submit" class="button primary">保存</button></footer></form>`;
    document.body.append(dialog);
    const form = dialog.querySelector("form");
    const baseline = draft?.baseline ?? JSON.stringify(values(form));
    const capture = () => {
      if (!dialog.isConnected || data !== state.data) return;
      const rows = values(form);
      const serialized = JSON.stringify(rows);
      const previous = data.agentSharingDrafts.get(agent.id);
      if (serialized === baseline) data.agentSharingDrafts.delete(agent.id);
      else if (previous?.revision !== revision || JSON.stringify(previous.rows) !== serialized)
        data.agentSharingDrafts.set(agent.id, { revision, rows, baseline });
    };
    const report = (message) => {
      const output = dialog.querySelector("[data-sharing-error]");
      output.textContent = message;
      output.hidden = !message;
    };
    const lock = (saving) => {
      for (const control of form.elements) {
        if (control.hasAttribute("data-sharing-close")) continue;
        saving.controls.push([control, control.disabled]);
        control.disabled = true;
      }
    };
    active = { dialog, capture, release: interactions.begin(), agentID: agent.id, data };
    dialog.showModal();
    bindEvent(form, "input", capture);
    bindEvent(form, "change", capture);
    bindEvent(dialog, "cancel", (event) => { event.preventDefault(); close(); });
    dialog.querySelectorAll("[data-sharing-close]").forEach((button) => bindEvent(button, "click", close));
    bindEvent(form.querySelector("[data-recipient-add]"), "click", () => {
      const rows = form.querySelector("[data-recipients]");
      rows.insertAdjacentHTML("beforeend", rowMarkup({ enabled: true }));
      rows.lastElementChild.querySelector('[name="username"]').focus();
      capture();
    });
    bindEvent(form.querySelector("[data-recipients]"), "click", (event) => {
      const reinvite = event.target.closest("[data-recipient-reinvite]");
      if (reinvite && data === state.data && !data.agentSharingSaves.has(agent.id)) {
        const row = reinvite.closest("[data-recipient]");
        row.dataset.reinvite = "true";
        row.querySelector('[name="enabled"]').checked = true;
        row.querySelector("[data-share-status]").textContent = "待发送";
        reinvite.disabled = true;
        capture();
        return;
      }
      const row = event.target.closest("[data-recipient-remove]")?.closest("[data-recipient]");
      if (!row || data !== state.data || data.agentSharingSaves.has(agent.id)) return;
      row.remove();
      capture();
      form.querySelector("[data-recipient-add]").focus();
    });
    bindEvent(form.querySelector("[data-sharing-reload]"), "click", async () => {
      if (await confirmAction("重新读取将丢弃此节点未保存的共享设置。", "重新读取")) {
        if (active?.dialog !== dialog || state.data !== data) return;
        data.agentSharingDrafts.delete(agent.id);
        active.capture = () => {};
        await open(agent);
      }
    });
    const pending = data.agentSharingSaves.get(agent.id);
    if (pending) lock(pending);
    bindEvent(form, "submit", async (event) => {
      event.preventDefault();
      if (data !== state.data || !dialog.isConnected || data.agentSharingSaves.has(agent.id)) return;
      let saving;
      try {
        const shares = values(form).map((row) => ({
          username: row.username.trim(), enabled: row.enabled,
          ports: parseSharedPorts(row.ports_text), limit_bytes: sharedLimitBytes(row.limit_gib),
          reinvite: row.reinvite,
        }));
        capture();
        const submitted = data.agentSharingDrafts.get(agent.id);
        saving = { controls: [] };
        data.agentSharingSaves.set(agent.id, saving);
        lock(saving);
        const saved = await api(`/agents/${encodeURIComponent(agent.id)}/sharing`, {
          method: "PUT", body: JSON.stringify({ revision, shares }),
        });
        if (data !== state.data) return;
        if (data.agentSharingDrafts.get(agent.id) === submitted) data.agentSharingDrafts.delete(agent.id);
        data.agentSharingSaves.delete(agent.id);
        if (active?.agentID === agent.id && active.data === data) {
          active.capture = () => {};
          close();
          show(agent, saved, data);
        }
        notify("共享设置已保存");
      } catch (error) {
        if (data !== state.data || error.name === "AbortError") return;
        if (dialog.isConnected) report(error.message);
        else notify(error.message, "error");
      } finally {
        if (data.agentSharingSaves.get(agent.id) === saving) data.agentSharingSaves.delete(agent.id);
        saving?.controls.forEach(([control, disabled]) => { control.disabled = disabled; });
      }
    });
  }
  return { open, close, hasUnsavedChanges };
}
