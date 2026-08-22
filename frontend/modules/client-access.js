export function installClientAccess(ctx) {
  const { api, state, engines, esc, engineName, short, shell, can, notify } = ctx;
async function clientAccess() {
  const [entries, agents] = await Promise.all([
    api("/client-access"),
    can("agents.read") ? api("/agents") : Promise.resolve([]),
  ]);
  state.data.agents = agents;
  state.data.clientAccessEntries = entries;
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
  const filteredProfiles = filtered.reduce(
    (total, entry) => total + (entry.profiles || []).length,
    0,
  );
  const totalProfiles = entries.reduce(
    (total, entry) => total + (entry.profiles || []).length,
    0,
  );
  const totalNodes = new Set(
    entries
      .filter((entry) => (entry.profiles || []).length > 0)
      .map((entry) => entry.agent_id),
  ).size;
  const results =
    filtered
      .map((entry, entryIndex) => {
        const agent = agents.find((item) => item.id === entry.agent_id) || {};
        const agentStatus = agent.status || "unknown";
        const managedAddress = Boolean(agent.labels?.client_address);
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
        const addressForm = `<form class="client-address-form" data-client-address-agent="${esc(entry.agent_id)}"><label><span>客户端连接地址</span><input name="address" required maxlength="253" autocomplete="off" value="${esc(entry.address || "")}" placeholder="例如 203.0.113.10 或 node.example.com"><small>填写客户端实际访问节点的域名或 IP，不要填写 0.0.0.0。</small></label><div><button class="button primary" type="submit">保存并生成配置</button>${managedAddress ? `<button class="button" type="button" data-clear-client-address="${esc(entry.agent_id)}">恢复自动识别</button>` : ""}</div></form>`;
        const addressSetup = entry.address_required
          ? can("agents.manage")
            ? addressForm
            : `<p class="client-address-missing">管理员尚未设置客户端连接地址，请联系节点管理员。</p>`
          : can("agents.manage")
            ? `<details class="client-address-editor"><summary>修改连接地址 <i>＋</i></summary>${addressForm}</details>`
            : "";
        const statusLabel = entry.address_required
          ? "待设置地址"
          : agentStatus === "online"
            ? "在线"
            : agentStatus === "offline"
              ? "离线"
              : "状态未知";
        return `<article class="client-access-entry"><header class="client-access-entry-head"><div class="client-access-node"><span class="node-avatar">●</span><span><strong>${esc(entry.agent_name)}</strong><small>${esc(agent.os || "节点")} / ${esc(agent.arch || "")}${agent.os || agent.arch ? ` · ${esc(short(entry.agent_id))}` : ""}</small></span></div><div class="client-access-engine"><span class="engine-badge ${esc(entry.engine)}">${esc(engineName(entry.engine))}</span><span class="status-label ${entry.address_required ? "warn" : agentStatus === "online" ? "ok" : "muted"}">${statusLabel}</span></div></header><div class="client-access-entry-meta"><span><small>连接地址</small><code>${esc(entry.address || "未设置")}</code></span><span><small>地址来源</small><strong>${esc(entry.source || "需要管理员设置")}</strong></span><a href="#agent-config" data-config-agent="${esc(entry.agent_id)}" data-config-engine="${esc(entry.engine)}">服务端配置 →</a></div>${addressSetup}<div class="client-access-profile-grid">${profiles || `<div class="client-access-entry-empty"><b>暂时无法生成客户端配置</b><span>配置已部署，但需要一个可访问的节点地址。</span></div>`}</div></article>`;
      })
      .join("") ||
    `<section class="client-access-empty-state"><span>⌁</span><h2>${entries.length ? "没有匹配的客户端配置" : "尚未生成客户端配置"}</h2><p>${entries.length ? "请调整搜索或筛选条件。" : "安装内核并成功部署可解析的服务端入站后，客户端连接信息会自动出现在这里。"}</p><a class="button primary" href="#node-settings">前往节点设置</a></section>`;
  const filterEngines = new Set(entries.map((entry) => entry.engine));
  const engineFilters = engines
    .filter((engine) => filterEngines.has(engine))
    .map(
      (engine) =>
        `<a class="${selectedEngine === engine ? "active" : ""}" href="#client-access" data-filter-engine="${esc(engine)}">${esc(engineName(engine))}</a>`,
    )
    .join("");
  const filters = entries.length
    ? `<section class="client-access-filter-panel" aria-label="客户端配置筛选"><form class="client-access-search" id="client-search"><label><span>搜索入站</span><input type="search" name="q" value="${esc(state.data.accessQuery || "")}" placeholder="节点、地址、协议或入站名称" autocomplete="off"></label><button class="button primary" type="submit">搜索</button>${query ? '<button class="button" type="button" data-clear-search>清除搜索</button>' : ""}</form><div class="client-access-filter-row"><span>内核</span><nav aria-label="按内核筛选"><a class="${selectedEngine ? "" : "active"}" href="#client-access" data-filter-engine="">全部内核</a>${engineFilters}</nav></div></section><div class="client-access-results-head"><span>当前结果</span><strong>${filtered.length} 组内核配置 · ${filteredProfiles} 个入站</strong></div>`
    : "";
  shell(
    `<section class="client-access-workspace"><header class="client-access-hero"><div><p class="eyebrow">Client access</p><h1>客户端配置</h1><p>集中查看已部署入站生成的客户端连接信息。凭据默认隐藏，只在本页按需显示或复制。</p></div><dl class="client-access-summary"><div><dt>可用节点</dt><dd>${totalNodes}</dd></div><div><dt>客户端入站</dt><dd>${totalProfiles}</dd></div></dl></header>${filters}<div class="client-access-entry-grid">${results}</div></section>`,
    "客户端配置",
  );
  bindClientAccessPage();
}

function bindClientAccessPage() {
  document
    .querySelectorAll("[data-access-agent]")
    .forEach((button) => {
      button.onclick = (event) => {
        event.preventDefault();
        state.data.accessAgent = button.dataset.accessAgent;
        return clientAccess();
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
  document.querySelectorAll("[data-client-address-agent]").forEach((form) => {
    form.addEventListener("submit", async (event) => {
      event.preventDefault();
      const button = form.querySelector("button[type=submit]");
      const address = new FormData(form).get("address");
      if (button) button.disabled = true;
      try {
        await api(`/agents/${encodeURIComponent(form.dataset.clientAddressAgent)}/client-address`, {
          method: "PUT",
          body: JSON.stringify({ address }),
        });
        notify("客户端连接地址已保存");
        await clientAccess();
      } catch (error) {
        notify(error.message, "error");
        if (button) button.disabled = false;
      }
    });
  });
  document.querySelectorAll("[data-clear-client-address]").forEach((button) => {
    button.addEventListener("click", async () => {
      button.disabled = true;
      try {
        await api(`/agents/${encodeURIComponent(button.dataset.clearClientAddress)}/client-address`, {
          method: "PUT",
          body: JSON.stringify({ address: "" }),
        });
        notify("已恢复自动识别连接地址");
        await clientAccess();
      } catch (error) {
        notify(error.message, "error");
        button.disabled = false;
      }
    });
  });
}
  return clientAccess;
}
