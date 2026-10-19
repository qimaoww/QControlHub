import { bindEvent } from "./refresh.js";
import { redactDiagnostic } from "./errors.js";
import { assertAgentConfigBaseline, deployPreflightError, liveConfigEngineEligible, liveConfigReadAction, liveConfigSnapshotReusable, liveSourceKey } from "./live-config-state.js";
import { presetRoute } from "./preset-route.js";
import { bindConfigFiles } from "./config-files.js";
import { bindConfigSourceEditor } from "./config-source-editor.js";
import { bindConfigRestrictions } from "./config-restrictions.js";
import { bindConfigInbounds } from "./config-inbounds.js";
import { createLiveConfigView, configWorkspaceStatus } from "./live-config-view.js";
import { bindLiveConfigNavigation } from "./live-config-navigation.js";
import { bindLiveConfigSubmit } from "./live-config-submit.js";
import { bindSharedInstanceRead } from "./shared-instance-read.js";

export function createLiveConfigPage(ctx, { presets, deployments, reader, workspaces }) {
  const { api, state, can, engineName, notify, shell, bindCodeEditors } = ctx;
  const { operationFor, saveOperation, renderPresetStatus, watchPresetOperation } = presets.status;
  const { mountPresetEditor } = presets;
  const { recordPendingDeploy, monitorDeployTask, reconcilePendingDeploy } = deployments;
  const { requestCurrentConfigSnapshot, readCurrentConfig } = reader;
  const renderLiveConfigView = createLiveConfigView(ctx);
let liveConfigRequest = 0;
let liveAgentRuntimeScope;
let liveAgentRuntimeLoaded = false;

async function refreshConfig({operationKey} = {}) {
  const record = workspaces.current(), owner = state.data, epoch = state.navigationEpoch;
  if (!record || operationKey && operationKey !== record.operationKey || record.submitting || document.querySelector('.config-inbound-dialog[data-saving="1"]')) return false;
  workspaces.capture();
  if (configHasUnsavedChanges() && !(await ctx.confirmAction("重新读取会放弃当前工作区未保存的修改，确定继续？", "重新读取配置"))) return false;
  if (owner !== state.data || epoch !== state.navigationEpoch || record !== workspaces.current()) return false;
  liveAgentRuntimeLoaded = false;
  workspaces.discard(record);
  state.data.presetDrafts?.clear(`${record.agentId}|${record.engine}|${record.sourceMode}|`);
  delete state.data.liveSources[record.sourceKey];
  await liveConfig({preferCachedRead:false});
  return true;
}

function configHasUnsavedChanges() {
  return workspaces.dirty() || presets.presetHasUnsavedChanges() || Boolean(document.querySelector('.config-inbound-dialog[data-saving="1"], .config-inbound-dialog[data-dirty="1"]'));
}

function maybeRerenderLiveConfig(agentId, engine) {
  const editor = document.querySelector("#live-config-form [data-code-editor]");
  const input = editor?.querySelector("[data-code-input]");
  const sourceDirty = editor?.configFileController ? editor.configFileController.dirty() :
    input && !input.readOnly && input.value !== input.defaultValue;
  if (presets.hasActiveEditor() || sourceDirty || document.querySelector('#live-config-form[data-saving="1"]') ||
      document.querySelector(".config-inbound-dialog[open], .config-access-dialog[open]")) return;
  if (
    state.route === "live-config" &&
    state.data.liveAgent === agentId &&
    state.data.liveEngine === engine
  )
    void liveConfig();
}


async function liveConfig(options = {}) {
  const preferCachedRead = options.preferCachedRead !== false;
  const request = ++liveConfigRequest;
  const ticket = workspaces.begin();
  if (!can("agent-config.read")) {
    shell('<section class="section"><div class="alert error" role="alert">当前账号无权查看配置。</div></section>', "配置");
    return;
  }
  const privateAccount = Boolean(state.session && state.session.role !== "admin");
  const accountData = state.data;
  const runtimeScope = state.navigationEpoch;
  const refreshRuntime =
    !liveAgentRuntimeLoaded ||
    liveAgentRuntimeScope !== runtimeScope ||
    !state.data.agents;
  // Preserve narrow config-only deep links: their workspace includes the
  // authorized node, so opening one need not require a fleet-read permission.
  // Start independent reads together for an already selected workspace.
  // Do not prefetch across pending deployment reconciliation or access scopes.
  const selectedAgent = state.data.liveAgent, selectedEngine = state.data.liveEngine;
  const confirmedDeployments = can("deployments.read")
    ? api("/deployments").catch(() => null) : Promise.resolve(null);
  const prefetchWorkspace = can("agents.read") && selectedAgent && selectedEngine &&
    !state.data.pendingDeployTasks?.[liveSourceKey(selectedAgent, selectedEngine)]
    ? api(`/agents/${encodeURIComponent(selectedAgent)}/configs/${encodeURIComponent(selectedEngine)}/workspace`)
      .then(value => ({value}), error => ({error})) : null;
  const targetedWorkspace = state.session && !can("agents.read") && state.data.liveAgent && state.data.liveEngine
    ? await api(`/agents/${encodeURIComponent(state.data.liveAgent)}/configs/${encodeURIComponent(state.data.liveEngine)}/workspace`)
    : null;
  const agents = targetedWorkspace ? [targetedWorkspace.agent].filter(Boolean) :
    state.session && !can("agents.read") ? [] : refreshRuntime ? await api("/agents") : state.data.agents;
  if (!ticket.current()) return;
  state.data.agents = agents;
  liveAgentRuntimeLoaded = true;
  liveAgentRuntimeScope = runtimeScope;
  const eligibleAgents = agents.filter((item) =>
    (item.capabilities || []).some(
      (engine) => privateAccount || liveConfigEngineEligible(item.runtime?.[engine], can("agent-config.write")),
    ),
  );
  if (
    !state.data.liveAgent ||
    !eligibleAgents.some((agent) => agent.id === state.data.liveAgent)
  ) {
    state.data.liveAgent =
      eligibleAgents.find((item) => item.status === "online")?.id ||
      eligibleAgents[0]?.id ||
      "";
  }
  let agent = eligibleAgents.find(
    (item) => item.id === state.data.liveAgent,
  );
  if (!agent) {
    shell(
      '<section class="empty large live-config-empty"><strong>没有可配置的节点</strong><p>请先添加节点并启用需要的内核类型。</p><a class="button primary" href="#node-settings">前往节点设置</a></section>',
      "配置",
    );
    return;
  }
  // An ordinary account starts with its private configuration. Only the host
  // owner can explicitly request a live/legacy snapshot; never auto-read a
  // shared host when the account has no configuration of its own.
  const installedEngines = (agent.capabilities || []).filter(
    (item) => privateAccount || liveConfigEngineEligible(agent.runtime?.[item], can("agent-config.write")),
  );
  if (
    !state.data.liveEngine ||
    !installedEngines.includes(state.data.liveEngine)
  ) {
    state.data.liveEngine = installedEngines.find(item => agent.runtime?.[item]?.installed) || installedEngines[0];
  }
  const engine = state.data.liveEngine;
  await reconcilePendingDeploy(agent.id, engine);
  if (!ticket.current()) return;
  const prefetched = prefetchWorkspace && selectedAgent === agent.id && selectedEngine === engine
    ? await prefetchWorkspace : null;
  if (prefetched?.error) throw prefetched.error;
  const configWorkspace = targetedWorkspace || prefetched?.value || await api(
    `/agents/${encodeURIComponent(agent.id)}/configs/${encodeURIComponent(engine)}/workspace`,
  );
  if (!ticket.current()) return;
  agent = configWorkspace.agent || agent;
  state.data.agents = agents.map(item => item.id === agent.id ? agent : item);
  const privateWorkspace = privateAccount && (agent.can_manage !== true ||
    !["managed", "import"].includes(state.data.liveConfigSource));
  const saved = configWorkspace.config || null;
  const deployment = (await confirmedDeployments)?.find(item => item.agent_id === agent.id &&
    item.engine === engine && saved?.id && item.config_id === saved.id);
  if (!ticket.current()) return;
  const runtime = agent.runtime?.[engine] || {};
  const unsupportedReason = String(
    privateWorkspace ? "" : runtime.existing_config_unsupported_reason || "",
  );
  const existingAvailable = (!privateAccount || agent.can_manage === true) && Boolean(runtime.existing_config_available);
  const managedAvailable = Boolean(runtime.installed);
  const emptyManaged = !managedAvailable && !existingAvailable && !unsupportedReason;
  const managedReadSupported = (agent.features || []).includes(
    "managed-config-read-v1",
  );
  const sourceMode =
    privateWorkspace ? "personal" : existingAvailable &&
    (state.data.liveConfigSource === "import" || !managedAvailable)
      ? "import"
      : "managed";
  state.data.liveConfigSource = sourceMode;
  if (globalThis.location?.hash.startsWith("#live-config") && globalThis.history?.replaceState)
    globalThis.history.replaceState(null, "", presetRoute({agentId:agent.id, engine, sourceMode}));
  const importSource = sourceMode === "import";
  const readAction = privateWorkspace || emptyManaged ? "" : liveConfigReadAction({
    sourceMode,
    managedReadSupported,
    existingAvailable,
  });
  const sourceKey = liveSourceKey(agent.id, engine, sourceMode);
  state.data.liveSources ||= {};
  if (!liveConfigSnapshotReusable(state.data.liveSources[sourceKey]))
    delete state.data.liveSources[sourceKey];
  const source = privateWorkspace
    ? { content: saved?.content || (engine === "mihomo" ? "listeners: []\nrules:\n  - MATCH,DIRECT\n" : "{}\n") }
    : state.data.liveSources[sourceKey] || ((emptyManaged || !importSource && (!readAction || !can("tasks.execute") || agent.status !== "online") && saved) ? {
      content: saved?.content || (engine === "mihomo" ? "listeners: []\nrules:\n  - MATCH,DIRECT\n" : "{}\n"),
      saved: true,
    } : null);
  const beforeDeploy = !privateWorkspace && sourceMode === "managed" && managedAvailable
    ? async () => {
        if (!readAction) {
          throw deployPreflightError(
            "Agent 不支持读取 QAgent 托管配置",
          );
        }
        const isCurrent = () =>
          accountData === state.data &&
          runtimeScope === state.navigationEpoch &&
          request === liveConfigRequest &&
          state.route === "live-config" &&
          state.data.liveAgent === agent.id &&
          state.data.liveEngine === engine &&
          state.data.liveConfigSource === sourceMode;
        let fresh;
        try {
          // Deliberately omit prefer_cached. This read runs immediately before
          // any save/API mutation and therefore cannot trust the 600s display
          // cache that supplied the editor baseline.
          fresh = await requestCurrentConfigSnapshot(agent, engine, readAction, { isCurrent });
        } catch (error) {
          if (error?.deployPreflight) throw error;
          throw deployPreflightError(
            redactDiagnostic(error.message) || "无法核验节点配置",
          );
        }
        if (!fresh || !isCurrent()) {
          throw deployPreflightError(
            "工作区已切换",
          );
        }
        assertAgentConfigBaseline(source?.agentContent, fresh.content);
      }
    : null;
  const current = (privateWorkspace || !unsupportedReason) && source?.content
    ? {
        ...(saved || {
          name: `${agent.name} · ${engineName(engine)}`,
          description: privateWorkspace ? "个人节点配置" : "节点实际配置",
          version: 0,
        }),
        content: source.content,
      }
    : null;
  const workspace = workspaces.open({operationKey:`${agent.id}|${engine}|${sourceMode}`, agentId:agent.id, agent, engine, sourceMode, sourceKey, source, saved, runtime,
    deployedVersion:deployment?.config_version,
    content:current?.content, inbounds:configWorkspace.inbound_targets || configWorkspace.inbounds || []});
  const workspaceElement = renderLiveConfigView({
    agent, engine, runtime, installedEngines, privateAccount, privateWorkspace,
    sourceMode, importSource, current, saved, source, unsupportedReason, existingAvailable,
    managedAvailable, emptyManaged, readAction, workspace,
  });
  state.data.liveEngines = installedEngines;
  bindLiveConfigNavigation(ctx, { agent, engine, accountData, sourceMode, current, workspaceElement, liveConfig, workspace, workspaces });
  bindEvent(workspaceElement?.querySelector("[data-config-refresh]"), "click", async () => {
    if (!ticket.current()) return;
    try { await refreshConfig(); }
    catch (error) { if (accountData === state.data && state.data.liveAgent === agent.id && state.data.liveEngine === engine && state.data.liveConfigSource === sourceMode) notify(redactDiagnostic(error.message) || "读取配置失败", "error"); }
  });
  bindEvent(document.querySelector("[data-read-current]"), "click", async () => {
      delete state.data.liveSources[sourceKey];
      await readCurrentConfig(agent, engine, sourceKey, readAction);
  });
  const configFiles = bindConfigFiles(document.querySelector("#live-config-form"), engine, notify, {
    document:workspace.document, inbounds:workspace.inbounds,
    onChange:model => {
      const labels = configWorkspaceStatus({runtime, workspace, saved:workspace.saved, source, importSource});
      const status = workspaceElement.querySelector("[data-workspace-save-state]");
      if (status) status.textContent = !importSource && model.dirty() ? "有未保存修改" : labels.saved;
      const deployment = workspaceElement.querySelector("[data-workspace-deploy-state]");
      if (deployment) deployment.textContent = labels.deployed;
    },
  });
  bindCodeEditors();
  bindConfigSourceEditor(ctx, {root:workspaceElement, files:configFiles, workspace, workspaces, current:ticket.current});
  const applyInboundMutation = async (result, chosen) => {
    if (accountData !== state.data) return;
    const key = workspace.operationKey;
    workspaces.commit(workspace, result.config);
    if (ticket.current()) configFiles?.rebase(workspace.document);
    state.data.liveSources[sourceKey] = {
      ...(state.data.liveSources[sourceKey] || source),
      content: result.config.content,
      saved: true,
      cached: false,
    };
    if (result.task?.id) {
      let operation = operationFor(key);
      if (operation?.task?.id !== result.task.id) operation = saveOperation({
        key, task:result.task, version:result.config.version, intent:result.task.action,
        message:`${result.task.action === "deploy" ? "部署" : "校验"}已提交`,
      });
      watchPresetOperation(operation);
      if (result.task.action === "deploy") {
        recordPendingDeploy(result.task.id, agent.id, engine);
        void monitorDeployTask(result.task.id, agent.id, engine);
      }
    }
    state.data.liveSelectedInbound = chosen ? {agentId:agent.id, engine, ...chosen} : null;
    if (state.route === "live-config" && state.data.liveAgent === agent.id && state.data.liveEngine === engine && state.data.liveConfigSource === sourceMode) {
      try { await liveConfig(); }
      catch (error) {
        if (accountData !== state.data) return;
        // The mutation is already durable. Do not report it as a failed save
        // or silently lose the error after the embedded editor is disposed.
        const message = redactDiagnostic(error.message) || "配置加载失败";
        const operation = operationFor(key);
        if (operation?.task?.id === result.task?.id) Object.assign(operation, {message, tone:"error", reload:true});
        renderPresetStatus();
      }
    }
  };
  const restrictionSelection = await bindConfigRestrictions({
    ...ctx, form: document.querySelector("#live-config-form"), files: configFiles,
    agent, engine, saved, sourceMode, inbounds:configWorkspace.inbound_targets || configWorkspace.inbounds || [],
    beforeDeploy, onSaved: applyInboundMutation,
  });
  if (!ticket.current()) return;
  const selected = state.data.liveSelectedInbound;
  if (selected?.agentId === agent.id && selected.engine === engine)
    (configFiles || restrictionSelection)?.selectInbound(selected.tag, selected.port);
  bindConfigInbounds({
    ...ctx, form:document.querySelector("#live-config-form"), container:workspaceElement, files:configFiles,
    selection:restrictionSelection, agent, engine, saved, workspace:configWorkspace, sourceMode,
    workspaceRecord:workspace, workspaces,
    sourceContent:current?.content, mountEditor:mountPresetEditor, beforeDeploy, onSaved:applyInboundMutation,
  });
  renderPresetStatus();
  bindLiveConfigSubmit(ctx, {
    agent, engine, accountData, sourceKey, source, saved, privateWorkspace, importSource,
    configFiles, beforeDeploy, workspaceElement, liveConfig, deployments, workspace, workspaces, status:presets.status,
  });
  bindSharedInstanceRead(ctx, {agent, engine, root:workspaceElement, requestSnapshot:requestCurrentConfigSnapshot});
  if (
    !current &&
    !unsupportedReason &&
    agent.status === "online" &&
    readAction &&
    !source?.error
  )
    void readCurrentConfig(agent, engine, sourceKey, readAction, preferCachedRead);
}


  return {
    liveConfig, refreshConfig, configHasUnsavedChanges, maybeRerenderLiveConfig,
    invalidateRuntime() { liveAgentRuntimeLoaded = false; },
  };
}
