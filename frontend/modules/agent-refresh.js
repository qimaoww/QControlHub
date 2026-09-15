import { createRefreshChannel } from "./refresh.js";
import { updatePublicIPDisplays } from "./agent-addresses.js";

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


// Roster changes, service structure, and metrics share one interaction-aware
// refresh lifecycle. Only a successful render advances the structural markers.
export function createAgentRefresh(ctx, { can, cardInteractions, renderAgentPage, syncBatchSnapshot, loadRegionDisplay }) {
  const { api, state, can: permission, statusTone, serviceStatusName, heartbeat, ago, percent, bytes, conciseVersion, rate, serviceActionDisabled, trafficChart, notify } = ctx;
  const agentPageActive = () => state.route === "node-settings" || state.route === "agents";
  const metricsRefresh = createRefreshChannel({
    isCurrent: agentPageActive,
    getScope: () => state.navigationEpoch,
  });
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
        syncBatchSnapshot(items);
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
    pollAgentMetrics, updateAgentMetrics, loadMetricHistory, requestAgentStructureRefresh,
    markRendered(visibleAgents, presetMode, deployments, savedConfigs) {
      renderedAgentStructure = agentStructureSignature(visibleAgents);
      if (presetMode) renderedPresetConfigs = presetConfigSignature(deployments, savedConfigs, state.data.selectedAgent);
    },
    start() {
      clearTimeout(state.agentPollTimer);
      if (agentPageActive()) state.agentPollTimer = setTimeout(pollAgentMetrics, 2000);
    },
  };
}
