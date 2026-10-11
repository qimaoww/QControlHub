import { regionAvatarMarkup } from "./regions.js";
import { agentPresenceMarkup } from "./agent-presence.js";
import { groupClientAccessEntries, clientAccessExport } from "./client-access-model.js";
import { clientDisplayDialog, clientParameterDialog } from "./client-access-dialogs.js";

export function createClientAccessResults({ esc, engineName, can }) {
  return function renderResults(filtered, entries, agents, filters, displayFormat = "url") {
    if (!filtered.length) {
      const selectedAgent = agents.find(agent => agent.id === filters.agent);
      const selectedHasEntries = entries.some(entry => entry.agent_id === filters.agent);
      const hasActiveFilter = Boolean(filters.engine || filters.query);
      const title = !entries.length ? "尚未生成客户端配置"
        : selectedAgent && !selectedHasEntries ? `${selectedAgent.name} 尚无客户端配置` : "没有匹配的客户端配置";
      const description = !entries.length || selectedAgent && !selectedHasEntries
        ? "部署支持的服务端入站后，客户端配置会显示在这里。" : "请调整搜索或内核筛选，也可以切换节点。";
      const action = hasActiveFilter ? '<button class="button" type="button" data-clear-client-filters>清除筛选</button>'
        : can("agents.read") ? '<a class="button" href="#node-settings">前往节点设置</a>' : '<p>请联系节点管理员部署入站。</p>';
      return `<section class="client-access-empty-state"><h2>${esc(title)}</h2><p>${description}</p>${action}</section>`;
    }
    return groupClientAccessEntries(filtered, agents).map(group => {
      const firstEntry = group.entries[0];
      const agent = agents.find(item => item.id === group.agent_id) || {};
      const engineSections = group.entries.map(entry => {
        const configLink = can("agent-config.read") ? `<a class="node-card-open" href="#live-config" data-config-agent="${esc(entry.agent_id)}" data-config-engine="${esc(entry.engine)}" aria-label="${esc(engineName(entry.engine))} 服务端配置">服务端配置</a>` : "";
        const profiles = (entry.profiles || []).map(item => {
          // Identity must survive filtering and profile reordering (including an open modal).
          const key = encodeURIComponent(JSON.stringify([entry.agent_id, entry.config_id, entry.engine, item.tag, item.port]));
          const inputID = `client-share-${key}`;
          const displayID = `client-display-${key}`;
          const parameterID = `client-parameters-${key}`;
          const exported = clientAccessExport(item, displayFormat);
          const format = displayFormat === "mihomo" ? "Mihomo YAML" : "URL";
          const title = item.client_name || item.tag;
          const meta = [item.protocol, `端口 ${Number(item.port)}`, title !== item.tag ? item.tag : ""].filter(Boolean).map(value => `<span>${esc(value)}</span>`).join("");
          const shareControl = exported.value.includes("\n")
            ? `<textarea class="client-share-yaml is-masked" id="${inputID}" readonly autocomplete="off" spellcheck="false" aria-label="${esc(title)} ${format}">${esc(exported.value)}</textarea>`
            : `<input id="${inputID}" type="password" readonly autocomplete="off" spellcheck="false" aria-label="${esc(title)} ${format}" value="${esc(exported.value)}">`;
          const shareMarkup = exported.error
            ? `<p class="client-export-unavailable" role="status">${esc(exported.error)} <span>请选择其他导出格式或查看参数。</span></p>`
            : `<details class="client-export-preview" data-refresh-key="preview-${key}"><summary>查看 ${format}</summary><div class="client-export-control">${shareControl}<button class="button small" type="button" data-secret-visibility aria-controls="${inputID}" aria-pressed="false">显示</button></div></details>`;
          const settings = can("agents.manage") ? `<button class="button small client-display-settings-open" type="button" data-client-display-open="${displayID}" aria-haspopup="dialog" aria-controls="${displayID}">修改显示参数</button>` : "";
          const copy = exported.error ? `<button class="button small client-copy" type="button" disabled>无法复制 ${format}</button>`
            : `<button class="button primary small client-copy" type="button" data-copy-target="${esc(inputID)}" data-copy-label="复制 ${format}" aria-label="复制 ${esc(title)} 的 ${format}">复制 ${format}</button>`;
          return `<article class="client-profile-row" data-refresh-key="client-profile-${key}"><header><b title="${esc(title)}">${esc(title)}</b><small class="client-profile-meta">${meta}</small>${item.address && item.address !== entry.address ? `<code class="client-profile-address">${esc(item.address)}</code>` : ""}</header>${copy}<div class="client-profile-actions"><button class="button small client-parameter-open" type="button" data-client-parameter-open="${parameterID}" aria-haspopup="dialog" aria-controls="${parameterID}">查看参数</button>${settings}</div>${shareMarkup}${clientParameterDialog({ entry, item, id: parameterID, esc, engineName })}${can("agents.manage") ? clientDisplayDialog({ entry, item, id: displayID, esc }) : ""}</article>`;
        }).join("");
        const missing = entry.address_required ? "未识别到连接地址。请检查节点公网地址上报后刷新。" : "此内核暂无可导出的入站，请检查服务端配置。";
        const next = can("agents.read") ? `<a class="button small" href="#settings-node-${esc(entry.agent_id)}">检查节点</a>` : "<span>请联系节点管理员。</span>";
        return `<section class="client-access-engine-group" data-refresh-key="engine-${esc(entry.engine)}"><header><span><b class="engine-badge ${esc(entry.engine)}">${esc(engineName(entry.engine))}</b><small>${(entry.profiles || []).length} 个入站</small></span>${configLink}</header><div>${profiles || `<div class="client-access-entry-empty"><p>${missing}</p>${next}</div>`}</div></section>`;
      }).join("");
      return `<article class="client-access-node-card node-card" data-refresh-key="client-access-node-${esc(group.agent_id)}"><header class="node-card-head">${regionAvatarMarkup({ id: group.agent_id }, esc, false)}<div class="node-card-title"><strong>${esc(firstEntry.agent_name)}</strong><small class="client-node-meta"><code>${esc(firstEntry.address || "连接地址缺失")}</code></small></div>${agentPresenceMarkup(agent.status || firstEntry.agent_status)}</header><div class="client-access-node-engines">${engineSections}</div></article>`;
    }).join("");
  };
}
