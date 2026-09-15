import { sharedEngineChoices } from "./engine-capabilities.js";
import { sharedLimitGiB, formatSharedPorts, agentShareStatus, usage } from "./user-model.js";
export const formValues = (form) => ({
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

export function createUserAllocationView({ esc }) {
  function allocationFields(share, agent) {
    const limit = share.limit_gib ?? sharedLimitGiB(share.limit_bytes);
    const unlimited = share.unlimited ?? Number(limit) === 0;
    return `${sharedEngineChoices(share.engines, agent?.supported_capabilities ?? agent?.capabilities)}
      <label class="settings-field"><span>可用端口</span><input name="ports" value="${esc(share.ports_text ?? formatSharedPorts(share.ports))}" placeholder="21000-21100, 22000" autocomplete="off"><small>留空未分配，0 无限制；最多指定 256 个端口</small></label>
      <fieldset class="user-allocation-quota"><legend>流量额度</legend><div class="user-quota-options"><label><input type="radio" name="quota_mode" value="unlimited" ${unlimited ? "checked" : ""}>不限量</label><label><input type="radio" name="quota_mode" value="limited" ${unlimited ? "" : "checked"}>设置总额度</label></div><label class="settings-field" data-quota-amount ${unlimited ? "hidden" : ""}><span>累计总额度（GiB）</span><input name="limit_gib" type="number" min="0" max="8388607" step="any" required value="${esc(limit)}" placeholder="例如 100" ${unlimited ? "disabled" : ""}></label><small>已用 ${usage(share.used_bytes)}</small></fieldset>`;
  }

  function renderAllocationDialog(dialog, { title, user, agentID, mode, available, share, agent, original }) {
    dialog.innerHTML = `<header><div><h2 id="user-allocation-dialog-title">${title}</h2><p>接收用户 · ${esc(user.display_name || user.username)}${user.display_name && user.display_name !== user.username ? `（${esc(user.username)}）` : ""}</p></div><button type="button" class="deploy-command-close" data-allocation-close aria-label="关闭分配弹窗">×</button></header>
      <form data-user-access-form data-user-form><div class="traffic-edit-body" data-share-row="${esc(agentID)}" data-reinvite="${Boolean(share.reinvite)}">
        <input type="checkbox" name="enabled" hidden ${share.enabled ? "checked" : ""}>
        ${mode === "add" ? `<label class="settings-field"><span>节点</span><select name="agent_id" data-share-agent required autofocus><option value="">选择节点</option>${available.map(item => `<option value="${esc(item.id)}" ${item.id === agentID ? "selected" : ""}>${esc(item.name)}</option>`).join("")}</select></label>` : `<div class="user-allocation-target"><span>节点</span><div><strong>${esc(agent?.name || original?.agent_name || agentID)}</strong><small>${agentShareStatus(original)}</small></div></div>`}
        <fieldset class="user-allocation-fields" data-allocation-fields ${agentID ? "" : "hidden disabled"}>${allocationFields(share, agent)}</fieldset>
        <p class="user-allocation-consent" data-allocation-consent></p>
        <div class="user-allocation-failure" hidden><p class="alert error" data-user-error role="alert" hidden></p><button type="button" class="button small" data-allocation-reload>重新读取</button></div>
      </div><footer><button class="button" type="button" data-allocation-close>取消</button><button class="button primary" type="submit" ${agentID ? "" : "disabled"}>${mode === "edit" ? "保存修改" : "发送邀请"}</button></footer></form>`;

  }
  return { allocationFields, renderAllocationDialog };
}
