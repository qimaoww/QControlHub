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
      profile.port,
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
  let activeTargetID = "";
  let masonryObserver = null;
  let pendingSelectionSave = null;
  const refresh = createRefreshChannel({
    isCurrent: () => state.route === "substore-sync",
    getScope: () => state.navigationEpoch,
  });

  async function subStoreSync() {
    let resource;
    const applied = await refresh.run(
      (signal) =>
        api(
          `/substore-sync${activeTargetID ? `?target_id=${encodeURIComponent(activeTargetID)}` : ""}`,
          { signal },
        ),
      (value) => {
        resource = value;
      },
    );
    if (!applied) return false;
    activeTargetID = resource.target_id || "";
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
    const targets = resource.targets || [];
    const activeTarget = targets.find((target) => target.id === resource.target_id) || null;
    const profiles = resource.profiles || [];
    const selected = profiles.filter((profile) => profile.selected);
    const availableSelected = selected.filter((profile) => profile.available);
    const agents = [];
    const seenAgents = new Set();
    agentFilter = String(state.data.subStoreAgent || agentFilter || "");
    for (const profile of profiles) {
      if (!profile.agent_id || seenAgents.has(profile.agent_id)) continue;
      seenAgents.add(profile.agent_id);
      agents.push({
        id: profile.agent_id,
        name: profile.agent_name || "已删除节点",
        status: profile.agent_status || "unknown",
      });
    }
    if (agentFilter && !seenAgents.has(agentFilter)) {
      agentFilter = "";
      state.data.subStoreAgent = "";
    }
    const filtered = visibleProfiles(profiles);
    const grouped = new Map();
    for (const profile of filtered) {
      const key = profile.agent_id || "missing";
      if (!grouped.has(key)) grouped.set(key, []);
      grouped.get(key).push(profile);
    }

    const statusClass = settings.configured ? "ok" : "muted";
    const statusText = settings.configured ? "已配置" : "未配置";
    const targetStatus = !activeTarget
      ? "尚未创建同步组"
      : activeTarget.last_sync_status === "failed"
        ? "上次同步失败"
        : activeTarget.last_synced_at
          ? `上次同步 ${new Date(activeTarget.last_synced_at).toLocaleString()}`
          : "等待首次同步";
    const targetTabs = targets
      .map(
        (target) =>
          `<button class="${target.id === activeTargetID ? "active" : ""}" type="button" data-substore-target="${esc(target.id)}"><span>${esc(target.display_name || target.subscription_name)}</span><b>${Number(target.selection_count || 0)}</b></button>`,
      )
      .join("");

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
              <span class="substore-node-source"><b>${esc(profile.profile_tag)}</b><small>${unavailable ? "源节点已失效" : `${esc(profile.protocol)}${profile.port ? ` · :${Number(profile.port)}` : ""}`}</small></span>
              ${profile.selected && !unavailable ? `<label class="substore-node-name"><span>同步名称</span><input data-substore-name maxlength="100" value="${esc(name)}" aria-label="${esc(profile.profile_tag)} 同步名称"></label>` : `<span class="substore-node-preview">${esc(name)}</span>`}
              ${profile.selected ? `<button class="substore-remove" type="button" data-substore-remove aria-label="移除 ${esc(name)}">移除</button>` : `<button class="button small" type="button" data-substore-add ${unavailable ? "disabled" : ""}>加入同步</button>`}
            </div>`;
          })
          .join("");
        return `<article class="substore-agent-card"><header><span class="node-avatar">●</span><span><strong>${esc(first.agent_name || "已删除节点")}</strong><small>${items.length} 个客户端节点</small></span><b>${checked}/${items.length}</b></header><div>${rows}</div></article>`;
      })
      .join("");

    const empty = `<section class="substore-empty"><strong>没有匹配的客户端节点</strong><span>请调整节点或搜索条件。</span></section>`;
    const manage = can("settings.manage");
    masonryObserver?.disconnect();
    masonryObserver = null;
    shell(
      `<section class="substore-workspace" data-substore-page>
        <section class="substore-status-bar">
          <div class="substore-connection"><i class="${statusClass}"></i><span><b>Sub-Store</b><small>${esc(settings.endpoint_hint || "尚未设置连接")}</small></span><em>${statusText}</em></div>
          <div class="substore-subscription"><span>当前同步组</span><b>${esc(activeTarget?.display_name || activeTarget?.subscription_name || "—")}</b><small>${esc(activeTarget && activeTarget.display_name !== activeTarget.subscription_name ? `Sub-Store：${activeTarget.subscription_name} · ${targetStatus}` : targetStatus)}</small></div>
          ${manage ? `<div class="substore-status-actions"><button class="button small" type="button" data-substore-test ${settings.configured ? "" : "disabled"}>测试连接</button><button class="button small" type="button" data-substore-settings>连接设置</button>${activeTarget ? '<button class="button small" type="button" data-substore-target-edit>组设置</button>' : ""}<button class="button primary small" type="button" data-substore-run ${settings.configured && activeTarget && availableSelected.length && availableSelected.length === selected.length ? "" : "disabled"}>同步当前组 · ${availableSelected.length}</button></div>` : ""}
        </section>
        ${activeTarget?.last_sync_status === "failed" && activeTarget.last_sync_error ? `<p class="substore-error">${esc(activeTarget.last_sync_error)}</p>` : ""}
        <section class="substore-target-bar"><nav aria-label="同步组">${targetTabs || '<span>还没有同步组</span>'}</nav><label class="substore-target-search"><input type="search" data-substore-query value="${esc(query)}" aria-label="搜索客户端节点" placeholder="搜索节点、协议、入站或端口"></label>${manage ? '<button class="button small" type="button" data-substore-target-add>＋ 新建同步组</button>' : ""}</section>
        <div class="substore-agent-grid${activeTarget ? "" : " empty"}">${activeTarget ? cards || empty : '<section class="substore-empty"><strong>先新建一个同步组</strong><span>每个同步组独立选择节点并同步。</span></section>'}</div>
        ${manage ? `<dialog class="traffic-edit-dialog substore-settings-dialog" data-substore-settings-dialog aria-labelledby="substore-settings-title"><header><span class="traffic-edit-icon" aria-hidden="true">↻</span><div><p class="eyebrow">Sub-Store</p><h2 id="substore-settings-title">连接设置</h2></div><button class="deploy-command-close" type="button" data-substore-settings-close aria-label="关闭连接设置">×</button></header><form data-substore-settings-form><div class="traffic-edit-body"><label>后端地址<input type="password" name="endpoint_url" autocomplete="new-password" ${settings.configured ? "" : "required"} placeholder="${settings.configured ? "留空保持当前地址" : "https://substore.example.com/路径口令"}"><small>${esc(settings.endpoint_hint || "地址必须包含后端路径口令")}</small></label></div><footer><button class="button" type="button" data-substore-settings-close>取消</button><button class="button primary" type="submit">保存设置</button></footer></form></dialog><dialog class="traffic-edit-dialog substore-settings-dialog" data-substore-target-dialog aria-labelledby="substore-target-title"><header><span class="traffic-edit-icon" aria-hidden="true">◎</span><div><p class="eyebrow">同步目标</p><h2 id="substore-target-title">同步组设置</h2></div><button class="deploy-command-close" type="button" data-substore-target-close aria-label="关闭同步组设置">×</button></header><form data-substore-target-form><input type="hidden" name="target_id"><div class="traffic-edit-body"><label>同步组名称<input name="display_name" required maxlength="100" autocomplete="off" placeholder="例如：香港节点"></label><fieldset class="substore-rename-options" data-substore-rename-options hidden><legend>改名范围</legend><label><input type="radio" name="rename_remote" value="false" checked><span><b>仅修改面板名称</b><small>Sub-Store 组名称保持不变</small></span></label><label><input type="radio" name="rename_remote" value="true"><span><b>同时修改远端组名</b><small>同步更新 Sub-Store 订阅组名称</small></span></label></fieldset><div class="substore-remote-import" data-substore-remote-import ${settings.configured ? "" : "hidden"}><span><b>Sub-Store 已有组</b><small>读取远端订阅组并加入同步组</small></span><select data-substore-remote-select aria-label="Sub-Store 已有组" disabled><option>读取中…</option></select><button class="button small" type="button" data-substore-remote-import-button disabled>加入同步组</button></div></div><footer><button class="button danger" type="button" data-substore-target-delete hidden>移除同步组</button><span></span><button class="button" type="button" data-substore-target-close>取消</button><button class="button primary" type="submit">保存同步组</button></footer></form></dialog><dialog class="traffic-edit-dialog substore-settings-dialog" data-substore-delete-dialog aria-labelledby="substore-delete-title"><header><span class="traffic-edit-icon danger" aria-hidden="true">×</span><div><p class="eyebrow">移除同步组</p><h2 id="substore-delete-title">确认移除</h2></div><button class="deploy-command-close" type="button" data-substore-delete-close aria-label="关闭移除确认">×</button></header><form data-substore-delete-form><div class="traffic-edit-body"><p class="substore-delete-message">仅移除面板中的同步关系，Sub-Store 远端组会保留。</p></div><footer><button class="button" type="button" data-substore-delete-close>取消</button><button class="button danger" type="submit">确认移除</button></footer></form></dialog>` : ""}
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

  async function saveSelections(targetID, selections) {
    await api("/substore-sync/selections", {
      method: "PUT",
      body: JSON.stringify({ target_id: targetID, selections }),
    });
    notify("同步清单已更新");
    await subStoreSync();
  }

  function trackSelectionSave(profiles) {
    const targetID = activeTargetID;
    const selections = subStoreSelectionPayload(profiles);
    // Keep rapid checkbox/name edits in browser order. Replacing the complete
    // selection set concurrently could otherwise let an older response win.
    const previous = pendingSelectionSave?.catch(() => {}) || Promise.resolve();
    const operation = previous.then(() => saveSelections(targetID, selections));
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
    const targets = resource.targets || [];
    const activeTarget = targets.find((target) => target.id === activeTargetID) || null;
    bindSubStoreMasonry();
    document.querySelectorAll("[data-substore-target]").forEach((button) => {
      button.onclick = async () => {
        if (button.dataset.substoreTarget === activeTargetID) return;
        activeTargetID = button.dataset.substoreTarget || "";
        pendingSelectionSave = null;
        try {
          await subStoreSync();
        } catch (error) {
          notify(error.message, "error");
        }
      };
    });
    document.querySelectorAll("[data-substore-agent]").forEach((link) => {
      link.onclick = (event) => {
        event.preventDefault();
        agentFilter = link.dataset.substoreAgent || "";
        state.data.subStoreAgent = agentFilter;
        render();
      };
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
    const targetDialog = document.querySelector("[data-substore-target-dialog]");
    const targetForm = targetDialog?.querySelector("[data-substore-target-form]");
    const loadRemoteTargets = async () => {
      const select = targetDialog?.querySelector("[data-substore-remote-select]");
      const button = targetDialog?.querySelector("[data-substore-remote-import-button]");
      if (!select || !button) return;
      select.disabled = true;
      button.disabled = true;
      select.innerHTML = "<option>读取中…</option>";
      try {
        const remoteTargets = await api("/substore-sync/remote-targets");
        const available = (remoteTargets || []).filter((target) => !target.imported);
        select.innerHTML = available.length
          ? available.map((target) => `<option value="${esc(target.subscription_name)}">${esc(target.subscription_name)} · ${Number(target.node_count || 0)} 个节点</option>`).join("")
          : "<option value=\"\">没有可加入的组</option>";
        select.disabled = !available.length;
        button.disabled = !available.length;
      } catch (error) {
        select.innerHTML = "<option value=\"\">读取失败</option>";
        notify(error.message, "error");
      }
    };
    const openTargetDialog = (target = null) => {
      if (!targetDialog || !targetForm) return;
      targetForm.reset();
      targetForm.elements.target_id.value = target?.id || "";
      targetForm.elements.display_name.value = target?.display_name || target?.subscription_name || "";
      const title = targetDialog.querySelector("#substore-target-title");
      if (title) title.textContent = target ? "同步组设置" : "新建同步组";
      const remove = targetDialog.querySelector("[data-substore-target-delete]");
      if (remove) remove.hidden = !target;
      const renameOptions = targetDialog.querySelector("[data-substore-rename-options]");
      if (renameOptions) renameOptions.hidden = !target;
      targetDialog.showModal();
      targetForm.elements.display_name.focus();
      loadRemoteTargets();
    };
    bindEvent(document.querySelector("[data-substore-target-add]"), "click", () => openTargetDialog());
    bindEvent(document.querySelector("[data-substore-target-edit]"), "click", () => openTargetDialog(activeTarget));
    document.querySelectorAll("[data-substore-target-close]").forEach((button) => {
      button.onclick = () => targetDialog?.close();
    });
    bindEvent(targetForm, "submit", async (event) => {
      event.preventDefault();
      const form = event.currentTarget;
      const submit = form.querySelector("button[type=submit]");
      const targetID = String(form.elements.target_id.value || "");
      const displayName = String(form.elements.display_name.value || "").trim();
      const renameRemote = targetID && new FormData(form).get("rename_remote") === "true";
      submit.disabled = true;
      try {
        const target = await api(
          targetID ? `/substore-sync/targets/${encodeURIComponent(targetID)}` : "/substore-sync/targets",
          { method: targetID ? "PUT" : "POST", body: JSON.stringify({ display_name: displayName, rename_remote: Boolean(renameRemote) }) },
        );
        activeTargetID = target.id;
        targetDialog?.close();
        notify(targetID ? "同步组已更新" : "同步组已创建");
        await subStoreSync();
      } catch (error) {
        notify(error.message, "error");
        submit.disabled = false;
      }
    });
    bindEvent(document.querySelector("[data-substore-remote-import-button]"), "click", async (event) => {
      const select = targetDialog?.querySelector("[data-substore-remote-select]");
      const subscriptionName = String(select?.value || "");
      if (!subscriptionName) return;
      const button = event.currentTarget;
      button.disabled = true;
      try {
        const target = await api("/substore-sync/targets/import", {
          method: "POST",
          body: JSON.stringify({ subscription_name: subscriptionName }),
        });
        activeTargetID = target.id;
        targetDialog?.close();
        notify(`已加入 Sub-Store 组“${target.subscription_name}”`);
        await subStoreSync();
      } catch (error) {
        notify(error.message, "error");
        button.disabled = false;
      }
    });
    const deleteDialog = document.querySelector("[data-substore-delete-dialog]");
    bindEvent(document.querySelector("[data-substore-target-delete]"), "click", () => {
      if (!activeTarget || !deleteDialog) return;
      targetDialog?.close();
      const title = deleteDialog.querySelector("#substore-delete-title");
      if (title) title.textContent = `移除“${activeTarget.display_name || activeTarget.subscription_name}”`;
      deleteDialog.querySelector("[data-substore-delete-form]")?.reset();
      deleteDialog.showModal();
    });
    document.querySelectorAll("[data-substore-delete-close]").forEach((button) => {
      button.onclick = () => deleteDialog?.close();
    });
    bindEvent(document.querySelector("[data-substore-delete-form]"), "submit", async (event) => {
      event.preventDefault();
      if (!activeTarget) return;
      const form = event.currentTarget;
      const button = form.querySelector("button[type=submit]");
      button.disabled = true;
      try {
        await api(`/substore-sync/targets/${encodeURIComponent(activeTarget.id)}`, { method: "DELETE" });
        activeTargetID = "";
        deleteDialog?.close();
        notify("同步组已从面板移除");
        await subStoreSync();
      } catch (error) {
        notify(error.message, "error");
        button.disabled = false;
      }
    });
    bindEvent(document.querySelector("[data-substore-run]"), "click", async (event) => {
      const button = event.currentTarget;
      button.disabled = true;
      try {
        if (pendingSelectionSave) await pendingSelectionSave;
        const result = await api("/substore-sync/run", {
          method: "POST",
          body: JSON.stringify({ target_id: activeTargetID }),
        });
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
