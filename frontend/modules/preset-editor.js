import { bindEvent } from "./refresh.js";
import { createPresetDrafts } from "./preset-drafts.js";
import { presetRoute } from "./preset-route.js";
import { createPresetReads } from "./preset-runtime.js";
import { commonConfigFields, renderCommonFieldStudio, renderGlobalFieldStudio, revealSelectedFields } from "./config-fields.js";
import { configFieldURL, renderSSRustFieldStudio, ssRustFieldGroups } from "./ss-rust-fields.js";
import { createPresetView } from "./preset-view.js";
import { createPresetStatus } from "./preset-status.js";
import { createPresetBindings } from "./preset-bindings.js";

export function createPresetEditor(ctx, { deployments, renderLiveConfig, onRuntimeChange }) {
  const { api, state, engines, can, esc, ago, confirmAction, notify } = ctx;
  // One controller serves both the legacy route and the embedded editor.
  // Bindings/status share this private lifecycle record, never copied values.
  const editor = {
    request: 0, savePending: false, syncControls: () => {}, context: undefined,
    host: null, sessionData: state.data, navigation: 0,
  };
  const presetVisible = () => editor.host ? editor.host.isCurrent() : state.route === "agent-config";
  const presetRead = createPresetReads();
  const drafts = () => state.data.presetDrafts ||= createPresetDrafts();
  const status = createPresetStatus(ctx, {
    editor, drafts, presetVisible, refreshPresetPage, renderLiveConfig, deployments, onRuntimeChange,
  });
  const { operationFor, saveOperation, renderPresetStatus } = status;
  const bindAgentConfigPage = createPresetBindings(ctx, {
    editor, drafts, presetVisible, capturePresetDrafts, agentConfig, refreshPresetPage, status, deployments,
  });
  const renderPresetView = createPresetView(ctx);
function capturePresetDrafts() { if (presetVisible()) drafts().capture(); }
function presetHasUnsavedChanges() { return (editor.sessionData === state.data && editor.savePending) || Boolean(state.data.presetDrafts?.dirty()); }

function agentConfig(options = {}) {
  const sessionData = state.data;
  const rendering = renderAgentConfig(options);
  const request = editor.request;
  return rendering.catch(error => {
    const ctx = editor.context;
    if (request === editor.request && state.data === sessionData && ctx?.sessionData === sessionData &&
        presetVisible() && state.data.agentId === ctx.agent.id && state.data.engine === ctx.engine) {
      // A failed route refresh can leave the old DOM on screen. Give it a
      // working recovery action instead of dead handlers from an old epoch.
      ctx.navigationEpoch = state.navigationEpoch;
      if (!editor.savePending && !ctx.navigating) {
        const key = `${ctx.agent.id}|${ctx.engine}`;
        const failure = {key, message:`配置加载失败：${error.message}。草稿已保留，请重新加载后继续编辑。`, tone:"error", reload:true};
        const operation = operationFor(key);
        if (operation) Object.assign(operation, failure);
        else saveOperation(failure);
      }
      bindAgentConfigPage(ctx, true);
    }
    throw error;
  });
}

async function renderAgentConfig({ workspace: loadedWorkspace } = {}) {
  if (editor.sessionData !== state.data) {
    editor.sessionData = state.data;
    editor.savePending = false;
    editor.context = null;
    editor.syncControls = () => {};
  }
  capturePresetDrafts();
  const request = ++editor.request;
  const host = editor.host;
  const root = host?.root || document;
  const sessionData = state.data;
  const navigationEpoch = state.navigationEpoch;
  const routeSignal = state.routeSignal;
  const isCurrent = () => request === editor.request && host === editor.host && presetVisible() &&
    state.data === sessionData && state.navigationEpoch === navigationEpoch && !routeSignal?.aborted;
  let agents = state.data.agents;
  let agent = agents?.find(item => item.id === state.data.agentId);
  let engine = state.data.engine;
  // Deep links already identify the workspace. Do not fetch the entire fleet
  // first, or require agents.read just to use agent-config.read.
  if (!state.data.agentId || !engine) {
    agents ||= await api("/agents");
    if (!isCurrent()) return;
    state.data.agents = agents;
    agent = agents.find(item => item.id === state.data.agentId);
    if (!agent) return void (location.hash = "#agents");
    engine = agent.capabilities?.[0] || engines[0];
  }
  state.data.engine = engine;
  const base = `/agents/${encodeURIComponent(state.data.agentId)}/configs/${encodeURIComponent(engine)}`;
  const workspace = loadedWorkspace || await api(`${base}/workspace`);
  if (!isCurrent()) return;
  const prior = editor.context;
  const scope = `${state.data.agentId}|${engine}|`;
  if (!loadedWorkspace && !editor.savePending && prior?.sessionData === sessionData &&
      prior.draftScope === scope && prior.draftVersion !== (workspace.config?.version || 0) && drafts().dirty(scope)) {
    const discard = await confirmAction(
      `服务端配置已从 v${prior.draftVersion} 更新为 v${workspace.config?.version || 0}。放弃此内核的旧版本草稿并加载新配置？取消会保留当前输入。`, "配置版本已更新",
    );
    if (!isCurrent()) return;
    if (!discard) {
      prior.navigationEpoch = navigationEpoch;
      Object.assign(state.data, {protocol:prior.protocol?.key, inboundTag:prior.selectedInbound?.tag || ""});
      const key = `${prior.agent.id}|${engine}`;
      saveOperation({key, message:"服务端已有更新版本，当前草稿已保留。请重新加载后再提交，避免覆盖其他修改。", tone:"error", reload:true});
      // The old editor may have been unmounted while visiting another page.
      // Recreate that exact revision so its retained drafts remain visible.
      await renderAgentConfig({workspace:prior.workspace});
      return;
    }
    drafts().clear(scope);
  }
  // The workspace carries a fresh runtime snapshot; a cached node list can
  // still say "not installed" after an installation has completed.
  agent = workspace.agent || agent;
  state.data.presetAgent = agent;
  if (agents) state.data.agents = agents.map((item) => item.id === agent.id ? agent : item);
  const engineInstalled = Boolean(agent.runtime?.[engine]?.installed);
  const config = workspace.config;
  let selectedInbound = (workspace.inbounds || []).find(
    (input) => input.tag === state.data.inboundTag,
  );
  const requestedProtocol = selectedInbound?.protocol || state.data.protocol;
  // Protocol keys are not shared by all engines. A stale key is possible
  // after switching engines from the preset page, so always fall back to a
  // protocol advertised by the current workspace.
  const selectedProtocolKey = workspace.protocols.some(
    (item) => item.key === requestedProtocol,
  )
    ? requestedProtocol
    : workspace.protocols[0]?.key;
  const protocol = workspace.protocols.find(
    (item) => item.key === selectedProtocolKey,
  );
  state.data.protocol = selectedProtocolKey;
  let plan = selectedInbound;
  const planKey = `${agent.id}|${engine}|${selectedProtocolKey}`;
  state.data.serverPlans ||= {};
  const editingPlan = !host || ["add", "modify"].includes(host.kind);
  if (!plan && editingPlan && can("agent-config.write") && protocol) {
    plan = state.data.serverPlans[planKey];
    if (!plan) {
      plan = await api(`${base}/plans`, {
        method: "POST",
        body: JSON.stringify({ protocol: selectedProtocolKey }),
      });
      if (!isCurrent()) return;
      state.data.serverPlans[planKey] = plan;
    }
  }
  plan ||= {
    protocol: selectedProtocolKey,
    listen: "0.0.0.0",
    port: protocol?.default_port || 443,
    transport: "raw",
  };
  const allFields = workspace.catalog.fields || [];
  const ssRustGroups = ssRustFieldGroups(allFields);
  const commonMutation = host?.kind.startsWith("common-") ? host.kind.slice("common-".length) : "";
  const fields = commonMutation
    ? commonConfigFields(engine, allFields).filter(field => Boolean(workspace.present_fields?.[field.key]) === (commonMutation !== "add"))
    : engine === "ss-rust" ? ssRustGroups.global : allFields;
  const selectedField =
    fields.find((field) => field.key === state.data.configField) || fields[0];
  state.data.configField = selectedField?.key || "";
  const selectedInboundField = ssRustGroups.inbound.find(
    (field) => field.key === state.data.configInboundField,
  ) || ssRustGroups.inbound.find((field) => field.key === "mode") || ssRustGroups.inbound[0];
  const hasInboundField = engine === "ss-rust" && config && selectedInbound && selectedInboundField;
  if (hasInboundField)
    state.data.configInboundField = selectedInboundField.key;
  // Auxiliary editors must not hold the preset form hostage to a slow field
  // read or revision query. They replace only their own placeholders below.
  const fieldRead = path => presetRead(workspace, path, () => api(path).then(value => {
    if (value.version !== undefined && value.version !== config.version)
      throw new Error("配置版本已变化，请重新加载配置后编辑字段");
    return value;
  }));
  const fieldValues = [
    config && selectedField && (!host || host.kind === "advanced" || commonMutation)
      ? fieldRead(`${base}/fields/${encodeURIComponent(selectedField.key)}`)
      : { value: { present: false, fragment: "" } },
    hasInboundField && (!host || host.kind === "advanced")
      ? fieldRead(configFieldURL(base, selectedInboundField.key, selectedInbound.tag))
      : { value: { present: false, fragment: "" } },
  ];
  const fieldValue = fieldValues[0].value || { present: false, fragment: "", error: "正在加载字段" };
  const inboundFieldValue = fieldValues[1].value || { present: false, fragment: "", error: "正在加载字段" };
  if (!isCurrent()) return;
  const { canAutoInstall } = renderPresetView({
    agent, engine, workspace, config, protocol, plan, selectedProtocolKey, selectedInbound,
    fields, ssRustGroups, selectedField, selectedInboundField, fieldValue, inboundFieldValue,
    commonMutation, engineInstalled, host,
  });
  const serverPlan = root.querySelector("#server-plan-form");
  if (serverPlan?.dataset) {
    serverPlan.dataset.blockMainlandDestination = plan.block_mainland_destination
      ? "1"
      : "0";
    serverPlan.dataset.blockMainlandSource = plan.block_mainland_source ? "1" : "0";
  }
  const protocolCatalog = root.querySelector(".protocol-catalog-wide");
  const protocolCatalogNav = protocolCatalog?.querySelector(
    ".protocol-browser > nav",
  );
  const updateProtocolCatalogLayout = () => {
    if (!protocolCatalogNav || protocolCatalogNav.isConnected === false) return;
    protocolCatalog.classList.remove("protocol-two-rows");
    protocolCatalogNav.style.removeProperty("--protocol-columns");
    if (protocolCatalogNav.scrollWidth > protocolCatalogNav.clientWidth + 1) {
      protocolCatalog.classList.add("protocol-two-rows");
      protocolCatalogNav.style.setProperty(
        "--protocol-columns",
        String(Math.ceil(workspace.protocols.length / 2)),
      );
    }
  };
  updateProtocolCatalogLayout();
  if (typeof window !== "undefined") {
    let resizePending = false;
    bindEvent(window, "resize", () => {
      if (resizePending) return;
      resizePending = true;
      requestAnimationFrame(() => {
        resizePending = false;
        if (!isCurrent()) return;
        updateProtocolCatalogLayout();
        revealSelectedFields(root);
      });
    });
  }
  const context = {
    agent,
    engine,
    workspace,
    protocol,
    plan,
    selectedInbound,
    selectedField,
    commonMutation,
    commonFields: commonMutation ? fields : [],
    selectedInboundField,
    fieldValue,
    base,
    engineInstalled,
    canAutoInstall,
    beforeDeploy: host?.beforeDeploy,
    host,
    root,
    request,
    draftScope: `${agent.id}|${engine}|`,
    draftVersion: config?.version || 0,
    generating: false,
    sessionData,
    navigationEpoch,
  };
  editor.context = context;
  if (globalThis.location?.hash.startsWith("#agent-config") && globalThis.history?.replaceState)
    globalThis.history.replaceState(null, "", presetRoute(state.data));
  bindAgentConfigPage(context);
  fieldValues.forEach((entry, index) => {
    if (entry.value) return;
    void entry.promise.catch(error => ({present:false, fragment:"", error:error.message})).then(value => {
    if (!isCurrent()) return;
    capturePresetDrafts();
    // Preserve each drawer's open state and the primary form's draft/focus.
    const replaceStudio = (id, html) => {
      const current = root.querySelector(`#${id}`);
      if (!current) return;
      const open = current.open;
      current.outerHTML = html;
      const updated = root.querySelector(`#${id}`);
      if (updated) updated.open = open;
    };
    if (index === 0) context.fieldValue = value;
    if (commonMutation && index === 0) {
      replaceStudio("common-options", renderCommonFieldStudio({ engine, fields, selected:selectedField, value, config,
        catalog:workspace.catalog, mutation:commonMutation }));
    } else if (engine === "ss-rust") {
      if (index === 1) replaceStudio("inbound-options", renderSSRustFieldStudio({ scope: "inbound", fields: ssRustGroups.inbound,
        selected: selectedInboundField, value, config, inbound: selectedInbound }));
      else replaceStudio("advanced", renderSSRustFieldStudio({ scope: "global", fields, selected: selectedField,
        value, config, presentFields: workspace.present_fields }));
    } else if (index === 0) {
      replaceStudio("advanced", renderGlobalFieldStudio({ fields, selected: selectedField, value,
        config, catalog: workspace.catalog, presentFields: workspace.present_fields }));
    }
    bindAgentConfigPage(context, true);
  }).catch(error => { if (isCurrent()) notify(`字段加载失败：${error.message}`, "error"); }); });
  if (!loadedWorkspace && operationFor(`${agent.id}|${engine}`)) {
    const operation = operationFor(`${agent.id}|${engine}`);
    if (operation.reload) {
      operation.message = `已重新加载配置 v${config?.version || 0}，可以继续编辑。`;
      operation.tone = "";
      operation.reload = false;
      renderPresetStatus();
      editor.syncControls();
    }
  }
  const history = root.querySelector("#revisions");
  if (history && can("configs.read")) {
    let loading = false;
    let loaded = false;
    const loadHistory = async () => {
      if (!history.open || loading || loaded) return;
      loading = true;
      const body = history.querySelector("[data-revision-body]");
      body.textContent = "正在加载版本历史…";
      try {
        const path = `/configs/${encodeURIComponent(config.id)}/revisions?limit=50`;
        const revisions = await presetRead(workspace, path, () => api(path)).promise;
        if (!history.isConnected || !isCurrent()) return;
        body.innerHTML = `<nav>${revisions.map((revision) => `<span class="${revision.version === config.version ? "current" : ""}"><i></i><span><b>v${revision.version}</b><strong>${esc(revision.name)}</strong><small>${ago(revision.updated_at)}</small></span></span>`).join("")}</nav>`;
        loaded = true;
      } catch (error) {
        if (history.isConnected && isCurrent()) body.textContent = `版本历史加载失败：${error.message}；重新展开可重试。`;
      } finally { loading = false; }
    };
    bindEvent(history, "toggle", loadHistory);
    void loadHistory();
  }
}

async function refreshPresetPage() {
  await agentConfig();
}

function mountPresetEditor(host, workspace, chosen) {
  capturePresetDrafts();
  editor.host = host;
  editor.context = null;
  const sessionData = state.data;
  const scope = `${workspace.agent.id}|${workspace.engine || state.data.liveEngine}|`;
  Object.assign(state.data, {
    agentId: workspace.agent.id, engine: state.data.liveEngine,
    inboundTag: chosen?.tag || "", protocol: workspace.inbounds?.find(item => item.tag === chosen?.tag)?.protocol || "",
    builderStep: "listen",
  });
  return {
    ready: agentConfig({workspace}),
    busy: () => editor.savePending || Boolean(editor.context?.generating || editor.context?.navigating),
    dirty: () => sessionData === state.data && drafts().dirty(scope),
    dispose(discard) {
      if (editor.host !== host) return;
      if (sessionData === state.data) {
        drafts().capture();
        if (discard) drafts().clear(scope);
      }
      editor.host = null;
      editor.context = null;
      editor.request += 1;
    },
  };
}


  return {
    agentConfig, capturePresetDrafts, presetHasUnsavedChanges, mountPresetEditor, status,
    hasActiveEditor: () => Boolean(editor.host || editor.savePending),
  };
}
