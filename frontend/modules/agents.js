import { createAgentEnrollment } from "./agent-enrollment.js";
import { batchAgentEligibility, batchSelectAllState } from "./agent-batch.js";
export { batchAgentEligibility, batchSelectAllState } from "./agent-batch.js";
import { createAgentKomariDisplay } from "./agent-komari.js";
export { komariCycleRange, komariResetDay } from "./agent-komari.js";
import { animateNodeCardDrop, clearNodeCardDragState, nodeCardDropIndex } from "./agent-card-drag.js";
export { animateNodeCardDrop, clearNodeCardDragState, nodeCardDropIndex } from "./agent-card-drag.js";
import { formatHostPort, manualConnectionAddressNote, publicAddressRows, updatePublicIPDisplays } from "./agent-addresses.js";
export { formatHostPort, manualConnectionAddressNote, publicAddressRows, updatePublicIPDisplays } from "./agent-addresses.js";
import {
  bindEvent,
  createInteractionGate,
  createRefreshChannel,
} from "./refresh.js";
import { bindCodeEditors } from "./code-editor.js";
import { engineCapabilityToggles } from "./engine-capabilities.js";
import { createAgentSharing } from "./agent-sharing.js";
import { createRegionDisplay, openRegionPicker, regionAvatarMarkup } from "./regions.js";
export { geoRegionDetails } from "./regions.js";
import {
  orderedNodeList,
  saveNodeOrder,
} from "./node-order.js";

export function developmentSourceVisible(engine, channel) {
  return engine === "mihomo" && channel === "development";
}

export function coreSourceForInstall(engine, channel, rawSource) {
  return developmentSourceVisible(engine, channel)
    ? rawSource || "official"
    : undefined;
}

export function agentStructureSignature(agents = []) {
  return JSON.stringify(
    [...agents]
      .map((agent) => JSON.stringify([
        String(agent?.id || ""),
        agent.can_manage,
        [...(agent.capabilities || [])].sort(),
        [...(agent.supported_capabilities || agent.capabilities || [])].sort(),
        Object.entries(agent.capability_transitions || {}).map(([engine, task]) => [engine, task.task_id, task.status]).sort(),
      ]))
      .sort((left, right) => left.localeCompare(right)),
  );
}

function mihomoDevelopmentSourceFieldset(canMirror) {
  return `<fieldset class="release-channel-fieldset development-source-field" data-development-source hidden><legend>开发版来源</legend><div class="release-channel-options"><label><input type="radio" name="core_source" value="official" checked><span>MetaCubeX 官方（默认，推荐）</span></label><label><input type="radio" name="core_source" value="mirror" ${canMirror ? "" : "disabled"}><span>vernesong/mihomo Alpha 镜像（第三方）${canMirror ? "" : "（需升级 Agent）"}</span></label></div>${canMirror ? "" : `<p class="source-upgrade-note">当前 Agent 尚未声明 mihomo-development-source-v1，镜像来源不可用；请先在面板升级 Agent。</p>`}</fieldset>`;
}

