import { formatHostPort, manualConnectionAddressNote, publicAddressRows } from "./agent-addresses.js";
import { engineCapabilityToggles } from "./engine-capabilities.js";
import { regionAvatarMarkup } from "./regions.js";
import { agentBatchBarMarkup } from "./agent-batch-view.js";
import { orderedNodeList } from "./node-order.js";

function mihomoDevelopmentSourceFieldset(canMirror) {
  return `<fieldset class="release-channel-fieldset development-source-field" data-development-source hidden><legend>开发版来源</legend><div class="release-channel-options"><label><input type="radio" name="core_source" value="official" checked><span>MetaCubeX 官方（推荐）</span></label><label><input type="radio" name="core_source" value="mirror" ${canMirror ? "" : "disabled"}><span>vernesong/mihomo Alpha 镜像（第三方）</span></label></div>${canMirror ? "" : `<p class="source-upgrade-note">使用第三方镜像需先升级 Agent。</p>`}</fieldset>`;
}


// Rendering is independent of polling and mutation controllers.
export function createAgentView(ctx, { can, komariUUIDFor, komariNetworkMarkup }) {
  const { state, engines, can: permission, esc, engineName, statusTone, serviceStatusName, short, date, ago, heartbeat, percent, bytes, conciseVersion, rate, actionName, serviceActionDisabled, shell } = ctx;
  const cardIPRow = (row) => {
    const value = row.value || "";
    const title = value ? `复制 ${row.label} 地址` : "暂无地址";
    const aria = `复制 ${row.label} 公网地址 ${value}`;
    return `<span class="card-ip-row ${value ? "" : "empty"}" data-ip-family="${row.cls}" data-ip-source="${esc(row.source)}" ${value ? "" : "hidden"}><i class="ip-family ${row.cls}">${row.label}</i><code title="${esc(value)}">${esc(value || "未探测到")}</code><button type="button" class="card-ip-copy" data-copy-ip="${esc(value)}" aria-label="${esc(aria)}" title="${esc(title)}" ${value ? "" : "hidden"}><svg viewBox="0 0 24 24" aria-hidden="true"><rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0 2 2v8a2 2 0 0 0 2 2h2"/><path class="copy-check" d="m9.5 13.5 2 2 4-4.5"/></svg></button></span>`;
  };

  return ({ agents, tokens, presetMode, configByService, deploymentByService, accessByService, configDiffByService }) => {
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
              <header class="node-panel-heading"><div><h3>${isShared ? "已分配内核" : "内核管理"}</h3>${isShared ? "<small>使用独立配置</small>" : ""}</div><span data-installed-summary>${installedCount ? `${installedCount} 个已安装` : "尚未安装内核"}</span></header><div class="core-runtime-list">${services}</div>
            </section>
            <section id="${esc(tabID("metrics-panel"))}" class="node-tab-panel node-metrics-panel" data-node-panel="metrics" role="tabpanel" aria-labelledby="${esc(tabID("metrics-tab"))}" ${activeTab === "metrics" ? "" : "hidden"}><header class="node-panel-heading"><div><h3>流量趋势</h3><small>最近 24 小时</small></div><span data-metric-text="stamp">${metrics.collected_at ? `采集于 ${ago(metrics.collected_at)}` : "等待资源数据"}</span></header><section class="metric-trend-empty" data-metric-history="${esc(agent.id)}" aria-label="暂无指标趋势"><span>⌁</span><b>正在载入指标趋势</b><small>节点上报指标后显示最近 24 小时的上下行速率。</small></section></section>
          <section id="${esc(tabID("agent-panel"))}" class="node-tab-panel node-agent-panel" data-node-panel="agent" role="tabpanel" aria-labelledby="${esc(tabID("agent-tab"))}" ${activeTab === "agent" ? "" : "hidden"}><header class="node-panel-heading"><div><h3>Agent 与身份</h3></div><span data-agent-version>${esc(agent.version || "未知")}</span></header>
            ${isShared ? `<dl class="identity-list node-identity-list"><div><dt>节点 ID</dt><dd><code>${esc(agent.id)}</code></dd></div><div><dt>系统平台</dt><dd>${esc(agent.os)} / ${esc(agent.arch)}</dd></div><div><dt>安全通道</dt><dd>WSS · Ed25519 签名</dd></div></dl><a class="button small" href="#my-quota">查看共享分配</a>` : `
            ${can("agents.manage") ? `<section class="node-name-settings"><header><div><b>共享 Agent</b><small>按用户分配内核、端口与额度</small></div><button class="button small" type="button" data-agent-sharing="${esc(agent.id)}">管理共享</button></header></section>` : ""}
            <section class="node-capability-settings" aria-label="节点内核能力" data-node-capabilities="${esc(agent.id)}">
              <header><b>内核能力</b><small>关闭即停止服务，开启恢复管理并启动已安装内核。</small></header>
              ${engineCapabilityToggles(agent.capabilities || [], { supported: agent.supported_capabilities ?? agent.capabilities ?? [], writable: can("agents.manage"), node: true, transitions: agent.capability_transitions || {} })}
              <p class="node-capability-note">启停成功后生效 · 离线节点上线后执行 · 未安装内核仅切换能力，配置保留</p>
            </section>
            <section class="node-name-settings" aria-label="节点名称"><header><div><b>节点名称</b><small>仅修改显示名，节点 ID、连接及凭据不变。</small></div></header><form data-agent-name-form="${esc(agent.id)}"><label><span>显示名称</span><input name="name" maxlength="100" required autocomplete="off" value="${esc(agent.name)}" ${can("agents.manage") ? "" : "disabled"}></label><button class="button small" type="submit" ${can("agents.manage") ? "" : "disabled"}>保存名称</button></form></section>${agent.can_hide ? `<section class="node-name-settings" aria-label="管理员可见性"><header><div><b>管理员可见性</b><small>隐藏节点列表、配置、任务、日志、指标与审计；管理员仍可在目录查看基本信息、删除节点。采集、计费与连接不受影响。</small></div></header><label class="node-visibility-toggle"><input type="checkbox" data-agent-visibility="${esc(agent.id)}" ${agent.admin_hidden ? "checked" : ""} ${can("agents.manage") ? "" : "disabled"}><span>${agent.admin_hidden ? "已对管理员隐藏" : "对管理员可见"}</span></label></section>` : ""}<dl class="identity-list node-identity-list"><div><dt>节点 ID</dt><dd><code>${esc(agent.id)}</code></dd></div><div><dt>系统平台</dt><dd>${esc(agent.os)} / ${esc(agent.arch)}</dd></div><div><dt>Agent 版本</dt><dd data-agent-version>${esc(agent.version || "未知")}</dd></div><div><dt>注册时间</dt><dd>${date(agent.enrolled_at)}</dd></div><div><dt>安全通道</dt><dd>WSS · Ed25519 签名</dd></div></dl><section class="node-public-ips" aria-label="公网地址"><header><b>公网地址 · 双栈</b><small>手动设置优先 · 出口探测 · 默认路由接口 · 已验证连接来源</small><small class="node-address-note" data-node-connection-address ${connectionAddressNote ? "" : "hidden"}>${esc(connectionAddressNote)}</small></header>${addressRows.map((row) => `<div class="public-ip-row ${row.ok ? "" : "empty"}" data-ip-family="${row.cls}" data-ip-source="${esc(row.source)}" ${row.value ? "" : "hidden"}><span class="ip-family ${row.cls}">${row.label}</span><code>${esc(row.value || "未探测到")}</code><small>${esc(row.source)}</small></div>`).join("")}</section><section class="node-komari-settings" aria-label="Komari 联动"><header><div><b>Komari 联动</b><small>关联后在节点卡片显示流量周期、用量与额度。</small></div></header><form data-komari-form="${esc(agent.id)}"><label><span>Komari 服务器 UUID</span><input name="uuid" maxlength="100" autocomplete="off" value="${esc(komariUUIDFor(agent))}" placeholder="例如 4addbaf1-7ffb-474c-98ee-4ffd476755ff" ${can("agents.manage") ? "" : "disabled"}></label><button class="button small" type="submit" ${can("agents.manage") ? "" : "disabled"}>保存</button></form>${komariUUIDFor(agent) ? "" : `<p class="node-komari-empty">尚未关联 Komari 服务器</p>`}</section>${labels ? `<div class="labels">${labels}</div>` : ""}<footer class="node-identity-refresh"><span>节点身份已验证</span><div>${can("enrollment.manage") && agent.enrollment_command_available ? `<button class="button small" type="button" data-view-enrollment-command="${esc(agent.id)}">查看安装部署命令</button>` : ""}</div></footer>${can("agents.manage") ? `<section class="node-danger-zone"><span><b>删除节点</b><small>断开节点并清理关联配置；QAgent 不会被远程卸载。</small></span><button class="button small danger-button" type="button" data-delete="${esc(agent.id)}">删除节点</button></section>` : ""}`}</section>
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
    ? agentBatchBarMarkup({ engines, engineName, esc })
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

    return { visibleAgents, tokenRows };
  };
}
