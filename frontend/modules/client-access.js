export function installClientAccess(ctx) {
  const { api, state, engines, esc, engineName, short, shell } = ctx;
async function clientAccess() {
  const [entries, agents, overview, settings] = await Promise.all([
    api("/client-access"),
    api("/agents"),
    api("/overview"),
    api("/settings"),
  ]);
  state.data.agents = agents;
  state.data.overview = overview;
  state.data.settings = settings;
  const selectedAgent = state.data.accessAgent || "";
  const selectedEngine = state.data.accessEngine || "";
  const query = String(state.data.accessQuery || "")
    .trim()
    .toLowerCase();
  const filtered = entries.filter((entry) => {
    if (selectedAgent && entry.agent_id !== selectedAgent) return false;
    if (selectedEngine && entry.engine !== selectedEngine) return false;
    if (!query) return true;
    return [
      entry.agent_name,
      entry.agent_id,
      entry.engine,
      entry.address,
      entry.source,
      ...(entry.profiles || []).flatMap((profile) => [
        profile.tag,
        profile.protocol,
        profile.profile?.format,
      ]),
    ]
      .join(" ")
      .toLowerCase()
      .includes(query);
  });
  const totalProfiles = filtered.reduce(
    (total, entry) => total + (entry.profiles || []).length,
    0,
  );
  const totalNodes = new Set(filtered.map((entry) => entry.agent_id)).size;
  const results =
    filtered
      .map((entry, entryIndex) => {
        const agent = agents.find((item) => item.id === entry.agent_id) || {};
        const profiles = (entry.profiles || [])
          .map((item, profileIndex) => {
            const inputID = `client-share-${entryIndex}-${profileIndex}`;
            const fields = (item.profile?.fields || [])
              .map(
                (field, fieldIndex) =>
                  `<div><span>${esc(field.label)}</span>${field.secret ? `<form class="secret-value-control" action="#"><input id="client-field-${entryIndex}-${profileIndex}-${fieldIndex}" type="password" readonly autocomplete="off" spellcheck="false" value="${esc(field.value)}"><button type="button" data-secret-visibility>显示</button><button type="button" data-copy-target="#client-field-${entryIndex}-${profileIndex}-${fieldIndex}">复制</button></form>` : `<code title="${esc(field.value)}">${esc(field.value)}</code>`}</div>`,
              )
              .join("");
            return `<article class="client-profile-card"><header><span><b>${esc(item.protocol)}</b><small>${esc(item.tag)} · ${esc(item.profile?.format)}</small></span><span class="status-label warn">含凭据</span></header><form class="secret-value-control client-share-control" action="#"><input id="${inputID}" type="password" readonly autocomplete="off" spellcheck="false" value="${esc(item.profile?.uri)}"><button type="button" data-secret-visibility>显示</button><button type="button" data-copy-target="#${inputID}">复制</button></form><details class="client-parameter-menu"><summary>逐项参数 <i>展开</i></summary><div class="client-parameters">${fields}</div></details></article>`;
          })
          .join("");
        return `<article class="client-access-entry"><header class="client-access-entry-head"><div class="client-access-node"><span class="node-avatar">●</span><span><strong>${esc(entry.agent_name)}</strong><small>${esc(agent.os)} / ${esc(agent.arch)} · ${esc(short(entry.agent_id))}</small></span></div><div class="client-access-engine"><span class="engine-badge ${esc(entry.engine)}">${esc(engineName(entry.engine))}</span><span class="status-label ${agent.status === "online" ? "ok" : "muted"}">${agent.status === "online" ? "在线" : "离线"}</span></div></header><div class="client-access-entry-meta"><span><small>连接地址</small><code>${esc(entry.address)}</code></span><span><small>地址来源</small><strong>${esc(entry.source)}</strong></span><a href="#agent-config" data-config-agent="${esc(entry.agent_id)}" data-config-engine="${esc(entry.engine)}">服务端配置 →</a></div><div class="client-access-profile-grid">${profiles}</div></article>`;
      })
      .join("") ||
    '<section class="client-access-empty-state"><span>⌁</span><h2>没有匹配的客户端配置</h2><p>客户端信息只会从已部署且可解析的入站生成。请调整筛选条件，或先在节点内核中完成配置与部署。</p><a class="button primary" href="#agents">返回节点</a></section>';
  const agentFilters = agents
    .map(
      (agent) =>
        `<a class="${selectedAgent === agent.id ? "active" : ""}" href="#client-access" data-filter-agent="${esc(agent.id)}">${esc(agent.name)}</a>`,
    )
    .join("");
  const engineFilters = engines
    .map(
      (engine) =>
        `<a class="${selectedEngine === engine ? "active" : ""}" href="#client-access" data-filter-engine="${esc(engine)}">${esc(engineName(engine))}</a>`,
    )
    .join("");
  shell(
    `<section class="client-access-workspace"><header class="client-access-hero"><div><p class="eyebrow">Client access</p><h1>客户端配置</h1><p>集中查看已部署入站生成的客户端连接信息。凭据默认隐藏，只在本页按需显示或复制。</p></div><dl class="client-access-summary"><div><dt>可用节点</dt><dd>${totalNodes}</dd></div><div><dt>客户端入站</dt><dd>${totalProfiles}</dd></div></dl></header><section class="client-access-filter-panel" aria-label="客户端配置筛选"><form class="client-access-search" id="client-search"><label><span>搜索入站</span><input type="search" name="q" value="${esc(state.data.accessQuery || "")}" placeholder="节点、地址、协议或入站名称" autocomplete="off"></label><button class="button primary" type="submit">搜索</button>${query ? '<button class="button" type="button" data-clear-search>清除搜索</button>' : ""}</form><div class="client-access-filter-row"><span>节点</span><nav aria-label="按节点筛选"><a class="${selectedAgent ? "" : "active"}" href="#client-access" data-filter-agent="">全部节点</a>${agentFilters}</nav></div><div class="client-access-filter-row"><span>内核</span><nav aria-label="按内核筛选"><a class="${selectedEngine ? "" : "active"}" href="#client-access" data-filter-engine="">全部内核</a>${engineFilters}</nav></div></section><div class="client-access-results-head"><span>当前结果</span><strong>${filtered.length} 组内核配置</strong></div><div class="client-access-entry-grid">${results}</div></section>`,
    "客户端配置",
  );
  bindClientAccessPage();
}

function bindClientAccessPage() {
  document
    .querySelectorAll("[data-filter-agent], [data-access-agent]")
    .forEach((button) => {
      button.onclick = (event) => {
        event.preventDefault();
        state.data.accessAgent =
          button.dataset.filterAgent ?? button.dataset.accessAgent;
        clientAccess();
      };
    });
  document.querySelectorAll("[data-filter-engine]").forEach((button) => {
    button.onclick = (event) => {
      event.preventDefault();
      state.data.accessEngine = button.dataset.filterEngine;
      clientAccess();
    };
  });
  document
    .querySelector("#client-search")
    ?.addEventListener("submit", (event) => {
      event.preventDefault();
      state.data.accessQuery = new FormData(event.currentTarget).get("q");
      clientAccess();
    });
  document
    .querySelector("[data-clear-search]")
    ?.addEventListener("click", () => {
      state.data.accessQuery = "";
      clientAccess();
    });
  document.querySelectorAll("[data-secret-visibility]").forEach((button) => {
    button.onclick = () => {
      const input = button.parentElement.querySelector("input");
      const reveal = input.type === "password";
      input.type = reveal ? "text" : "password";
      button.textContent = reveal ? "隐藏" : "显示";
    };
  });
  document.querySelectorAll("[data-copy-target]").forEach((button) => {
    button.onclick = async () => {
      const input = document.querySelector(button.dataset.copyTarget);
      await navigator.clipboard.writeText(input?.value || "");
      button.textContent = "已复制";
    };
  });
  document.querySelectorAll("[data-config-agent]").forEach((link) => {
    link.onclick = () => {
      state.data.agentId = link.dataset.configAgent;
      state.data.engine = link.dataset.configEngine;
    };
  });
}
  return clientAccess;
}