export function installAgents(ctx) {
  const { api, optionalAPI, state, engines, can: permission, esc, engineName, statusTone, serviceStatusName, short, date, ago, heartbeat, percent, bytes, conciseVersion, rate, actionName, serviceActionDisabled, trafficChart, renderConfigDiff, notify, confirmAction, shell } = ctx;
  const can = (capability, agent) => agent?.can_manage === false &&
    ["operator", "agents.manage", "enrollment.manage"].includes(capability) ? false : permission(capability, agent);
  const { komariUUIDFor, komariNetworkMarkup, loadKomariDisplay } = createAgentKomariDisplay({ api, state, esc, bytes });
  const { bindEnrollmentRecordButtons, showAgentDirectoryDialog, showEnrollmentDialog, enrollmentInstallCommand, showCommand } = createAgentEnrollment({ api, esc, engineName, notify, refreshAgentPage });
  const pendingAgentNames = new Set();
  const pendingEngineCapabilities = new Set();
  const loadRegionDisplay = createRegionDisplay(ctx);
  const cardIPRow = (row) => {
    const value = row.value || "";
    const title = value ? `复制 ${row.label} 地址` : "暂无地址";
    const aria = `复制 ${row.label} 公网地址 ${value}`;
    return `<span class="card-ip-row ${value ? "" : "empty"}" data-ip-family="${row.cls}" data-ip-source="${esc(row.source)}" ${value ? "" : "hidden"}><i class="ip-family ${row.cls}">${row.label}</i><code title="${esc(value)}">${esc(value || "未探测到")}</code><button type="button" class="card-ip-copy" data-copy-ip="${esc(value)}" aria-label="${esc(aria)}" title="${esc(title)}" ${value ? "" : "hidden"}><svg viewBox="0 0 24 24" aria-hidden="true"><rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0 2 2v8a2 2 0 0 0 2 2h2"/><path class="copy-check" d="m9.5 13.5 2 2 4-4.5"/></svg></button></span>`;
  };
  const agentPageActive = () => state.route === "node-settings" || state.route === "agents";
  const metricsRefresh = createRefreshChannel({
    isCurrent: agentPageActive,
    getScope: () => state.navigationEpoch,
  });
  const cardInteractions = createInteractionGate();
  const sharing = createAgentSharing(ctx, cardInteractions);
  let cancelCardDrag = () => {};
  let agentPageRequest = 0;
  let syncActiveBatchSnapshot = null;
  let structureRefreshQueued = false;
  let structureRefreshRunning = false;
  let renderedAgentStructure = null;
  let renderedPresetConfigs = null;
  const presetConfigSignature = (deployments, configs, agentID) => JSON.stringify([
    deployments.filter((item) => item.agent_id === agentID).map((item) => [item.engine, item.config_id, item.config_version]).sort(),
    configs.filter((item) => item.agent_id === agentID).map((item) => [item.engine, item.id, item.version]).sort(),
  ]);
  const visibleAgentStructure = (items) =>
    agentStructureSignature(
      state.route === "agents" || state.data.nodeView === "detail"
        ? items.filter((item) => item.id === state.data.selectedAgent)
        : items,
    );
  const requestAgentStructureRefresh = () => {
    structureRefreshQueued = true;
    cardInteractions.defer(() => void flushAgentStructureRefresh(), "structure");
  };
  async function flushAgentStructureRefresh() {
    if (
      structureRefreshRunning ||
      !structureRefreshQueued ||
      !agentPageActive()
    )
      return;
    structureRefreshQueued = false;
    structureRefreshRunning = true;
    try {
      await renderAgentPage();
    } catch (error) {
      notify(error.message, "error");
    } finally {
      structureRefreshRunning = false;
      if (structureRefreshQueued)
        cardInteractions.defer(
          () => void flushAgentStructureRefresh(),
          "structure",
        );
    }
  }
async function agents(options = {}) {
  return nodeSettings(true, options);
}

async function nodeSettings(presetMode = false, { overview: preloadedOverview } = {}) {
  const request = ++agentPageRequest;
  const expectedRoute = presetMode ? "agents" : "node-settings";
  const [agents, deployments, accessEntries, tokens] =
    await Promise.all([
      api("/agents"),
      presetMode && can("deployments.read")
        ? api("/deployments")
        : Promise.resolve([]),
      presetMode && can("client-access.read")
        ? api("/client-access")
        : Promise.resolve([]),
      !presetMode && can("enrollment.manage")
        ? api("/enrollment-tokens")
        : Promise.resolve([]),
    ]);
  if (request !== agentPageRequest || state.route !== expectedRoute) return;
  state.data.agents = agents;
  const detailAnchor =
    !presetMode &&
    (String(state.anchor || "").startsWith("settings-node-") ||
      (String(state.anchor || "").startsWith("node-") && state.anchor !== "node-settings"));
  if (!detailAnchor && !agents.some((agent) => agent.id === state.data.selectedAgent))
    state.data.selectedAgent = agents[0]?.id || "";
  const anchor = String(state.anchor || "");
  if (!presetMode) {
    if (
      anchor.startsWith("settings-node-") ||
      (anchor.startsWith("node-") && anchor !== "node-settings")
    )
      state.data.nodeView = "detail";
    else if (anchor === "node-settings" || anchor === "enrollment")
      state.data.nodeView = "overview";
  }
  const overview = can("overview.read")
    ? preloadedOverview || await api("/overview")
    : {};
  if (request !== agentPageRequest || state.route !== expectedRoute) return;
  state.data.overview = overview;

  const savedConfigs = presetMode && state.data.selectedAgent && can("agent-config.read")
    ? await api(`/agents/${encodeURIComponent(state.data.selectedAgent)}/configs`)
    : [];
  if (request !== agentPageRequest || state.route !== expectedRoute) return;
  const configByService = new Map(
    savedConfigs.map((config) => [
      `${config.agent_id}|${config.engine}`,
      config,
    ]),
  );
  const deploymentByService = new Map(
    deployments.map((item) => [`${item.agent_id}|${item.engine}`, item]),
  );
  const accessByService = new Map(
    accessEntries.map((item) => [`${item.agent_id}|${item.engine}`, item]),
  );
  const configDiffByService = new Map();
  await Promise.all(
    savedConfigs.map(async (saved) => {
      const key = `${saved.agent_id}|${saved.engine}`;
      const deployed = deploymentByService.get(key);
      if (
        !deployed?.config_id ||
        (deployed.config_id === saved.id &&
          deployed.config_version === saved.version)
      )
        return;
      const deployedConfig = await optionalAPI(
        `/configs/${encodeURIComponent(deployed.config_id)}/revisions/${deployed.config_version}`,
      );
      if (!deployedConfig) return;
      const diff = renderConfigDiff(saved.content, deployedConfig.content);
      if (diff) configDiffByService.set(key, diff);
    }),
  );
  if (request !== agentPageRequest || state.route !== expectedRoute) return;

  const tokenRows =
    tokens
      .map(
        (token) =>
          `<article><div><strong>${esc(token.name)}</strong><small>${token.reusable ? `可重复安装 · 删除前长期有效 · 已安装 ${token.used_count} 次` : `有效至 ${date(token.expires_at)} · 已使用 ${token.used_count}/${token.max_uses} 次`}</small></div><div class="access-history-actions">${token.command_available ? `<button class="button small" type="button" data-view-enrollment-record="${esc(token.id)}" aria-label="查看添加命令 ${esc(token.name)}">查看命令</button>` : ""}<button class="access-history-delete" type="button" data-delete-enrollment="${esc(token.id)}" aria-label="删除添加命令 ${esc(token.name)}">删除</button></div></article>`,
      )
      .join("") || "";

  const selectedAgent = agents.find(
    (agent) => agent.id === state.data.selectedAgent,
  );
  const detailRoute = !presetMode && state.data.nodeView === "detail";
  const detailMode = detailRoute && Boolean(selectedAgent);
  const detailMissing = detailRoute && !selectedAgent;
  const visibleAgents = detailMode
    ? [selectedAgent]
    : detailMissing
      ? []
    : presetMode
      ? selectedAgent
        ? [selectedAgent]
        : []
      : orderedNodeList(agents);
  const batchAvailable =
    !presetMode && !detailRoute && agents.filter((agent) => can("operator", agent)).length > 1 && can("operator");
  if (!batchAvailable) state.data.nodeBatchMode = false;
  const batchMode = batchAvailable && Boolean(state.data.nodeBatchMode);
  const serviceActionIcons = {
    status:
      '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M4 12h3l2-5 4 10 2-5h5"/></svg>',
    start:
      '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="m8 5 11 7-11 7z"/></svg>',
    restart:
      '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M20 7v5h-5"/><path d="M18.5 16a8 8 0 1 1 .8-7.2L20 12"/></svg>',
    stop:
      '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="7" y="7" width="10" height="10" rx="1"/></svg>',
  };
  const nodeCards = visibleAgents
    .map((agent) => {
      const isShared = agent.can_manage === false;
      const sharedBadge = isShared ? '<span class="agent-shared-badge">共享</span>' : "";
      const can = (capability) => permission(capability, agent) &&
        !(agent.can_manage === false && ["operator", "agents.manage", "enrollment.manage"].includes(capability));
      const metrics = can("metrics.read") ? agent.metrics || {} : {};
      const addressRows = publicAddressRows(
        metrics,
        agent.labels || {},
        agent.features || [],
      );
      const connectionAddressNote = manualConnectionAddressNote(agent.labels);
      const services = (agent.capabilities || [])
        .map((engine) => {
          const key = `${agent.id}|${engine}`;
          const runtime = agent.runtime?.[engine] || {};
          const deployed = deploymentByService.get(key);
          const saved = configByService.get(key);
          const access = accessByService.get(key);
          const configDiff = configDiffByService.get(key) || "";
          const drift =
            saved &&
            (!deployed ||
              deployed.config_id !== saved.id ||
              deployed.config_version < saved.version);
          const firstProfile = access?.profiles?.[0];
          const port = firstProfile?.profile?.fields?.find(
            (field) => field.label === "端口",
          )?.value;
          const endpoint = access ? formatHostPort(access.address, port) : "";
          const installed = Boolean(runtime.installed);
          const existingPending = Boolean(
            runtime.existing_config_available,
          );
          const existingUnsupportedReason = String(
            runtime.existing_config_unsupported_reason || "",
          );
          const existingBlocked = Boolean(existingUnsupportedReason);
          const canMirror = (agent.features || []).includes(
            "mihomo-development-source-v1",
          );
          const serviceState = existingBlocked
            ? "检测到但不可迁移"
            : installed
            ? serviceStatusName(runtime.service_status)
            : "未安装";
          const serviceTone = existingBlocked
            ? "warn"
            : installed
            ? statusTone(runtime.service_status)
            : "muted";
          const optionalImportChip = !can("agent-config.read") || agent.can_manage === false
            ? ""
            : existingBlocked
              ? `<button class="service-import-chip blocked" type="button" data-manual-import data-manual-agent="${esc(agent.id)}" data-manual-engine="${esc(engine)}" aria-label="查看现有服务不可导入原因" title="查看现有服务不可导入原因">不可导入</button>`
              : existingPending
                ? `<button class="service-import-chip" type="button" data-manual-import data-manual-agent="${esc(agent.id)}" data-manual-engine="${esc(engine)}" aria-label="导入现有服务" title="导入现有服务">可导入</button>`
                : "";
          let primaryActions = "";
          if (presetMode && can("agent-config.read")) {
            primaryActions = drift
              ? `<button class="button service-config" type="button" data-config="${esc(agent.id)}" data-engine="${esc(engine)}">查看配置</button>${can("tasks.execute") ? `<button class="button primary" type="button" data-deploy="${esc(agent.id)}" data-engine="${esc(engine)}" data-config-id="${esc(saved.id)}" data-config-version="${saved.version}" ${!installed || existingBlocked || agent.status !== "online" ? 'disabled title="请先确认节点在线且内核已安装"' : ""}>部署 v${saved.version}</button>` : ""}`
              : `<button class="button primary service-config" type="button" data-config="${esc(agent.id)}" data-engine="${esc(engine)}">配置 <span>→</span></button>`;
          }
          if (!presetMode) {
            if (isShared) {
              return `<article class="service-card core-runtime-row service-${esc(engine)}" data-runtime-structure="full" data-core-installed="${installed ? 1 : 0}" data-existing-pending="0" data-existing-unsupported="">
                <div class="core-runtime-summary">
                  <div class="core-runtime-name"><span class="engine-badge ${esc(engine)}">${esc(engineName(engine))}</span><span class="engine-state ${serviceTone}"><i></i><b data-core-service="${esc(engine)}">${esc(serviceState)}</b></span></div>
                  <div class="core-runtime-version"><small>当前版本</small><strong data-core-version="${esc(engine)}">${esc(installed ? conciseVersion(engine, runtime.version) : "尚未安装")}</strong></div>
                  ${can("agent-config.read") ? `<div class="core-runtime-actions"><button class="button small" type="button" data-config="${esc(agent.id)}" data-engine="${esc(engine)}">配置</button></div>` : ""}
                </div>
              </article>`;
            }
            const runtimeActions = installed
              ? ["status", "start", "restart", "stop"]
                  .map(
                    (action) =>
                      `<button class="core-action ${action === "stop" ? "danger" : ""}" type="button" data-task-agent="${esc(agent.id)}" data-task-engine="${esc(engine)}" data-task-action="${action}" data-service-action="${action}" aria-label="${esc(`${actionName(action)} ${engineName(engine)}`)}" title="${esc(actionName(action))}" ${existingBlocked || (action !== "status" && !can("operator")) || serviceActionDisabled(action, agent.status === "online", installed, runtime.service_status, agent) ? "disabled" : ""}>${serviceActionIcons[action]}</button>`,
                  )
                  .join("")
              : "";
            return `<article class="service-card core-runtime-row service-${esc(engine)}" data-runtime-structure="full" data-core-installed="${installed ? 1 : 0}" data-existing-pending="${existingPending ? 1 : 0}" data-existing-unsupported="${esc(existingUnsupportedReason)}">
              <div class="core-runtime-summary">
                <div class="core-runtime-name"><span class="engine-badge ${esc(engine)}">${esc(engineName(engine))}</span>${optionalImportChip}<span class="engine-state ${serviceTone}"><i></i><b data-core-service="${esc(engine)}">${esc(serviceState)}</b></span></div>
                <div class="core-runtime-version"><small>当前版本</small><strong data-core-version="${esc(engine)}" title="${esc(installed ? runtime.version || "版本未知" : "尚未安装")}">${esc(installed ? conciseVersion(engine, runtime.version) : "尚未安装")}</strong></div>
                <div class="core-runtime-actions">${runtimeActions ? `<div class="core-action-group" aria-label="${esc(engineName(engine))} 服务操作">${runtimeActions}</div>` : ""}<button class="button small ${installed ? "" : "primary"}" type="button" data-open-version-form ${existingBlocked ? "disabled" : ""}>${existingBlocked ? "不可迁移" : installed ? "版本" : "安装"}</button></div>
              </div>
              <details class="core-version-panel version-drawer"><summary><b>${installed ? "版本管理" : `安装 ${esc(engineName(engine))}`}</b><span>收起</span></summary><div class="runtime-drawer-body"><form class="core-version-form" data-version-agent="${esc(agent.id)}" data-version-engine="${esc(engine)}"><fieldset class="release-channel-fieldset"><legend>版本来源</legend><div class="release-channel-options"><label><input type="radio" name="release_channel" value="stable" checked><span>最新稳定版</span></label><label><input type="radio" name="release_channel" value="development"><span>最新开发版</span></label><label><input type="radio" name="release_channel" value="custom"><span>指定版本</span></label></div></fieldset>${mihomoDevelopmentSourceFieldset(canMirror)}<label class="custom-version-field"><span>指定版本</span><input name="custom_version" maxlength="64" autocomplete="off" placeholder="例如 1.19.29"></label><button class="button small" type="submit" ${existingBlocked || agent.status !== "online" || !can("operator") ? "disabled" : ""}>${existingBlocked ? "不可自动迁移" : installed ? "升级或切换版本" : "安装内核"}</button><small>${existingBlocked ? esc(existingUnsupportedReason) : installed ? "Release · SHA-256 校验" : "安装至 QAgent 专用目录，不影响系统已有内核 · Release · SHA-256 校验"}</small></form></div></details>
            </article>`;
          }
          return `<article class="service-card service-${esc(engine)}" data-refresh-key="service-${esc(engine)}" data-runtime-structure="full" data-core-installed="${installed ? 1 : 0}" data-existing-pending="${existingPending ? 1 : 0}" data-existing-unsupported="${esc(existingUnsupportedReason)}">
            <div class="service-card-main ${presetMode ? "" : "operations-only"}">
              <div class="service-overview"><header><span class="service-engine-title"><span class="engine-badge ${esc(engine)}">${esc(engineName(engine))}</span>${optionalImportChip}</span><span class="engine-state ${serviceTone}"><i></i><b data-core-service="${esc(engine)}">${esc(serviceState)}</b></span></header><div class="service-version"><span class="service-version-label"><small>内核版本</small>${isShared ? "" : `<button class="service-version-toggle" type="button" data-open-version-form aria-label="打开 ${esc(engineName(engine))} ${installed ? "版本切换" : "安装内核"}" ${existingBlocked ? "disabled" : ""}>${existingBlocked ? "不可迁移" : installed ? "切换版本" : "安装内核"}</button>`}</span><strong data-core-version="${esc(engine)}" title="${esc(installed ? runtime.version || "版本未知" : "尚未安装")}">${esc(installed ? conciseVersion(engine, runtime.version) : "尚未安装")}</strong></div></div>
              ${presetMode ? `<div class="service-deployment"><dl class="service-facts"><div><dt>已部署配置</dt><dd>${deployed?.config_version ? `v${deployed.config_version}` : "—"}</dd></div><div><dt>已保存配置</dt><dd>${saved?.version ? `v${saved.version}` : "—"}</dd></div></dl>${drift ? `<div class="deployment-drift"><span>${deployed ? "已保存版本尚未部署" : "已保存配置尚未部署"}</span><b>待部署 v${saved.version}</b></div>` : ""}${configDiff ? `<details class="config-diff-drawer"><summary>查看配置差异 <i>＋</i></summary>${configDiff}</details>` : ""}<div class="service-endpoint ${endpoint ? "" : "empty"}">${endpoint ? `<span><b>${esc(firstProfile?.protocol || "客户端入站")}</b><small>${esc(firstProfile?.profile?.format || "已部署配置")}</small></span><code>${esc(endpoint)}</code>` : `<b>${deployed ? "自定义配置" : saved ? "尚未部署" : "尚未配置"}</b>`}</div></div>` : ""}
              ${primaryActions ? `<div class="service-primary-action">${primaryActions}</div>` : ""}
            </div>
              ${isShared ? "" : `<details class="runtime-drawer version-drawer"><summary><span><b>${installed ? "版本切换" : "安装内核"}</b><small>${existingBlocked ? "检测到的现有服务未被接管" : installed ? "升级或切换内核版本" : "从 Release 安装"}</small></span><i>＋</i></summary><div class="runtime-drawer-body"><form class="core-version-form" data-version-agent="${esc(agent.id)}" data-version-engine="${esc(engine)}"><fieldset class="release-channel-fieldset"><legend>版本来源</legend><div class="release-channel-options"><label><input type="radio" name="release_channel" value="stable" checked><span>最新稳定版</span></label><label><input type="radio" name="release_channel" value="development"><span>最新开发版</span></label><label><input type="radio" name="release_channel" value="custom"><span>指定版本</span></label></div></fieldset>${mihomoDevelopmentSourceFieldset(canMirror)}<label class="custom-version-field"><span>指定版本</span><input name="custom_version" maxlength="64" autocomplete="off" placeholder="例如 1.19.29"></label><button class="button small" type="submit" ${existingBlocked || agent.status !== "online" || !can("operator") ? "disabled" : ""}>${existingBlocked ? "不可自动迁移" : installed ? "升级或切换版本" : "安装内核"}</button><small>${existingBlocked ? esc(existingUnsupportedReason) : installed ? "Release · SHA-256 校验" : "安装至 QAgent 专用目录，不影响系统已有内核 · Release · SHA-256 校验"}</small></form></div></details>`}
            ${presetMode && access?.profiles?.length ? `<a class="service-client-access" href="#client-access" data-client-agent="${esc(agent.id)}" data-client-engine="${esc(engine)}"><span><b>客户端配置</b><small>${esc(access.source)} · ${esc(access.address)}</small></span><strong>${access.profiles.length} 个入站 <i>→</i></strong></a>` : ""}
          </article>`;
        })
        .join("");
      const labels = Object.entries(agent.labels || {})
        .filter(([key]) => !key.startsWith("client_profile_name_"))
        .map(([key, value]) => `<span>${esc(key)}=${esc(value)}</span>`)
        .join("");
      if (detailMode) {
        const installedCount = (agent.capabilities || []).filter(
          (engine) => agent.runtime?.[engine]?.installed,
        ).length;
        const activeTab = ["cores", "metrics", "agent"].includes(
          state.data.nodeSettingsTab,
        )
          ? state.data.nodeSettingsTab
          : "cores";
        const tabID = (name) => `node-${agent.id}-${name}`;
        const tabButton = (name, label, count = "") =>
          `<button id="${esc(tabID(`${name}-tab`))}" type="button" role="tab" data-node-tab="${name}" aria-controls="${esc(tabID(`${name}-panel`))}" aria-selected="${activeTab === name}" tabindex="${activeTab === name ? 0 : -1}">${label}${count ? `<span>${count}</span>` : ""}</button>`;
        return `<section class="node-operations-workspace" id="settings-node-${esc(agent.id)}" data-refresh-key="agent-${esc(agent.id)}" data-agent-node="${esc(agent.id)}" data-agent-metrics="${esc(agent.id)}" data-available="${metrics.collected_at ? 1 : 0}">
          <header class="node-operations-header"><div class="node-operations-title">${regionAvatarMarkup(agent, esc, can("agents.manage"))}<div><span class="node-live-state">${sharedBadge}<i class="status-dot ${statusTone(agent.status)}" data-agent-status-dot></i><b data-agent-status-label>${agent.status === "online" ? "在线" : "离线"}</b><small data-agent-heartbeat>${esc(heartbeat(agent.last_seen))}</small></span><h2>${esc(agent.name)}</h2><code>${esc(agent.os)} / ${esc(agent.arch)} · ${esc(short(agent.id))}</code></div></div><div class="node-operations-actions">${isShared ? '<a class="button small" href="#my-quota">共享额度</a>' : ""}${can("metrics.read") ? `<button class="button small" type="button" data-agent-refresh title="刷新节点状态">刷新</button>` : ""}${can("operator") ? `<button type="button" class="button primary small" data-upgrade-agent="${esc(agent.id)}">升级 Agent</button>` : ""}</div></header>
          <section class="node-resource-strip" aria-label="节点资源"><div><span>CPU</span><strong data-metric-text="cpu">${metrics.cpu_available ? `${Number(metrics.cpu_percent).toFixed(1)}%` : "等待采集"}</strong><progress aria-label="CPU 使用率" data-metric-progress="cpu" max="100" value="${metrics.cpu_available ? Number(metrics.cpu_percent) : 0}"></progress></div><div><span>内存</span><strong data-metric-text="memory">${metrics.memory_available ? `${bytes(metrics.memory_used_bytes)} / ${bytes(metrics.memory_total_bytes)}` : "等待采集"}</strong><progress aria-label="内存使用率" data-metric-progress="memory" max="100" value="${percent(metrics.memory_used_bytes, metrics.memory_total_bytes)}"></progress></div><div><span>磁盘</span><strong data-metric-text="disk">${metrics.disk_available ? `${bytes(metrics.disk_used_bytes)} / ${bytes(metrics.disk_total_bytes)}` : "等待采集"}</strong><progress aria-label="根磁盘使用率" data-metric-progress="disk" max="100" value="${percent(metrics.disk_used_bytes, metrics.disk_total_bytes)}"></progress></div><div class="node-resource-network"><span>网络</span><strong>↓ <i data-metric-text="download-rate">${metrics.network_available ? rate(metrics.network_rx_bps) : "等待采集"}</i> · ↑ <i data-metric-text="upload-rate">${metrics.network_available ? rate(metrics.network_tx_bps) : "等待采集"}</i></strong><small>累计 ↓ <b data-metric-text="download-total">${metrics.network_available ? bytes(metrics.network_rx_bytes) : "—"}</b> · ↑ <b data-metric-text="upload-total">${metrics.network_available ? bytes(metrics.network_tx_bytes) : "—"}</b></small></div><span class="machine-resource-live" data-metric-poll role="status" aria-label="资源自动更新"></span></section>
          <nav class="node-settings-tabs" role="tablist" aria-label="节点设置分区">${tabButton("cores", "内核", `${installedCount}/${(agent.capabilities || []).length}`)}${tabButton("metrics", "监控")}${tabButton("agent", "Agent")}</nav>
          <div class="node-settings-panels">
            <section id="${esc(tabID("cores-panel"))}" class="node-tab-panel node-cores-panel" data-node-panel="cores" role="tabpanel" aria-labelledby="${esc(tabID("cores-tab"))}" ${activeTab === "cores" ? "" : "hidden"}>
              <header class="node-panel-heading"><div><h3>${isShared ? "已分配内核" : "内核管理"}</h3><small>${isShared ? "使用独立配置" : "服务状态与版本"}</small></div><span data-installed-summary>${installedCount ? `${installedCount} 个已安装` : "尚未安装内核"}</span></header><div class="core-runtime-list">${services}</div>
            </section>
            <section id="${esc(tabID("metrics-panel"))}" class="node-tab-panel node-metrics-panel" data-node-panel="metrics" role="tabpanel" aria-labelledby="${esc(tabID("metrics-tab"))}" ${activeTab === "metrics" ? "" : "hidden"}><header class="node-panel-heading"><div><h3>流量趋势</h3><small>最近 24 小时</small></div><span data-metric-text="stamp">${metrics.collected_at ? `采集于 ${ago(metrics.collected_at)}` : "等待资源数据"}</span></header><section class="metric-trend-empty" data-metric-history="${esc(agent.id)}" aria-label="暂无指标趋势"><span>⌁</span><b>正在载入指标趋势</b><small>节点上报指标后显示最近 24 小时的上下行速率。</small></section></section>
          <section id="${esc(tabID("agent-panel"))}" class="node-tab-panel node-agent-panel" data-node-panel="agent" role="tabpanel" aria-labelledby="${esc(tabID("agent-tab"))}" ${activeTab === "agent" ? "" : "hidden"}><header class="node-panel-heading"><div><h3>Agent 与身份</h3><small>注册信息和安全通道</small></div><span data-agent-version>${esc(agent.version || "未知")}</span></header>
            ${isShared ? `<dl class="identity-list node-identity-list"><div><dt>节点 ID</dt><dd><code>${esc(agent.id)}</code></dd></div><div><dt>系统平台</dt><dd>${esc(agent.os)} / ${esc(agent.arch)}</dd></div><div><dt>安全通道</dt><dd>WSS · Ed25519 签名</dd></div></dl><a class="button small" href="#my-quota">查看共享分配</a>` : `
            ${can("agents.manage") ? `<section class="node-name-settings"><header><div><b>共享 Agent</b><small>按用户分配内核、端口与额度</small></div><button class="button small" type="button" data-agent-sharing="${esc(agent.id)}">管理共享</button></header></section>` : ""}
            <section class="node-capability-settings" aria-label="节点内核能力" data-node-capabilities="${esc(agent.id)}">
              <header><b>内核能力</b><small>关闭即停止服务，开启恢复管理并启动已安装内核。</small></header>
              ${engineCapabilityToggles(agent.capabilities || [], { supported: agent.supported_capabilities ?? agent.capabilities ?? [], writable: can("agents.manage"), node: true, transitions: agent.capability_transitions || {} })}
              <p class="node-capability-note">启停成功后生效 · 离线节点上线后执行 · 未安装内核仅切换能力，配置保留</p>
            </section>
            <section class="node-name-settings" aria-label="节点名称"><header><div><b>节点名称</b><small>自定义面板显示名称；不改变节点 ID、连接或安装凭据。</small></div></header><form data-agent-name-form="${esc(agent.id)}"><label><span>显示名称</span><input name="name" maxlength="100" required autocomplete="off" value="${esc(agent.name)}" ${can("agents.manage") ? "" : "disabled"}></label><button class="button small" type="submit" ${can("agents.manage") ? "" : "disabled"}>保存名称</button></form></section>${agent.can_hide ? `<section class="node-name-settings" aria-label="管理员可见性"><header><div><b>管理员可见性</b><small>开启后管理员在节点列表、配置、任务、日志、指标和审计中看不到此节点，只能在“其他用户节点”里只读查看；后台采集、计费与 Agent 连接不受影响。</small></div></header><label class="node-visibility-toggle"><input type="checkbox" data-agent-visibility="${esc(agent.id)}" ${agent.admin_hidden ? "checked" : ""} ${can("agents.manage") ? "" : "disabled"}><span>${agent.admin_hidden ? "已对管理员隐藏" : "对管理员可见"}</span></label></section>` : ""}<dl class="identity-list node-identity-list"><div><dt>节点 ID</dt><dd><code>${esc(agent.id)}</code></dd></div><div><dt>系统平台</dt><dd>${esc(agent.os)} / ${esc(agent.arch)}</dd></div><div><dt>Agent 版本</dt><dd data-agent-version>${esc(agent.version || "未知")}</dd></div><div><dt>注册时间</dt><dd>${date(agent.enrolled_at)}</dd></div><div><dt>安全通道</dt><dd>WSS · Ed25519 签名</dd></div></dl><section class="node-public-ips" aria-label="公网地址"><header><b>公网地址 · 双栈</b><small>手动设置优先 · 出口探测 · 默认路由接口 · 已验证连接来源</small><small class="node-address-note" data-node-connection-address ${connectionAddressNote ? "" : "hidden"}>${esc(connectionAddressNote)}</small></header>${addressRows.map((row) => `<div class="public-ip-row ${row.ok ? "" : "empty"}" data-ip-family="${row.cls}" data-ip-source="${esc(row.source)}" ${row.value ? "" : "hidden"}><span class="ip-family ${row.cls}">${row.label}</span><code>${esc(row.value || "未探测到")}</code><small>${esc(row.source)}</small></div>`).join("")}</section><section class="node-komari-settings" aria-label="Komari 联动"><header><div><b>Komari 联动</b><small>填写 Komari 服务器 UUID；周期日期、已用量和额度会显示在节点卡片的网络区。</small></div></header><form data-komari-form="${esc(agent.id)}"><label><span>Komari 服务器 UUID</span><input name="uuid" maxlength="100" autocomplete="off" value="${esc(komariUUIDFor(agent))}" placeholder="例如 4addbaf1-7ffb-474c-98ee-4ffd476755ff" ${can("agents.manage") ? "" : "disabled"}></label><button class="button small" type="submit" ${can("agents.manage") ? "" : "disabled"}>保存</button></form>${komariUUIDFor(agent) ? "" : `<p class="node-komari-empty">尚未关联 Komari 服务器</p>`}</section>${labels ? `<div class="labels">${labels}</div>` : ""}<footer class="node-identity-refresh"><span>节点身份已验证</span><div>${can("enrollment.manage") && agent.enrollment_command_available ? `<button class="button small" type="button" data-view-enrollment-command="${esc(agent.id)}">查看安装部署命令</button>` : ""}</div></footer>${can("agents.manage") ? `<section class="node-danger-zone"><span><b>删除节点</b><small>断开节点并清理关联配置；QAgent 不会被远程卸载。</small></span><button class="button small danger-button" type="button" data-delete="${esc(agent.id)}">删除节点</button></section>` : ""}`}</section>
          </div>
        </section>`;
      }
      if (!presetMode) {
        const installedCount = (agent.capabilities || []).filter(
          (engine) => agent.runtime?.[engine]?.installed,
        ).length;
        const coreChips = (agent.capabilities || [])
          .map((engine) => {
            const runtime = agent.runtime?.[engine] || {};
            const installed = Boolean(runtime.installed);
            const existingUnsupportedReason = String(
              runtime.existing_config_unsupported_reason || "",
            );
            const serviceState = existingUnsupportedReason
                ? "检测到但不可迁移"
                : installed
                  ? serviceStatusName(runtime.service_status)
                  : "未安装";
            const tone =
              existingUnsupportedReason
                ? "warn"
                : installed
                  ? statusTone(runtime.service_status)
                  : "muted";
            return `<span class="core-chip service-${esc(engine)}" data-core-installed="${installed ? 1 : 0}"><span class="engine-badge ${esc(engine)}">${esc(engineName(engine))}</span><span class="engine-state ${tone}"><i></i><b data-core-service="${esc(engine)}">${esc(serviceState)}</b></span></span>`;
          })
          .join("");
        const cardTag = batchMode ? "article" : "a";
        const cardInteraction = batchMode
          ? `data-node-batch-card`
          : `href="#settings-node-${esc(agent.id)}"`;
        const batchSelect = batchMode
          ? `<label class="node-card-select" title="选择 ${esc(agent.name)}"><input type="checkbox" data-batch-checkbox value="${esc(agent.id)}" aria-label="选择 ${esc(agent.name)} 参与批量操作"><span aria-hidden="true"></span></label>`
          : "";
        return `<${cardTag} class="node-card ${batchMode ? "batch-selecting" : ""}" ${cardInteraction} data-refresh-key="agent-${esc(agent.id)}" data-agent-node="${esc(agent.id)}" data-agent-metrics="${esc(agent.id)}" data-state="${agent.status === "online" ? "online" : "offline"}" data-available="${metrics.collected_at ? 1 : 0}">
              <header class="node-card-head">${regionAvatarMarkup(agent, esc, can("agents.manage"))}<div class="node-card-title"><strong>${esc(agent.name)}</strong><small data-core-installed-summary>${esc(agent.os)} / ${esc(agent.arch)} · ${installedCount ? `${installedCount}/${(agent.capabilities || []).length} 内核已安装` : "尚未安装内核"}</small></div><span class="node-card-state">${sharedBadge}<i class="status-dot ${statusTone(agent.status)}" data-agent-status-dot></i><b data-agent-status-label>${agent.status === "online" ? "在线" : "离线"}</b><small data-agent-heartbeat>${esc(heartbeat(agent.last_seen))}</small></span>${batchMode ? batchSelect : '<span class="node-card-grip" title="拖动调整顺序" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M9 6h.01M9 12h.01M9 18h.01M15 6h.01M15 12h.01M15 18h.01"/></svg></span>'}</header>
              <div class="node-card-ips" aria-label="公网地址">${addressRows.map(cardIPRow).join("")}<small class="node-address-note" data-node-connection-address ${connectionAddressNote ? "" : "hidden"}>${esc(connectionAddressNote)}</small></div>
              <section class="node-card-resources" aria-label="节点资源"><div><span>CPU</span><strong data-metric-text="cpu">${metrics.cpu_available ? `${Number(metrics.cpu_percent).toFixed(1)}%` : "等待采集"}</strong><progress aria-label="CPU 使用率" data-metric-progress="cpu" max="100" value="${metrics.cpu_available ? Number(metrics.cpu_percent) : 0}"></progress></div><div><span>内存</span><strong data-metric-text="memory">${metrics.memory_available ? `${bytes(metrics.memory_used_bytes)} / ${bytes(metrics.memory_total_bytes)}` : "等待采集"}</strong><progress aria-label="内存使用率" data-metric-progress="memory" max="100" value="${percent(metrics.memory_used_bytes, metrics.memory_total_bytes)}"></progress></div><div><span>磁盘</span><strong data-metric-text="disk">${metrics.disk_available ? `${bytes(metrics.disk_used_bytes)} / ${bytes(metrics.disk_total_bytes)}` : "等待采集"}</strong><progress aria-label="根磁盘使用率" data-metric-progress="disk" max="100" value="${percent(metrics.disk_used_bytes, metrics.disk_total_bytes)}"></progress></div><div class="node-card-network"><span>网络</span><strong>↓ <i data-metric-text="download-rate">${metrics.network_available ? rate(metrics.network_rx_bps) : "等待采集"}</i> · ↑ <i data-metric-text="upload-rate">${metrics.network_available ? rate(metrics.network_tx_bps) : "等待采集"}</i></strong>${komariNetworkMarkup(agent) || `<small>累计 ↓ <b data-metric-text="download-total">${metrics.network_available ? bytes(metrics.network_rx_bytes) : "—"}</b> · ↑ <b data-metric-text="upload-total">${metrics.network_available ? bytes(metrics.network_tx_bytes) : "—"}</b></small>`}</div><span class="machine-resource-live" data-metric-poll role="status" aria-label="资源自动更新"></span></section>
              <section class="node-card-cores" aria-label="内核状态">${coreChips}</section>
              <footer class="node-card-foot"><small><i></i><span data-agent-version>${esc(agent.version || "未知")}</span></small><span class="node-card-stamp" data-metric-text="stamp">${metrics.collected_at ? `采集于 ${ago(metrics.collected_at)}` : "等待资源数据"}</span>${batchMode ? "" : `<span class="node-card-open">${isShared ? "查看节点" : "管理节点"} <i aria-hidden="true">→</i></span>`}</footer>
            </${cardTag}>`;
      }
      return `<section class="preset-node-workspace workspace-panel machine-body" id="preset-node-${esc(agent.id)}" data-refresh-key="agent-${esc(agent.id)}" data-agent-node="${esc(agent.id)}" data-agent-metrics="${esc(agent.id)}" data-available="${metrics.collected_at ? 1 : 0}" aria-label="选中节点的内核预设"><section class="service-canvas"><header class="service-canvas-head"><h2>节点内核</h2><span>${(agent.capabilities || []).length} 个内核</span></header><div class="service-grid">${services}</div></section></section>`;
    })
    .join("");

  const batchBar = batchMode
      ? `<aside class="node-batch-bar" aria-label="批量操作栏"><div class="batch-selection-head"><label class="batch-select-all"><input type="checkbox" data-batch-select-all aria-label="全选当前合格节点" aria-checked="false"><span data-batch-select-all-label>全选</span></label><strong data-batch-count aria-live="polite">已选择 0 个节点</strong></div><div class="batch-controls"><label><span>动作</span><select name="action"><option value="upgrade-agent">批量更新 Agent</option><option value="restart">重启服务</option><option value="status">查询状态</option><option value="start">启动服务</option><option value="stop">停止服务</option></select></label><label data-batch-engine-wrap><span>内核</span><select name="engine">${engines.map((engine) => `<option value="${engine}">${esc(engineName(engine))}</option>`).join("")}</select></label><button class="button small" type="button" data-batch-clear disabled>清空</button><button class="button small primary" type="submit" disabled>执行</button><button class="node-batch-close" type="button" data-close-node-batch aria-label="退出批量操作" title="退出批量操作">×</button></div><section class="batch-results" data-batch-results aria-live="polite" hidden></section></aside>`
      : "";
  const detailMissingState = detailMissing
    ? '<section class="node-settings-missing" data-node-missing role="status"><strong>节点不可用</strong><p>该节点已删除、撤销或不再属于当前作用域。</p><a class="button small" href="#node-settings">返回全部节点</a></section>'
    : "";
  const pageBody = detailMissingState || (nodeCards
    ? `<section class="${presetMode ? "machine-stack" : detailMode ? "node-settings-stack" : "node-card-grid"}">${nodeCards}</section>`
    : '<div class="empty large"><strong>还没有节点</strong><p>点击上方“添加节点”生成部署命令。</p></div>');
  const batchPage = batchMode
    ? `<form class="node-batch-form" id="batch-form">${pageBody}${batchBar}</form>`
    : pageBody;
  shell(
    `${presetMode ? "" : `<div class="node-settings-page">${detailRoute ? `<a class="node-back-link" href="#node-settings">← 全部节点</a>` : ""}`}${batchPage}${presetMode ? "" : "</div>"}`,
    presetMode ? "内核配置预设" : "节点设置",
    {
      viewKey: presetMode
        ? `preset-${state.data.selectedAgent || "empty"}`
        : detailRoute
          ? `node-settings-${state.data.selectedAgent || "empty"}`
          : "node-settings-overview",
    },
  );
  renderedAgentStructure = agentStructureSignature(visibleAgents);
  if (presetMode) renderedPresetConfigs = presetConfigSignature(deployments, savedConfigs, state.data.selectedAgent);
  document.querySelectorAll("[data-context-agent]").forEach((link) => {
    const prefix = presetMode ? "preset-node" : "settings-node";
    link.href = `#${prefix}-${link.dataset.contextAgent}`;
  });
  if (presetMode) compactPresetPage();
  bindAgentPage(agents, presetMode, { tokenRows, tokenCount: tokens.length });
}

function renderAgentPage() {
  return state.route === "agents" ? agents() : nodeSettings();
}

function refreshAgentPage() {
  if (
    state.route === "node-settings" &&
    cardInteractions.activeCount() > 0
  ) {
    requestAgentStructureRefresh();
    return Promise.resolve(false);
  }
  return renderAgentPage();
}

function cancelAgentInteractions() {
  sharing.close();
  cardInteractions.cancel();
  cancelCardDrag();
  cancelCardDrag = () => {};
}

// Drag reordering of the overview cards. A cloned ghost follows the cursor and
// the target card is highlighted, while the grid itself does not reflow
// mid-drag; on release the cards FLIP-animate to their new layout and the
// order is committed to localStorage.
function enableCardDrag(grid) {
  let drag = null;
  let cancelLanding = null;
  const dropIndex = (pointerX, pointerY) => {
    const rects = [...grid.querySelectorAll(".node-card")].map((card) =>
      card.getBoundingClientRect(),
    );
    return nodeCardDropIndex(
      rects,
      { x: pointerX, y: pointerY },
      drag.grabOffset,
    );
  };
  const highlight = (index) => {
    grid
      .querySelectorAll(".node-card")
      .forEach((card) => card.classList.remove("drop-target"));
    if (index == null) return;
    const rest = [...grid.querySelectorAll(".node-card")].filter(
      (card) => card !== drag.card,
    );
    rest[index]?.classList.add("drop-target");
  };
  const clearDragState = (clearAnimationStyles) => {
    if (!drag) return;
    clearNodeCardDragState(grid, drag, {
      clearAnimationStyles,
    });
    drag = null;
  };
  const reset = () => {
    const landing = cancelLanding;
    cancelLanding = null;
    const releaseInteraction = drag?.releaseInteraction;
    landing?.();
    clearDragState(true);
    if (!landing) releaseInteraction?.();
  };
  cancelCardDrag = reset;
  const finish = (event) => {
    if (!drag || event.pointerId !== drag.pointerId) return;
    const { card, moved, drop, releaseInteraction } = drag;
    if (!moved || !grid.contains(card)) return reset();
    const rest = [...grid.querySelectorAll(".node-card")].filter(
      (item) => item !== card,
    );
    const ghostRect = drag.ghost
      ? drag.ghost.getBoundingClientRect()
      : card.getBoundingClientRect();
    const oldRects = new Map(
      [card, ...rest].map((item) => [
        item,
        item === card ? ghostRect : item.getBoundingClientRect(),
      ]),
    );
    const target = drop == null || drop >= rest.length ? null : rest[drop];
    if (target) target.before(card);
    else grid.append(card);
    const next = [...grid.querySelectorAll(".node-card")];
    const ids = next.map((item) => item.dataset.agentNode);
    clearDragState(false);
    let settled = false;
    const cancelAnimation = animateNodeCardDrop(next, oldRects, {
      onSettled: () => {
        settled = true;
        cancelLanding = null;
        releaseInteraction();
      },
    });
    if (!settled) cancelLanding = cancelAnimation;
    saveNodeOrder(ids);
  };
  const cancel = (event) => {
    if (!drag || event.pointerId !== drag.pointerId) return;
    reset();
  };
  grid.querySelectorAll(".node-card-grip").forEach((grip) => {
    bindEvent(grip, "pointerdown", (event) => {
      if (event.button !== 0 || drag) return;
      const releaseInteraction = cardInteractions.begin();
      cancelLanding?.();
      cancelLanding = null;
      const card = grip.closest(".node-card");
      if (!card) {
        releaseInteraction();
        return;
      }
      event.preventDefault();
      const rect = card.getBoundingClientRect();
      drag = {
        card,
        pointerId: event.pointerId,
        startX: event.clientX,
        startY: event.clientY,
        grabOffset: {
          x: event.clientX - (rect.left + rect.width / 2),
          y: event.clientY - (rect.top + rect.height / 2),
        },
        rect,
        started: false,
        moved: false,
        drop: null,
        ghost: null,
        releaseInteraction,
      };
      grip.setPointerCapture(event.pointerId);
    });
    bindEvent(grip, "pointermove", (event) => {
      if (!drag || event.pointerId !== drag.pointerId) return;
      if (!drag.card.isConnected) {
        reset();
        return;
      }
      if (!drag.started) {
        if (
          Math.hypot(event.clientX - drag.startX, event.clientY - drag.startY) < 4
        )
          return;
        drag.started = true;
        drag.card.classList.add("dragging");
        document.body.classList.add("node-card-dragging");
        const ghost = drag.card.cloneNode(true);
        ghost.classList.remove("dragging");
        ghost.classList.add("node-card-ghost");
        ghost.removeAttribute("href");
        ghost.removeAttribute("data-agent-node");
        ghost.removeAttribute("data-agent-metrics");
        ghost.removeAttribute("data-metric-poll");
        ghost.style.position = "fixed";
        ghost.style.left = `${drag.rect.left}px`;
        ghost.style.top = `${drag.rect.top}px`;
        ghost.style.width = `${drag.rect.width}px`;
        drag.ghost = ghost;
        document.body.appendChild(ghost);
      }
      drag.moved = true;
      const dx = event.clientX - drag.startX;
      const dy = event.clientY - drag.startY;
      drag.ghost.style.transform = `translate(${dx}px, ${dy}px) scale(.99) rotate(.3deg)`;
      const index = dropIndex(event.clientX, event.clientY);
      drag.drop = index;
      highlight(index);
    });
    bindEvent(grip, "pointerup", finish);
    bindEvent(grip, "pointercancel", cancel);
    bindEvent(grip, "lostpointercapture", cancel);
    bindEvent(grip, "click", (event) => {
      event.preventDefault();
      event.stopPropagation();
    });
  });
}

function compactPresetPage() {
  document.querySelector("#enrollment")?.remove();
  document.querySelector("#batch-form")?.remove();
  document.querySelectorAll(".preset-node-workspace").forEach((item) => {
    item.querySelector(".machine-resource-summary")?.remove();
    item.querySelector(".machine-state")?.remove();
    item.querySelector(".node-inspector")?.remove();
    item.querySelector(".machine-footer")?.remove();
    item.querySelectorAll(".service-management-unavailable, [data-upgrade-agent]").forEach((element) => element.remove());
    item.querySelectorAll("[data-batch-checkbox]").forEach((element) => element.closest("label")?.remove());
  });
}

function bindAgentPage(agentItems, presetMode = false, enrollmentHistory = {}) {
  const agentsByID = new Map(agentItems.map((agent) => [agent.id, agent]));
  document.querySelectorAll("[data-agent-sharing]").forEach((button) => {
    bindEvent(button, "click", () => sharing.open(agentsByID.get(button.dataset.agentSharing)));
  });
  syncActiveBatchSnapshot = null;
  document
    .querySelectorAll(
      ".preset-node-workspace, .machine-workspace, .node-operations-workspace",
    )
    .forEach((item) => {
      const agent = agentsByID.get(item.dataset.agentNode);
      const installedCount = (agent?.capabilities || []).filter(
        (engine) => agent.runtime?.[engine]?.installed,
      ).length;
      const serviceCount = item.querySelector(
        ".service-canvas-head > span, [data-installed-summary]",
      );
      if (serviceCount) {
        const compact = serviceCount.hasAttribute("data-installed-summary");
        serviceCount.textContent = installedCount
          ? compact
            ? `${installedCount} 个已安装`
            : `${installedCount} 个已安装 · ${(agent?.capabilities || []).length} 个可用`
          : "尚未安装内核";
      }
      const machineFooter = item.querySelector(".machine-footer");
      item.querySelectorAll("[data-upgrade-agent]").forEach((button) => {
        const supported = (agent?.features || []).includes(
          "agent-self-upgrade-v1",
        );
        if (!supported) {
          button.disabled = true;
          button.title =
            "当前 Agent 不支持远程升级，请重新执行一次添加节点命令";
          button.textContent = "需重新安装 Agent";
        }
      });
      machineFooter?.remove();
      if (agent) updateAgentMetrics(agent);
      if (item instanceof HTMLDetailsElement) {
        bindEvent(item, "toggle", () => {
          if (item.open) {
            state.data.selectedAgent = item.dataset.agentNode;
            if (can("metrics.read")) loadMetricHistory(state.data.selectedAgent);
          }
        });
        if (item.open && can("metrics.read"))
          loadMetricHistory(item.dataset.agentNode);
      } else if (
        can("metrics.read") &&
        item.querySelector('[data-node-panel="metrics"]:not([hidden])')
      ) {
        loadMetricHistory(item.dataset.agentNode);
      }
    });
  document.querySelectorAll("[data-node-tab]").forEach((button) => {
    button.onclick = () => {
      const workspace = button.closest(".node-operations-workspace");
      if (!workspace) return;
      const tab = button.dataset.nodeTab;
      state.data.nodeSettingsTab = tab;
      workspace.querySelectorAll("[data-node-tab]").forEach((candidate) => {
        const selected = candidate === button;
        candidate.setAttribute("aria-selected", String(selected));
        candidate.tabIndex = selected ? 0 : -1;
      });
      workspace.querySelectorAll("[data-node-panel]").forEach((panel) => {
        panel.hidden = panel.dataset.nodePanel !== tab;
      });
      if (tab === "metrics" && can("metrics.read"))
        loadMetricHistory(workspace.dataset.agentNode);
    };
    button.onkeydown = (event) => {
      if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key))
        return;
      event.preventDefault();
      const tabs = [...button.closest("[role=tablist]").querySelectorAll("[role=tab]")];
      const current = tabs.indexOf(button);
      const next =
        event.key === "Home"
          ? 0
          : event.key === "End"
            ? tabs.length - 1
            : (current + (event.key === "ArrowRight" ? 1 : -1) + tabs.length) %
              tabs.length;
      tabs[next].focus();
      tabs[next].click();
    };
  });
  document.querySelectorAll("[data-config]").forEach((button) => {
    button.onclick = () => {
      state.data.agentId = button.dataset.config;
      state.data.engine = button.dataset.engine;
      // A preset card can be opened after editing another engine. The
      // protocol and inbound are engine-specific; retaining them can render
      // an empty editor or submit the wrong generated plan.
      state.data.protocol = "";
      state.data.inboundTag = "";
      state.data.configField = "";
      state.data.configInboundField = "";
      state.data.liveAgent = state.data.agentId;
      state.data.liveEngine = state.data.engine;
      state.data.liveConfigSource = "";
      location.hash = `#live-config?${new URLSearchParams({agent:state.data.agentId, engine:state.data.engine})}`;
    };
  });
  document.querySelectorAll("[data-client-agent]").forEach((link) => {
    link.onclick = () => {
      state.data.accessAgent = link.dataset.clientAgent;
      state.data.accessEngine = link.dataset.clientEngine;
    };
  });
  document.querySelectorAll("[data-task-action]").forEach((button) => {
    button.onclick = async () => {
      if (
        button.dataset.taskAction === "stop" &&
        !(await confirmAction(
          `确定停止 ${engineName(button.dataset.taskEngine)} 服务？现有连接会立即中断，需再次启动才能恢复。`,
          "停止服务",
        ))
      )
        return;
      await submitTask({
        agent_id: button.dataset.taskAgent,
        engine: button.dataset.taskEngine,
        action: button.dataset.taskAction,
      });
    };
  });
  document.querySelectorAll("[data-deploy]").forEach((button) => {
    button.onclick = async () => {
      if (button.disabled) return;
      const label = button.textContent;
      button.disabled = true;
      try {
        if (!(await confirmAction(
          `确定将已保存配置 v${button.dataset.configVersion} 部署到 ${engineName(button.dataset.engine)} 并重启服务？`, label.trim(),
        ))) return;
        button.textContent = "正在提交部署…";
        button.setAttribute("aria-busy", "true");
        await submitTask({
          agent_id: button.dataset.deploy,
          engine: button.dataset.engine,
          action: "deploy",
          config_id: button.dataset.configId,
          expected_config_version: Number(button.dataset.configVersion),
        });
      } catch (error) {
        notify(error.message, "error");
      } finally {
        button.textContent = label;
        button.removeAttribute("aria-busy");
        button.disabled = false;
      }
    };
  });
  document.querySelectorAll("[data-manual-import]").forEach((button) => {
    button.onclick = () => {
      state.data.liveAgent = button.dataset.manualAgent;
      state.data.liveEngine = button.dataset.manualEngine;
      state.data.liveConfigSource = "import";
      location.hash = "#live-config";
    };
  });
  document.querySelectorAll(".core-version-form").forEach((form) => {
    form.onsubmit = async (event) => {
      event.preventDefault();
      const values = new FormData(form);
      const channel = values.get("release_channel");
      const engine = form.dataset.versionEngine;
      const version =
        channel === "custom" ? values.get("custom_version") : channel;
      const payload = {
        agent_id: form.dataset.versionAgent,
        engine,
        action: "install",
        core_version: version,
      };
      const source = coreSourceForInstall(engine, channel, values.get("core_source"));
      if (source !== undefined) payload.core_source = source;
      const sourceNote =
        payload.core_source === "mirror"
          ? "来源：vernesong/mihomo Alpha 镜像（第三方）。"
          : payload.core_source === "official"
            ? "来源：MetaCubeX/mihomo 官方（默认）。"
            : "";
      if (
        !(await confirmAction(
          `确定提交内核安装或版本切换任务？${sourceNote}下载和校验完成后，目标服务会重启。`,
          "提交任务",
        ))
      )
        return;
      await submitTask(payload);
    };
  });
  document.querySelectorAll("[data-open-version-form]").forEach((button) => {
    button.onclick = () => {
      const drawer = button
        .closest(".service-card")
        ?.querySelector(".version-drawer");
      if (drawer) drawer.open = true;
    };
  });
  document.querySelectorAll(".core-version-form").forEach((form) => {
    const custom = form.querySelector(".custom-version-field");
    const input = custom?.querySelector("input");
    const developmentSource = form.querySelector("[data-development-source]");
    const sync = () => {
      const checked = form.querySelector('input[name="release_channel"]:checked');
      const channel = checked?.value;
      const enabled = channel === "custom";
      custom?.classList.toggle("is-disabled", !enabled);
      if (input) {
        input.disabled = !enabled;
        input.required = enabled;
      }
      if (developmentSource) {
        developmentSource.hidden = !developmentSourceVisible(
          form.dataset.versionEngine,
          channel,
        );
      }
    };
    form
      .querySelectorAll('input[name="release_channel"]')
      .forEach((radio) => bindEvent(radio, "change", sync));
    sync();
  });
  document.querySelectorAll("[data-delete]").forEach((button) => {
    button.onclick = async () => {
      if (
        !(await confirmAction(
          "确定删除此节点？控制面会断开连接并清理关联配置，节点上的 QAgent 不会被远程卸载；以后可通过新的添加节点命令重新安装。",
          "删除节点",
        ))
      )
        return;
      await api(`/agents/${encodeURIComponent(button.dataset.delete)}`, {
        method: "DELETE",
      });
      await refreshAgentPage();
    };
  });
  const setNodeBatchMode = async (enabled, trigger) => {
    if (trigger?.disabled) return;
    if (trigger) trigger.disabled = true;
    state.data.nodeBatchMode = enabled;
    cancelCardDrag();
    try {
      await renderAgentPage();
    } catch (error) {
      state.data.nodeBatchMode = !enabled;
      if (trigger?.isConnected) trigger.disabled = false;
      notify(error.message, "error");
    }
  };
  document.querySelectorAll("[data-node-batch-toggle]").forEach((button) => {
    button.onclick = () =>
      setNodeBatchMode(!Boolean(state.data.nodeBatchMode), button);
  });
  const batchForm = document.querySelector("#batch-form");
  const updateBatch = () => {
    if (!batchForm) return;
    const engine = batchForm.elements.engine.value;
    const action = batchForm.elements.action.value;
    const busy = batchForm.dataset.busy === "1";
    const inputs = [...batchForm.querySelectorAll("[data-batch-checkbox]")];
    inputs.forEach((input) => {
      const agent = agentsByID.get(input.value);
      const eligibility = batchAgentEligibility(agent, action, engine);
      input.dataset.batchEligible = eligibility.eligible ? "1" : "0";
      input.disabled = busy || !eligibility.eligible;
      const card = input.closest(".node-card");
      const control = input.closest(".node-card-select");
      if (control) control.title = eligibility.reason;
      if (card) card.dataset.batchEligible = eligibility.eligible ? "1" : "0";
      if (!eligibility.eligible) input.checked = false;
      card?.classList.toggle("selected", input.checked);
    });
    const selection = batchSelectAllState(inputs);
    const selectAll = batchForm.querySelector("[data-batch-select-all]");
    if (selectAll) {
      selectAll.disabled = busy || selection.eligible === 0;
      selectAll.checked = selection.checked;
      selectAll.indeterminate = selection.indeterminate;
      selectAll.setAttribute(
        "aria-checked",
        selection.indeterminate ? "mixed" : String(selection.checked),
      );
    }
    const selectAllLabel = batchForm.querySelector(
      "[data-batch-select-all-label]",
    );
    if (selectAllLabel)
      selectAllLabel.textContent = selection.checked ? "取消全选" : "全选";
    const button = batchForm?.querySelector("button[type=submit]");
    if (button)
      button.disabled = selection.selected === 0 || batchForm.dataset.busy === "1";
    const clear = batchForm.querySelector("[data-batch-clear]");
    if (clear) clear.disabled = busy || selection.selected === 0;
    const label = batchForm?.querySelector("[data-batch-count]");
    if (label)
      label.textContent = `已选择 ${selection.selected} 个节点 · 当前可选 ${selection.eligible} 个`;
    const engineWrap = batchForm.querySelector("[data-batch-engine-wrap]");
    if (engineWrap) engineWrap.hidden = action === "upgrade-agent";
    batchForm.elements.action.disabled = busy;
    batchForm.elements.engine.disabled = busy;
    const close = batchForm.querySelector("[data-close-node-batch]");
    if (close) close.disabled = busy;
    batchForm
      .querySelectorAll("[data-batch-retry]")
      .forEach((retry) => {
        const eligibility = batchAgentEligibility(
          agentsByID.get(retry.dataset.batchRetry),
          retry.dataset.batchRetryAction,
          retry.dataset.batchRetryEngine,
        );
        retry.disabled = busy || !eligibility.eligible;
        retry.title = eligibility.eligible ? "重试失败任务" : eligibility.reason;
      });
  };
  const setBatchBusy = (busy) => {
    if (!batchForm) return;
    batchForm.dataset.busy = busy ? "1" : "";
    updateBatch();
  };
  syncActiveBatchSnapshot = (items) => {
    if (!batchForm?.isConnected) return;
    agentsByID.clear();
    items.forEach((agent) => agentsByID.set(agent.id, agent));
    updateBatch();
  };
  batchForm
    ?.querySelectorAll("[data-batch-checkbox]")
    .forEach((input) => (input.onchange = updateBatch));
  batchForm?.querySelectorAll("[data-node-batch-card]").forEach((card) => {
    card.onclick = (event) => {
      if (event.target.closest("input,button,label,select")) return;
      const input = card.querySelector("[data-batch-checkbox]");
      if (!input || input.disabled || batchForm.dataset.busy === "1") return;
      input.checked = !input.checked;
      updateBatch();
    };
  });
  const selectAll = batchForm?.querySelector("[data-batch-select-all]");
  if (selectAll)
    selectAll.onchange = () => {
      const shouldSelect = selectAll.checked;
      [...batchForm.querySelectorAll("[data-batch-checkbox]")]
        .filter((input) => input.dataset.batchEligible === "1")
        .forEach((input) => (input.checked = shouldSelect));
      updateBatch();
    };
  const clearBatch = batchForm?.querySelector("[data-batch-clear]");
  if (clearBatch)
    clearBatch.onclick = () => {
      batchForm
        .querySelectorAll("[data-batch-checkbox]")
        .forEach((input) => (input.checked = false));
      updateBatch();
    };
  const closeBatch = batchForm?.querySelector("[data-close-node-batch]");
  if (closeBatch)
    closeBatch.onclick = () => setNodeBatchMode(false, closeBatch);
  bindEvent(batchForm?.elements.engine, "change", updateBatch);
  bindEvent(batchForm?.elements.action, "change", updateBatch);
  updateBatch();
  if (batchForm)
    batchForm.onsubmit = async (event) => {
      event.preventDefault();
      const values = new FormData(batchForm);
      const action = String(values.get("action"));
      const engine = String(values.get("engine"));
      let selected = [
        ...batchForm.querySelectorAll("[data-batch-checkbox]:checked"),
      ].filter((input) =>
        batchAgentEligibility(agentsByID.get(input.value), action, engine)
          .eligible,
      );
      if (!selected.length || batchForm.dataset.busy === "1" || batchForm.dataset.confirming === "1")
        return;
      batchForm.dataset.confirming = "1";
      let confirmed = false;
      try {
        confirmed = await confirmAction(
          action === "upgrade-agent"
            ? `确定在 ${selected.length} 个在线节点上批量更新 Agent？升级期间节点会短暂离线。`
            : `确定在 ${selected.length} 个节点上执行 ${engineName(engine)} ${actionName(action)}？`,
          "提交批量任务",
        );
      } finally {
        batchForm.dataset.confirming = "";
      }
      if (!confirmed || batchForm.dataset.busy === "1")
        return;
      updateBatch();
      selected = [
        ...batchForm.querySelectorAll("[data-batch-checkbox]:checked"),
      ].filter((input) =>
        batchAgentEligibility(agentsByID.get(input.value), action, engine)
          .eligible,
      );
      if (!selected.length) return;
      setBatchBusy(true);
      const results = batchForm.querySelector("[data-batch-results]");
      const settled = [];
      for (const input of selected) {
        const agent = agentsByID.get(input.value);
        const eligibility = batchAgentEligibility(agent, action, engine);
        if (!eligibility.eligible) {
          settled.push({
            agent,
            error: new Error(`节点状态已变化：${eligibility.reason}`),
            ok: false,
          });
          continue;
        }
        try {
          const task = await api("/tasks", {
            method: "POST",
            body: JSON.stringify({
              agent_id: input.value,
              ...(action === "upgrade-agent" ? {} : { engine }),
              action,
            }),
          });
          settled.push({ agent, task, ok: true });
        } catch (error) {
          settled.push({ agent, error, ok: false });
        }
      }
      if (results) {
        results.hidden = false;
        results.innerHTML = `<header><b>批量结果</b><small>${settled.filter((item) => item.ok).length}/${settled.length} 成功</small></header>${settled.map((item) => `<div class="batch-result-row ${item.ok ? "ok" : "error"}"><span><b>${esc(item.agent?.name || item.agent?.id || "节点")}</b><small>${item.ok ? `任务 ${esc(item.task?.id || "已提交")}` : esc(item.error?.message || "提交失败")}</small></span>${item.ok ? "" : `<button type="button" class="button small" data-batch-retry="${esc(item.agent?.id || "")}" data-batch-retry-action="${esc(action)}" data-batch-retry-engine="${esc(engine)}">重试</button>`}</div>`).join("")}`;
      }
      setBatchBusy(false);
      const success = settled.filter((item) => item.ok).length;
      notify(success === settled.length ? `已提交 ${success} 个任务` : `已提交 ${success}/${settled.length} 个任务`, success === settled.length ? "success" : "error");
      bindBatchRetries(
        batchForm,
        action,
        engine,
        agentsByID,
        setBatchBusy,
      );
    };
  document.querySelectorAll("[data-open-enrollment]").forEach((button) => {
    button.onclick = () =>
      showEnrollmentDialog({
        tokenRows: enrollmentHistory.tokenRows || "",
        tokenCount: enrollmentHistory.tokenCount || 0,
        onDelete: async (id) => {
          await api(`/enrollment-tokens/${encodeURIComponent(id)}`, {
            method: "DELETE",
          });
          try {
            await refreshAgentPage();
          } catch (error) {
            notify(`添加记录刷新失败：${error.message}`, "error");
          }
        },
        onSubmit: async (name, adminHidden, close) => {
          const created = await api("/enrollment-tokens", {
            method: "POST",
            body: JSON.stringify({ name, admin_hidden: adminHidden }),
          });
          const command = enrollmentInstallCommand(created);
          close();
          showCommand(command, async () => {
            try {
              await refreshAgentPage();
            } catch (error) {
              notify(`添加记录刷新失败，部署命令未受影响：${error.message}`, "error");
            }
          });
        },
      });
  });
  bindEnrollmentRecordButtons(document);
  document
    .querySelectorAll("[data-agent-refresh]")
    .forEach((button) => (button.onclick = () => pollAgentMetrics()));
  document.querySelectorAll("[data-agent-visibility]").forEach((input) => {
    input.onchange = async () => {
      input.disabled = true;
      try {
        await api(`/agents/${encodeURIComponent(input.dataset.agentVisibility)}/visibility`, {
          method: "PUT",
          body: JSON.stringify({ admin_hidden: input.checked }),
        });
        notify(input.checked ? "已对管理员隐藏此节点" : "此节点已对管理员可见");
        await refreshAgentPage();
      } catch (error) {
        input.checked = !input.checked;
        input.disabled = false;
        notify(error.message, "error");
      }
    };
  });
  document.querySelectorAll("[data-open-agent-directory]").forEach((button) => {
    button.onclick = () => showAgentDirectoryDialog();
  });
  document.querySelectorAll("[data-node-capabilities]").forEach((section) => {
    const agentID = section.dataset.nodeCapabilities;
    const can = (capability) => permission(capability, agentsByID.get(agentID)) && agentsByID.get(agentID)?.can_manage !== false;
    section.querySelectorAll("[data-engine-capability]").forEach((input) => {
      const engine = input.dataset.engineCapability;
      const key = `${agentID}|${engine}`;
      // These are immediate-action controls, not an unsaved form draft.
      // Reconciliation updates attributes but a checkbox's dirty-checkedness
      // flag can retain its old property after an asynchronous service result.
      if (!pendingEngineCapabilities.has(key)) {
        input.checked = (agentsByID.get(agentID)?.capabilities || []).includes(engine);
        input.defaultChecked = input.checked;
      }
      if (pendingEngineCapabilities.has(key)) input.disabled = true;
      bindEvent(input, "change", async () => {
        if (!can("agents.manage") || pendingEngineCapabilities.has(key)) return;
        const enabled = input.checked;
        let awaitingService = false;
        pendingEngineCapabilities.add(key);
        input.disabled = true;
        try {
          const saved = await api(`/agents/${encodeURIComponent(agentID)}/capabilities/${engine}`, {
            method: "PUT", body: JSON.stringify({ enabled }),
          });
          input.checked = saved.enabled;
          input.defaultChecked = saved.enabled;
          awaitingService = Boolean(saved.task_id);
          notify(saved.task_id
            ? `${engineName(engine)} ${enabled ? "启动" : "停止"}任务已提交，成功后更新能力；离线节点上线后执行`
            : `${engineName(engine)} 能力已${enabled ? "开启" : "关闭"}（未安装内核，不执行启停）`);
        } catch (error) {
          input.checked = !enabled;
          notify(error.message, "error");
        } finally {
          pendingEngineCapabilities.delete(key);
          input.disabled = awaitingService || !can("agents.manage");
          try {
            await refreshAgentPage();
          } catch (error) {
            notify(error.message, "error");
          }
        }
      });
    });
  });
  document.querySelectorAll("[data-agent-name-form]").forEach((form) => {
    const agentID = form.dataset.agentNameForm;
    const can = (capability) => permission(capability, agentsByID.get(agentID)) && agentsByID.get(agentID)?.can_manage !== false;
    const button = form.querySelector("button[type=submit]");
    if (button) button.disabled = !can("agents.manage") || pendingAgentNames.has(agentID);
    bindEvent(form, "submit", async (event) => {
      event.preventDefault();
      if (!can("agents.manage") || pendingAgentNames.has(agentID)) return;
      const name = String(new FormData(form).get("name") || "").trim();
      if (!name || [...name].length > 100 || /[\u0000-\u001f\u007f-\u009f]/u.test(name)) {
        notify("节点名称须为 1–100 个字符，且不能包含控制字符", "error");
        return;
      }
      pendingAgentNames.add(agentID);
      form.dataset.busy = "1";
      if (button) button.disabled = true;
      try {
        const saved = await api(`/agents/${encodeURIComponent(agentID)}/name`, {
          method: "PUT", body: JSON.stringify({ name }),
        });
        const agent = agentsByID.get(agentID);
        if (agent) agent.name = saved.name;
        const input = form.elements.namedItem("name");
        // Keep any newer draft typed while the save request was in flight.
        if (input.value.trim() === name) input.value = saved.name;
        input.defaultValue = saved.name;
        await refreshAgentPage();
        notify("节点名称已保存");
      } catch (error) {
        notify(error.message, "error");
      } finally {
        pendingAgentNames.delete(agentID);
        delete form.dataset.busy;
        if (button) button.disabled = !can("agents.manage");
        document.querySelectorAll("[data-agent-name-form]").forEach((current) => {
          if (current.dataset.agentNameForm === agentID) current.querySelector("button[type=submit]").disabled = !can("agents.manage");
        });
      }
    });
  });
  document.querySelectorAll("[data-komari-form]").forEach((form) => {
    const can = (capability) => permission(capability, agentsByID.get(form.dataset.komariForm)) && agentsByID.get(form.dataset.komariForm)?.can_manage !== false;
    form.onsubmit = async (event) => {
      event.preventDefault();
      if (!can("agents.manage")) return;
      const uuid = String(new FormData(form).get("uuid") || "").trim();
      const agentID = form.dataset.komariForm;
      const button = form.querySelector("button[type=submit]");
      if (button) button.disabled = true;
      try {
        await api(`/agents/${encodeURIComponent(agentID)}/komari`, {
          method: "PUT",
          body: JSON.stringify({ uuid }),
        });
        const agent = agentsByID.get(agentID);
        if (agent) {
          agent.labels = { ...(agent.labels || {}) };
          if (uuid) agent.labels.komari_uuid = uuid;
          else delete agent.labels.komari_uuid;
        }
        await refreshAgentPage();
        notify(uuid ? "Komari 服务器已关联" : "Komari 服务器关联已清除");
      } catch (error) {
        notify(error.message, "error");
        if (button) button.disabled = false;
      }
    };
  });
  document.querySelectorAll("[data-agent-metrics]").forEach((root) => {
    const agent = agentsByID.get(root.dataset.agentMetrics);
    if (!agent) return;
    loadRegionDisplay(agent, root);
    if (root.querySelector("[data-komari-link]")) loadKomariDisplay(agent, root);
  });
  document.querySelectorAll("[data-region-edit]").forEach((button) => {
    button.onclick = (event) => {
      event.preventDefault();
      event.stopPropagation();
      const agent = (state.data.agents || []).find((item) => item.id === button.dataset.regionAvatar) || agentsByID.get(button.dataset.regionAvatar);
      if (!agent || !can("agents.manage", agent)) return;
      const release = cardInteractions.begin();
      openRegionPicker(agent, {
        api, esc, onClose: release,
        onSave: (code) => {
          for (const item of new Set([agent, agentsByID.get(agent.id), ...(state.data.agents || []).filter((item) => item.id === agent.id)])) {
            if (!item) continue;
            item.labels = { ...(item.labels || {}) };
            if (code) item.labels.region_code = code;
            else delete item.labels.region_code;
          }
          document.querySelectorAll("[data-agent-metrics]").forEach((root) => {
            if (root.dataset.agentMetrics === agent.id) loadRegionDisplay(agent, root);
          });
          notify(code ? "国家/地区旗帜已保存" : "已恢复自动识别旗帜");
        },
      });
    };
  });
  document.querySelectorAll("[data-upgrade-agent]").forEach((button) => {
    button.onclick = async () => {
      if (
        !(await confirmAction(
          "确定升级这个节点的 QAgent？控制面会把当前版本的 Agent 二进制签名下发到节点，原子替换后自动重连；升级期间节点会短暂离线。",
          "升级 Agent",
        ))
      )
        return;
      const task = await submitTask({
        agent_id: button.dataset.upgradeAgent,
        action: "upgrade-agent",
      });
      if (task) location.hash = "#tasks";
    };
  });
  document.querySelectorAll("[data-view-enrollment-command]").forEach((button) => {
    button.onclick = async () => {
      try {
        const created = await api(
          `/agents/${encodeURIComponent(button.dataset.viewEnrollmentCommand)}/enrollment-command`,
          { method: "POST" },
        );
        showCommand(enrollmentInstallCommand(created), null, "复制 Agent 安装命令");
      } catch (error) {
        notify(error.message, "error");
      }
    };
  });
  const cardGrid = document.querySelector(".node-card-grid");
  if (cardGrid && !batchForm) enableCardDrag(cardGrid);
  document.querySelectorAll("[data-copy-ip]").forEach((button) => {
    bindEvent(button, "click", async (event) => {
      // The card itself is a link to the node workspace; copying must not
      // navigate away from the overview.
      event.preventDefault();
      event.stopPropagation();
      const value = button.dataset.copyIp;
      if (!value) return;
      try {
        await navigator.clipboard.writeText(value);
      } catch {
        const fallback = document.createElement("textarea");
        fallback.value = value;
        fallback.style.position = "fixed";
        fallback.style.opacity = "0";
        document.body.append(fallback);
        fallback.select();
        document.execCommand("copy");
        fallback.remove();
      }
      const originalTitle = button.title;
      button.classList.add("copied");
      button.title = "已复制";
      window.setTimeout(() => {
        button.classList.remove("copied");
        if (button.isConnected) button.title = originalTitle;
      }, 1600);
    });
  });
  clearTimeout(state.agentPollTimer);
  if (agentPageActive())
    state.agentPollTimer = setTimeout(pollAgentMetrics, 2000);
}

