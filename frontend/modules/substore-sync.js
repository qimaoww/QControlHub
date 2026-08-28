import { bindEvent, createRefreshChannel } from "./refresh.js";

export function filterSubStoreProfiles(profiles, agentID = "", search = "") {
  const normalizedQuery = String(search || "").trim().toLowerCase();
  return (profiles || []).filter((profile) => {
    if (agentID && profile.agent_id !== agentID) return false;
    if (!normalizedQuery) return true;
    return [
      profile.agent_name,
      profile.default_name,
      profile.custom_name,
      profile.profile_tag,
      profile.protocol,
      profile.engine,
    ]
      .join(" ")
      .toLowerCase()
      .includes(normalizedQuery);
  });
}

export function subStoreSelectionPayload(profiles) {
  return (profiles || [])
    .filter((profile) => profile.selected)
    .map((profile) => ({
      agent_id: profile.agent_id,
      engine: profile.engine,
      profile_tag: profile.profile_tag,
      custom_name: String(profile.custom_name || profile.default_name || "").trim(),
    }));
}

export function installSubStoreSync(ctx) {
  const { api, state, can, esc, engineName, notify, shell } = ctx;
  let agentFilter = "";
  let query = "";
  let masonryObserver = null;
  let pendingSelectionSave = null;
  const refresh = createRefreshChannel({
    isCurrent: () => state.route === "substore-sync",
    getScope: () => state.navigationEpoch,
  });

  async function subStoreSync() {
    let resource;
    const applied = await refresh.run(
      (signal) => api("/substore-sync", { signal }),
      (value) => {
        resource = value;
      },
    );
    if (!applied) return false;
    state.data.subStoreSync = resource;
    render();
    return true;
  }

  function visibleProfiles(profiles) {
    return filterSubStoreProfiles(profiles, agentFilter, query);
  }

  function render() {
    const resource = state.data.subStoreSync || {};
    const settings = resource.settings || {};
    const profiles = resource.profiles || [];
    const selected = profiles.filter((profile) => profile.selected);
    const availableSelected = selected.filter((profile) => profile.available);
    const agents = [];
    const seenAgents = new Set();
    for (const profile of profiles) {
      if (!profile.agent_id || seenAgents.has(profile.agent_id)) continue;
      seenAgents.add(profile.agent_id);
      agents.push({ id: profile.agent_id, name: profile.agent_name || "已删除节点" });
    }
    if (agentFilter && !seenAgents.has(agentFilter)) agentFilter = "";
    const filtered = visibleProfiles(profiles);
    const grouped = new Map();
    for (const profile of filtered) {
      const key = profile.agent_id || "missing";
      if (!grouped.has(key)) grouped.set(key, []);
      grouped.get(key).push(profile);
    }

    const statusClass = !settings.configured
      ? "muted"
      : settings.last_sync_status === "failed"
        ? "bad"
        : "ok";
    const statusText = !settings.configured
      ? "未配置"
      : settings.last_sync_status === "failed"
        ? "上次同步失败"
        : settings.last_sync_status === "success"
          ? "已连接"
          : "等待首次同步";
    const syncMeta = settings.last_synced_at
      ? `上次同步 ${new Date(settings.last_synced_at).toLocaleString()} · 当前已选 ${availableSelected.length}`
      : `${availableSelected.length} 个待同步节点`;

    const cards = [...grouped.values()]
      .map((items) => {
        const first = items[0];
        const checked = items.filter((item) => item.selected).length;
        const rows = items
          .map((profile) => {
            const unavailable = !profile.available;
            const name = profile.custom_name || profile.default_name || profile.profile_tag;
            return `<div class="substore-node-row ${profile.selected ? "selected" : ""} ${unavailable ? "unavailable" : ""}" data-substore-key="${esc(encodeURIComponent(`${profile.agent_id}\u0000${profile.engine}\u0000${profile.profile_tag}`))}">
              <label class="substore-node-toggle"><input type="checkbox" data-substore-select ${profile.selected ? "checked" : ""} ${unavailable ? "disabled" : ""}><span></span></label>
              <span class="engine-badge ${esc(profile.engine)}">${esc(engineName(profile.engine))}</span>
              <span class="substore-node-source"><b>${esc(profile.profile_tag)}</b><small>${unavailable ? "源节点已失效" : esc(profile.protocol)}</small></span>
              ${profile.selected && !unavailable ? `<label class="substore-node-name"><span>同步名称</span><input data-substore-name maxlength="100" value="${esc(name)}" aria-label="${esc(profile.profile_tag)} 同步名称"></label>` : `<span class="substore-node-preview">${esc(name)}</span>`}
              ${profile.selected ? `<button class="substore-remove" type="button" data-substore-remove aria-label="移除 ${esc(name)}">移除</button>` : `<button class="button small" type="button" data-substore-add ${unavailable ? "disabled" : ""}>加入同步</button>`}
            </div>`;
          })
          .join("");
        return `<article class="substore-agent-card"><header><span class="node-avatar">●</span><span><strong>${esc(first.agent_name || "已删除节点")}</strong><small>${items.length} 个客户端节点</small></span><b>${checked}/${items.length}</b></header><div>${rows}</div></article>`;
      })
      .join("");

    const agentOptions = agents
      .map((agent) => `<option value="${esc(agent.id)}" ${agentFilter === agent.id ? "selected" : ""}>${esc(agent.name)}</option>`)
      .join("");
    const empty = `<section class="substore-empty"><strong>没有匹配的客户端节点</strong><span>请调整节点或搜索条件。</span></section>`;
    const manage = can("settings.manage");
    masonryObserver?.disconnect();
    masonryObserver = null;
    shell(
      `<section class="substore-workspace" data-substore-page>
        <section class="substore-status-bar">
          <div class="substore-connection"><i class="${statusClass}"></i><span><b>Sub-Store</b><small>${esc(settings.endpoint_hint || "尚未设置连接")}</small></span><em>${statusText}</em></div>
          <div class="substore-subscription"><span>订阅</span><b>${esc(settings.subscription_name || "—")}</b><small>${esc(syncMeta)}</small></div>
          ${manage ? `<div class="substore-status-actions"><button class="button small" type="button" data-substore-test ${settings.configured ? "" : "disabled"}>测试连接</button><button class="button small" type="button" data-substore-settings>连接设置</button><button class="button primary small" type="button" data-substore-run ${settings.configured && availableSelected.length && availableSelected.length === selected.length ? "" : "disabled"}>同步 ${availableSelected.length} 个节点</button></div>` : ""}
        </section>
        ${settings.last_sync_status === "failed" && settings.last_sync_error ? `<p class="substore-error">${esc(settings.last_sync_error)}</p>` : ""}
        <section class="substore-filter-bar">
          <label><span>节点</span><select data-substore-agent-filter><option value="">全部节点</option>${agentOptions}</select></label>
          <label class="substore-search"><span>搜索</span><input type="search" data-substore-query value="${esc(query)}" placeholder="节点、协议或入站"></label>
          <div><span>同步清单</span><b>${availableSelected.length}</b><small>/ ${profiles.filter((profile) => profile.available).length}</small></div>
        </section>
        <div class="substore-agent-grid">${cards || empty}</div>
        ${manage ? `<dialog class="traffic-edit-dialog substore-settings-dialog" data-substore-settings-dialog aria-labelledby="substore-settings-title"><header><span class="traffic-edit-icon" aria-hidden="true">↻</span><div><p class="eyebrow">Sub-Store</p><h2 id="substore-settings-title">连接设置</h2></div><button class="deploy-command-close" type="button" data-substore-settings-close aria-label="关闭连接设置">×</button></header><form data-substore-settings-form><div class="traffic-edit-body"><label>后端地址<input type="password" name="endpoint_url" autocomplete="new-password" ${settings.configured ? "" : "required"} placeholder="${settings.configured ? "留空保持当前地址" : "https://substore.example.com/路径口令"}"><small>${esc(settings.endpoint_hint || "地址必须包含后端路径口令")}</small></label><label>订阅名称<input name="subscription_name" required maxlength="100" value="${esc(settings.subscription_name || "QControlHub")}"></label></div><footer><button class="button" type="button" data-substore-settings-close>取消</button><button class="button primary" type="submit">保存设置</button></footer></form></dialog>` : ""}
      </section>`,
      "Sub-Store 同步",
      { viewKey: "substore-sync" },
    );
    bindPage();
  }

  function bindSubStoreMasonry() {
    const grid = document.querySelector(".substore-agent-grid:not(.empty)");
    if (!grid) return;
    const cards = [...grid.querySelectorAll(".substore-agent-card")];
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

  async function saveSelections(profiles) {
    const selections = subStoreSelectionPayload(profiles);
    await api("/substore-sync/selections", {
      method: "PUT",
      body: JSON.stringify({ selections }),
    });
    notify("同步清单已更新");
    await subStoreSync();
  }

  function trackSelectionSave(profiles) {
    const operation = saveSelections(profiles);
    pendingSelectionSave = operation;
    operation.then(
      () => {
        if (pendingSelectionSave === operation) pendingSelectionSave = null;
      },
      () => {
        if (pendingSelectionSave === operation) pendingSelectionSave = null;
      },
    );
    return operation;
  }

  function profileForRow(row) {
    const resource = state.data.subStoreSync || {};
    const profiles = resource.profiles || [];
    const [agentID, engine, tag] = decodeURIComponent(
      String(row.dataset.substoreKey || ""),
    ).split("\u0000");
    return profiles.find(
      (profile) => profile.agent_id === agentID && profile.engine === engine && profile.profile_tag === tag,
    );
  }

  function bindPage() {
    const resource = state.data.subStoreSync || {};
    const profiles = resource.profiles || [];
    bindSubStoreMasonry();
    bindEvent(document.querySelector("[data-substore-agent-filter]"), "change", (event) => {
      agentFilter = event.currentTarget.value;
      render();
    });
    bindEvent(document.querySelector("[data-substore-query]"), "input", (event) => {
      query = event.currentTarget.value;
      render();
      const input = document.querySelector("[data-substore-query]");
      input?.focus();
      input?.setSelectionRange(query.length, query.length);
    });
    document.querySelectorAll("[data-substore-add], [data-substore-select]").forEach((control) => {
      control.onclick = async () => {
        const profile = profileForRow(control.closest("[data-substore-key]"));
        if (!profile) return;
        profile.selected = control.matches("[data-substore-select]") ? control.checked : true;
        if (profile.selected && !profile.custom_name) profile.custom_name = profile.default_name;
        try {
          await trackSelectionSave(profiles);
        } catch (error) {
          notify(error.message, "error");
          await subStoreSync();
        }
      };
    });
    document.querySelectorAll("[data-substore-remove]").forEach((button) => {
      button.onclick = async () => {
        const profile = profileForRow(button.closest("[data-substore-key]"));
        if (!profile) return;
        profile.selected = false;
        try {
          await trackSelectionSave(profiles);
        } catch (error) {
          notify(error.message, "error");
          await subStoreSync();
        }
      };
    });
    document.querySelectorAll("[data-substore-name]").forEach((input) => {
      input.oninput = () => {
        const profile = profileForRow(input.closest("[data-substore-key]"));
        if (profile) profile.custom_name = input.value;
      };
      input.onchange = async () => {
        const profile = profileForRow(input.closest("[data-substore-key]"));
        if (!profile) return;
        const previous = input.defaultValue || profile.default_name;
        profile.custom_name = input.value.trim();
        try {
          await trackSelectionSave(profiles);
        } catch (error) {
          profile.custom_name = previous;
          notify(error.message, "error");
          await subStoreSync();
        }
      };
    });
    const dialog = document.querySelector("[data-substore-settings-dialog]");
    bindEvent(document.querySelector("[data-substore-settings]"), "click", () => dialog?.showModal());
    document.querySelectorAll("[data-substore-settings-close]").forEach((button) => {
      button.onclick = () => dialog?.close();
    });
    bindEvent(document.querySelector("[data-substore-settings-form]"), "submit", async (event) => {
      event.preventDefault();
      const form = event.currentTarget;
      const submit = form.querySelector("button[type=submit]");
      submit.disabled = true;
      try {
        const data = Object.fromEntries(new FormData(form));
        await api("/substore-sync/settings", { method: "PUT", body: JSON.stringify(data) });
        dialog?.close();
        notify("Sub-Store 连接设置已保存");
        await subStoreSync();
      } catch (error) {
        notify(error.message, "error");
        submit.disabled = false;
      }
    });
    bindEvent(document.querySelector("[data-substore-test]"), "click", async (event) => {
      const button = event.currentTarget;
      button.disabled = true;
      try {
        await api("/substore-sync/test", { method: "POST" });
        notify("Sub-Store 连接正常");
      } catch (error) {
        notify(error.message, "error");
      } finally {
        if (button.isConnected) button.disabled = false;
      }
    });
    bindEvent(document.querySelector("[data-substore-run]"), "click", async (event) => {
      const button = event.currentTarget;
      button.disabled = true;
      try {
        if (pendingSelectionSave) await pendingSelectionSave;
        const result = await api("/substore-sync/run", { method: "POST" });
        notify(`${result.node_count} 个节点已同步到 Sub-Store`);
        await subStoreSync();
      } catch (error) {
        notify(error.message, "error");
        await subStoreSync();
      }
    });
  }

  return subStoreSync;
}
