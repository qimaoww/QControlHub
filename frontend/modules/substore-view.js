import { filterSubStoreProfiles, groupSubStoreProfiles, subStoreProfileNodeCount, subStoreAddressChoices, subStoreAddressModeLabel } from "./substore-model.js";
export function createSubStoreView({ state, can, esc, engineName, shell }, { lifecycle, masonry }) {
  function visibleProfiles(profiles) {
    return filterSubStoreProfiles(profiles, lifecycle.agentFilter, lifecycle.query);
  }

  function render() {
    const resource = state.data.subStoreSync || {};
    const settings = resource.settings || {};
    const targets = resource.targets || [];
    const manage = can("settings.manage");
    const manageGlobal = manage;
    const activeTarget = targets.find((target) => target.id === resource.target_id) || null;
    const profiles = resource.profiles || [];
    const selected = profiles.filter((profile) => profile.selected);
    const availableSelected = selected.filter((profile) =>
      profile.available && !(activeTarget?.sync_format === "mihomo" && profile.mihomo_error));
    const selectedNodeCount = availableSelected.reduce(
      (total, profile) => total + subStoreProfileNodeCount(profile),
      0,
    );
    const canRunSync = Boolean(
      settings.configured
      && activeTarget
      && availableSelected.length === selected.length
      && (availableSelected.length > 0 || activeTarget.last_synced_at),
    );
    const agents = [];
    const seenAgents = new Set();
    lifecycle.agentFilter = String(state.data.subStoreAgent || lifecycle.agentFilter || "");
    for (const profile of profiles) {
      if (!profile.agent_id || seenAgents.has(profile.agent_id)) continue;
      seenAgents.add(profile.agent_id);
      agents.push({
        id: profile.agent_id,
        name: profile.agent_name || "源节点不可用",
        status: profile.agent_status || "unknown",
      });
    }
    if (lifecycle.agentFilter && !seenAgents.has(lifecycle.agentFilter)) {
      lifecycle.agentFilter = "";
      state.data.subStoreAgent = "";
    }
    const filtered = visibleProfiles(profiles);
    const grouped = groupSubStoreProfiles(filtered, agents);

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
          `<button class="${target.id === lifecycle.activeTargetID ? "active" : ""}" type="button" data-substore-target="${esc(target.id)}"><span>${esc(target.display_name || target.subscription_name)}</span><b>${Number(target.selection_count || 0)}</b></button>`,
      )
      .join("");

    const cards = grouped
      .map((group) => {
        const items = group.profiles;
        const first = items[0];
        const checked = items.filter((item) => item.selected).length;
        const rows = items
          .map((profile) => {
            const formatError = activeTarget?.sync_format === "mihomo" ? profile.mihomo_error : "";
            const unavailable = !profile.available || Boolean(formatError);
            const name = profile.custom_name || profile.default_name || profile.profile_tag;
            const addressChoices = subStoreAddressChoices(profile);
            const addressMode = profile.address_mode || "auto";
            const addressField = addressChoices.length > 1
              ? `<label><span>同步地址</span><select name="address_mode" aria-label="${esc(profile.profile_tag)} 同步地址">${addressChoices.map((choice) => `<option value="${esc(choice.value)}" ${choice.value === addressMode ? "selected" : ""}>${esc(choice.label)}</option>`).join("")}</select></label>`
              : `<input type="hidden" name="address_mode" value="${esc(addressMode)}">`;
            const settingsRow = manage && profile.selected && !unavailable
              ? `<form class="substore-node-settings-row" data-substore-parameters-form><label><span>同步名称</span><input name="custom_name" required maxlength="100" autocomplete="off" value="${esc(name)}"></label>${addressField}<button class="button primary small" type="submit">保存参数</button></form>`
              : "";
            return `<div class="substore-node-item ${profile.selected ? "selected" : ""} ${unavailable ? "unavailable" : ""}" data-substore-key="${esc(encodeURIComponent(`${profile.agent_id}\u0000${profile.engine}\u0000${profile.profile_tag}\u0000${profile.config_id || ""}`))}"><div class="substore-node-row">
                <label class="substore-node-toggle"><input type="checkbox" data-substore-select ${profile.selected ? "checked" : ""} ${unavailable || !manage ? "disabled" : ""}><span></span></label>
                <span class="engine-badge ${esc(profile.engine)}">${esc(engineName(profile.engine))}</span>
                <span class="substore-node-source"><b>${esc(profile.profile_tag)}</b><small>${formatError ? esc(formatError) : unavailable ? "源配置已变更或不可用" : `${esc(profile.protocol)}${profile.port ? ` · :${Number(profile.port)}` : ""}`}</small></span>
                <span class="substore-node-preview">${esc(profile.selected ? `${name} · ${subStoreAddressModeLabel(addressMode)}` : name)}</span>
                ${!manage ? "" : profile.selected ? `<button class="substore-remove" type="button" data-substore-remove aria-label="移除 ${esc(name)}">移除</button>` : `<button class="button small" type="button" data-substore-add ${unavailable ? "disabled" : ""}>加入同步</button>`}
              </div>${settingsRow}
            </div>`;
          })
          .join("");
        return `<article class="substore-agent-card"><header><span class="node-avatar">●</span><span><strong>${esc(first.agent_name || "源节点不可用")}</strong><small>${items.length} 个客户端节点</small></span><b>${checked}/${items.length}</b></header><div>${rows}</div></article>`;
      })
      .join("");

    const empty = `<section class="substore-empty"><strong>没有匹配的客户端节点</strong><span>请调整节点或搜索条件。</span></section>`;
    masonry.disconnect();
    shell(
      `<section class="substore-workspace" data-substore-page>
        <section class="substore-status-bar">
          <div class="substore-connection"><i class="${statusClass}"></i><span><b>Sub-Store</b><small>${esc(settings.endpoint_hint || "尚未设置连接")}</small></span><em>${statusText}</em></div>
          <div class="substore-subscription"><span>当前同步组</span><b>${esc(activeTarget?.display_name || activeTarget?.subscription_name || "—")}</b><small>${esc(activeTarget && activeTarget.display_name !== activeTarget.subscription_name ? `Sub-Store：${activeTarget.subscription_name} · ${targetStatus}` : targetStatus)}</small></div>
          ${manage ? `<div class="substore-status-actions"><button class="button small" type="button" data-substore-test ${settings.configured ? "" : "disabled"}>测试连接</button>${manageGlobal ? '<button class="button small" type="button" data-substore-settings>连接设置</button>' : ""}${activeTarget ? '<button class="button small" type="button" data-substore-target-edit>组设置</button>' : ""}<button class="button primary small" type="button" data-substore-run ${canRunSync ? "" : "disabled"}>同步当前组 · ${selectedNodeCount}</button></div>` : ""}
        </section>
        ${activeTarget?.last_sync_status === "failed" && activeTarget.last_sync_error ? `<p class="substore-error">${esc(activeTarget.last_sync_error)}</p>` : ""}
        <section class="substore-target-bar"><nav aria-label="同步组">${targetTabs || '<span>还没有同步组</span>'}</nav><label class="substore-target-search"><input type="search" data-substore-query value="${esc(lifecycle.query)}" aria-label="搜索客户端节点" placeholder="搜索节点、协议、入站或端口"></label>${manage ? '<button class="button small" type="button" data-substore-target-add>＋ 新建同步组</button>' : ""}</section>
        <div class="substore-agent-grid qch-swap-panel${activeTarget ? "" : " empty"}" data-refresh-key="substore-results-${esc(activeTarget?.id || "empty")}-${esc(lifecycle.agentFilter || "all")}">${activeTarget ? cards || empty : '<section class="substore-empty"><strong>先新建一个同步组</strong><span>每个同步组独立选择节点并同步。</span></section>'}</div>
        ${manage ? `<dialog class="traffic-edit-dialog substore-settings-dialog" data-substore-settings-dialog aria-labelledby="substore-settings-title"><header><span class="traffic-edit-icon" aria-hidden="true">↻</span><div><p class="eyebrow">Sub-Store</p><h2 id="substore-settings-title">连接设置</h2></div><button class="deploy-command-close" type="button" data-substore-settings-close aria-label="关闭连接设置">×</button></header><form data-substore-settings-form><div class="traffic-edit-body"><label>后端地址<input type="password" name="endpoint_url" autocomplete="new-password" ${settings.configured ? "" : "required"} placeholder="${settings.configured ? "留空保持当前地址" : "https://substore.example.com/路径口令"}"><small>${esc(settings.endpoint_hint || "地址必须包含后端路径口令")}</small></label></div><footer><button class="button" type="button" data-substore-settings-close>取消</button><button class="button primary" type="submit">保存设置</button></footer></form></dialog><dialog class="traffic-edit-dialog substore-settings-dialog" data-substore-target-dialog aria-labelledby="substore-target-title"><header><span class="traffic-edit-icon" aria-hidden="true">◎</span><div><p class="eyebrow">同步目标</p><h2 id="substore-target-title">同步组设置</h2></div><button class="deploy-command-close" type="button" data-substore-target-close aria-label="关闭同步组设置">×</button></header><form data-substore-target-form><input type="hidden" name="target_id"><div class="traffic-edit-body"><label>同步组名称<input name="display_name" required maxlength="100" autocomplete="off" placeholder="例如：香港节点"></label><fieldset class="substore-mode-options"><legend>同步格式</legend><label><input type="radio" name="sync_format" value="url" checked><span><b>URL</b><small>使用分享链接；无通用链接的协议保留原生格式</small></span></label><label><input type="radio" name="sync_format" value="mihomo"><span><b>Mihomo</b><small>将所选节点同步为 Mihomo 代理配置</small></span></label></fieldset><fieldset class="substore-mode-options"><legend>同步模式</legend><label><input type="radio" name="sync_mode" value="incremental" checked><span><b>增量模式</b><small>新增或更新所选节点，保留 Sub-Store 远端已有节点</small></span></label><label><input type="radio" name="sync_mode" value="managed"><span><b>完全托管模式</b><small>远端组严格保持为当前选择的节点清单</small></span></label></fieldset><fieldset class="substore-rename-options" data-substore-rename-options hidden><legend>改名范围</legend><label><input type="radio" name="rename_remote" value="false" checked><span><b>仅修改面板名称</b><small>Sub-Store 组名称保持不变</small></span></label><label><input type="radio" name="rename_remote" value="true"><span><b>同时修改远端组名</b><small>同步更新 Sub-Store 订阅组名称</small></span></label></fieldset><div class="substore-remote-import" data-substore-remote-import ${settings.configured ? "" : "hidden"}><span><b>Sub-Store 已有组</b><small>读取远端订阅组并加入同步组</small></span><select data-substore-remote-select aria-label="Sub-Store 已有组" disabled><option>读取中…</option></select><button class="button small" type="button" data-substore-remote-import-button disabled>加入同步组</button></div></div><footer><button class="button danger" type="button" data-substore-target-delete hidden>移除同步组</button><span></span><button class="button" type="button" data-substore-target-close>取消</button><button class="button primary" type="submit">保存同步组</button></footer></form></dialog><dialog class="traffic-edit-dialog substore-settings-dialog" data-substore-delete-dialog aria-labelledby="substore-delete-title"><header><span class="traffic-edit-icon danger" aria-hidden="true">×</span><div><p class="eyebrow">移除同步组</p><h2 id="substore-delete-title">确认移除</h2></div><button class="deploy-command-close" type="button" data-substore-delete-close aria-label="关闭移除确认">×</button></header><form data-substore-delete-form><div class="traffic-edit-body"><p class="substore-delete-message">仅移除面板中的同步关系，Sub-Store 远端组会保留。</p></div><footer><button class="button" type="button" data-substore-delete-close>取消</button><button class="button danger" type="submit">确认移除</button></footer></form></dialog>` : ""}
      </section>`,
      "Sub-Store 同步",
      { viewKey: `substore-sync-${activeTarget?.id || "empty"}-${lifecycle.agentFilter || "all"}` },
    );
  }

  return render;
}