function bindBatchRetries(form, action, engine, agentsByID, setBatchBusy) {
  form.querySelectorAll("[data-batch-retry]").forEach((button) => {
    button.onclick = async () => {
      if (form.dataset.busy === "1") return;
      const agentID = button.dataset.batchRetry;
      const agent = agentsByID.get(agentID);
      if (!agent) return;
      const eligibility = batchAgentEligibility(agent, action, engine);
      if (!eligibility.eligible) {
        notify(`无法重试：${eligibility.reason}`, "error");
        return;
      }
      setBatchBusy(true);
      try {
        const currentAgent = agentsByID.get(agentID);
        const currentEligibility = batchAgentEligibility(
          currentAgent,
          action,
          engine,
        );
        if (!currentEligibility.eligible) {
          notify(`无法重试：${currentEligibility.reason}`, "error");
          return;
        }
        const task = await api("/tasks", {
          method: "POST",
          body: JSON.stringify({
            agent_id: agentID,
            ...(action === "upgrade-agent" ? {} : { engine }),
            action,
          }),
        });
        button.closest(".batch-result-row").className = "batch-result-row ok";
        button.closest(".batch-result-row").querySelector("small").textContent = `任务 ${task?.id || "已提交"}`;
        button.remove();
        notify("重试任务已提交");
      } catch (error) {
        notify(error.message, "error");
      } finally {
        setBatchBusy(false);
      }
    };
  });
}

