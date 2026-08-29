import { bindEvent, createRefreshChannel } from "./refresh.js";

export function normalizeClientAccessFilters(entries, agents, filters = {}) {
  const agentIDs = new Set((agents || []).map((agent) => agent.id));
  let agent = String(filters.agent || "");
  if (agent && !agentIDs.has(agent)) agent = "";

  const scopedEntries = agent
    ? (entries || []).filter((entry) => entry.agent_id === agent)
    : entries || [];
  const engineIDs = new Set(scopedEntries.map((entry) => entry.engine));
  let engine = String(filters.engine || "");
  if (engine && !engineIDs.has(engine)) engine = "";

  return {
    agent,
    engine,
    query: String(filters.query || "").trim(),
  };
}

export function filterClientAccessEntries(entries, filters = {}) {
  const query = String(filters.query || "").trim().toLowerCase();
  return (entries || []).flatMap((entry) => {
    if (filters.agent && entry.agent_id !== filters.agent) return [];
    if (filters.engine && entry.engine !== filters.engine) return [];
    if (!query) return [entry];
    const entryMatches = [
      entry.agent_name,
      entry.agent_id,
      entry.engine,
      entry.address,
      entry.source,
      ...(entry.address_options || []).flatMap((option) => [option.address, option.source]),
    ]
      .join(" ")
      .toLowerCase()
      .includes(query);
    if (entryMatches) return [entry];
    const profiles = (entry.profiles || []).filter((profile) =>
      [
        profile.tag,
        profile.protocol,
        profile.profile?.format,
      ]
        .join(" ")
        .toLowerCase()
        .includes(query),
    );
    return profiles.length ? [{ ...entry, profiles }] : [];
  });
}

export function groupClientAccessEntries(entries) {
  const groups = [];
  const byAgent = new Map();
  for (const entry of entries || []) {
    let group = byAgent.get(entry.agent_id);
    if (!group) {
      group = { agent_id: entry.agent_id, entries: [] };
      byAgent.set(entry.agent_id, group);
      groups.push(group);
    }
    group.entries.push(entry);
  }
  return groups;
}

export function clientAccessAddressChoices(entry) {
  const options = entry?.address_options || [];
  const byFamily = new Map();
  for (const option of options) {
    if ((option.family === "ipv4" || option.family === "ipv6") && !byFamily.has(option.family)) {
      byFamily.set(option.family, option);
    }
  }
  if (!byFamily.has("ipv4") || !byFamily.has("ipv6")) return [];
  return [
    { value: "auto", label: "自动选择" },
    { value: "ipv4", label: `IPv4 · ${byFamily.get("ipv4").address}` },
    { value: "ipv6", label: `IPv6 · ${byFamily.get("ipv6").address}` },
  ];
}

export function clientAccessEntryForAddress(entry, mode = "auto") {
  const options = entry?.address_options || [];
  const selected = mode === "auto"
    ? options[0]
    : options.find((option) => option.family === mode) || options[0];
  return selected
    ? { ...entry, address: selected.address, source: selected.source, profiles: selected.profiles }
    : entry;
}

export async function copyClientValue(
  input,
  {
    navigatorObject = globalThis.navigator,
    documentObject = globalThis.document,
  } = {},
) {
  if (!input) throw new Error("没有可复制的内容");
  if (navigatorObject?.clipboard?.writeText) {
    await navigatorObject.clipboard.writeText(input.value || "");
    return "clipboard";
  }
  if (typeof documentObject?.execCommand !== "function")
    throw new Error("当前浏览器不支持剪贴板操作");

  const active = documentObject.activeElement;
  const selection = {
    start: input.selectionStart,
    end: input.selectionEnd,
    direction: input.selectionDirection,
  };
  input.focus({ preventScroll: true });
  input.select();
  try {
    if (!documentObject.execCommand("copy"))
      throw new Error("浏览器拒绝了剪贴板操作");
  } finally {
    if (selection.start != null && typeof input.setSelectionRange === "function")
      input.setSelectionRange(
        selection.start,
        selection.end,
        selection.direction || "none",
      );
    active?.focus?.({ preventScroll: true });
  }
  return "legacy";
}

