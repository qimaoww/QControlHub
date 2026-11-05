import { createConfigMutations } from "./config-mutations.js";
import { createConfigArchive } from "./config-archive.js";
import { createConfigDeployment } from "./config-deployment.js";
import { createLiveConfigReader } from "./live-config-reader.js";
import { createPresetEditor } from "./preset-editor.js";
import { createLiveConfigPage } from "./live-config-page.js";

export { liveConfigEditorState, liveConfigEngineEligible, liveConfigReadAction, liveConfigSnapshotReusable, submitLiveConfigChange } from "./live-config-state.js";
export { bindServerPlanRegeneration, readServerPlanInput } from "./server-plan-form.js";

export function installConfigPages(ctx) {
  const mutations = createConfigMutations(ctx, {
    waitForTerminal: id => deployments.waitForDeployTerminal(id),
    onTask(result, record) {
      const key = `${record.agentId}|${record.engine}`;
      const previous = presets.status.operationFor(key);
      if (record.intent === "deploy") void deployments.monitorDeployTask(result.task.id, record.agentId, record.engine);
      if (previous?.version > result.task.config_version || result.config.version > result.task.config_version) return;
      if (typeof result.config.content === "string") {
        ctx.state.data.liveSources ||= {};
        ctx.state.data.liveSources[key] = {content:result.config.content, saved:true, cached:false};
      }
      const operation = previous?.task?.id === result.task.id ? previous : presets.status.saveOperation({
        key, task:result.task, version:result.task.config_version ?? result.config.version, intent:record.intent,
        message:"配置已保存，任务已提交。",
      });
      if (record.intent === "deploy") deployments.recordPendingDeploy(result.task.id, record.agentId, record.engine);
      presets.status.watchPresetOperation(operation);
    },
  });
  ctx = {...ctx, api:mutations.request};
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
  const live = createLiveConfigPage(ctx, { presets, deployments, reader, mutations });
  return {
    agentConfig: (...args) => {
      void mutations.recover(ctx.state.data.agentId, ctx.state.data.engine);
      return presets.agentConfig(...args);
    },
    liveConfig: live.liveConfig,
    archiveConfigs,
    capturePresetDrafts: presets.capturePresetDrafts,
    presetHasUnsavedChanges: presets.presetHasUnsavedChanges,
    configHasUnsavedChanges: live.configHasUnsavedChanges,
  };
}