async function submitTask(payload) {
  try {
    const agent = state.data.agents?.find((item) => item.id === payload.agent_id);
    if (agent?.can_manage === false && !["deploy", "validate", "status"].includes(payload.action))
      throw new Error("共享节点的主机操作仅限所有者");
    const task = await api("/tasks", {
      method: "POST",
      body: JSON.stringify(payload),
    });
    notify("任务已提交");
    return task;
  } catch (error) {
    notify(error.message, "error");
    return null;
  }
}

async function loadMetricHistory(agentID) {
  const target = document.querySelector(
    `[data-metric-history="${CSS.escape(agentID)}"]`,
  );
  if (!target || target.dataset.loaded) return;
  target.dataset.loaded = "1";
  try {
    const samples = await api(`/metrics/${encodeURIComponent(agentID)}`);
    const chart = trafficChart(samples);
    if (!target.isConnected) return;
    if (chart) {
      const panel = document.createElement("section");
      panel.className = "metric-trend-panel";
      panel.setAttribute("aria-label", "最近 24 小时流量趋势");
      panel.innerHTML = `<header><b>流量趋势</b><small>最近 24 小时 · 每分钟采样</small></header>${chart}`;
      target.replaceWith(panel);
    } else {
      target.innerHTML =
        "<span>⌁</span><b>暂无指标趋势</b><small>节点上线并上报指标后，这里将显示最近 24 小时的上下行速率曲线。</small>";
    }
  } catch {
    if (target.isConnected) {
      target.dataset.loaded = "";
      target.innerHTML =
        "<span>⌁</span><b>指标趋势载入失败</b><small>点击节点资源刷新按钮后重试。</small>";
    }
  }
}

