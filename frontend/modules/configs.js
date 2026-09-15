import { createConfigArchive } from "./config-archive.js";
import { createConfigDeployment } from "./config-deployment.js";
import { createLiveConfigReader } from "./live-config-reader.js";
import { createPresetEditor } from "./preset-editor.js";
import { createLiveConfigPage } from "./live-config-page.js";

export { assertAgentConfigBaseline, liveConfigEditorState, liveConfigEngineEligible, liveConfigReadAction, liveConfigSnapshotReusable, submitLiveConfigChange } from "./live-config-state.js";
export { bindServerPlanRegeneration, readServerPlanInput } from "./server-plan-form.js";

export function installConfigPages(ctx) {
  const archiveConfigs = createConfigArchive(ctx);
  const reader = createLiveConfigReader(ctx, { onRead: () => live.liveConfig() });
  const deployments = createConfigDeployment(ctx, {
    invalidateLiveSnapshot: reader.invalidateLiveSnapshot,
    maybeRerenderLiveConfig: (agentId, engine) => live.maybeRerenderLiveConfig(agentId, engine),
  });
  const presets = createPresetEditor(ctx, {
    deployments,
    renderLiveConfig: () => live.liveConfig(),
    onRuntimeChange(agentId, engine) {
      live.invalidateRuntime();
      live.maybeRerenderLiveConfig(agentId, engine);
    },
  });
  const live = createLiveConfigPage(ctx, { presets, deployments, reader });
  return {
    agentConfig: presets.agentConfig,
    liveConfig: live.liveConfig,
    archiveConfigs,
    capturePresetDrafts: presets.capturePresetDrafts,
    presetHasUnsavedChanges: presets.presetHasUnsavedChanges,
    configHasUnsavedChanges: live.configHasUnsavedChanges,
  };
}
