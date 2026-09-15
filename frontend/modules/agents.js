import { bindEvent, createInteractionGate } from "./refresh.js";
import { bindCodeEditors } from "./code-editor.js";
import { createAgentEnrollment } from "./agent-enrollment.js";
import { createAgentKomariDisplay } from "./agent-komari.js";
import { createAgentSharing } from "./agent-sharing.js";
import { createRegionDisplay } from "./regions.js";
import { createAgentView } from "./agent-view.js";
import { createAgentRefresh } from "./agent-refresh.js";
import { createAgentCoreActions } from "./agent-core-actions.js";
import { createAgentSettings } from "./agent-settings.js";
import { createAgentBatchController } from "./agent-batch-controller.js";
import { createAgentCardInteractions } from "./agent-card-interactions.js";
import { bindAgentWorkspace, compactPresetPage } from "./agent-workspace.js";

export { developmentSourceVisible, coreSourceForInstall } from "./agent-core-actions.js";
export { agentStructureSignature } from "./agent-refresh.js";
export { batchAgentEligibility, batchSelectAllState } from "./agent-batch.js";
export { komariCycleRange, komariResetDay } from "./agent-komari.js";
export { animateNodeCardDrop, clearNodeCardDragState, nodeCardDropIndex } from "./agent-card-drag.js";
export { formatHostPort, manualConnectionAddressNote, publicAddressRows, updatePublicIPDisplays } from "./agent-addresses.js";
export { geoRegionDetails } from "./regions.js";

export function installAgents(ctx) {
  const { api, optionalAPI, state, can: permission, esc, bytes, notify, renderConfigDiff } = ctx;
  const can = (capability, agent) => agent?.can_manage === false &&
    ["operator", "agents.manage", "enrollment.manage"].includes(capability) ? false : permission(capability, agent);
  const cardInteractions = createInteractionGate();
  const sharing = createAgentSharing(ctx, cardInteractions);
  const loadRegionDisplay = createRegionDisplay(ctx);
  const komari = createAgentKomariDisplay({ api, state, esc, bytes });
  const enrollment = createAgentEnrollment({ ...ctx, refreshAgentPage });
  const { showCommand } = enrollment;
  const core = createAgentCoreActions(ctx);
  const { submitTask } = core;
  const cards = createAgentCardInteractions(ctx, { can, cardInteractions, loadRegionDisplay, loadKomariDisplay: komari.loadKomariDisplay });
  const batch = createAgentBatchController(ctx, { renderAgentPage, cancelCardDrag: cards.cancel });
  const refresh = createAgentRefresh(ctx, { can, cardInteractions, renderAgentPage, syncBatchSnapshot: batch.syncSnapshot, loadRegionDisplay });
  const { pollAgentMetrics, updateAgentMetrics, requestAgentStructureRefresh } = refresh;
  const bindAgentSettings = createAgentSettings(ctx, { refreshAgentPage });
  const renderAgentView = createAgentView(ctx, { can, ...komari });
  let agentPageRequest = 0;

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

  const { visibleAgents, tokenRows } = renderAgentView({ agents, tokens, presetMode, configByService, deploymentByService, accessByService, configDiffByService });
  refresh.markRendered(visibleAgents, presetMode, deployments, savedConfigs);
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
  cards.cancel();
}

function bindAgentPage(agentItems, presetMode = false, enrollmentHistory = {}) {
  const agentsByID = new Map(agentItems.map((agent) => [agent.id, agent]));
  document.querySelectorAll("[data-agent-sharing]").forEach((button) => {
    bindEvent(button, "click", () => sharing.open(agentsByID.get(button.dataset.agentSharing)));
  });
  batch.reset();
  bindAgentWorkspace({ state, can }, agentsByID, refresh);
  core.bindCoreActions();
  bindAgentSettings(agentsByID);
  const batchForm = batch.bind(agentsByID);
  enrollment.bindEnrollmentPage(enrollmentHistory);
  document.querySelectorAll("[data-agent-refresh]").forEach((button) => {
    button.onclick = () => pollAgentMetrics();
  });
  cards.bindCards(agentsByID, batchForm);
  refresh.start();
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
