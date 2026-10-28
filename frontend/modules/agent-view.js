import { coreInstallBlockReason } from "./core-install-state.js";
import { cardMoveGrip, cardMoveHelp } from "./card-move-view.js";
import { formatHostPort, manualConnectionAddressNote, publicAddressRows } from "./agent-addresses.js";
import { engineCapabilityToggles } from "./engine-capabilities.js";
import { regionAvatarMarkup } from "./regions.js";
import { agentBatchBarMarkup } from "./agent-batch-view.js";
import { orderedNodeList } from "./node-order.js";
import { agentPresenceMarkup } from "./agent-presence.js";
import {
  describeConnectionState,
  describeCoreRuntime,
  describeNodeMetrics,
  describeServiceAction,
  resolveRetainedNodeMetrics,
} from "./status-controls.js";
import { sharedInstancePresentation, sharedAllocationText } from "./shared-instance-state.js";

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
  state.data.lastKnownAgentMetrics ||= {};
  const nodeCards = visibleAgents
    .map((agent) => {
      const isShared = agent.can_manage === false;
      const sharedBadge = isShared ? '<span class="agent-shared-badge">共享</span>' : "";
      const can = (capability) => permission(capability, agent) &&
        !(agent.can_manage === false && ["operator", "agents.manage", "enrollment.manage"].includes(capability));
      const { metrics, retained: metricsRetained } = can("metrics.read")
        ? resolveRetainedNodeMetrics(agent, state.data.lastKnownAgentMetrics)
        : { metrics: {}, retained: false };
      const metricInfo = describeNodeMetrics(agent, metrics, {
        bytes,
        rate,
        percent,
        ago,
        date,
        retained: metricsRetained,
      });
      const connInfo = describeConnectionState(agent.status, agent.last_seen, { ago, date });
      const addressRows = publicAddressRows(
        metrics,
        agent.labels || {},
        agent.features || [],
      );
      const managedEngines = [...(agent.capabilities || [])];
      if (detailMode && !isShared) {
        for (const engine of agent.supported_capabilities || []) {
          if (!managedEngines.includes(engine) &&
              agent.runtime?.[engine]?.installed)
            managedEngines.push(engine);
        }
      }
      const connectionAddressNote = manualConnectionAddressNote(agent.labels);
      const services = managedEngines
        .map((engine) => {
          const key = `${agent.id}|${engine}`;
          const capabilityEnabled = (agent.capabilities || []).includes(engine);
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
          const installedKnown = typeof runtime.installed === "boolean";
          const coreVersion = installed ? conciseVersion(engine, runtime.version) : installedKnown ? "尚未安装" : "安装状态未知";
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
          const canUninstall = (agent.features || []).includes("core-uninstall-v1");
          const uninstallPending = Boolean(state.data.coreUninstallTasks?.[key]);
          const installReason = coreInstallBlockReason(agent, engine, permission, {
            pending: Boolean(state.data.coreInstallTasks?.[key]),
          });
          const instanceState = sharedInstancePresentation(agent, engine);
          const coreInfo = describeCoreRuntime(engine, runtime, {
            capabilityEnabled,
            online: agent.status === "online",
            isShared,
            deployed,
            uninstallPending,
            canUninstall,
            conciseVersion,
            serviceStatusName,
            statusTone,
          });
          const serviceState = (isShared && runtime.instance)
            ? instanceState.label
            : (!installedKnown ? "状态未知" : (coreInfo.serviceState || (existingBlocked ? "检测到但不可迁移" : "")));
          const serviceTone = (isShared && runtime.instance)
            ? instanceState.tone
            : (!installedKnown ? "muted" : coreInfo.serviceTone);
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
              : `<button class="button primary service-config" type="button" data-config="${esc(agent.id)}" data-engine="${esc(engine)}">配置</button>`;
          }
          if (!presetMode) {
            if (isShared) {
              const sharedActionGroup = installed
                ? `<div class="core-action-group" aria-label="${esc(engineName(engine))} 本人实例操作">${["status", "start", "restart", "stop"].map(action => `<button class="core-action ${action === "stop" ? "danger" : ""}" type="button" data-task-agent="${esc(agent.id)}" data-task-engine="${esc(engine)}" data-task-action="${action}" data-service-action="${action}" aria-label="${esc(`${actionName(action)} ${engineName(engine)}`)}" title="${esc(actionName(action))}" ${serviceActionDisabled(action, agent.status === "online", installed, runtime.service_status, agent, engine) ? "disabled" : ""}>${serviceActionIcons[action]}</button>`).join("")}</div>`
                : "";
              return `<article class="service-card core-runtime-row is-shared-core service-${esc(engine)}" data-runtime-structure="full" data-core-installed="${installedKnown ? installed ? 1 : 0 : "unknown"}" data-capability-enabled="${capabilityEnabled ? 1 : 0}" data-existing-pending="0" data-existing-unsupported="" data-runtime-state="${coreInfo.runtimeState}">
                <div class="core-runtime-summary">
                  <div class="core-card-upper">
                    <div class="core-runtime-name"><span class="engine-badge ${esc(engine)}">${esc(engineName(engine))}</span><span class="engine-state ${serviceTone}" title="个人共享实例状态"><i></i><b data-core-service="${esc(engine)}">${esc(serviceState)}</b></span></div>
                    <div class="core-runtime-version"><small>当前版本</small><strong data-core-version="${esc(engine)}" title="${esc(installed ? runtime.version || "版本未知" : coreVersion)}">${esc(coreVersion)}</strong><small class="core-install-hint" data-instance-reason="${esc(engine)}" ${instanceState.reason ? "" : "hidden"}>${esc(instanceState.reason)}</small>${!installed ? `<small class="core-install-hint" data-core-install-block ${installReason ? "" : "hidden"}>${esc(installReason)}</small>` : ""}</div>
                  </div>
                  <div class="core-multiuser-split is-recipient-split" data-core-recipient-split="${esc(engine)}" aria-label="${esc(engineName(engine))} 个人实例与宿主状态">
                    <span class="core-split-item"><small>个人实例</small><b class="tone-${runtime.instance ? serviceTone : coreInfo.userInstanceTone}" data-core-user-instance="${esc(engine)}" data-instance-version="${esc(engine)}" title="${esc(runtime.instance?.observed_at ? `实例状态更新于 ${date(runtime.instance.observed_at)}` : "等待实例状态上报")}">${esc(runtime.instance ? instanceState.version : `${coreInfo.userInstanceLabel}${coreInfo.userConfigVersion ? ` · v${coreInfo.userConfigVersion}` : ""}`)}</b></span>
                    <span class="core-split-item"><small>宿主内核</small><b class="tone-${coreInfo.hostInstallTone}" data-core-host-state="${esc(engine)}">${esc(coreInfo.hostInstallLabel)}</b></span>
                  </div>
                  <div class="core-runtime-actions">${sharedActionGroup}<div class="core-package-buttons">${!installed ? `<button class="button small primary" type="button" data-open-version-form ${installReason ? `disabled title="${esc(installReason)}"` : ""}>安装</button>` : ""}${can("agent-config.read") ? `<button class="button small primary" type="button" data-config="${esc(agent.id)}" data-engine="${esc(engine)}">我的配置</button>` : ""}</div></div>
                </div>
                ${!installed ? `<details class="core-version-panel version-drawer"><summary><b>安装 ${esc(engineName(engine))}</b><span>收起</span></summary><div class="runtime-drawer-body"><form class="core-version-form" data-version-agent="${esc(agent.id)}" data-version-engine="${esc(engine)}"><fieldset class="release-channel-fieldset"><legend>版本来源</legend><div class="release-channel-options"><label><input type="radio" name="release_channel" value="stable" checked><span>最新稳定版</span></label></div></fieldset><button class="button small" type="submit" ${installReason ? `disabled title="${esc(installReason)}"` : ""}>安装内核</button><small data-core-install-hint>${esc(installReason || "受控准备缺少的稳定版内核；已有版本直接复用，不启停宿主服务。")}</small><small data-core-install-status role="status" aria-live="polite" ${state.data.coreInstallFeedback?.[key] ? "" : "hidden"}>${esc(state.data.coreInstallFeedback?.[key] || "")}</small></form></div></details>` : ""}
              </article>`;
            }
            const runtimeActions = installed
              ? ["status", "start", "restart", "stop"]
                  .map(
                    (action) => {
                      const actionInfo = describeServiceAction(action, {
                        online: agent.status === "online",
                        installed,
                        capabilityEnabled,
                        serviceStatus: runtime.service_status,
                        agent,
                        can,
                        existingUnsupportedReason,
                        serviceActionDisabled,
                        actionName,
                        engineName,
                        engine,
                      });
                      const isDisabled = existingBlocked || !capabilityEnabled || (action !== "status" && !can("operator")) || serviceActionDisabled(action, agent.status === "online", installed, runtime.service_status, agent, engine);
                      const buttonTitle = isDisabled ? (actionInfo.title || actionName(action)) : actionName(action);
                      return `<button class="core-action ${action === "stop" ? "danger" : ""}" type="button" data-task-agent="${esc(agent.id)}" data-task-engine="${esc(engine)}" data-task-action="${action}" data-service-action="${action}" aria-label="${esc(`${actionName(action)} ${engineName(engine)}`)}" title="${esc(buttonTitle)}" ${actionInfo.reason ? `data-disabled-reason="${esc(actionInfo.reason)}"` : ""} ${isDisabled ? "disabled" : ""}>${serviceActionIcons[action]}</button>`;
                    },
                  )
                  .join("")
              : "";
            const versionButtonDisabled = existingBlocked || !capabilityEnabled || Boolean(installReason);
            const versionButtonTitle = installReason || (existingBlocked
              ? `不可迁移：${existingUnsupportedReason}`
              : !capabilityEnabled
                ? "内核已关闭，请先在 Agent 页启用"
                : installed
                  ? `管理 ${engineName(engine)} 版本`
                  : `安装 ${engineName(engine)} 内核`);
            const versionSubmitDisabled = existingBlocked || !capabilityEnabled || agent.status !== "online" || !can("operator") || Boolean(installReason);
            const versionSubmitTitle = installReason || (existingBlocked
              ? existingUnsupportedReason
              : !capabilityEnabled
                ? "请先在 Agent 页启用"
                : agent.status !== "online"
                  ? "节点当前离线，上线后可执行安装或版本切换"
                  : !can("operator")
                    ? "当前账号无操作权限"
                    : "");
            const hostConfigVersion = deployed?.config_version || runtime?.config_version || 0;
            const sharedSplitMarkup = coreInfo.shared.hasShared
              ? `<div class="core-multiuser-split is-owner-split" data-core-shared-list="${esc(engine)}" aria-label="${esc(engineName(engine))} 多用户内核状态">
                  <div class="core-multiuser-head">
                    <span class="core-split-scope"><b>主实例</b><em class="tone-${serviceTone}" data-core-host-instance="${esc(engine)}">${esc(serviceState)}</em>${hostConfigVersion ? `<code>v${hostConfigVersion}</code>` : ""}</span>
                    <span class="core-split-scope"><b>共享实例</b><em data-core-shared-counts="${esc(engine)}">${esc(coreInfo.shared.summaryDetail || `${coreInfo.shared.runningCount}/${coreInfo.shared.totalCount} 运行中`)}</em></span>
                  </div>
                  <div class="core-shared-user-list">
                    ${coreInfo.shared.items.map((item) => `<span class="core-shared-user-chip ${item.tone}" data-share-id="${esc(item.shareId)}" data-user-state="${esc(item.state)}" title="${esc(`${item.userLabel} · ${item.statusLabel}${item.configVersion ? ` · 配置 v${item.configVersion}` : ""}`)}"><i aria-hidden="true"></i><b>${esc(item.username)}</b><span>${esc(item.statusLabel)}</span>${item.configVersion ? `<code>v${item.configVersion}</code>` : ""}</span>`).join("")}
                  </div>
                </div>`
              : "";
            return `<article class="service-card core-runtime-row service-${esc(engine)}" data-runtime-structure="full" data-core-installed="${installedKnown ? installed ? 1 : 0 : "unknown"}" data-capability-enabled="${capabilityEnabled ? 1 : 0}" data-existing-pending="${existingPending ? 1 : 0}" data-existing-unsupported="${esc(existingUnsupportedReason)}" data-runtime-state="${coreInfo.runtimeState}" data-shared-count="${coreInfo.shared.totalCount}">
              <div class="core-runtime-summary">
                <div class="core-card-upper">
                  <div class="core-runtime-name"><span class="engine-badge ${esc(engine)}">${esc(engineName(engine))}</span>${optionalImportChip}<span class="engine-state ${serviceTone}" title="宿主主实例状态"><i></i><b data-core-service="${esc(engine)}">${esc(serviceState)}</b></span>${coreInfo.shared.hasShared ? `<span class="core-multiuser-pill ${coreInfo.shared.summaryTone}" data-core-shared-summary="${esc(engine)}" title="${esc(`多用户共享实例：${coreInfo.shared.runningCount}/${coreInfo.shared.totalCount} 运行中`)}">${esc(coreInfo.shared.summaryLabel)}</span>` : ""}</div>
                  <div class="core-runtime-version"><small>当前版本</small><strong data-core-version="${esc(engine)}" title="${esc(installed ? runtime.version || "版本未知" : coreVersion)}">${esc(coreVersion)}</strong><small class="core-install-hint" data-core-install-block ${installReason ? "" : "hidden"}>${esc(installReason)}</small></div>
                </div>
                ${sharedSplitMarkup}
                <div class="core-runtime-actions">${runtimeActions ? `<div class="core-action-group" aria-label="${esc(engineName(engine))} 服务操作">${runtimeActions}</div>` : ""}<div class="core-package-buttons"><button class="button small ${installed ? "" : "primary"}" type="button" data-open-version-form title="${esc(versionButtonTitle)}" ${versionButtonDisabled ? "disabled" : ""}>${existingBlocked ? "不可迁移" : !capabilityEnabled ? "已关闭" : installed ? "版本" : "安装"}</button>${installed ? `<button class="button small danger-button" type="button" data-core-uninstall data-task-agent="${esc(agent.id)}" data-task-engine="${esc(engine)}" aria-label="卸载 ${esc(engineName(engine))} 内核" title="${uninstallPending ? "卸载任务执行中，请等待状态刷新" : canUninstall ? `卸载 ${esc(engineName(engine))} 内核` : "请先升级 Agent 后卸载内核"}" ${!canUninstall || existingBlocked || uninstallPending || serviceActionDisabled("uninstall", agent.status === "online", installed, runtime.service_status, agent) ? "disabled" : ""}>卸载内核</button>` : ""}</div></div>
              </div>
              <details class="core-version-panel version-drawer"><summary><b>${installed ? "版本管理" : `安装 ${esc(engineName(engine))}`}</b><span>收起</span></summary><div class="runtime-drawer-body"><form class="core-version-form" data-version-agent="${esc(agent.id)}" data-version-engine="${esc(engine)}"><fieldset class="release-channel-fieldset"><legend>版本来源</legend><div class="release-channel-options"><label><input type="radio" name="release_channel" value="stable" checked><span>最新稳定版</span></label><label><input type="radio" name="release_channel" value="development"><span>最新开发版</span></label><label><input type="radio" name="release_channel" value="custom"><span>指定版本</span></label></div></fieldset>${mihomoDevelopmentSourceFieldset(canMirror)}<label class="custom-version-field"><span>指定版本</span><input name="custom_version" maxlength="64" autocomplete="off" placeholder="例如 1.19.29"></label><button class="button small" type="submit" ${versionSubmitTitle ? `title="${esc(versionSubmitTitle)}"` : ""} ${versionSubmitDisabled ? "disabled" : ""}>${existingBlocked ? "不可自动迁移" : installed ? "升级或切换版本" : "安装内核"}</button><small data-core-install-hint>${installReason ? esc(installReason) : existingBlocked ? esc(existingUnsupportedReason) : !capabilityEnabled ? "先在 Agent 页启用，才能切换版本" : installed ? "Release · SHA-256 校验" : "安装至 QAgent 专用目录，不影响系统已有内核 · Release · SHA-256 校验"}</small><small data-core-install-status role="status" aria-live="polite" ${state.data.coreInstallFeedback?.[key] ? "" : "hidden"}>${esc(state.data.coreInstallFeedback?.[key] || "")}</small></form></div></details>
            </article>`;
          }
          return `<article class="service-card service-${esc(engine)}" data-refresh-key="service-${esc(engine)}" data-runtime-structure="full" data-core-installed="${installedKnown ? installed ? 1 : 0 : "unknown"}" data-capability-enabled="${capabilityEnabled ? 1 : 0}" data-existing-pending="${existingPending ? 1 : 0}" data-existing-unsupported="${esc(existingUnsupportedReason)}" data-runtime-state="${coreInfo.runtimeState}">
            <div class="service-card-main ${presetMode ? "" : "operations-only"}">
              <div class="service-overview"><header><span class="service-engine-title"><span class="engine-badge ${esc(engine)}">${esc(engineName(engine))}</span>${optionalImportChip}</span><span class="engine-state ${serviceTone}"><i></i><b data-core-service="${esc(engine)}">${esc(serviceState)}</b></span></header><div class="service-version"><span class="service-version-label"><small>内核版本</small>${isShared ? "" : `<button class="service-version-toggle" type="button" data-open-version-form aria-label="打开 ${esc(engineName(engine))} ${installed ? "版本切换" : "安装内核"}" ${installReason ? `disabled title="${esc(installReason)}"` : ""}>${existingBlocked ? "不可迁移" : installed ? "切换版本" : "安装内核"}</button>`}</span><strong data-core-version="${esc(engine)}" title="${esc(installed ? runtime.version || "版本未知" : coreVersion)}">${esc(coreVersion)}</strong></div></div>
              ${presetMode ? `<div class="service-deployment"><dl class="service-facts"><div><dt>已部署配置</dt><dd>${deployed?.config_version ? `v${deployed.config_version}` : "—"}</dd></div><div><dt>已保存配置</dt><dd>${saved?.version ? `v${saved.version}` : "—"}</dd></div></dl>${drift ? `<div class="deployment-drift"><span>${deployed ? "已保存版本尚未部署" : "已保存配置尚未部署"}</span><b>待部署 v${saved.version}</b></div>` : ""}${configDiff ? `<details class="config-diff-drawer"><summary>查看配置差异 <i>＋</i></summary>${configDiff}</details>` : ""}<div class="service-endpoint ${endpoint ? "" : "empty"}">${endpoint ? `<span><b>${esc(firstProfile?.protocol || "客户端入站")}</b><small>${esc(firstProfile?.profile?.format || "已部署配置")}</small></span><code>${esc(endpoint)}</code>` : `<b>${deployed ? "自定义配置" : saved ? "尚未部署" : "尚未配置"}</b>`}</div></div>` : ""}
              ${primaryActions ? `<div class="service-primary-action">${primaryActions}</div>` : ""}
            </div>
              ${isShared ? "" : `<details class="runtime-drawer version-drawer"><summary><span><b>${installed ? "版本切换" : "安装内核"}</b><small>${existingBlocked ? "检测到的现有服务未被接管" : installed ? "升级或切换内核版本" : "从 Release 安装"}</small></span><i>＋</i></summary><div class="runtime-drawer-body"><form class="core-version-form" data-version-agent="${esc(agent.id)}" data-version-engine="${esc(engine)}"><fieldset class="release-channel-fieldset"><legend>版本来源</legend><div class="release-channel-options"><label><input type="radio" name="release_channel" value="stable" checked><span>最新稳定版</span></label><label><input type="radio" name="release_channel" value="development"><span>最新开发版</span></label><label><input type="radio" name="release_channel" value="custom"><span>指定版本</span></label></div></fieldset>${mihomoDevelopmentSourceFieldset(canMirror)}<label class="custom-version-field"><span>指定版本</span><input name="custom_version" maxlength="64" autocomplete="off" placeholder="例如 1.19.29"></label><button class="button small" type="submit" ${installReason ? `disabled title="${esc(installReason)}"` : ""}>${existingBlocked ? "不可自动迁移" : installed ? "升级或切换版本" : "安装内核"}</button><small data-core-install-hint>${installReason ? esc(installReason) : installed ? "Release · SHA-256 校验" : "安装至 QAgent 专用目录，不影响系统已有内核 · Release · SHA-256 校验"}</small><small data-core-install-status role="status" aria-live="polite" ${state.data.coreInstallFeedback?.[key] ? "" : "hidden"}>${esc(state.data.coreInstallFeedback?.[key] || "")}</small></form></div></details>`}
            ${presetMode && access?.profiles?.length ? `<a class="service-client-access" href="#client-access" data-client-agent="${esc(agent.id)}" data-client-engine="${esc(engine)}"><span><b>客户端配置</b><small>${esc(access.source)} · ${esc(access.address)}</small></span><strong>${access.profiles.length} 个入站</strong></a>` : ""}
          </article>`;
        })
        .join("");
      const labels = Object.entries(agent.labels || {})
        .filter(([key]) => !key.startsWith("client_profile_name_"))
        .map(([key, value]) => `<span>${esc(key)}=${esc(value)}</span>`)
        .join("");
      if (detailMode) {
        const installedCount = managedEngines.filter(
          (engine) => agent.runtime?.[engine]?.installed,
        ).length;
        const serviceCardsList = managedEngines.map((engine, index) => ({
          engine,
          enabled: (agent.capabilities || []).includes(engine),
          markup: services.split(/(?=<article class="service-card core-runtime-row )/).filter(Boolean)[index] || "",
        }));
        const enabledServiceCards = serviceCardsList.filter((item) => item.enabled).map((item) => item.markup).join("");
        const disabledServiceCards = serviceCardsList.filter((item) => !item.enabled).map((item) => item.markup).join("");
        const enabledCount = serviceCardsList.filter((item) => item.enabled).length;
        const disabledCount = serviceCardsList.filter((item) => !item.enabled).length;
        const hasArchivedSubpage = !isShared && Boolean(disabledServiceCards);
        const activeCoresSubpage = hasArchivedSubpage && state.data.nodeCoresSubpage === "archived"
          ? "archived"
          : "active";
        const allowedAgentSubpages = can("agents.manage")
          ? ["identity", "capabilities", "settings", "lifecycle"]
          : ["identity", "capabilities", "settings"];
        const activeAgentSubpage = allowedAgentSubpages.includes(state.data.nodeAgentSubpage)
          ? state.data.nodeAgentSubpage
          : "identity";
        const groupedServicesMarkup = !hasArchivedSubpage
          ? `<div class="cores-stage-items node-subpage-panel is-active" data-node-subpanel="cores:active" aria-label="已启用内核">${enabledServiceCards || services}</div>`
          : `<div class="cores-stage-items node-subpage-panel ${activeCoresSubpage === "active" ? "is-active" : ""}" data-node-subpanel="cores:active" aria-label="已启用内核">${enabledServiceCards}</div><div class="cores-stage-items is-disabled-grid node-subpage-panel ${activeCoresSubpage === "archived" ? "is-active" : ""}" data-node-subpanel="cores:archived" aria-label="已关闭">${disabledServiceCards}</div>`;
        const activeTab = ["cores", "agent"].includes(
          state.data.nodeSettingsTab,
        )
          ? state.data.nodeSettingsTab
          : "cores";
        const tabID = (name) => `node-${agent.id}-${name}`;
        const tabButton = (name, label, count = "") =>
          `<button id="${esc(tabID(`${name}-tab`))}" type="button" role="tab" data-node-tab="${name}" data-tab-title="${esc(label)}" aria-controls="${esc(tabID(`${name}-panel`))}" aria-selected="${activeTab === name}" tabindex="${activeTab === name ? 0 : -1}">${label}${count ? `<span>${count}</span>` : ""}</button>`;
        const subtabButton = (group, subpage, label, activeSubpage, count = "", heading = false) => {
          const selected = activeSubpage === subpage;
          const titleTag = heading ? "h3" : "b";
          return `<button type="button" role="tab" class="node-subtab-btn" data-node-subtab="${group}:${subpage}" data-subtab-title="${esc(label)}" aria-selected="${selected}" tabindex="${selected ? 0 : -1}"><${titleTag}>${esc(label)}</${titleTag}>${count !== "" ? `<span>${esc(String(count))}</span>` : ""}</button>`;
        };
        const sharedCoreTotals = managedEngines.reduce(
          (acc, engine) => {
            const list = Array.isArray(agent.runtime?.[engine]?.shared_instances)
              ? agent.runtime[engine].shared_instances
              : [];
            acc.total += list.length;
            acc.running += list.filter((item) => item?.running || ["active", "running"].includes(item?.service_status)).length;
            return acc;
          },
          { total: 0, running: 0 },
        );
        const installedSummaryText = !installedCount
          ? "尚未安装内核"
          : sharedCoreTotals.total > 0
            ? `${installedCount} 个已安装 · 多用户实例 ${sharedCoreTotals.running}/${sharedCoreTotals.total} 运行`
            : `${installedCount} 个已安装`;
        const sharedHeaderNote = isShared ? sharedAllocationText(agent, bytes) : "";
        return `<section class="node-operations-workspace" id="settings-node-${esc(agent.id)}" data-refresh-key="agent-${esc(agent.id)}" data-agent-node="${esc(agent.id)}" data-agent-metrics="${esc(agent.id)}" data-state="${agent.status === "online" ? "online" : "offline"}" data-available="${metricInfo.hasSample ? 1 : 0}" data-freshness="${metricInfo.freshnessState}">
          <div class="node-operations-deck" aria-label="节点资源">
            <header class="node-operations-header">
              <div class="node-operations-title">
                ${regionAvatarMarkup(agent, esc, can("agents.manage"))}
                <div class="node-identity-cluster">
                  <div class="node-title-line"><h2 title="${esc(agent.name)}">${esc(agent.name)}</h2><span class="node-live-state">${sharedBadge}${agentPresenceMarkup(agent.status, { tracked: true })}<small data-agent-heartbeat title="${esc(connInfo.heartbeatTitle)}">${esc(heartbeat(agent.last_seen))}</small></span></div>
                  <div class="node-meta-line"><code>${esc(agent.os)} / ${esc(agent.arch)} · ${esc(short(agent.id))}</code><span class="node-card-stamp" data-metric-text="stamp" data-freshness="${metricInfo.freshnessState}" title="${esc(metricInfo.stampTitle)}">${esc(metricInfo.stampText)}</span></div>
                </div>
              </div>
              <div class="node-operations-toolbar">
                <div class="node-operations-actions">${isShared ? '<a class="button small" href="#my-quota">共享额度</a>' : ""}${can("metrics.read") ? `<button class="button small" type="button" data-agent-refresh title="刷新节点状态">刷新</button>` : ""}${can("operator") ? `<button type="button" class="button primary small" data-upgrade-agent="${esc(agent.id)}">升级 Agent</button>` : ""}</div>
              </div>
            </header>
            <section class="node-resource-strip" aria-label="节点资源"><div><span>CPU</span><strong data-metric-text="cpu">${esc(metricInfo.cpu.text)}</strong><progress aria-label="CPU 使用率" data-metric-progress="cpu" data-available="${metricInfo.cpu.available ? 1 : 0}" data-level="${metricInfo.cpu.level}" max="100" value="${metricInfo.cpu.value}"></progress></div><div><span>内存</span><strong data-metric-text="memory">${esc(metricInfo.memory.text)}</strong><progress aria-label="内存使用率" data-metric-progress="memory" data-available="${metricInfo.memory.available ? 1 : 0}" data-level="${metricInfo.memory.level}" max="100" value="${metricInfo.memory.value}"></progress></div><div><span>磁盘</span><strong data-metric-text="disk">${esc(metricInfo.disk.text)}</strong><progress aria-label="根磁盘使用率" data-metric-progress="disk" data-available="${metricInfo.disk.available ? 1 : 0}" data-level="${metricInfo.disk.level}" max="100" value="${metricInfo.disk.value}"></progress></div><div class="node-resource-network"><span>网络</span><strong>↓ <i data-metric-text="download-rate">${esc(metricInfo.network.downloadRateText)}</i> · ↑ <i data-metric-text="upload-rate">${esc(metricInfo.network.uploadRateText)}</i></strong>${komariNetworkMarkup(agent)}<small class="node-resource-cumulative">累计 ↓ <b data-metric-text="download-total">${esc(metricInfo.network.downloadTotalText)}</b> · ↑ <b data-metric-text="upload-total">${esc(metricInfo.network.uploadTotalText)}</b></small></div><span class="machine-resource-live" data-metric-poll role="status" aria-label="资源自动更新"></span></section>
          </div>

          <div class="node-operations-workbench">
            <nav class="node-settings-tabs" role="tablist" aria-label="节点设置分区">${tabButton("cores", "内核", `${installedCount}/${managedEngines.length}`)}${tabButton("agent", "Agent")}</nav>
            <div class="node-settings-panels">
            <section id="${esc(tabID("cores-panel"))}" class="node-tab-panel node-cores-panel" data-node-panel="cores" role="tabpanel" aria-labelledby="${esc(tabID("cores-tab"))}" ${activeTab === "cores" ? "" : "hidden"}>
              <header class="node-panel-heading subpage-heading">
                <div class="subpage-heading-left">
                  <nav class="node-subpage-nav" role="tablist" aria-label="内核管理分区">
                    ${subtabButton("cores", "active", isShared ? "已分配内核" : "已启用", activeCoresSubpage, enabledCount, true)}
                    ${hasArchivedSubpage ? subtabButton("cores", "archived", "已关闭", activeCoresSubpage, disabledCount) : ""}
                  </nav>
                  ${sharedHeaderNote ? `<small>${esc(sharedHeaderNote)}</small>` : ""}
                </div>
                <span data-installed-summary>${esc(installedSummaryText)}</span>
              </header>
              <div class="core-runtime-list">${groupedServicesMarkup}</div>
            </section>
            <section id="${esc(tabID("agent-panel"))}" class="node-tab-panel node-agent-panel" data-node-panel="agent" role="tabpanel" aria-labelledby="${esc(tabID("agent-tab"))}" ${activeTab === "agent" ? "" : "hidden"}>
              <header class="node-panel-heading subpage-heading">
                <div class="subpage-heading-left">
                  ${isShared ? `<div class="panel-heading-copy"><h3>Agent 与身份</h3></div>` : `<nav class="node-subpage-nav" role="tablist" aria-label="Agent 与身份分区">
                    ${subtabButton("agent", "identity", "身份与地址", activeAgentSubpage)}
                    ${subtabButton("agent", "capabilities", "内核开关", activeAgentSubpage)}
                    ${subtabButton("agent", "settings", "名称与联动", activeAgentSubpage)}
                    ${can("agents.manage") ? subtabButton("agent", "lifecycle", "共享与删除", activeAgentSubpage) : ""}
                  </nav>`}
                </div>
                <span data-agent-version>${esc(agent.version || "未知")}</span>
              </header>
              ${isShared ? `<div class="agent-subpage-body node-subpage-panel is-active" data-node-subpanel="agent:identity"><dl class="identity-list node-identity-list"><div><dt>节点 ID</dt><dd><code>${esc(agent.id)}</code></dd></div><div><dt>系统平台</dt><dd>${esc(agent.os)} / ${esc(agent.arch)}</dd></div><div><dt>安全通道</dt><dd>WSS · Ed25519 签名</dd></div></dl><a class="button small" href="#my-quota">查看共享分配</a></div>` : `
              <div class="agent-subpage-body group-identity node-subpage-panel ${activeAgentSubpage === "identity" ? "is-active" : ""}" data-node-subpanel="agent:identity" aria-label="身份与地址">
                <dl class="identity-list node-identity-list"><div><dt>节点 ID</dt><dd><code>${esc(agent.id)}</code></dd></div><div><dt>系统平台</dt><dd>${esc(agent.os)} / ${esc(agent.arch)}</dd></div><div><dt>Agent 版本</dt><dd data-agent-version>${esc(agent.version || "未知")}</dd></div><div><dt>注册时间</dt><dd>${date(agent.enrolled_at)}</dd></div><div><dt>安全通道</dt><dd>WSS · Ed25519 签名</dd></div></dl>
                <section class="node-public-ips" aria-label="公网地址"><header><b>公网地址 · 双栈</b><small>手动设置优先 · 出口探测 · 默认路由接口 · 已验证连接来源</small><small class="node-address-note" data-node-connection-address ${connectionAddressNote ? "" : "hidden"}>${esc(connectionAddressNote)}</small></header>${addressRows.map((row) => `<div class="public-ip-row ${row.ok ? "" : "empty"}" data-ip-family="${row.cls}" data-ip-source="${esc(row.source)}" ${row.value ? "" : "hidden"}><span class="ip-family ${row.cls}">${row.label}</span><code>${esc(row.value || "未探测到")}</code><small>${esc(row.source)}</small><button type="button" class="card-ip-copy" data-copy-ip="${esc(row.value || "")}" aria-label="复制 ${esc(row.label)} 公网地址 ${esc(row.value || "")}" title="复制 ${esc(row.label)} 地址" ${row.value ? "" : "hidden"}><svg viewBox="0 0 24 24" aria-hidden="true"><rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0 2 2v8a2 2 0 0 0 2 2h2"/><path class="copy-check" d="m9.5 13.5 2 2 4-4.5"/></svg></button></div>`).join("")}</section>
                <footer class="node-identity-refresh"><div class="identity-footer-meta"><span class="verified-pill">节点身份已验证</span>${labels ? `<div class="labels">${labels}</div>` : ""}</div><div>${can("enrollment.manage") && agent.enrollment_command_available ? `<button class="button small" type="button" data-view-enrollment-command="${esc(agent.id)}">查看安装部署命令</button>` : ""}</div></footer>
              </div>
              <div class="agent-subpage-body group-capabilities node-subpage-panel ${activeAgentSubpage === "capabilities" ? "is-active" : ""}" data-node-subpanel="agent:capabilities" aria-label="节点内核开关">
                <section class="node-capability-settings" aria-label="节点内核开关" data-node-capabilities="${esc(agent.id)}">
                  <header><b>内核开关</b><small>关闭即停止服务，开启恢复管理并启动已安装内核。</small></header>
                  ${engineCapabilityToggles(agent.capabilities || [], { supported: agent.supported_capabilities ?? agent.capabilities ?? [], writable: can("agents.manage"), node: true, transitions: agent.capability_transitions || {} })}
                  <p class="node-capability-note">启停成功后生效 · 离线节点上线后执行 · 未安装内核仅切换开关，配置保留</p>
                </section>
              </div>
              <div class="agent-subpage-body config-rows node-subpage-panel ${activeAgentSubpage === "settings" ? "is-active" : ""}" data-node-subpanel="agent:settings" aria-label="名称与联动">
                <section class="node-name-settings" aria-label="节点名称"><header><div><b>节点名称</b><small>仅修改显示名，节点 ID、连接及凭据不变。</small></div></header><form data-agent-name-form="${esc(agent.id)}"><label><span>显示名称</span><input name="name" maxlength="100" required autocomplete="off" value="${esc(agent.name)}" ${can("agents.manage") ? "" : "disabled"}></label><button class="button small" type="submit" ${can("agents.manage") ? "" : "disabled"}>保存名称</button></form></section>
                <section class="node-komari-settings" aria-label="Komari 联动"><header><div><b>Komari 联动</b><small>关联后在节点卡片显示流量周期、用量与额度。</small></div></header><form data-komari-form="${esc(agent.id)}"><label><span>Komari 服务器 UUID</span><input name="uuid" maxlength="100" autocomplete="off" value="${esc(komariUUIDFor(agent))}" placeholder="例如 4addbaf1-7ffb-474c-98ee-4ffd476755ff" ${can("agents.manage") ? "" : "disabled"}></label><button class="button small" type="submit" ${can("agents.manage") ? "" : "disabled"}>保存</button></form>${komariUUIDFor(agent) ? "" : `<p class="node-komari-empty">尚未关联 Komari 服务器</p>`}</section>
                ${agent.can_hide ? `<section class="node-name-settings" aria-label="管理员可见性"><header><div><b>管理员可见性</b><small>隐藏节点列表、配置、任务、日志、指标与审计；管理员仍可在目录查看基本信息、删除节点。采集、计费与连接不受影响。</small></div></header><label class="node-visibility-toggle"><input type="checkbox" data-agent-visibility="${esc(agent.id)}" ${agent.admin_hidden ? "checked" : ""} ${can("agents.manage") ? "" : "disabled"}><span>${agent.admin_hidden ? "已对管理员隐藏" : "对管理员可见"}</span></label></section>` : ""}
              </div>
              ${can("agents.manage") ? `<div class="agent-subpage-body lifecycle-rows node-subpage-panel ${activeAgentSubpage === "lifecycle" ? "is-active" : ""}" data-node-subpanel="agent:lifecycle" aria-label="共享与删除">
                <section class="node-name-settings"><header><div><b>共享 Agent</b><small>按用户分配内核、端口与额度</small></div><button class="button small" type="button" data-agent-sharing="${esc(agent.id)}">管理共享</button></header></section>
                <section class="node-danger-zone"><span><b>删除节点</b><small>断开节点并清理关联配置；QAgent 不会被远程卸载。</small></span><button class="button small danger-button" type="button" data-delete="${esc(agent.id)}">删除节点</button></section>
              </div>` : ""}`}
            </section>
            </div>
          </div>
        </section>`;
      }
      if (!presetMode) {
        const installedCount = (agent.capabilities || []).filter(
          (engine) => agent.runtime?.[engine]?.installed,
        ).length;
        const installationUnknown = (agent.capabilities || []).some(engine => typeof agent.runtime?.[engine]?.installed !== "boolean");
        const overviewEngines = [...(agent.capabilities || [])];
        if (!isShared) {
          for (const engine of agent.supported_capabilities || []) {
            if (!overviewEngines.includes(engine) && agent.runtime?.[engine]?.installed) {
              overviewEngines.push(engine);
            }
          }
        }
        const coreChips = overviewEngines
          .map((engine) => {
            const runtime = agent.runtime?.[engine] || {};
            const installedKnown = typeof runtime.installed === "boolean";
            const capabilityEnabled = (agent.capabilities || []).includes(engine);
            const instanceState = sharedInstancePresentation(agent, engine);
            const coreInfo = describeCoreRuntime(engine, runtime, {
              capabilityEnabled,
              online: agent.status === "online",
              isShared,
              conciseVersion,
              serviceStatusName,
              statusTone,
            });
            const chipServiceState = (isShared && runtime.instance)
              ? instanceState.label
              : (!installedKnown ? "状态未知" : coreInfo.serviceState);
            const chipTone = (isShared && runtime.instance)
              ? instanceState.tone
              : (!installedKnown ? "muted" : coreInfo.serviceTone);
            const chipTitle = coreInfo.existingUnsupportedReason
              ? `${engineName(engine)}：${coreInfo.existingUnsupportedReason}`
              : coreInfo.shared.hasShared
                ? `${engineName(engine)} · 主实例 ${chipServiceState} · ${coreInfo.shared.summaryLabel}`
                : `${engineName(engine)} · ${chipServiceState}`;
            const sharedChipBadge = !isShared && coreInfo.shared.hasShared
              ? `<small class="core-chip-shared ${coreInfo.shared.summaryTone}" data-core-chip-shared="${esc(engine)}">${esc(coreInfo.shared.chipLabel)}</small>`
              : "";
            return `<span class="core-chip service-${esc(engine)}" data-core-installed="${installedKnown ? (coreInfo.installed ? 1 : 0) : "unknown"}" data-capability-enabled="${capabilityEnabled ? 1 : 0}" data-existing-pending="${coreInfo.existingPending ? 1 : 0}" data-runtime-state="${coreInfo.runtimeState}" data-shared-count="${coreInfo.shared.totalCount}" title="${esc(chipTitle)}"><span class="engine-badge ${esc(engine)}">${esc(engineName(engine))}</span><span class="engine-state ${chipTone}"><i></i><b data-core-service="${esc(engine)}">${esc(chipServiceState)}</b></span>${sharedChipBadge}</span>`;
          })
          .join("");
        const cardTag = batchMode ? "article" : "a";
        const cardInteraction = batchMode
          ? `data-node-batch-card`
          : `href="#settings-node-${esc(agent.id)}"`;
        const batchSelect = batchMode
          ? `<label class="node-card-select" title="选择 ${esc(agent.name)}"><input type="checkbox" data-batch-checkbox value="${esc(agent.id)}" aria-label="选择 ${esc(agent.name)} 参与批量操作"><span aria-hidden="true"></span></label>`
          : "";
        const cardSummaryText = isShared && sharedAllocationText(agent, bytes)
          ? sharedAllocationText(agent, bytes)
          : `${agent.os} / ${agent.arch} · ${installedCount ? `${installedCount}/${(agent.capabilities || []).length} 内核已安装` : installationUnknown ? "安装状态未知" : "尚未安装内核"}`;
        return `<${cardTag} class="node-card ${batchMode ? "batch-selecting" : ""}" ${cardInteraction} data-refresh-key="agent-${esc(agent.id)}" data-agent-node="${esc(agent.id)}" data-agent-metrics="${esc(agent.id)}" data-state="${agent.status === "online" ? "online" : "offline"}" data-available="${metricInfo.hasSample ? 1 : 0}" data-freshness="${metricInfo.freshnessState}">
              <header class="node-card-head">${regionAvatarMarkup(agent, esc, can("agents.manage"))}<div class="node-card-title"><strong title="${esc(agent.name)}">${esc(agent.name)}</strong><small data-core-installed-summary>${esc(cardSummaryText)}</small></div><span class="node-card-state">${sharedBadge}${agentPresenceMarkup(agent.status, { tracked: true })}<small data-agent-heartbeat title="${esc(connInfo.heartbeatTitle)}">${esc(heartbeat(agent.last_seen))}</small></span>${batchMode ? batchSelect : cardMoveGrip("nodes", "", esc(agent.name))}</header>
              <div class="node-card-ips" aria-label="公网地址" data-ip-placeholder="${agent.status === "online" ? "公网地址待探测 · 自动同步" : "节点离线 · 尚未上报公网地址"}">${addressRows.map(cardIPRow).join("")}<small class="node-address-note" data-node-connection-address ${connectionAddressNote ? "" : "hidden"}>${esc(connectionAddressNote)}</small></div>
              <section class="node-card-resources" aria-label="节点资源"><div><span>CPU</span><strong data-metric-text="cpu">${esc(metricInfo.cpu.text)}</strong><progress aria-label="CPU 使用率" data-metric-progress="cpu" data-available="${metricInfo.cpu.available ? 1 : 0}" data-level="${metricInfo.cpu.level}" max="100" value="${metricInfo.cpu.value}"></progress></div><div><span>内存</span><strong data-metric-text="memory">${esc(metricInfo.memory.text)}</strong><progress aria-label="内存使用率" data-metric-progress="memory" data-available="${metricInfo.memory.available ? 1 : 0}" data-level="${metricInfo.memory.level}" max="100" value="${metricInfo.memory.value}"></progress></div><div><span>磁盘</span><strong data-metric-text="disk">${esc(metricInfo.disk.text)}</strong><progress aria-label="根磁盘使用率" data-metric-progress="disk" data-available="${metricInfo.disk.available ? 1 : 0}" data-level="${metricInfo.disk.level}" max="100" value="${metricInfo.disk.value}"></progress></div><div class="node-card-network"><span>网络</span><strong>↓ <i data-metric-text="download-rate">${esc(metricInfo.network.downloadRateText)}</i> · ↑ <i data-metric-text="upload-rate">${esc(metricInfo.network.uploadRateText)}</i></strong>${komariNetworkMarkup(agent) || `<small>累计 ↓ <b data-metric-text="download-total">${esc(metricInfo.network.downloadTotalText)}</b> · ↑ <b data-metric-text="upload-total">${esc(metricInfo.network.uploadTotalText)}</b></small>`}</div><span class="machine-resource-live" data-metric-poll role="status" aria-label="资源自动更新"></span></section>
              <section class="node-card-cores" aria-label="内核状态">${coreChips}</section>
              <footer class="node-card-foot"><small><i></i><span data-agent-version>${esc(agent.version || "未知")}</span></small><span class="node-card-stamp" data-metric-text="stamp" data-freshness="${metricInfo.freshnessState}" title="${esc(metricInfo.stampTitle)}">${esc(metricInfo.stampText)}</span>${batchMode ? "" : `<span class="node-card-open">${isShared ? "查看节点" : "管理节点"}</span>`}</footer>
            </${cardTag}>`;
      }
      return `<section class="preset-node-workspace workspace-panel machine-body" id="preset-node-${esc(agent.id)}" data-refresh-key="agent-${esc(agent.id)}" data-agent-node="${esc(agent.id)}" data-agent-metrics="${esc(agent.id)}" data-available="${metricInfo.hasSample ? 1 : 0}" aria-label="选中节点的内核预设"><section class="service-canvas"><header class="service-canvas-head"><h2>节点内核</h2><span>${(agent.capabilities || []).length} 个内核</span></header><div class="service-grid">${services}</div></section></section>`;
    })
    .join("");

  const batchBar = batchMode
    ? agentBatchBarMarkup({ engines, engineName, esc })
    : "";
  const detailMissingState = detailMissing
    ? '<section class="node-settings-missing" data-node-missing role="status"><strong>节点不可用</strong><p>该节点已删除、撤销或不再属于当前作用域。</p><a class="button small" href="#node-settings">返回全部节点</a></section>'
    : "";
  const pageBody = detailMissingState || (nodeCards
    ? `${!presetMode && !detailMode && !batchMode ? cardMoveHelp("nodes") : ""}<section class="${presetMode ? "machine-stack" : detailMode ? "node-settings-stack" : "node-card-grid"}">${nodeCards}</section>`
    : '<div class="empty large"><strong>还没有节点</strong><p>点击上方“添加节点”生成部署命令。</p></div>');
  const batchPage = batchMode
    ? `<form class="node-batch-form" id="batch-form">${pageBody}${batchBar}</form>`
    : pageBody;
  shell(
    `${presetMode ? "" : `<div class="node-settings-page">`}${batchPage}${presetMode ? "" : "</div>"}`,
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