export function installClientAccess(ctx) {
  const { api, state, engines, esc, engineName, shell, can, notify } = ctx;
  let masonryObserver = null;
  const refresh = createRefreshChannel({
    isCurrent: () => state.route === "client-access",
    getScope: () => state.navigationEpoch,
  });

  async function clientAccess() {
    let payload;
    let applied;
    try {
      applied = await refresh.run(
        (signal) =>
          Promise.all([
            api("/client-access", { signal }),
            can("agents.read")
              ? api("/agents", { signal })
              : Promise.resolve([]),
          ]),
        (value) => {
          payload = value;
        },
      );
    } catch (error) {
      const current = document.querySelector("[data-client-access-page]");
      if (!current) throw error;
      current.dataset.refreshError = "1";
      notify(error.message, "error");
      return false;
    }
    if (!applied) return false;
    const [entries, agents] = payload;
    state.data.agents = agents;
    state.data.clientAccessEntries = entries;
    state.data.clientAccessAddressModes = Object.fromEntries(
      (entries || []).map((entry) => [entry.agent_id, entry.address_mode || "auto"]),
    );
    renderClientAccess();
    return true;
  }

  function renderClientAccess() {
    const addressModes = state.data.clientAccessAddressModes || {};
    const entries = (state.data.clientAccessEntries || []).map((entry) =>
      clientAccessEntryForAddress(entry, addressModes[entry.agent_id] || "auto"),
    );
    const agents = state.data.agents || [];
    const filters = normalizeClientAccessFilters(entries, agents, {
      agent: state.data.accessAgent,
      engine: state.data.accessEngine,
      query: state.data.accessQuery,
    });
    state.data.accessAgent = filters.agent;
    state.data.accessEngine = filters.engine;
    state.data.accessQuery = filters.query;

    const filtered = filterClientAccessEntries(entries, filters);
    const scopedEntries = filters.agent
      ? entries.filter((entry) => entry.agent_id === filters.agent)
      : entries;
    const scopedProfiles = scopedEntries.reduce(
      (total, entry) => total + (entry.profiles || []).length,
      0,
    );
    const results = renderResults(filtered, entries, agents, filters);
    const relevantEngines = new Set(scopedEntries.map((entry) => entry.engine));
    const engineFilters = engines
      .filter((engine) => relevantEngines.has(engine))
      .map((engine) => {
        const count = scopedEntries
          .filter((entry) => entry.engine === engine)
          .reduce((total, entry) => total + (entry.profiles || []).length, 0);
        return `<a class="${filters.engine === engine ? "active" : ""}" href="#client-access" data-filter-engine="${esc(engine)}">${esc(engineName(engine))}<b>${count}</b></a>`;
      })
      .join("");
    const filtersMarkup = scopedEntries.length
      ? `<section class="client-access-toolbar" aria-label="客户端配置筛选"><nav aria-label="按内核筛选"><a class="${filters.engine ? "" : "active"}" href="#client-access" data-filter-engine="">全部<b>${scopedProfiles}</b></a>${engineFilters}</nav><div><a class="button small" href="#substore-sync">Sub-Store 同步</a><button class="button small" type="button" data-refresh-client-access>刷新</button><details class="client-access-search-menu" ${filters.query ? "open" : ""}><summary>${filters.query ? `搜索：${esc(filters.query)}` : "搜索"}</summary><form id="client-search"><input type="search" name="q" value="${esc(filters.query)}" aria-label="搜索入站" placeholder="节点、地址、协议或入站" autocomplete="off"><button class="button primary small" type="submit">搜索</button>${filters.query ? '<button class="button small" type="button" data-clear-search>清除</button>' : ""}</form></details></div></section>`
      : '<section class="client-access-toolbar empty"><span>暂无客户端配置</span><button class="button small" type="button" data-refresh-client-access>刷新</button></section>';
    masonryObserver?.disconnect();
    masonryObserver = null;
    shell(
      `<section class="client-access-workspace compact" data-client-access-page><h1 class="visually-hidden">客户端配置</h1>${filtersMarkup}<div class="client-access-node-grid${filtered.length ? "" : " empty"}">${results}</div></section>`,
      "客户端配置",
    );
    bindClientAccessPage();
  }

  function bindClientAccessMasonry() {
    const grid = document.querySelector(".client-access-node-grid:not(.empty)");
    if (!grid) return;
    const cards = [...grid.querySelectorAll(".client-access-node-card")];
    if (!cards.length) return;

    const layout = () => {
      const styles = getComputedStyle(grid);
      const rowHeight = Number.parseFloat(styles.gridAutoRows) || 1;
      const rowGap = Number.parseFloat(styles.rowGap) || 0;
      cards.forEach((card) => {
        const height = card.getBoundingClientRect().height;
        const span = Math.ceil((height + rowGap) / (rowHeight + rowGap));
        card.style.gridRowEnd = `span ${span}`;
      });
    };

    layout();
    requestAnimationFrame(layout);
    if (typeof ResizeObserver === "function") {
      masonryObserver = new ResizeObserver(layout);
      cards.forEach((card) => masonryObserver.observe(card));
    }
  }

  function renderResults(filtered, entries, agents, filters) {
    const addressModes = state.data.clientAccessAddressModes || {};
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

    return groupClientAccessEntries(filtered)
      .map((group, groupIndex) => {
        const firstEntry = group.entries[0];
        const agent = agents.find((item) => item.id === group.agent_id) || {};
        const agentStatus = agent.status || "unknown";
        const managedAddress = Boolean(agent.labels?.client_address);
        const automaticAddress = firstEntry.address_options?.[0]?.address || firstEntry.address || "";
        const addressChoices = clientAccessAddressChoices(firstEntry);
        const addressMode = addressModes[group.agent_id] || "auto";
        const displayAddressModeField = addressChoices.length
          ? `<label class="client-display-stack-field"><span>客户端地址协议栈</span><select name="address_mode">${addressChoices.map((choice) => `<option value="${esc(choice.value)}" ${choice.value === addressMode ? "selected" : ""}>${esc(choice.label)}</option>`).join("")}</select><small>双栈节点可选择自动、IPv4 或 IPv6，选择会持久保存。</small></label>`
          : "";
        const displayDialogID = `client-display-${groupIndex}`;
        const displayDialogTitleID = `${displayDialogID}-title`;
        const displayButton = can("agents.manage")
          ? `<button class="button small client-display-settings-open" type="button" data-client-display-open="${displayDialogID}" aria-haspopup="dialog" aria-controls="${displayDialogID}">修改显示参数</button>`
          : "";
        const displayDialog = can("agents.manage")
          ? `<dialog class="traffic-edit-dialog client-display-dialog" id="${displayDialogID}" aria-labelledby="${displayDialogTitleID}"><header><span class="traffic-edit-icon" aria-hidden="true">✎</span><div><p class="eyebrow">客户端配置</p><h2 id="${displayDialogTitleID}">修改显示参数</h2><p>${esc(firstEntry.agent_name)} · 名称、连接地址与协议栈</p></div><button class="deploy-command-close" type="button" data-client-display-close aria-label="关闭修改显示参数">×</button></header><form data-client-address-agent="${esc(group.agent_id)}"><div class="traffic-edit-body client-display-dialog-body"><div class="client-display-form-grid"><label><span>客户端节点名称</span><input name="name" maxlength="100" autocomplete="off" value="${esc(firstEntry.client_name || "")}" placeholder="留空使用节点名称"><small>名称会写入客户端 URI，便于在客户端列表中识别。</small></label><label><span>客户端连接地址</span><input name="address" required maxlength="253" autocomplete="off" value="${esc(automaticAddress)}" placeholder="例如 203.0.113.10 或 node.example.com"><small>填写客户端实际访问节点的域名或 IP，不要填写 0.0.0.0。</small></label>${displayAddressModeField}</div></div><footer>${managedAddress ? `<button class="button" type="button" data-clear-client-address="${esc(group.agent_id)}">恢复自动识别</button>` : "<span></span>"}<span></span><button class="button" type="button" data-client-display-close>取消</button><button class="button primary" type="submit">保存参数</button></footer></form></dialog>`
          : "";
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
                const inputID = `client-share-${groupIndex}-${engineIndex}-${profileIndex}`;
                const dialogID = `client-parameters-${groupIndex}-${engineIndex}-${profileIndex}`;
                const dialogTitleID = `${dialogID}-title`;
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
                return `<article class="client-profile-row" data-refresh-key="client-profile-${esc(entry.agent_id)}-${esc(entry.engine)}-${esc(item.tag)}"><header><b>${esc(item.protocol)}</b><small>${esc(item.tag)} · ${esc(item.profile?.format)}</small></header><form class="secret-value-control client-share-control" action="#">${shareControl}<button type="button" data-secret-visibility aria-controls="${inputID}" aria-pressed="false">显示</button><button type="button" data-copy-target="#${inputID}">复制</button></form><div class="client-profile-actions">${displayButton}<button class="button small client-parameter-open" type="button" data-client-parameter-open="${dialogID}" aria-haspopup="dialog" aria-controls="${dialogID}">参数详情 <span aria-hidden="true">→</span></button></div><dialog class="traffic-edit-dialog client-parameter-dialog" id="${dialogID}" aria-labelledby="${dialogTitleID}"><header><span class="traffic-edit-icon client-parameter-icon" aria-hidden="true">&lt;/&gt;</span><div><p class="eyebrow">客户端参数</p><h2 id="${dialogTitleID}">${esc(item.protocol)}</h2><p><span class="engine-badge ${esc(entry.engine)}">${esc(engineName(entry.engine))}</span><span class="client-parameter-meta">${esc(item.tag)} · ${esc(item.profile?.format)}</span></p></div><button class="deploy-command-close" type="button" data-client-parameter-close aria-label="关闭参数详情">×</button></header><div class="traffic-edit-body client-parameter-dialog-body"><dl class="client-parameter-list">${fields || '<div class="empty"><dt>参数</dt><dd>暂无参数</dd></div>'}</dl></div></dialog></article>`;
              })
              .join("");
            return `<section class="client-access-engine-group"><header><span><span class="engine-badge ${esc(entry.engine)}">${esc(engineName(entry.engine))}</span><small>${(entry.profiles || []).length} 个入站</small></span><a href="#agent-config" data-config-agent="${esc(entry.agent_id)}" data-config-engine="${esc(entry.engine)}">服务端配置</a></header><div>${profiles || '<p class="client-access-entry-empty">需要先设置可访问的节点地址。</p>'}</div></section>`;
          })
          .join("");
        return `<article class="client-access-node-card" data-refresh-key="client-access-node-${esc(group.agent_id)}"><header><div class="client-access-node"><span class="node-avatar">●</span><span><strong>${esc(firstEntry.client_name || firstEntry.agent_name)}</strong><small>${esc(agent.os || "节点")} / ${esc(agent.arch || "")} · <code>${esc(firstEntry.address || "未设置地址")}</code></small></span></div><span class="client-access-node-state ${firstEntry.address_required ? "warn" : agentStatus === "online" ? "ok" : "muted"}"><i></i>${statusLabel}</span></header>${addressWarning}<div class="client-access-node-engines">${engineSections}</div>${displayDialog}</article>`;
      })
      .join("");
  }

  function bindClientAccessPage() {
    bindClientAccessMasonry();
    document.querySelectorAll("[data-access-agent]").forEach((button) => {
      button.onclick = (event) => {
        event.preventDefault();
        state.data.accessAgent = button.dataset.accessAgent;
        renderClientAccess();
      };
    });
    document.querySelectorAll("[data-filter-engine]").forEach((button) => {
      button.onclick = (event) => {
        event.preventDefault();
        state.data.accessEngine = button.dataset.filterEngine;
        renderClientAccess();
      };
    });
    bindEvent(document.querySelector("#client-search"), "submit", (event) => {
      event.preventDefault();
      state.data.accessQuery = String(
        new FormData(event.currentTarget).get("q") || "",
      ).trim();
      renderClientAccess();
    });
    bindEvent(document.querySelector("[data-clear-search]"), "click", () => {
      state.data.accessQuery = "";
      const input = document.querySelector("#client-search [name=q]");
      if (input) {
        input.value = "";
        input.defaultValue = "";
      }
      renderClientAccess();
    });
    bindEvent(
      document.querySelector("[data-clear-client-filters]"),
      "click",
      () => {
        state.data.accessEngine = "";
        state.data.accessQuery = "";
        const input = document.querySelector("#client-search [name=q]");
        if (input) {
          input.value = "";
          input.defaultValue = "";
        }
        renderClientAccess();
      },
    );
    bindEvent(
      document.querySelector("[data-refresh-client-access]"),
      "click",
      async (event) => {
        const button = event.currentTarget;
        button.disabled = true;
        button.setAttribute("aria-busy", "true");
        try {
          const refreshed = await clientAccess();
          if (refreshed) notify("客户端配置已刷新");
          else if (button.isConnected) {
            button.disabled = false;
            button.removeAttribute("aria-busy");
          }
        } catch (error) {
          notify(error.message, "error");
          button.disabled = false;
          button.removeAttribute("aria-busy");
        }
      },
    );
    document.querySelectorAll("[data-secret-visibility]").forEach((button) => {
      button.onclick = () => {
        const input = button.parentElement.querySelector("input, textarea");
        const reveal = input.matches("textarea")
          ? input.classList.contains("is-masked")
          : input.type === "password";
        if (input.matches("textarea")) input.classList.toggle("is-masked", !reveal);
        else input.type = reveal ? "text" : "password";
        button.textContent = reveal ? "隐藏" : "显示";
        button.setAttribute("aria-pressed", String(reveal));
      };
    });
    document.querySelectorAll("[data-client-parameter-open]").forEach((button) => {
      button.onclick = () => {
        const dialog = document.getElementById(button.dataset.clientParameterOpen);
        dialog?.showModal();
      };
    });
    document.querySelectorAll("[data-client-parameter-close]").forEach((button) => {
      button.onclick = () => button.closest("dialog")?.close();
    });
    document.querySelectorAll("[data-client-display-open]").forEach((button) => {
      button.onclick = () => document.getElementById(button.dataset.clientDisplayOpen)?.showModal();
    });
    document.querySelectorAll("[data-client-display-close]").forEach((button) => {
      button.onclick = () => button.closest("dialog")?.close();
    });
    document.querySelectorAll(".client-parameter-dialog").forEach((dialog) => {
      dialog.onclick = (event) => {
        if (event.target === dialog) dialog.close();
      };
    });
    document.querySelectorAll(".client-display-dialog").forEach((dialog) => {
      dialog.onclick = (event) => {
        if (event.target === dialog) dialog.close();
      };
    });
    document.querySelectorAll("[data-copy-target]").forEach((button) => {
      button.onclick = async () => {
        const input = document.querySelector(button.dataset.copyTarget);
        try {
          await copyClientValue(input);
          button.textContent = "已复制";
          button.dataset.copyState = "success";
          setTimeout(() => {
            if (!button.isConnected) return;
            button.textContent = "复制";
            delete button.dataset.copyState;
          }, 1600);
        } catch (error) {
          notify(`复制失败：${error.message}`, "error");
        }
      };
    });
    document.querySelectorAll("[data-config-agent]").forEach((link) => {
      link.onclick = () => {
        state.data.agentId = link.dataset.configAgent;
        state.data.engine = link.dataset.configEngine;
      };
    });
    document.querySelectorAll("[data-client-address-agent]").forEach((form) => {
      bindEvent(form, "submit", async (event) => {
        event.preventDefault();
        const button = form.querySelector("button[type=submit]");
        const formData = new FormData(form);
        const address = String(formData.get("address") || "").trim();
        const name = String(formData.get("name") || "").trim();
        const payload = { address, name };
        if (formData.has("address_mode")) {
          payload.address_mode = String(formData.get("address_mode") || "auto");
        }
        if (button) button.disabled = true;
        try {
          await api(
            `/agents/${encodeURIComponent(form.dataset.clientAddressAgent)}/client-address`,
            { method: "PUT", body: JSON.stringify(payload) },
          );
          const input = form.elements.namedItem("address");
          if (input) {
            input.value = address;
            input.defaultValue = address;
          }
          form.closest("dialog")?.close();
          notify("客户端显示参数已保存");
          await clientAccess();
        } catch (error) {
          notify(error.message, "error");
          if (button) button.disabled = false;
        }
      });
    });
    document.querySelectorAll("[data-clear-client-address]").forEach((button) => {
      bindEvent(button, "click", async () => {
        button.disabled = true;
        try {
          await api(
            `/agents/${encodeURIComponent(button.dataset.clearClientAddress)}/client-address`,
            { method: "PUT", body: JSON.stringify({ address: "" }) },
          );
          const input = button.form?.elements.namedItem("address");
          if (input) {
            input.value = "";
            input.defaultValue = "";
          }
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