function updateAgentMetrics(item) {
  const can = (capability) => permission(capability, item) &&
    !(item.can_manage === false && ["operator", "agents.manage", "enrollment.manage"].includes(capability));
  const root = document.querySelector(
    `[data-agent-metrics="${CSS.escape(item.id)}"]`,
  );
  if (!root) return;
  loadRegionDisplay(item, root);
  if (
    typeof root.matches === "function" &&
    root.matches(".node-operations-workspace")
  ) {
    const commandShouldExist =
      can("enrollment.manage") && Boolean(item.enrollment_command_available);
    const commandExists = Boolean(
      root.querySelector("[data-view-enrollment-command]"),
    );
    if (commandShouldExist !== commandExists) requestAgentStructureRefresh();
  }
  const metrics = item.metrics || {};
  const setText = (name, value) => {
    const element = root.querySelector(`[data-metric-text="${name}"]`);
    if (element) element.textContent = value;
  };
  const setProgress = (name, available, value) => {
    const element = root.querySelector(`[data-metric-progress="${name}"]`);
    if (!element) return;
    element.value = available ? value : 0;
    element.dataset.available = available ? "1" : "0";
    element.dataset.level = !available
      ? "idle"
      : Number(value) >= 90
        ? "danger"
        : Number(value) >= 75
          ? "warn"
          : "normal";
  };
  const online = item.status === "online";
  const unavailable = metrics.collected_at ? "不可用" : "等待采集";
  root.dataset.available = metrics.collected_at ? "1" : "0";
  const dot = root.querySelector("[data-agent-status-dot]");
  if (dot) dot.className = `status-dot ${statusTone(item.status)}`;
  const status = root.querySelector("[data-agent-status-label]");
  if (status) status.textContent = online ? "在线" : "离线";
  const lastSeen = root.querySelector("[data-agent-heartbeat]");
  if (lastSeen) lastSeen.textContent = heartbeat(item.last_seen);
  root
    .querySelectorAll("[data-agent-version]")
    .forEach((element) => (element.textContent = item.version || "未知"));
  setText(
    "stamp",
    metrics.collected_at ? `采集于 ${ago(metrics.collected_at)}` : "等待资源数据",
  );
  setText(
    "cpu",
    metrics.cpu_available
      ? `${Number(metrics.cpu_percent).toFixed(1)}%`
      : unavailable,
  );
  setProgress("cpu", metrics.cpu_available, Number(metrics.cpu_percent || 0));
  setText(
    "memory",
    metrics.memory_available
      ? `${bytes(metrics.memory_used_bytes)} / ${bytes(metrics.memory_total_bytes)}`
      : unavailable,
  );
  setProgress(
    "memory",
    metrics.memory_available,
    percent(metrics.memory_used_bytes, metrics.memory_total_bytes),
  );
  setText(
    "disk",
    metrics.disk_available
      ? `${bytes(metrics.disk_used_bytes)} / ${bytes(metrics.disk_total_bytes)}`
      : unavailable,
  );
  setProgress(
    "disk",
    metrics.disk_available,
    percent(metrics.disk_used_bytes, metrics.disk_total_bytes),
  );
  setText(
    "download-rate",
    metrics.network_available ? rate(metrics.network_rx_bps) : unavailable,
  );
  setText(
    "upload-rate",
    metrics.network_available ? rate(metrics.network_tx_bps) : unavailable,
  );
  setText(
    "download-total",
    metrics.network_available ? bytes(metrics.network_rx_bytes) : "—",
  );
  setText(
    "upload-total",
    metrics.network_available ? bytes(metrics.network_tx_bytes) : "—",
  );
  Object.entries(item.runtime || {}).forEach(([engine, runtime]) => {
    const card = root.querySelector(`.service-${CSS.escape(engine)}`);
    const installed = Boolean(runtime.installed);
    const existingPending = Boolean(runtime.existing_config_available);
    const existingUnsupportedReason = String(
      runtime.existing_config_unsupported_reason || "",
    );
    // Structure transitions go through the interaction-aware, coalesced
    // refresh path and must not commit the comparison marker first: if the
    // render rejects, the next poll still sees the mismatch and retries
    // instead of leaving the DOM permanently stale.
    if (
      card?.dataset.runtimeStructure === "full" &&
      (card.dataset.coreInstalled !== (installed ? "1" : "0") ||
        card.dataset.existingPending !== (existingPending ? "1" : "0") ||
        card.dataset.existingUnsupported !== existingUnsupportedReason)
    ) {
      requestAgentStructureRefresh();
      return;
    }
    if (card && card.dataset.runtimeStructure !== "full")
      card.dataset.coreInstalled = installed ? "1" : "0";
    const version = root.querySelector(
      `[data-core-version="${CSS.escape(engine)}"]`,
    );
    if (version)
      version.textContent = runtime.installed
        ? conciseVersion(engine, runtime.version)
        : "尚未安装";
    const service = root.querySelector(
      `[data-core-service="${CSS.escape(engine)}"]`,
    );
    if (service) {
      service.textContent = existingUnsupportedReason
        ? "检测到但不可迁移"
        : installed
        ? serviceStatusName(runtime.service_status)
        : "未安装";
      service.closest(".engine-state").className =
        `engine-state ${existingUnsupportedReason ? "warn" : installed ? statusTone(runtime.service_status) : "muted"}`;
      service
        .closest(".service-card")
        ?.querySelectorAll("[data-service-action]")
        .forEach((button) => {
          button.disabled = serviceActionDisabled(
            button.dataset.serviceAction,
            online,
            installed,
            runtime.service_status,
            item,
          ) || (button.dataset.serviceAction !== "status" && !can("operator")) || Boolean(existingUnsupportedReason);
        });
    }
  });
  const installedSummary = root.querySelector("[data-core-installed-summary]");
  if (installedSummary) {
    const installedCount = (item.capabilities || []).filter(
      (engine) => item.runtime?.[engine]?.installed,
    ).length;
    installedSummary.textContent = `${item.os || ""} / ${item.arch || ""} · ${
      installedCount
        ? `${installedCount}/${(item.capabilities || []).length} 内核已安装`
        : "尚未安装内核"
    }`;
  }
  root.querySelectorAll(".core-version-form button[type=submit]").forEach(
    (button) => {
      const card = button.closest(".service-card");
      button.disabled =
        !online ||
        !can("operator") ||
        Boolean(card?.dataset.existingUnsupported);
    },
  );
  updatePublicIPDisplays(root, metrics, item.labels || {}, item.features || []);
}

