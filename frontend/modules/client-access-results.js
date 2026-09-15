import { regionAvatarMarkup } from "./regions.js";

import { groupClientAccessEntries, clientAccessAddressChoices } from "./client-access-model.js";
export function createClientAccessResults({ esc, engineName, can }) {
  function renderResults(filtered, entries, agents, filters) {
    if (!filtered.length) {
      const selectedAgent = agents.find((agent) => agent.id === filters.agent);
      const selectedHasEntries = entries.some(
        (entry) => entry.agent_id === filters.agent,
      );
      const hasActiveFilter = Boolean(filters.engine || filters.query);
      const title = !entries.length
        ? "尚未生成客户端配置"
        : selectedAgent && !selectedHasEntries
          ? `${selectedAgent.name} 尚无客户端配置`
          : "没有匹配的客户端配置";
      const description = !entries.length
        ? "安装内核并成功部署可解析的服务端入站后，客户端连接信息会自动出现在这里。"
        : selectedAgent && !selectedHasEntries
          ? "为该节点部署一个支持生成客户端连接信息的服务端入站后即可查看。"
          : "请调整搜索词或内核筛选条件。";
      const action = hasActiveFilter
        ? '<button class="button primary" type="button" data-clear-client-filters>清除筛选</button>'
        : '<a class="button primary" href="#node-settings">前往节点设置</a>';
      return `<section class="client-access-empty-state"><span>⌁</span><h2>${esc(title)}</h2><p>${description}</p>${action}</section>`;
    }

    return groupClientAccessEntries(filtered, agents)
      .map((group, groupIndex) => {
        const firstEntry = group.entries[0];
        const agent = agents.find((item) => item.id === group.agent_id) || {};
        const agentStatus = agent.status || "unknown";
        const addressChoices = clientAccessAddressChoices(firstEntry);
        const displayButton = (displayDialogID) => can("agents.manage")
          ? `<button class="button small client-display-settings-open" type="button" data-client-display-open="${displayDialogID}" aria-haspopup="dialog" aria-controls="${displayDialogID}">修改显示参数</button>`
          : "";
        const displayDialog = (entry, item, displayDialogID, modeField) => {
          const displayDialogTitleID = `${displayDialogID}-title`;
          return can("agents.manage")
          ? `<dialog class="traffic-edit-dialog client-display-dialog" id="${displayDialogID}" aria-labelledby="${displayDialogTitleID}"><header><span class="traffic-edit-icon" aria-hidden="true">✎</span><div><p class="eyebrow">客户端配置</p><h2 id="${displayDialogTitleID}">修改显示参数</h2><p>${esc(firstEntry.agent_name)} · ${esc(item.tag)} · 端口 ${Number(item.port)}</p></div><button class="deploy-command-close" type="button" data-client-display-close aria-label="关闭修改显示参数">×</button></header><form data-client-address-agent="${esc(group.agent_id)}" data-client-profile-engine="${esc(entry.engine)}" data-client-profile-tag="${esc(item.tag)}" data-client-profile-port="${Number(item.port)}"><div class="traffic-edit-body client-display-dialog-body"><div class="client-display-form-grid"><label><span>客户端节点名称</span><input name="name" maxlength="100" autocomplete="off" value="${esc(item.client_name || "")}" placeholder="留空使用入站标签"><small>仅对当前内核、当前监听端口生效，不影响其他端口。</small></label><label><span>客户端连接地址</span><input name="address" maxlength="253" autocomplete="off" value="${item.address_overridden ? esc(item.address || "") : ""}" placeholder="留空使用自动识别地址"><small>仅对当前内核、当前监听端口生效；留空时使用自动识别地址（当前 ${esc(item.address || "未识别")}）。</small></label>${modeField}</div></div><footer>${item.address_overridden ? `<button class="button" type="button" data-clear-client-address="${esc(group.agent_id)}" data-clear-client-profile-engine="${esc(entry.engine)}" data-clear-client-profile-tag="${esc(item.tag)}" data-clear-client-profile-port="${Number(item.port)}">恢复自动识别</button>` : "<span></span>"}<span></span><button class="button" type="button" data-client-display-close>取消</button><button class="button primary" type="submit">保存参数</button></footer></form></dialog>`
          : "";
        };
        const addressWarning = !can("agents.manage") && firstEntry.address_required
          ? '<p class="client-address-missing">管理员尚未设置客户端连接地址，请联系节点管理员。</p>'
          : "";
        const statusLabel = firstEntry.address_required
          ? "待设置地址"
          : agentStatus === "online"
            ? "在线"
            : agentStatus === "offline"
              ? "离线"
              : "状态未知";
        const engineSections = group.entries
          .map((entry, engineIndex) => {
            const profiles = (entry.profiles || [])
              .map((item, profileIndex) => {
                const displayDialogID = `client-display-${groupIndex}-${engineIndex}-${profileIndex}`;
                const inputID = `client-share-${groupIndex}-${engineIndex}-${profileIndex}`;
                const dialogID = `client-parameters-${groupIndex}-${engineIndex}-${profileIndex}`;
                const dialogTitleID = `${dialogID}-title`;
                const profileMode = item.address_mode || "auto";
                const addressModeHelp = item.address_overridden
                  ? "当前使用手动连接地址；先点“恢复自动识别”才能切换协议栈。"
                  : "仅对当前内核、当前监听端口生效：自动、IPv4 或 IPv6。";
                const displayAddressModeField = addressChoices.length
                  ? `<label class="client-display-stack-field"><span>客户端地址协议栈</span><select name="address_mode" data-saved-mode="${esc(profileMode)}"${item.address_overridden ? " disabled" : ""}>${addressChoices.map((choice) => `<option value="${esc(choice.value)}" ${choice.value === profileMode ? "selected" : ""}>${esc(choice.label)}</option>`).join("")}</select><small>${addressModeHelp}</small></label>`
                  : "";
                const fields = (item.profile?.fields || [])
                  .map((field, fieldIndex) => {
                    const fieldID = `client-field-${groupIndex}-${engineIndex}-${profileIndex}-${fieldIndex}`;
                    return `<div class="${field.secret ? "secret" : ""}"><dt>${esc(field.label)}</dt><dd>${field.secret ? `<form class="secret-value-control" action="#"><input id="${fieldID}" type="password" readonly autocomplete="off" spellcheck="false" value="${esc(field.value)}"><button type="button" data-secret-visibility aria-controls="${fieldID}" aria-pressed="false">显示</button><button type="button" data-copy-target="#${fieldID}">复制</button></form>` : `<code title="${esc(field.value)}">${esc(field.value)}</code>`}</dd></div>`;
                  })
                  .join("");
                const shareValue = String(item.profile?.uri || "");
                const shareControl = shareValue.includes("\n")
                  ? `<textarea class="client-share-yaml is-masked" id="${inputID}" readonly autocomplete="off" spellcheck="false">${esc(shareValue)}</textarea>`
                  : `<input id="${inputID}" type="password" readonly autocomplete="off" spellcheck="false" value="${esc(shareValue)}">`;
                return `<article class="client-profile-row" data-refresh-key="client-profile-${esc(entry.agent_id)}-${esc(entry.engine)}-${esc(item.tag)}"><header><b>${esc(item.client_name || item.tag)}</b><small>${esc(item.protocol)} · ${esc(item.tag)} · ${Number(item.port)} · ${esc(item.profile?.format)}</small></header><form class="secret-value-control client-share-control" action="#">${shareControl}<button type="button" data-secret-visibility aria-controls="${inputID}" aria-pressed="false">显示</button><button type="button" data-copy-target="#${inputID}">复制</button></form><div class="client-profile-actions">${displayButton(displayDialogID)}<button class="button small client-parameter-open" type="button" data-client-parameter-open="${dialogID}" aria-haspopup="dialog" aria-controls="${dialogID}">参数详情 <span aria-hidden="true">→</span></button></div><dialog class="traffic-edit-dialog client-parameter-dialog" id="${dialogID}" aria-labelledby="${dialogTitleID}"><header><span class="traffic-edit-icon client-parameter-icon" aria-hidden="true">&lt;/&gt;</span><div><p class="eyebrow">客户端参数</p><h2 id="${dialogTitleID}">${esc(item.protocol)}</h2><p><span class="engine-badge ${esc(entry.engine)}">${esc(engineName(entry.engine))}</span><span class="client-parameter-meta">${esc(item.tag)} · ${esc(item.profile?.format)}</span></p></div><button class="deploy-command-close" type="button" data-client-parameter-close aria-label="关闭参数详情">×</button></header><div class="traffic-edit-body client-parameter-dialog-body"><dl class="client-parameter-list">${fields || '<div class="empty"><dt>参数</dt><dd>暂无参数</dd></div>'}</dl></div></dialog>${displayDialog(entry, item, displayDialogID, displayAddressModeField)}</article>`;
              })
              .join("");
            return `<section class="client-access-engine-group"><header><span><span class="engine-badge ${esc(entry.engine)}">${esc(engineName(entry.engine))}</span><small>${(entry.profiles || []).length} 个入站</small></span><a href="#agent-config" data-config-agent="${esc(entry.agent_id)}" data-config-engine="${esc(entry.engine)}">服务端配置</a></header><div>${profiles || '<p class="client-access-entry-empty">需要先设置可访问的节点地址。</p>'}</div></section>`;
          })
          .join("");
        return `<article class="client-access-node-card" data-refresh-key="client-access-node-${esc(group.agent_id)}"><header><div class="client-access-node">${regionAvatarMarkup({ id: group.agent_id }, esc, false, "node-avatar")}<span><strong>${esc(firstEntry.agent_name)}</strong><small>${esc(agent.os || "节点")} / ${esc(agent.arch || "")} · <code>${esc(firstEntry.address || "未设置地址")}</code></small></span></div><span class="client-access-node-state ${firstEntry.address_required ? "warn" : agentStatus === "online" ? "ok" : "muted"}"><i></i>${statusLabel}</span></header>${addressWarning}<div class="client-access-node-engines">${engineSections}</div></article>`;
      })
      .join("");
  }

  return renderResults;
}
