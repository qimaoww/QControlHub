import { clientAccessAddressChoices } from "./client-access-model.js";

export function clientDisplayDialog({ entry, item, id, esc }) {
  const choices = clientAccessAddressChoices(entry);
  const mode = item.address_mode || "auto";
  return `<dialog class="traffic-edit-dialog client-display-dialog" id="${id}" aria-labelledby="${id}-title"><header><div><h2 id="${id}-title">修改显示参数</h2><p class="client-profile-meta"><span>${esc(entry.agent_name)}</span><span>${esc(item.tag)}</span><span>端口 ${Number(item.port)}</span></p></div><button class="button small client-dialog-close" type="button" data-client-display-close aria-label="关闭修改显示参数">关闭</button></header>
    <form data-client-address-agent="${esc(entry.agent_id)}" data-client-profile-engine="${esc(entry.engine)}" data-client-profile-tag="${esc(item.tag)}" data-client-profile-port="${Number(item.port)}">
      <div class="traffic-edit-body client-display-dialog-body">
        <p class="client-display-scope">作用于当前配置中此内核的监听地址与端口；复用同一配置的节点也会更新。仅改变客户端导出，不修改服务端入站。</p>
        <div class="client-display-form-grid">
          <label><span>客户端名称</span><input name="name" maxlength="100" autocomplete="off" value="${esc(item.client_name || "")}" placeholder="留空使用入站标签"><small>留空使用 ${esc(item.tag)}。</small></label>
          <label><span>连接地址 <small data-client-address-state>${item.address_overridden ? "手动覆盖" : "自动识别"}</small></span><input name="address" maxlength="253" autocomplete="off" spellcheck="false" value="${item.address_overridden ? esc(item.address || "") : ""}" placeholder="留空使用自动识别地址"><small>填写域名、IPv4 或 IPv6，不含协议和端口。${!item.address_overridden && item.address ? `当前自动地址：${esc(item.address)}` : ""}</small></label>
          ${choices.length ? `<label class="client-display-stack-field"><span>客户端地址协议栈</span><select name="address_mode" data-saved-mode="${esc(mode)}"${item.address_overridden ? " disabled" : ""}>${choices.map(choice => `<option value="${esc(choice.value)}"${choice.value === mode ? " selected" : ""}>${esc(choice.label)}</option>`).join("")}</select><small data-client-mode-help>${item.address_overridden ? "使用手动地址中；恢复自动识别后可切换协议栈。" : "自动识别按此偏好选择节点地址；指定协议栈不可用时自动回退。"}</small></label>` : ""}
        </div>
        <button class="button small client-reset-address" type="button" data-clear-client-address="${esc(entry.agent_id)}">恢复自动识别</button><small class="client-reset-note">清空手动地址，保留协议栈偏好；保存后生效。</small>
        <p class="client-display-error" role="alert" data-client-display-error hidden></p>
      </div><footer><button class="button" type="button" data-client-display-close>取消</button><button class="button primary" type="submit">保存参数</button></footer>
    </form></dialog>`;
}

export function clientParameterDialog({ entry, item, id, esc, engineName }) {
  const fields = (item.profile?.fields || []).map((field, index) => {
    const fieldID = `${id}-field-${index}`;
    return `<div class="${field.secret ? "secret" : ""}"><dt>${esc(field.label)}</dt><dd>${field.secret ? `<div class="secret-value-control"><input id="${fieldID}" aria-label="${esc(field.label)}" type="password" readonly autocomplete="off" spellcheck="false" value="${esc(field.value)}"><button type="button" data-secret-visibility aria-controls="${fieldID}" aria-pressed="false">显示</button><button type="button" data-copy-target="#${fieldID}">复制</button></div>` : `<code>${esc(field.value)}</code>`}</dd></div>`;
  }).join("");
  return `<dialog class="traffic-edit-dialog client-parameter-dialog" id="${id}" aria-labelledby="${id}-title"><header><div><h2 id="${id}-title">${esc(item.client_name || item.tag)}</h2><p class="client-parameter-meta client-profile-meta"><span>${esc(engineName(entry.engine))}</span><span>${esc(item.protocol)}</span><span>端口 ${Number(item.port)}</span></p></div><button class="button small client-dialog-close" type="button" data-client-parameter-close aria-label="关闭参数详情">关闭</button></header><div class="traffic-edit-body client-parameter-dialog-body"><dl class="client-parameter-list">${fields || '<div class="empty"><dt>参数</dt><dd>暂无参数</dd></div>'}</dl></div></dialog>`;
}