async function pollAgentMetrics() {
  if (!agentPageActive()) return;
  clearTimeout(state.agentPollTimer);
  state.agentPollTimer = null;
  if (document.hidden) {
    clearTimeout(state.agentPollTimer);
    state.agentPollTimer = setTimeout(pollAgentMetrics, 2000);
    return;
  }
  const indicators = document.querySelectorAll("[data-metric-poll]");
  cardInteractions.defer(
    () =>
      indicators.forEach((element) => (element.textContent = "正在刷新…")),
    "metrics",
  );
  try {
    await metricsRefresh.run(
      async (signal) => {
        const preset = state.route === "agents";
        const selected = state.data.selectedAgent;
        const [items, deployments, configs] = await Promise.all([
          api("/agents", { signal }),
          preset && can("deployments.read") ? api("/deployments", { signal }) : [],
          preset && selected && can("agent-config.read") ? api(`/agents/${encodeURIComponent(selected)}/configs`, { signal }) : [],
        ]);
        return { items, preset, signature: presetConfigSignature(deployments, configs, selected) };
      },
      ({ items, preset, signature }) => {
        if (
          renderedAgentStructure === null &&
          Array.isArray(state.data.agents)
        )
          renderedAgentStructure = visibleAgentStructure(state.data.agents);
        const structureChanged =
          renderedAgentStructure !== null &&
          visibleAgentStructure(items) !== renderedAgentStructure;
        // Keep the shared runtime snapshot current even when an active card
        // interaction defers DOM patches. Other routes (notably live-config)
        // must not inherit the stale state that preceded an Agent upgrade.
        state.data.agents = items;
        syncActiveBatchSnapshot?.(items);
        if (structureChanged) {
          requestAgentStructureRefresh();
          return;
        }
        if (preset && signature !== renderedPresetConfigs) {
          requestAgentStructureRefresh();
          return;
        }
        if (
          state.data.nodeView === "detail" &&
          !items.some((item) => item.id === state.data.selectedAgent)
        ) {
          requestAgentStructureRefresh();
          return;
        }
        cardInteractions.defer(() => {
          items.forEach(updateAgentMetrics);
          const online = items.filter((item) => item.status === "online").length;
          const count = document.querySelector("[data-online-count]");
          if (count) {
            count.textContent = String(online);
            count.hidden = online === 0;
          }
          const sync = document.querySelector("[data-sync-state]");
          sync?.classList.toggle("inactive", online === 0);
          const syncLabel = document.querySelector("[data-sync-label]");
          if (syncLabel)
            syncLabel.textContent = online
              ? `${online} 个节点在线`
              : "等待节点连接";
          indicators.forEach((element) => (element.textContent = "刚刚更新"));
        }, "metrics");
      },
    );
  } catch {
    cardInteractions.defer(
      () =>
        indicators.forEach(
          (element) => (element.textContent = "刷新失败，保留上次数据"),
        ),
      "metrics",
    );
  } finally {
    clearTimeout(state.agentPollTimer);
    if (agentPageActive())
      state.agentPollTimer = setTimeout(pollAgentMetrics, 2000);
  }
}

  return {
    agents,
    nodeSettings,
    submitTask,
    bindCodeEditors,
    showCommand,
    pollAgentMetrics,
    updateAgentMetrics,
    cancelAgentInteractions,
    sharingHasUnsavedChanges: sharing.hasUnsavedChanges,
    compactPresetPage,
  };
}
