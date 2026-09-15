import { createConfigArchive } from "./config-archive.js";
import { assertAgentConfigBaseline, deployPreflightError, liveConfigEditorState, liveConfigEngineEligible, liveConfigReadAction, liveConfigSnapshotReusable, submitLiveConfigChange } from "./live-config-state.js";
export { assertAgentConfigBaseline, liveConfigEditorState, liveConfigEngineEligible, liveConfigReadAction, liveConfigSnapshotReusable, submitLiveConfigChange } from "./live-config-state.js";
import { bindProtocolOptionVisibility, bindServerPlanRegeneration, installGeneratedFieldButtons, protocolNavigationNames, readServerPlanInput, snellProtocolOptions, sudokuProtocolOptions } from "./server-plan-form.js";
export { bindServerPlanRegeneration, readServerPlanInput } from "./server-plan-form.js";
import { diagnosticError } from "./errors.js";
import { bindEvent } from "./refresh.js";
import { bindConfigFiles } from "./config-files.js";
import { bindConfigRestrictions } from "./config-restrictions.js";
import { bindConfigInbounds, renderEmbeddedPreset } from "./config-inbounds.js";
import { createPresetDrafts } from "./preset-drafts.js";
import { presetRoute } from "./preset-route.js";
import { createPresetReads, renderPresetIdentity } from "./preset-runtime.js";
import { createTaskMonitor, taskTerminal } from "./task-monitor.js";
import { commonConfigFields, renderCommonFieldStudio, renderConfigSourceStudio, renderGlobalFieldStudio, revealSelectedFields } from "./config-fields.js";
import { configFieldURL, renderSSRustFieldStudio, ssRustFieldGroups, ssRustPlanBinding } from "./ss-rust-fields.js";

export function installConfigPages(ctx) {
  const { api, state, engines, can, esc, engineName, conciseVersion, ago, bytes, confirmAction, notify, shell, submitTask, bindCodeEditors } = ctx;
const archiveConfigs = createConfigArchive(ctx);
let agentConfigRequest = 0;
let presetSavePending = false;
let syncPresetControls = () => {};
let activePresetContext;
// The preset form has one controller whether rendered on a legacy test page
// or mounted in the configuration workspace's scoped dialog.
let presetHost = null;
const presetVisible = () => presetHost ? presetHost.isCurrent() : state.route === "agent-config";
let presetSessionData = state.data;
let presetNavigation = 0;
const presetRead = createPresetReads();
const drafts = () => state.data.presetDrafts ||= createPresetDrafts();
const operationFor = (key = `${state.data.agentId}|${state.data.engine}`) =>
  state.data.presetOperations?.[key] || (state.data.presetOperation?.key === key ? state.data.presetOperation : undefined);
function saveOperation(operation) {
  state.data.presetOperations ||= {};
  state.data.presetOperations[operation.key] = operation;
  state.data.presetOperation = operation;
  return operation;
}
function capturePresetDrafts() { if (presetVisible()) drafts().capture(); }
function presetHasUnsavedChanges() { return (presetSessionData === state.data && presetSavePending) || Boolean(state.data.presetDrafts?.dirty()); }
function configHasUnsavedChanges() {
  const editor = document.querySelector("#live-config-form [data-code-editor]");
  const input = editor?.querySelector("[data-code-input]");
  const sourceDirty = editor?.configFileController ? editor.configFileController.dirty() :
    input && !input.readOnly && input.value !== input.defaultValue;
  return presetHasUnsavedChanges() || Boolean(sourceDirty || document.querySelector('#live-config-form[data-saving="1"], .config-inbound-dialog[data-saving="1"], .config-inbound-dialog[data-dirty="1"]'));
}
let liveConfigRequest = 0;
let liveReadRequest = 0;
let liveAgentRuntimeScope;
let liveAgentRuntimeLoaded = false;

function liveSourceKey(agentId, engine, source = "managed") {
  return source === "import"
    ? `${agentId}|${engine}|import`
    : `${agentId}|${engine}`;
}

function markStaleReadTask(agentId, engine) {
  const key = `${agentId}|${engine}`;
  const entry = state.data.liveSources?.[key];
  const staleId = entry?.pendingTaskId || entry?.taskId;
  if (staleId) {
    state.data.staleReadTasks ||= {};
    state.data.staleReadTasks[key] = staleId;
  }
}

function invalidateLiveSnapshot(agentId, engine) {
  const key = liveSourceKey(agentId, engine);
  markStaleReadTask(agentId, engine);
  liveReadRequest += 1;
  if (state.data.liveSources?.[key]) delete state.data.liveSources[key];
}

const waitForDeployTerminal = createTaskMonitor({
  // Explicit GET bypasses render-scoped read caching for live task state.
  read: id => api(`/tasks/${encodeURIComponent(id)}?view=status`, {method:"GET"}),
  session: () => state.data,
});


function recordPendingDeploy(taskID, agentId, engine) {
  const key = liveSourceKey(agentId, engine);
  state.data.pendingDeployTasks ||= {};
  state.data.pendingDeployTasks[key] = { taskId: taskID };
}

// CAS clear: only remove if the tracked taskId still matches.
function clearPendingDeploy(agentId, engine, expectedTaskID) {
  const key = liveSourceKey(agentId, engine);
  const entry = state.data.pendingDeployTasks?.[key];
  if (entry && (!expectedTaskID || entry.taskId === expectedTaskID))
    delete state.data.pendingDeployTasks[key];
  return entry?.taskId === expectedTaskID;
}

function handleDeployTerminal(result, taskID, agentId, engine) {
  if (result.status === "succeeded") {
    // Every successful deploy changes the node file — always invalidate,
    // even if this task is no longer the latest pending record. This is
    // idempotent (deleting a non-existent key is a no-op).
    invalidateLiveSnapshot(agentId, engine);
    maybeRerenderLiveConfig(agentId, engine);
    // Clean up pending only if this is still the tracked task.
    clearPendingDeploy(agentId, engine, taskID);
  } else {
    // Failed/canceled: notify and clear only if still current pending.
    if (clearPendingDeploy(agentId, engine, taskID)) {
      notify(
        diagnosticError(result.error) ||
          `部署${result.status === "canceled" ? "已取消" : "失败"}`,
        "error",
      );
      maybeRerenderLiveConfig(agentId, engine);
    }
    // Old failed/canceled tasks do NOT clear newer pending records.
  }
}

function maybeRerenderLiveConfig(agentId, engine) {
  const editor = document.querySelector("#live-config-form [data-code-editor]");
  const input = editor?.querySelector("[data-code-input]");
  const sourceDirty = editor?.configFileController ? editor.configFileController.dirty() :
    input && !input.readOnly && input.value !== input.defaultValue;
  if (presetHost || presetSavePending || sourceDirty || document.querySelector('#live-config-form[data-saving="1"]') ||
      document.querySelector(".config-inbound-dialog[open], .config-access-dialog[open]")) return;
  if (
    state.route === "live-config" &&
    state.data.liveAgent === agentId &&
    state.data.liveEngine === engine
  )
    void liveConfig();
}

// Track active reconcilers: Map<key, Set<taskID>> so multiple tasks
// for the same key can each have their own poller.
const activeReconcilers = new WeakMap();

// Fire-and-forget monitor for a newly submitted deploy task.
function monitorDeployTask(taskID, agentId, engine) {
  if (!can("tasks.read")) return;
  const sessionData = state.data;
  let watchers = activeReconcilers.get(sessionData);
  if (!watchers) activeReconcilers.set(sessionData, watchers = new Map());
  if (watchers.has(taskID)) return watchers.get(taskID);
  const monitoring = (async () => {
    try {
      const result = await waitForDeployTerminal(taskID);
      if (state.data !== sessionData) return null;
      handleDeployTerminal(result, taskID, agentId, engine);
      return result;
    } catch {
      return null;
    } finally { watchers.delete(taskID); }
  })();
  watchers.set(taskID, monitoring);
  return monitoring;
}

// Single-instance recovery poller per key. Survives while the page is open;
// cleans up on abort so a future visit can restart it.
function startRecoveryPoller(taskID, agentId, engine) {
  void monitorDeployTask(taskID, agentId, engine);
}

// Called when entering live-config to resolve any pending deploy that
// outlived navigation. Uses the current (route-scoped) api context.
async function reconcilePendingDeploy(agentId, engine) {
  if (!can("tasks.read")) return;
  const key = liveSourceKey(agentId, engine);
  const pending = state.data.pendingDeployTasks?.[key];
  if (!pending?.taskId) return;
  const sessionData = state.data;
  if (activeReconcilers.get(sessionData)?.has(pending.taskId)) return;
  try {
    const task = await api(`/tasks/${encodeURIComponent(pending.taskId)}`);
    if (state.data !== sessionData) return;
    if (["succeeded", "failed", "canceled"].includes(task.status))
      handleDeployTerminal(task, pending.taskId, agentId, engine);
    else startRecoveryPoller(pending.taskId, agentId, engine);
  } catch {
    // Network error or abort — retry on next visit; keep pending entry.
  }
}

function agentConfig(options = {}) {
  const sessionData = state.data;
  const rendering = renderAgentConfig(options);
  const request = agentConfigRequest;
  return rendering.catch(error => {
    const ctx = activePresetContext;
    if (request === agentConfigRequest && state.data === sessionData && ctx?.sessionData === sessionData &&
        presetVisible() && state.data.agentId === ctx.agent.id && state.data.engine === ctx.engine) {
      // A failed route refresh can leave the old DOM on screen. Give it a
      // working recovery action instead of dead handlers from an old epoch.
      ctx.navigationEpoch = state.navigationEpoch;
      if (!presetSavePending && !ctx.navigating) {
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
  if (presetSessionData !== state.data) {
    presetSessionData = state.data;
    presetSavePending = false;
    activePresetContext = null;
    syncPresetControls = () => {};
  }
  capturePresetDrafts();
  const request = ++agentConfigRequest;
  const host = presetHost;
  const root = host?.root || document;
  const sessionData = state.data;
  const navigationEpoch = state.navigationEpoch;
  const routeSignal = state.routeSignal;
  const isCurrent = () => request === agentConfigRequest && host === presetHost && presetVisible() &&
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
  const prior = activePresetContext;
  const scope = `${state.data.agentId}|${engine}|`;
  if (!loadedWorkspace && !presetSavePending && prior?.sessionData === sessionData &&
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
  const inboundNav = (workspace.inbounds || [])
    .map(
      (input) =>
        `<a class="${selectedInbound?.tag === input.tag ? "active" : ""}" href="#agent-config" data-inbound="${esc(input.tag)}"><span><strong>${esc(input.tag)}</strong><small>${esc(input.listen)}:${input.port}</small></span></a>`,
    )
    .join("");
  const protocolNav = workspace.protocols
    .map(
      (item) =>
        `<a class="${item.key === selectedProtocolKey ? "active" : ""}" href="#agent-config" data-protocol="${esc(item.key)}" title="${esc(item.name)}"><b>${esc(item.badge)}</b><span><strong>${esc(protocolNavigationNames[item.key] || item.name)}</strong></span></a>`,
    )
    .join("");
  const methods = (protocol?.methods || [])
    .map(
      (method) =>
        `<option value="${esc(method)}" ${method === plan.method ? "selected" : ""}>${esc(method)}</option>`,
    )
    .join("");
  const transports = (protocol?.transports || ["raw"])
    .map(
      (transport) =>
        `<option value="${esc(transport)}" ${transport === plan.transport ? "selected" : ""}>${transport === "raw" ? "Raw / TCP" : transport === "websocket" ? "WebSocket" : transport === "xhttp" ? "XHTTP" : "gRPC"}</option>`,
    )
    .join("");
  const portForward = Boolean(protocol?.port_forward);
  const protocolOptions =
    engine === "ss-rust"
      ? `<details class="preset-option-panel" open><summary><b>当前端口 · 出站绑定</b><small>仅当前端口生效</small></summary><div class="plan-fields one"><label>出站绑定 IP<input name="ss_rust_outbound_bind_addr" value="${esc(ssRustPlanBinding(plan, config, selectedInbound))}" placeholder="留空继承全局绑定"><small>填写节点本机网卡上的 IPv4 / IPv6 地址。留空删除端口覆盖，不代表禁用全局绑定。DNS、IPv6 优先与性能选项在下方「全局与默认值」单独保存。</small></label></div></details>`
      : selectedProtocolKey === "snell"
      ? snellProtocolOptions(plan, false)
      : selectedProtocolKey === "snell-shadow-tls-v3"
        ? snellProtocolOptions(plan, true)
      : selectedProtocolKey === "sudoku"
        ? sudokuProtocolOptions(plan)
        : "";
  const vlessEncryptionOptions = protocol?.uses_vless_encryption
    ? `<div class="plan-fields one"><label class="secret-input">服务端 VLESS Decryption<span class="secret-value-control"><input type="password" name="vless_decryption" required value="${esc(plan.vless_decryption || "")}" autocomplete="off"><button type="button" data-secret-visibility>显示</button></span><small>由 xray vlessenc 兼容算法生成的 X25519 私有值；只写入服务端，客户端配置不得包含。</small></label><label>客户端 VLESS Encryption<input name="vless_encryption" required value="${esc(plan.vless_encryption || "")}"><small>由服务端 Decryption 自动推导的公开值；分享链接的 encryption 参数使用此值。</small></label></div>`
    : '<input type="hidden" name="vless_decryption" value=""><input type="hidden" name="vless_encryption" value="">';
  const identitySection = `<section class="builder-section" id="target"><header><span class="section-number">02</span><strong>转发目标</strong></header><div><div class="plan-fields three"><label>目标地址<input name="target_address" maxlength="253" required value="${esc(plan.target_address)}" placeholder="127.0.0.1 或 target.example.com"></label><label>目标端口<input type="number" name="target_port" min="1" max="65535" required value="${Number(plan.target_port)}"></label><label>转发协议<select name="network"><option value="tcp" ${plan.network === "tcp" ? "selected" : ""}>TCP</option><option value="udp" ${plan.network === "udp" ? "selected" : ""}>UDP</option><option value="tcp,udp" ${plan.network === "tcp,udp" ? "selected" : ""}>TCP + UDP</option></select></label></div><p class="validation-note">流量由当前内核直连转发到目标地址；部署前请确认监听端口与防火墙已放行。</p><input type="hidden" name="username" value=""><input type="hidden" name="credential" value=""><input type="hidden" name="secondary_credential" value=""><input type="hidden" name="method" value=""></div></section>`;
  const xrayRealityAdvanced = engine === "xray"
    ? `<div class="plan-fields one"><label>最低客户端 Xray 版本<input name="reality_min_client_ver" required pattern="[0-9]{1,3}(\\.[0-9]{1,3}){2}" value="${esc(plan.reality_min_client_ver || "0.0.0")}"><small>保持原预设默认值 0.0.0；可在此自定义客户端最低版本。提高版本会拒绝更旧的客户端。</small></label></div><div class="plan-fields one"><label class="secret-input">ML-DSA-65 Seed（可选，仅服务端）<span class="secret-value-control"><input type="password" name="reality_mldsa65_seed" value="${esc(plan.reality_mldsa65_seed || "")}" autocomplete="off"><button type="button" data-secret-visibility>显示</button></span><small>使用 xray mldsa65 生成；系统会推导客户端 Verify。启用时强制 target 证书链严格大于 3500 bytes，并要求 X25519MLKEM768。</small></label></div>`
    : '<input type="hidden" name="reality_min_client_ver" value="0.0.0"><input type="hidden" name="reality_mldsa65_seed" value="">';
  const security = protocol?.uses_reality
    ? `<input type="hidden" name="reality_enabled" value="1"><section class="builder-section security-section" id="security"><header><span class="section-number">04</span><strong>Reality</strong></header><div><div class="plan-fields two"><label>目标域名 / ServerName<input name="reality_server_name" list="reality-presets" required value="${esc(plan.reality_server_name)}"><datalist id="reality-presets">${workspace.reality_presets.map((value) => `<option value="${esc(value)}">`).join("")}</datalist><small>校验公网 DNS；拒绝 Cloudflare 与非公网地址。</small></label><label>Short ID<input name="reality_short_id" required value="${esc(plan.reality_short_id)}"></label></div><div class="plan-fields one"><label>客户端 Public Key<input name="reality_public_key" required value="${esc(plan.reality_public_key)}"></label><label class="secret-input">服务端 Private Key<span class="secret-value-control"><input type="password" name="reality_private_key" required value="${esc(plan.reality_private_key)}"><button type="button" data-secret-visibility>显示</button></span></label></div>${xrayRealityAdvanced}</div></section>`
    : protocol?.supports_tls
      ? `<input type="hidden" name="reality_enabled" value="0"><section class="builder-section security-section" id="security"><header><span class="section-number">04</span><strong>TLS</strong></header><div><label class="tls-switch"><input type="checkbox" name="tls_enabled" value="1" ${plan.tls_enabled || protocol.requires_tls ? "checked" : ""} ${protocol.requires_tls ? "disabled" : ""}><strong>${protocol.requires_tls ? "TLS" : "启用 TLS"}</strong></label><div class="plan-fields two"><label>证书路径<input name="certificate_path" value="${esc(plan.certificate_path)}"></label><label>私钥路径<input name="private_key_path" value="${esc(plan.private_key_path)}"></label></div><p class="validation-note">私钥仅目标内核服务组可读。</p></div></section>`
      : '<input type="hidden" name="reality_enabled" value="0"><input type="hidden" name="tls_enabled" value="0">';
  const sourceStudio = renderConfigSourceStudio({ config, catalog: workspace.catalog, engineInstalled,
    open: state.data.settings?.default_config_editor === "source", ssRust: engine === "ss-rust" });
  const revisionTimeline = config
    ? `<details class="revision-timeline node-revision-timeline" id="revisions"><summary><b>版本历史</b><strong>当前 v${config.version}</strong></summary><div class="timeline-body" data-revision-body>展开加载版本历史</div></details>`
    : "";
  const canAutoInstall = !engineInstalled && (state.session?.role === "admin" || agent.can_manage === true) &&
    (agent.features || []).includes("preset-auto-install-v1");
  const executionCallout = !engineInstalled
    ? `<aside class="config-execution-callout"><span><b>${esc(engineName(engine))} 尚未安装</b><small>${commonMutation ? "通用配置提交需要已安装内核。增加入站可自动安装稳定版；管理内核版本请到节点设置。" : canAutoInstall ? "增加入站并提交时，将自动安装最新稳定版，再执行校验或部署。切换版本请到节点设置。" : "自动安装需要节点管理权及新版 Agent；请到节点设置检查权限或升级 Agent。"}</small></span><a class="button small" href="#node-${esc(agent.id)}">节点设置</a></aside>`
    : agent.runtime?.[engine]?.existing_config_unsupported_reason
      ? `<aside class="config-execution-callout"><span><b>当前内核暂不可提交任务</b><small>${esc(agent.runtime[engine].existing_config_unsupported_reason)}</small></span><a class="button small" href="#node-settings">检查节点配置</a></aside>`
      : agent.status !== "online"
        ? '<aside class="config-execution-callout"><span><b>节点当前离线</b><small>可以继续编辑草稿，节点上线并重新加载后可校验或部署。</small></span></aside>'
        : !can("agent-config.write") || !can("tasks.execute")
          ? '<aside class="config-execution-callout"><span><b>当前账号不可提交预设</b><small>保存并执行需要配置写入和任务执行权限。</small></span></aside>' : "";
  const advancedStudio = commonMutation
    ? renderCommonFieldStudio({ engine, fields, selected:selectedField, value:fieldValue, config,
        catalog:workspace.catalog, mutation:commonMutation })
    : engine === "ss-rust"
    ? renderSSRustFieldStudio({ scope: "inbound", fields: ssRustGroups.inbound, selected: selectedInboundField,
        value: inboundFieldValue, config, inbound: selectedInbound }) +
      renderSSRustFieldStudio({ scope: "global", fields, selected: selectedField, value: fieldValue,
        config, presentFields: workspace.present_fields }) + sourceStudio
    : renderGlobalFieldStudio({ fields, selected: selectedField, value: fieldValue, config,
        catalog: workspace.catalog, presentFields: workspace.present_fields }) + sourceStudio;
  (host ? (markup, _title, options) => renderEmbeddedPreset(host, markup, options) : shell)(
    renderPresetIdentity(`<section class="config-command-bar loaded"><header class="config-command-head"><div class="config-command-title"><span class="engine-badge ${esc(engine)}">${esc(engineName(engine))}</span><div><p class="eyebrow">Server recipe</p><h2>${esc(protocol?.name || "Protocol")} · ${selectedInbound ? esc(selectedInbound.tag) : "新入站"}</h2><small>${esc(agent.name)} · ${esc(workspace.catalog.name)}</small></div></div><div class="config-command-state"><button class="button small" type="button" data-refresh-preset>刷新状态</button><span class="status-label ${!engineInstalled ? "muted" : config ? "ok" : "warn"}">${!engineInstalled ? "内核未安装" : config ? "已读取" : "新方案"}</span><span class="recipe-version"><b>${config ? `v${config.version}` : "草稿"}</b><small>${esc(workspace.catalog.format)}</small></span><a href="${esc(protocol?.docs)}" target="_blank" rel="noopener noreferrer">文档 ↗</a></div></header><details class="config-hierarchy-menu" open><summary><b>切换入站 / 协议</b><i>＋</i></summary><div class="config-command-selectors${workspace.protocols.length > 5 ? " protocol-catalog-wide" : ""}">${inboundNav ? `<section class="inbound-browser config-selector"><header><span><b>入站</b><small>${workspace.inbounds.length} 个</small></span><button class="button small" type="button" data-new-inbound>＋ 新增</button></header><nav>${inboundNav}</nav></section>` : ""}<section class="protocol-browser config-selector"><header><span><b>协议</b><small>${workspace.protocols.length} 种</small></span></header><nav>${protocolNav}</nav></section></div></details></section>${executionCallout}<article class="recipe-workspace"><form class="server-form" id="server-plan-form"><div class="config-mutation"><label>操作<select name="operation">${selectedInbound ? `<option value="modify">修改 · ${esc(selectedInbound.tag)}</option><option value="add">新增入站</option><option value="delete">删除 · ${esc(selectedInbound.tag)}</option>` : '<option value="add">新增入站</option>'}</select></label></div><div class="builder-layout" data-builder-workbench><nav class="builder-index"><a href="#listen" data-builder-step="listen"><b>01</b><strong>监听</strong></a><a href="#identity" data-builder-step="identity"><b>02</b><strong>认证</strong></a>${protocol?.transport_config ? '<a href="#transport" data-builder-step="transport"><b>03</b><strong>传输</strong></a>' : ""}${protocol?.uses_reality || protocol?.supports_tls ? '<a href="#security" data-builder-step="security"><b>04</b><strong>安全</strong></a>' : ""}</nav><div class="builder-sections"><section class="builder-section" id="listen"><header><span class="section-number">01</span><strong>监听</strong></header><div class="plan-fields three"><label>入站标签<input name="tag" maxlength="64" required value="${esc(plan.tag)}"></label><label>监听地址<input name="listen" required value="${esc(plan.listen)}"></label><label>监听端口<input type="number" name="port" min="1" max="65535" required value="${Number(plan.port)}"></label></div></section><section class="builder-section" id="identity"><header><span class="section-number">02</span><strong>认证</strong></header><div><div class="plan-fields two">${protocol?.ignores_username ? '<input type="hidden" name="username" value="default">' : `<label>用户名或备注<input name="username" maxlength="64" required value="${esc(plan.username)}"></label>`}<label class="secret-input">${esc(protocol?.credential_label || "凭据")}<span class="secret-value-control"><input type="password" name="credential" required value="${esc(plan.credential)}"><button type="button" data-secret-visibility>显示</button></span></label>${protocol?.secondary_credential_label ? `<label class="secret-input">${esc(protocol.secondary_credential_label)}<span class="secret-value-control"><input type="password" name="secondary_credential" required value="${esc(plan.secondary_credential)}"><button type="button" data-secret-visibility>显示</button></span></label>` : '<input type="hidden" name="secondary_credential" value="">'}</div>${methods ? `<div class="plan-fields one"><label>加密方式<select name="method">${methods}</select></label></div>` : '<input type="hidden" name="method" value="">'}</div></section>${protocol?.transport_config ? `<section class="builder-section" id="transport"><header><span class="section-number">03</span><strong>传输</strong></header><div class="plan-fields two"><label>传输<select name="transport">${transports}</select></label><label>路径 / ServiceName<input name="transport_path" value="${esc(plan.transport_path)}"></label></div></section>` : '<input type="hidden" name="transport" value="raw"><input type="hidden" name="transport_path" value="">'}${security}</div></div><footer class="builder-actions compact"><span class="builder-regenerate-status" data-regenerate-status role="status" aria-live="polite"></span><div><button class="button" type="button" data-regenerate>重新生成参数</button><button class="button" type="submit" data-plan-intent="validate" ${agent.status !== "online" || !engineInstalled ? "disabled" : ""}>保存并校验</button><button class="button primary" type="submit" data-plan-intent="deploy" ${agent.status !== "online" || !engineInstalled ? "disabled" : ""}>保存并部署</button></div></footer></form></article>${revisionTimeline}${advancedStudio}`, protocolOptions + vlessEncryptionOptions, portForward ? identitySection : ""),
    "节点配置",
    {
      viewKey: `agent-config-${agent.id}-${engine}-${selectedProtocolKey}-${selectedInbound?.tag || "new"}-${config?.version || 0}-${state.data.presetDraftReset || 0}`,
      commonFields: commonMutation ? fields : [],
      selectedField,
    },
  );
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
  activePresetContext = context;
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
      syncPresetControls();
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

function renderPresetStatus() {
  const key = state.route === "live-config" ? `${state.data.liveAgent}|${state.data.liveEngine}` : `${state.data.agentId}|${state.data.engine}`;
  if (state.route !== "live-config" && !presetVisible()) return;
  const operation = operationFor(key);
  if (!operation) return;
  const root = presetHost?.root || document;
  let status = root.querySelector("[data-preset-status]");
  if (!status) {
    status = document.createElement("div");
    status.dataset.presetStatus = "";
    const anchor = root.querySelector(".config-command-bar, .live-config-details");
    if (anchor) anchor.after(status);
    else if (presetHost) root.prepend(status);
  }
  status.className = `alert preset-submit-status ${operation.tone}`;
  status.setAttribute("role", operation.tone === "error" ? "alert" : "status");
  status.setAttribute("aria-live", "polite");
  status.textContent = operation.message;
  if (operation.task?.id) {
    const link = document.createElement("a");
    link.href = "#tasks";
    link.textContent = " 查看执行记录 →";
    status.append(link);
  }
  if (operation.reload) {
    const retry = document.createElement("button");
    retry.type = "button";
    retry.className = "button small";
    retry.textContent = "重新加载配置";
    retry.onclick = async () => {
      if (presetSavePending || activePresetContext?.generating) return;
      const sessionData = state.data;
      const epoch = state.navigationEpoch;
      retry.disabled = true;
      try {
        if (drafts().dirty() && !(await confirmAction("重新加载将放弃当前未保存的草稿，是否继续？", "重新加载配置"))) return;
        if (state.data !== sessionData || state.navigationEpoch !== epoch || operationFor(key) !== operation) return;
        drafts().clear(operation.key + "|");
        // Same-version reconciliation normally preserves inputs. An explicit
        // discard must instead mount fresh controls with saved defaults.
        state.data.presetDraftReset = (state.data.presetDraftReset || 0) + 1;
        if (presetVisible()) await refreshPresetPage();
        else {
          operation.reload = false;
          await liveConfig();
        }
      }
      catch (error) {
        if (operationFor(key) !== operation) return;
        operation.reload = true;
        operation.message = `页面加载失败：${error.message}。请重新加载后继续编辑。`;
        renderPresetStatus();
        syncPresetControls();
      }
      finally { retry.disabled = false; }
    };
    status.append(retry);
  }
}

function watchPresetOperation(operation) {
  if (!operation?.task?.id || operation.monitoring || taskTerminal(operation.task) || !can("tasks.read")) return;
  const sessionData = state.data;
  const {key, intent, version, task} = operation;
  const [agentId, engine] = key.split("|");
  const tracked = () => sessionData === state.data && operationFor(key) === operation;
  operation.monitoring = true;
  const progress = (latest, error) => {
    if (!tracked() || operation.reload) return;
    const message = error ? `配置 v${version} 已保存，任务状态连接暂时中断，正在重试…` :
      `配置 v${version} 已保存，${task.install_if_missing ? "稳定版安装及" : ""}${intent === "deploy" ? "部署" : "校验"}任务${latest.status === "running" ? "正在执行" : "正在排队"}…`;
    if (operation.message === message) return;
    operation.message = message;
    operation.tone = "";
    renderPresetStatus();
  };
  const waiting = waitForDeployTerminal(task.id, progress);
  if (intent === "deploy") void monitorDeployTask(task.id, agentId, engine);
  void waiting.then(latest => {
    if (!tracked()) return;
    operation.task = latest;
    operation.message = latest.status === "succeeded"
      ? `配置 v${version} ${intent === "deploy" ? "部署" : "校验"}成功。`
      : `配置已保存，任务${latest.status === "canceled" ? "已取消" : "失败"}：${latest.error || "请查看执行记录"}`;
    if (operation.reload) operation.message += " 请重新加载配置后继续编辑。";
    operation.tone = latest.status === "succeeded" ? "success" : "error";
    if (task.install_if_missing) {
      liveAgentRuntimeLoaded = false;
      maybeRerenderLiveConfig(agentId, engine);
    }
    renderPresetStatus();
  }).catch(error => {
    if (!tracked() || error.name === "AbortError") return;
    operation.message = "配置已保存，任务状态暂不可读取，请查看执行记录。";
    operation.tone = "error";
    renderPresetStatus();
  }).finally(() => { operation.monitoring = false; });
}

function bindAgentConfigPage(ctx, fieldsOnly = false) {
  const root = ctx.root;
  const visible = () => ctx.host === presetHost && presetVisible() && activePresetContext === ctx &&
    state.data === ctx.sessionData && state.navigationEpoch === ctx.navigationEpoch;
  const current = () => visible() &&
    state.data.agentId === ctx.agent.id && state.data.engine === ctx.engine;
  const draftKey = selector => `${ctx.draftScope}${ctx.draftVersion}|${selector}|${
    selector === "#server-plan-form" ? ctx.selectedInbound?.tag || `new:${ctx.protocol?.key}` :
    selector === "#field-form" ? `${ctx.selectedField?.key}|${ctx.commonMutation || "advanced"}` :
    selector === "#inbound-field-form" ? `${ctx.selectedInbound?.tag}|${ctx.selectedInboundField?.key}` : "source"}`;
  const navigate = (selection, reuseWorkspace = true) => {
    if (presetSavePending || ctx.generating || !visible()) return;
    const previous = {
      engine: ctx.engine, protocol: ctx.protocol?.key, inboundTag: ctx.selectedInbound?.tag || "",
      configField: ctx.selectedField?.key, configInboundField: ctx.selectedInboundField?.key,
    };
    if (!ctx.navigating && Object.entries(selection).every(([key,value]) => previous[key] === value)) return;
    capturePresetDrafts();
    const navigation = ++presetNavigation;
    ctx.navigating = navigation;
    syncPresetControls();
    Object.assign(state.data, previous, selection);
    const rendering = agentConfig(reuseWorkspace ? { workspace: ctx.workspace } : {});
    const request = agentConfigRequest;
    void rendering.catch(async (error) => {
      if (request !== agentConfigRequest || !visible()) return;
      Object.assign(state.data, previous);
      // Request IDs are monotonic; old async reads stay invalidated.
      notify(`切换配置失败：${error.message}`, "error");
      // Rebind any deferred editors whose earlier request was invalidated.
      try { await agentConfig({workspace:ctx.workspace}); }
      catch (recoveryError) { if (visible()) notify(recoveryError.message, "error"); }
    }).finally(() => {
      if (ctx.navigating === navigation) ctx.navigating = false;
      syncPresetControls();
    });
  };
  revealSelectedFields(root);
  root.querySelectorAll(".config-field-studio").forEach((studio) => {
    bindEvent(studio, "toggle", () => { if (studio.open) revealSelectedFields(root); });
  });
  if (!ctx.engineInstalled)
    root
      .querySelectorAll(
        "#field-form button[type=submit], #inbound-field-form button[type=submit], #source-config-form button[type=submit]",
      )
      .forEach((button) => (button.disabled = true));
  root.querySelectorAll("[data-engine-select]").forEach(
    (link) =>
      (link.onclick = (event) => {
        event.preventDefault();
        if (link.dataset.engineSelect === ctx.engine && !ctx.navigating) return;
        navigate({ engine: link.dataset.engineSelect, protocol: "", inboundTag: "" }, false);
      }),
  );
  root.querySelectorAll("[data-inbound]").forEach(
    (link) =>
      (link.onclick = (event) => {
        event.preventDefault();
        const input = ctx.workspace.inbounds.find(
          (item) => item.tag === link.dataset.inbound,
        );
        if (input) navigate({ inboundTag: input.tag, protocol: input.protocol });
      }),
  );
  root.querySelectorAll("[data-protocol]").forEach(
    (link) =>
      (link.onclick = (event) => {
        event.preventDefault();
        navigate({ protocol: link.dataset.protocol, inboundTag: "" });
      }),
  );
  bindEvent(root.querySelector("[data-preset-protocol]"), "change", event => {
    navigate({protocol:event.target.value, inboundTag:""});
  });
  bindEvent(root.querySelector("[data-common-field]"), "change", event => {
    if (ctx.commonFields.some(field => field.key === event.target.value))
      navigate({configField:event.target.value});
  });
  bindEvent(root.querySelector("[data-new-inbound]"), "click", () => {
      if (presetSavePending || ctx.generating || !current()) return;
      navigate({ inboundTag: "", protocol: ctx.protocol.key });
  });
  root.querySelectorAll("[data-config-field]").forEach(
    (link) =>
      (link.onclick = (event) => {
        event.preventDefault();
        navigate({ configField: link.dataset.configField });
      }),
  );
  root.querySelectorAll("[data-inbound-field]").forEach((link) => {
    link.onclick = (event) => {
      event.preventDefault();
      navigate({ configInboundField: link.dataset.inboundField });
    };
  });
  root.querySelectorAll("[data-secret-visibility]").forEach(
    (button) =>
      (button.onclick = () => {
        const input = button.parentElement.querySelector("input");
        input.type = input.type === "password" ? "text" : "password";
        button.textContent = input.type === "password" ? "显示" : "隐藏";
      }),
  );
  const serverPlanForm = root.querySelector("#server-plan-form");
  if (serverPlanForm) drafts().bind(serverPlanForm, draftKey("#server-plan-form"));
  if (serverPlanForm && !fieldsOnly) {
    bindProtocolOptionVisibility(serverPlanForm);
    installGeneratedFieldButtons(serverPlanForm, ctx.protocol);
  }
  const regenerateStatus = serverPlanForm?.querySelector(
    "[data-regenerate-status]",
  );
  const regenerateButtons = [
    ...(serverPlanForm?.querySelectorAll("[data-regenerate]") || []),
  ];
  if (!fieldsOnly && serverPlanForm && regenerateButtons.length && regenerateStatus && can("agent-config.write")) {
    bindServerPlanRegeneration({
      form: serverPlanForm,
      buttons: regenerateButtons,
      api,
      base: ctx.base,
      protocol: ctx.protocol,
      canApply: () => current() && !presetSavePending && !ctx.navigating,
      onBusy: busy => { ctx.generating = busy; syncPresetControls(); },
      report: (message, tone = "success") => {
        regenerateStatus.classList.toggle("error", tone === "error");
        regenerateStatus.setAttribute(
          "role",
          tone === "error" ? "alert" : "status",
        );
        regenerateStatus.textContent = message;
      },
      onApplied: (plan) => {
        if (!ctx.selectedInbound) state.data.serverPlans[
          `${ctx.agent.id}|${ctx.engine}|${ctx.protocol.key}`
        ] = plan;
      },
    });
  }
  const operationKey = `${ctx.agent.id}|${ctx.engine}`;
  const showStatus = (message, tone = "", task) => {
    const previous = operationFor(operationKey);
    const reload = task?.id && previous?.task?.id === task.id && previous.reload;
    // Preserve the active watcher when only the post-save refresh failed.
    if (task?.id && previous?.task?.id === task.id) Object.assign(previous, {message, tone, reload});
    else saveOperation({key:operationKey, message, tone, task, reload});
    renderPresetStatus();
  };
  renderPresetStatus();
  if (!fieldsOnly) watchPresetOperation(operationFor(operationKey));
  const forms = ["#server-plan-form", "#field-form", "#inbound-field-form", "#source-config-form"];
  const canWrite = can("agent-config.write");
  const canSubmit = canWrite && can("tasks.execute") &&
    ctx.agent.status === "online" && !ctx.agent.runtime?.[ctx.engine]?.existing_config_unsupported_reason;
  const canSubmitForm = selector => canSubmit && (ctx.engineInstalled ||
    (selector === "#server-plan-form" && ctx.canAutoInstall && serverPlanForm?.elements.operation.value === "add"));
  const needsReload = () => operationFor(operationKey)?.reload;
  const refreshButton = root.querySelector("[data-refresh-preset]");
  bindEvent(refreshButton, "click", async () => {
    if (presetSavePending || ctx.generating || ctx.navigating || !current()) return;
    capturePresetDrafts();
    ctx.navigating = ++presetNavigation;
    syncPresetControls();
    try {
      await refreshPresetPage();
    } catch (error) {
      if (visible()) {
        showStatus(`刷新失败：${error.message}。草稿已保留，可重试刷新。`, "error");
      }
    } finally {
      ctx.navigating = false;
      syncPresetControls();
    }
  });
  syncPresetControls = () => {
    if (!visible()) return;
    const blocked = presetSavePending || Boolean(ctx.navigating);
    if (refreshButton) {
      refreshButton.disabled = blocked || ctx.generating || needsReload();
      refreshButton.textContent = ctx.navigating ? "正在加载…" : "刷新状态";
    }
    root.querySelector(".config-command-bar")?.setAttribute("aria-busy", String(blocked));
    const protocolSelect = root.querySelector("[data-preset-protocol]");
    if (protocolSelect) protocolSelect.disabled = blocked || ctx.generating;
    const commonSelect = root.querySelector("[data-common-field]");
    if (commonSelect) commonSelect.disabled = blocked || ctx.generating;
    for (const selector of forms) {
      const element = root.querySelector(selector);
      if (!element) continue;
      element.inert = blocked;
      element.querySelectorAll("button[type=submit]").forEach((button) => {
        button.disabled = !canSubmitForm(selector) || presetSavePending || ctx.generating || ctx.navigating || needsReload();
      });
    }
  };
  syncPresetControls();
  for (const selector of forms) {
    const element = root.querySelector(selector);
    if (!element) continue;
    drafts().bind(element, draftKey(selector));
    element.noValidate = true;
    element.querySelectorAll("button[type=submit]").forEach((button) => {
      button.disabled = !canSubmitForm(selector) || presetSavePending || ctx.generating || ctx.navigating || needsReload();
    });
    if (!canWrite) element.querySelectorAll("input, select, textarea, [data-regenerate]").forEach((control) => {
      control.disabled = true;
    });
    bindEvent(element, "submit", async (event) => {
      event.preventDefault();
      if (presetSavePending || ctx.generating || ctx.navigating || !current()) return;
      if (!canSubmitForm(selector) || needsReload()) {
        notify("当前节点、内核状态或账号权限不允许提交任务", "error");
        return;
      }
      // Keep DOM and intent references before the first await: currentTarget
      // is cleared by the browser when event dispatch returns.
      const formElement = element;
      const sessionData = state.data;
      const submitter = event.submitter;
      const values = new FormData(formElement);
      const isPlan = selector === "#server-plan-form";
      const isSource = selector === "#source-config-form";
      const perPort = selector === "#inbound-field-form";
      const intent = submitter?.dataset[isPlan ? "planIntent" : isSource ? "sourceIntent" : "fieldIntent"] || "validate";
      const operation = values.get("operation");
      const deleting = isPlan && operation === "delete";
      const commonField = selector === "#field-form" && ctx.commonMutation;
      const deletingCommon = commonField && ctx.commonMutation === "delete";
      if (commonField && (values.get("mutation") !== ctx.commonMutation ||
          !ctx.commonFields.some(field => field.key === ctx.selectedField?.key) || ctx.fieldValue.error ||
          Boolean(ctx.fieldValue.present) !== (ctx.commonMutation !== "add"))) {
        notify("通用配置项状态或操作已变化，请重新加载后再提交。", "error");
        return;
      }
      if (!deleting && !deletingCommon) {
        const invalid = [...formElement.elements].find((control) => control.willValidate && !control.validity.valid);
        if (invalid) {
          const section = invalid.closest(".builder-section");
          if (section) root.querySelector(`[data-builder-step="${section.id}"]`)?.click();
          for (let parent = invalid.parentElement; parent; parent = parent.parentElement)
            if (parent.tagName === "DETAILS") parent.open = true;
          invalid.focus();
          invalid.reportValidity();
          showStatus("请检查已标出的必填项或参数格式。", "error");
          return;
        }
      }
      presetSavePending = true;
      syncPresetControls();
      const buttons = forms.flatMap((id) => [...root.querySelectorAll(`${id} button[type=submit]`)]);
      const originalLabel = submitter?.textContent;
      buttons.forEach((button) => (button.disabled = true));
      formElement.setAttribute("aria-busy", "true");
      if (submitter) submitter.textContent = "正在保存…";
      let committedConfig, committedTask;
      try {
        if (drafts().otherDirty(draftKey(selector), ctx.draftScope) && !(await confirmAction(
          "本次只保存当前表单。其他入站、字段或源码中有未保存的修改，继续后这些草稿将被清除。是否继续？", "存在其他未保存修改",
        ))) return;
        if (deleting && !(await confirmAction(
          `确定删除入站“${ctx.selectedInbound?.tag}”？${intent === "deploy" ? "将部署配置并重启内核。" : "本次只保存并校验，节点配置需部署后生效。"}`,
          "删除入站",
        ))) return;
        if (deletingCommon && !(await confirmAction(
          `确定删除通用配置项“${ctx.selectedField.label}”（${ctx.selectedField.key}）？不会删除公共配置文件或其他入站。${intent === "deploy" ? "将部署配置并重启内核。" : "本次只保存并校验，节点配置需部署后生效。"}`,
          "删除通用配置项",
        ))) return;
        if (!current() || !formElement.isConnected) return;
        if (intent === "deploy" && ctx.beforeDeploy) {
          showStatus("正在核验 Agent 当前配置…");
          if (submitter) submitter.textContent = "正在核验…";
          await ctx.beforeDeploy();
          if (!current() || !formElement.isConnected) return;
        }
        showStatus("正在保存配置并创建任务…");
        if (submitter) submitter.textContent = "正在保存…";
        let result;
        let input;
        if (isPlan) {
          input = deleting ? ctx.selectedInbound : readServerPlanInput(formElement, ctx.protocol);
          result = await api(`${ctx.base}/server-inbounds`, {
            method: "POST",
            body: JSON.stringify({
              operation,
              install_if_missing: operation === "add" && !ctx.engineInstalled,
              original_tag: operation === "add" ? "" : ctx.selectedInbound?.tag || "",
              expected_version: ctx.workspace.config?.version || 0,
              name: ctx.workspace.config?.name || `${ctx.agent.name} · ${engineName(ctx.engine)}`,
              description: `${ctx.protocol.name} 服务端入站，由 QControlHub 方案生成`,
              intent,
              preserve_ss_rust_globals: ctx.engine === "ss-rust",
              input,
            }),
          });
        } else if (isSource) {
          result = await api(`${ctx.base}/source`, {
            method: "POST",
            body: JSON.stringify({
              agent_id: ctx.agent.id, engine: ctx.engine, name: values.get("name"),
              description: values.get("description"), content: values.get("content"),
              version: ctx.workspace.config.version,
              intent,
            }),
          });
        } else {
          result = await api(configFieldURL(ctx.base,
            (perPort ? ctx.selectedInboundField : ctx.selectedField).key,
            perPort ? ctx.selectedInbound?.tag || "" : undefined), {
            method: "POST", body: JSON.stringify({
              mutation: commonField ? ctx.commonMutation : values.get("mutation"),
              fragment: deletingCommon ? "" : values.get("fragment"),
              expected_version: ctx.workspace.config.version,
              name: ctx.workspace.config.name, description: ctx.workspace.config.description, intent,
            }),
          });
        }
        if (state.data !== sessionData) return;
        committedConfig = result.config;
        committedTask = result.task;
        drafts().clear(ctx.draftScope);
        if (intent === "deploy" && result?.task?.id) {
          recordPendingDeploy(result.task.id, ctx.agent.id, ctx.engine);
        }
        showStatus(`配置 v${result.config.version} 已保存，${intent === "deploy" ? "部署" : "校验"}任务已提交。`, "", result.task);
        const operationStatus = operationFor(operationKey);
        Object.assign(operationStatus, {version:result.config.version, intent});
        watchPresetOperation(operationStatus);
        if (isPlan) {
          if (state.data.agentId === ctx.agent.id && state.data.engine === ctx.engine)
            state.data.inboundTag = deleting ? "" : input.tag;
          // Never reuse a committed identity when starting another inbound,
          // even if the user left the preset page while saving.
          state.data.serverPlans[operationKey + "|" + ctx.protocol.key] = null;
        }
        if (ctx.host) {
          // The parent replaces its saved snapshot only after the atomic
          // mutation succeeds, then refreshes the existing source editor.
          await ctx.host.onSaved(result, input);
          return;
        }
        if (!current()) {
          if (state.route === "agent-config" && state.data.agentId === ctx.agent.id && state.data.engine === ctx.engine) {
            await refreshPresetPage();
          }
          return;
        }
        // Keep the whole operation locked until the saved revision is bound.
        await refreshPresetPage();
      } catch (error) {
        if (current()) {
          if (committedConfig && committedTask) {
            showStatus(`配置 v${committedConfig.version} 已保存且任务已提交，页面刷新失败：${error.message}。请重新加载后继续编辑。`, "error", committedTask);
            operationFor(operationKey).reload = true;
            renderPresetStatus();
          } else {
            const uncertain = !error.deployPreflight && (!error.status || error.status >= 500);
            showStatus(error.deployPreflight ? error.message : error.status === 409
              ? `配置或节点状态已变化：${error.message}。草稿已保留，请重新加载后再编辑。`
              : uncertain ? `未能确认保存结果：${error.message}。草稿已保留，请重新加载核对版本后再提交。` : error.message, "error");
            if (!error.deployPreflight && (error.status === 409 || uncertain)) {
              operationFor(operationKey).reload = true;
              renderPresetStatus();
            }
          }
          notify(error.message, "error");
        }
      } finally {
        if (state.data === sessionData) presetSavePending = false;
        formElement.removeAttribute("aria-busy");
        if (submitter) submitter.textContent = originalLabel;
        if (formElement.isConnected && current())
          buttons.forEach((button) => (button.disabled = !canSubmitForm(selector)));
        if (state.data === sessionData) syncPresetControls();
      }
    });
  }
  root.querySelectorAll("[data-builder-workbench]").forEach((workbench) => {
    const links = [...workbench.querySelectorAll("[data-builder-step]")];
    const sections = [...workbench.querySelectorAll(".builder-sections > .builder-section")];
    if (!links.length || !sections.length) return;
    links[0].parentElement?.setAttribute("role", "tablist");
    const activate = (id) => {
      const selected = sections.find((section) => section.id === id) || sections[0];
      state.data.builderStep = selected.id;
      sections.forEach((section) => {
        const active = section === selected;
        section.hidden = !active;
        section.setAttribute("role", "tabpanel");
        section.setAttribute("aria-hidden", active ? "false" : "true");
      });
      links.forEach((link) => {
        const active = link.dataset.builderStep === selected.id;
        link.classList.toggle("active", active);
        link.setAttribute("role", "tab");
        link.setAttribute("aria-selected", active ? "true" : "false");
        link.tabIndex = active ? 0 : -1;
      });
    };
    links.forEach((link) => bindEvent(link, "click", (event) => {
      event.preventDefault();
      activate(link.dataset.builderStep);
    }));
    links.forEach((link,index) => bindEvent(link, "keydown", event => {
      if (!["ArrowLeft","ArrowRight","Home","End"].includes(event.key)) return;
      event.preventDefault();
      const next = event.key === "Home" ? 0 : event.key === "End" ? links.length - 1 :
        (index + (event.key === "ArrowRight" ? 1 : -1) + links.length) % links.length;
      activate(links[next].dataset.builderStep);
      links[next].focus();
    }));
    activate(state.data.builderStep || sections[0].id);
  });
}

function mountPresetEditor(host, workspace, chosen) {
  capturePresetDrafts();
  presetHost = host;
  activePresetContext = null;
  const sessionData = state.data;
  const scope = `${workspace.agent.id}|${workspace.engine || state.data.liveEngine}|`;
  Object.assign(state.data, {
    agentId: workspace.agent.id, engine: state.data.liveEngine,
    inboundTag: chosen?.tag || "", protocol: workspace.inbounds?.find(item => item.tag === chosen?.tag)?.protocol || "",
    builderStep: "listen",
  });
  return {
    ready: agentConfig({workspace}),
    busy: () => presetSavePending || Boolean(activePresetContext?.generating || activePresetContext?.navigating),
    dirty: () => sessionData === state.data && drafts().dirty(scope),
    dispose(discard) {
      if (presetHost !== host) return;
      if (sessionData === state.data) {
        drafts().capture();
        if (discard) drafts().clear(scope);
      }
      presetHost = null;
      activePresetContext = null;
      agentConfigRequest += 1;
    },
  };
}

async function liveConfig(options = {}) {
  const preferCachedRead = options.preferCachedRead !== false;
  const request = ++liveConfigRequest;
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
  const prefetchWorkspace = can("agents.read") && selectedAgent && selectedEngine &&
    !state.data.pendingDeployTasks?.[liveSourceKey(selectedAgent, selectedEngine)]
    ? api(`/agents/${encodeURIComponent(selectedAgent)}/configs/${encodeURIComponent(selectedEngine)}/workspace`)
      .then(value => ({value}), error => ({error})) : null;
  const targetedWorkspace = state.session && !can("agents.read") && state.data.liveAgent && state.data.liveEngine
    ? await api(`/agents/${encodeURIComponent(state.data.liveAgent)}/configs/${encodeURIComponent(state.data.liveEngine)}/workspace`)
    : null;
  const agents = targetedWorkspace ? [targetedWorkspace.agent].filter(Boolean) :
    state.session && !can("agents.read") ? [] : refreshRuntime ? await api("/agents") : state.data.agents;
  if (accountData !== state.data || request !== liveConfigRequest || state.route !== "live-config") return;
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
  if (globalThis.location?.hash.startsWith("#live-config") && globalThis.history?.replaceState)
    globalThis.history.replaceState(null, "", presetRoute({agentId:agent.id, engine}));
  await reconcilePendingDeploy(agent.id, engine);
  const prefetched = prefetchWorkspace && selectedAgent === agent.id && selectedEngine === engine
    ? await prefetchWorkspace : null;
  if (prefetched?.error) throw prefetched.error;
  const configWorkspace = targetedWorkspace || prefetched?.value || await api(
    `/agents/${encodeURIComponent(agent.id)}/configs/${encodeURIComponent(engine)}/workspace`,
  );
  if (accountData !== state.data || request !== liveConfigRequest || state.route !== "live-config") return;
  agent = configWorkspace.agent || agent;
  state.data.agents = agents.map(item => item.id === agent.id ? agent : item);
  const privateWorkspace = privateAccount && (agent.can_manage !== true ||
    !["managed", "import"].includes(state.data.liveConfigSource));
  const saved = configWorkspace.config || null;
  const runtime = agent.runtime?.[engine] || {};
  const unsupportedReason = String(
    runtime.existing_config_unsupported_reason || "",
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
    : state.data.liveSources[sourceKey] || (emptyManaged ? {
      content: saved?.content || (engine === "mihomo" ? "listeners: []\nrules:\n  - MATCH,DIRECT\n" : "{}\n"),
      saved: true,
    } : null);
  const beforeDeploy = !privateWorkspace && sourceMode === "managed" && managedAvailable
    ? async () => {
        if (!readAction) {
          throw deployPreflightError(
            "当前 Agent 版本无法在部署前独立读取 QAgent 托管配置，已停止保存和部署。请先升级 Agent。",
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
            `部署前无法核验 Agent 当前配置，已停止保存和部署：${diagnosticError(error.message)}`,
          );
        }
        if (!fresh || !isCurrent()) {
          throw deployPreflightError(
            "部署前页面状态已变化，已停止保存和部署。当前草稿已保留，请重新确认节点与内核。",
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
  const language = engine === "mihomo" ? "YAML" : "JSON";

  const editorState = liveConfigEditorState({
    existingAvailable: importSource,
    canOperate: can("agent-config.write"),
    sourceContent: source?.content,
    formContent: current?.content,
  });
  const canExecute = can("tasks.execute") && !unsupportedReason && (importSource || managedAvailable);
  const liveActions = !can("agent-config.write")
    ? ""
    : (privateWorkspace ? '<button class="button" type="submit" data-live-intent="save">保存个人配置</button>' : "") +
      (!canExecute ? "" : importSource
        ? '<button class="button primary" type="submit" data-live-intent="import">手动导入并迁移</button>'
        : '<button class="button" type="submit" data-live-intent="validate">保存并校验</button>' +
        '<button class="button primary" type="submit" data-live-intent="deploy">保存并部署</button>');
  let sourceSwitch = existingAvailable
    ? `<nav class="live-config-source-switch" aria-label="配置来源">${managedAvailable ? `<button class="${sourceMode === "managed" ? "active" : ""}" type="button" data-live-source="managed"><b>QAgent 配置</b><small>/etc/qagent 托管</small></button>` : ""}<button class="${sourceMode === "import" ? "active" : ""}" type="button" data-live-source="import"><b>系统服务配置</b><small>可选导入</small></button></nav>`
    : "";
  if (importSource && engine === "ss-rust") {
    sourceSwitch += '<p class="validation-note">导入 install-ss-rust：保留多端口、DNS、出站绑定及 IPv6 优先，复制出站 ACL。脚本自有入站防火墙和重应用服务不会迁移；修改端口前请单独处理。日志统一为 QAgent info。SS Rust 无离线检查模式，启动失败会回滚。</p>';
  }
  if (privateWorkspace) {
    sourceSwitch = '<p class="validation-note">仅显示我的配置。可保存多份方案；同一主机每种内核只运行一份配置，不能覆盖其他用户正在运行的配置。</p>';
  }
  if (privateAccount && agent.can_manage === true) {
    sourceSwitch = `<nav class="live-config-source-switch" aria-label="配置来源"><button type="button" data-live-source="personal" class="${privateWorkspace ? "active" : ""}"><b>我的配置</b><small>个人工作区</small></button>${managedAvailable ? `<button type="button" data-live-source="managed" class="${sourceMode === "managed" ? "active" : ""}"><b>读取自有主机配置</b><small>当前托管文件</small></button>` : ""}${existingAvailable ? `<button type="button" data-live-source="import" class="${importSource ? "active" : ""}"><b>系统服务配置</b><small>可选导入</small></button>` : ""}</nav>` +
      (privateWorkspace ? sourceSwitch : '<p class="validation-note">只允许读取自有主机且未被其他账号占用的配置；共享给他人不授予读取其配置的权限。</p>');
  }
  const liveConfigPhase = current
    ? "ready"
    : source?.error
      ? "error"
      : agent.status !== "online"
        ? "offline"
        : unsupportedReason
          ? "unsupported"
          : !importSource && !readAction
            ? "upgrade"
          : "loading";
  const engineBar = `<nav class="live-engine-bar" aria-label="选择内核">${installedEngines.map(item => {
    const info = agent.runtime?.[item] || {};
    const active = item === engine;
    const label = info.installed ? "已安装" : info.existing_config_available ? "待导入" : "未安装";
    return `<button type="button" class="live-engine-tab ${active ? "active" : ""}" data-live-engine="${esc(item)}" aria-pressed="${active}" aria-label="${esc(engineName(item))} · ${label}" title="${label}" ${active ? 'aria-current="true"' : ""}><span>${esc(engineName(item))}</span><small class="live-engine-status${info.installed ? " installed" : ""}">${info.installed ? "已安装" : "未安装"}</small></button>`;
  }).join("")}</nav>`;
  shell(
    `<article class="live-config-workspace" data-refresh-key="live-config-content-${esc(agent.id)}-${esc(engine)}-${esc(sourceMode)}-${esc(liveConfigPhase)}" data-live-config-phase="${esc(liveConfigPhase)}"><header class="editor-toolbar"><div><p class="live-config-eyebrow">${privateWorkspace ? "我的节点配置" : "节点配置工作区"}</p><h2>${esc(agent.name)}</h2>${sourceSwitch}</div><div class="editor-toolbar-state"><span class="engine-badge ${esc(engine)}">${esc(engineName(engine))}</span><b>${unsupportedReason ? "不可自动迁移" : importSource ? "可导入" : saved?.version ? `v${saved.version}` : "未保存"}</b></div></header>${engineBar}<div class="live-config-details"><span><i class="status-dot ${agent.status === "online" ? "ok" : ""}"></i>${agent.status === "online" ? "节点在线" : "节点离线"}</span><span>${esc(agent.os)} / ${esc(agent.arch)}</span><span>${esc(engineName(engine))} · ${esc(conciseVersion(engine, runtime.version))}</span><span>${privateWorkspace ? "个人配置 · 可保存并部署到此主机" : importSource ? "系统服务 · 只读快照" : "QAgent 托管 · 编辑后需保存部署"}</span></div>${current ? `<form class="live-config-editor" id="live-config-form" data-profile-editor data-new-config="0" data-engine="${esc(engine)}"><section class="code-workspace" data-code-editor data-code-language="${language}" data-code-max-bytes="2097152"><header class="code-editor-toolbar"><div class="code-file-meta"><span class="code-file-icon" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M7 3.5h7l4 4V20.5H7zM14 3.5v4h4M10 12h5M10 16h3"/></svg></span><b>${engine === "mihomo" ? "config.yaml" : "config.json"}</b></div><div class="code-editor-meta"><span class="code-language">${language}</span><span data-code-status aria-live="polite">${importSource ? "系统服务只读快照" : "QAgent 配置"}</span><span data-code-bytes>—</span><span data-code-position>行 1，列 1</span></div></header><div class="code-editor-frame"><aside class="code-gutter" aria-hidden="true" data-line-numbers>1</aside><textarea class="code-editor-input" name="content" data-code-input aria-label="${esc(engineName(engine))} 节点配置源码" spellcheck="false" required ${editorState.readOnly ? "readonly" : ""}>${esc(current.content)}</textarea></div><footer><span><i class="code-status-dot" data-code-status-dot></i><span data-code-validation aria-live="polite"></span></span><div><button class="button code-reset" type="button" data-code-reset disabled>恢复原文</button>${can("agent-config.write") && !editorState.readOnly ? '<button class="button code-format" type="button" data-code-format>格式化配置</button>' : ""}${liveActions}</div></footer></section><input type="hidden" name="name" value="${esc(current.name)}"><input type="hidden" name="description" value="${esc(current.description)}"><input type="hidden" name="version" value="${current.version}"></form>` : agent.status !== "online" ? '<section class="node-config-source"><h2>节点离线</h2><span class="status-label warn">无法读取</span></section>' : unsupportedReason ? `<section class="node-config-source" role="status"><h2>检测到现有服务，但不可自动迁移</h2><span class="status-label bad">${esc(unsupportedReason)}</span><p>QAgent 未执行或接管该服务。所有相关内核任务均已禁用；请按提示调整为受支持的精确布局并重启 Agent 重新发现。</p></section>` : !importSource && !readAction ? '<section class="node-config-source"><h2>需要升级 Agent</h2><span class="status-label warn">暂不可读取 QAgent 配置</span><p>升级后即可在不影响系统服务可选导入的情况下独立读取 QAgent 托管配置。</p></section>' : source?.error ? `<section class="node-config-source"><h2>读取配置失败</h2><span class="status-label bad">${esc(diagnosticError(source.error))}</span><button class="button" type="button" data-read-current>重新读取</button></section>` : `<section class="node-config-source" role="status" aria-live="polite"><h2>正在读取${importSource ? "系统服务配置" : "QAgent 配置"}</h2><span class="status-label warn">读取中</span><form data-auto-read-current hidden></form></section>`}</article>`,
    "配置",
    { viewKey: `live-config-${agent.id}-${engine}` },
  );
  const workspaceElement = document.querySelector(".live-config-workspace");
  if (emptyManaged) {
    const hint = document.createElement("p");
    hint.className = "config-install-hint";
    hint.textContent = `${engineName(engine)} 尚未安装。通过源码工具栏的“＋ 增加入站”提交时，将自动安装最新稳定版；切换版本请到节点设置。`;
    workspaceElement.querySelector(".live-config-details").after(hint);
  } else if (source?.cached) {
    const hint = document.createElement("p");
    hint.className = "config-install-hint";
    hint.textContent = "当前显示最近 600 秒内已校验的节点快照；手动刷新及部署前核验会跳过缓存。";
    workspaceElement.querySelector(".live-config-details").after(hint);
  } else if (source?.saved) {
    const hint = document.createElement("p");
    hint.className = "config-install-hint";
    hint.textContent = "当前显示已保存配置；需部署成功后才会在节点生效。";
    workspaceElement.querySelector(".live-config-details").after(hint);
  }
  state.data.liveEngines = installedEngines;
  const confirmSwitch = async (title) => {
    if (document.querySelector('#live-config-form[data-saving="1"]')) {
      notify("配置正在保存，请等待提交结果后再切换。", "error");
      return false;
    }
    const editor = document.querySelector("#live-config-form [data-code-editor]");
    const input = editor?.querySelector("[data-code-input]");
    const dirty = editor?.configFileController?.dirty() ?? (input && !input.readOnly && input.value !== current?.content);
    return !dirty || await confirmAction("当前配置有未保存的修改，切换后将丢弃这些修改。确定切换？", title);
  };
  document.querySelectorAll("[data-live-agent]").forEach(
    (link) =>
      (link.onclick = async (event) => {
        event.preventDefault();
        if (link.dataset.liveAgent === agent.id || !(await confirmSwitch("切换节点"))) return;
        state.data.liveAgent = link.dataset.liveAgent;
        state.data.liveEngine = "";
        state.data.liveConfigSource = "";
        liveConfig();
      }),
  );
  let engineSwitch = 0;
  const frozenForSwitch = new Map();
  document.querySelectorAll("[data-live-engine]").forEach(
    (link) =>
      (link.onclick = async (event) => {
        event.preventDefault();
        if (link.dataset.liveEngine === state.data.liveEngine) return;
        if (!(await confirmSwitch("切换内核"))) return;
        if (accountData !== state.data || !workspaceElement.isConnected || link.dataset.liveEngine === state.data.liveEngine) return;
        const switchRequest = ++engineSwitch;
        const previousSource = sourceMode;
        state.data.liveEngine = link.dataset.liveEngine;
        state.data.liveConfigSource = "";
        workspaceElement.setAttribute("aria-busy", "true");
        workspaceElement.querySelectorAll(".live-config-editor, .config-workspace-tools, .config-empty-actions, .live-config-source-switch").forEach(element => {
          if (!frozenForSwitch.has(element)) frozenForSwitch.set(element, element.inert);
          element.inert = true;
        });
        const tabs = [...workspaceElement.querySelectorAll("[data-live-engine]")];
        tabs.forEach(tab => {
          tab.classList.toggle("active", tab === link);
          tab.setAttribute("aria-pressed", String(tab === link));
        });
        workspaceElement.querySelector(".live-engine-loading")?.remove();
        const status = document.createElement("span");
        status.className = "live-engine-loading";
        status.setAttribute("role", "status");
        status.textContent = `正在切换到 ${engineName(link.dataset.liveEngine)}…`;
        workspaceElement.querySelector(".live-config-details").append(status);
        try { await liveConfig(); }
        catch (error) {
          if (accountData !== state.data || engineSwitch !== switchRequest || state.data.liveEngine !== link.dataset.liveEngine) return;
          state.data.liveEngine = engine;
          state.data.liveConfigSource = previousSource;
          if (globalThis.history?.replaceState) globalThis.history.replaceState(null, "", presetRoute({agentId:agent.id, engine}));
          tabs.forEach(tab => {
            const active = tab.dataset.liveEngine === engine;
            tab.classList.toggle("active", active);
            tab.setAttribute("aria-pressed", String(active));
          });
          notify(`切换内核失败：${error.message}`, "error");
        } finally {
          if (workspaceElement.isConnected && engineSwitch === switchRequest) {
            workspaceElement.removeAttribute("aria-busy"); status.remove();
            frozenForSwitch.forEach((inert, element) => { element.inert = inert; });
            frozenForSwitch.clear();
            tabs.forEach(tab => { tab.disabled = false; });
          }
        }
      }),
  );
  document.querySelectorAll("[data-live-source]").forEach(
    (button) =>
      (button.onclick = async () => {
        if (button.dataset.liveSource === sourceMode) return;
        if (!(await confirmSwitch("切换配置来源"))) return;
        if (accountData !== state.data) return;
        state.data.liveConfigSource = button.dataset.liveSource;
        liveConfig();
      }),
  );
  bindEvent(document.querySelector("[data-read-current]"), "click", async () => {
      delete state.data.liveSources[sourceKey];
      await readCurrentConfig(agent, engine, sourceKey, readAction);
  });
  const configFiles = bindConfigFiles(document.querySelector("#live-config-form"), engine, notify);
  bindCodeEditors();
  const applyInboundMutation = async (result, chosen) => {
    if (accountData !== state.data) return;
    const key = `${agent.id}|${engine}`;
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
        message:`配置 v${result.config.version} 已保存，任务已提交。`,
      });
      watchPresetOperation(operation);
      if (result.task.action === "deploy") {
        recordPendingDeploy(result.task.id, agent.id, engine);
        void monitorDeployTask(result.task.id, agent.id, engine);
      }
    }
    state.data.liveSelectedInbound = chosen ? {agentId:agent.id, engine, ...chosen} : null;
    if (state.route === "live-config" && state.data.liveAgent === agent.id && state.data.liveEngine === engine) {
      try { await liveConfig(); }
      catch (error) {
        if (accountData !== state.data) return;
        // The mutation is already durable. Do not report it as a failed save
        // or silently lose the error after the embedded editor is disposed.
        const message = `配置 v${result.config.version} 已保存且任务已提交，页面刷新失败：${error.message}。请重新加载后继续编辑。`;
        const operation = operationFor(key);
        if (operation?.task?.id === result.task?.id) Object.assign(operation, {message, tone:"error", reload:true});
        renderPresetStatus();
        notify(message, "error");
      }
    }
  };
  const restrictionSelection = await bindConfigRestrictions({
    ...ctx, form: document.querySelector("#live-config-form"), files: configFiles,
    agent, engine, saved, sourceMode, inbounds:configWorkspace.inbound_targets || configWorkspace.inbounds || [],
    beforeDeploy, onSaved: applyInboundMutation,
  });
  if (accountData !== state.data || request !== liveConfigRequest || state.route !== "live-config") return;
  const selected = state.data.liveSelectedInbound;
  if (selected?.agentId === agent.id && selected.engine === engine)
    (configFiles || restrictionSelection)?.selectInbound(selected.tag, selected.port);
  bindConfigInbounds({
    ...ctx, form:document.querySelector("#live-config-form"), container:workspaceElement, files:configFiles,
    selection:restrictionSelection, agent, engine, saved, workspace:configWorkspace, sourceMode,
    sourceContent:current?.content, mountEditor:mountPresetEditor, beforeDeploy, onSaved:applyInboundMutation,
    onRefresh:async () => {
      liveAgentRuntimeLoaded = false;
      delete state.data.liveSources[sourceKey];
      await liveConfig({ preferCachedRead: false });
    },
  });
  renderPresetStatus();
  let liveSaving = false, liveSaveUncertain = false;
  bindEvent(document.querySelector("#live-config-form"), "submit", async (event) => {
      event.preventDefault();
      const formElement = event.currentTarget;
      if (accountData !== state.data || liveSaving || liveSaveUncertain || !formElement.isConnected ||
          state.data.liveAgent !== agent.id || state.data.liveEngine !== engine) return;
      const form = new FormData(formElement);
      const intent = event.submitter?.dataset.liveIntent || (privateWorkspace ? "save" : "validate");
      if (!["save", "import", "deploy", "validate"].includes(intent)) return;
      const controls = [...(workspaceElement || formElement).querySelectorAll("button, input, select, textarea")]
        .map(element => [element, element.disabled]);
      const submitter = event.submitter, label = submitter?.textContent;
      let submitted = false, persisted;
      liveSaving = true;
      formElement.dataset.saving = "1";
      formElement.setAttribute("aria-busy", "true");
      controls.forEach(([element]) => { element.disabled = true; });
      formElement.querySelector("[data-live-save-status]")?.remove();
      const status = document.createElement("span");
      status.dataset.liveSaveStatus = "";
      status.setAttribute("role", "status");
      status.textContent = "正在检查配置…";
      formElement.querySelector(".code-workspace>footer").prepend(status);
      try {
        if (configFiles) form.set("content", configFiles.content());
        if (
          intent === "import" &&
          !(await confirmAction(
            "确定导入当前快照并迁移服务？Agent 将停止并禁用原服务，启动 QAgent 专用服务；迁移任一步失败都会自动恢复原服务。",
            "手动导入并迁移",
          ))
        )
          return;
        if (
          intent === "deploy" &&
          !(await confirmAction(
            "确定保存当前源码、替换此主机该内核的当前配置并重启服务？同一内核只运行一份配置，可能影响其他用户的已部署服务。",
            "保存并部署",
          ))
        )
          return;
        if (accountData !== state.data || !formElement.isConnected) return;
        submitted = true;
        if (submitter) submitter.textContent = "正在提交…";
        status.textContent = "正在保存配置并提交任务…";
        const result = await submitLiveConfigChange({
          api,
          submitTask,
          agent,
          engine,
          intent,
          form,
          source,
          existingAvailable: importSource,
          savedConfig: saved,
          beforeDeploy: beforeDeploy ? async () => {
            status.textContent = "正在核验 Agent 当前配置…";
            if (submitter) submitter.textContent = "正在核验…";
            await beforeDeploy();
            status.textContent = "正在保存配置并提交任务…";
            if (submitter) submitter.textContent = "正在提交…";
          } : null,
          onSavedConfig: value => { persisted = value; },
          onDeployTask: (taskId) => {
            recordPendingDeploy(taskId, agent.id, engine);
            monitorDeployTask(taskId, agent.id, engine);
          },
        });
        if (accountData !== state.data) return;
        if (intent === "save") notify("个人配置已保存");
        if (intent === "import") {
          notify("配置已保存，服务迁移任务已提交");
        }
        state.data.liveSources[sourceKey] = {
          ...source,
          content: result.content,
          saved: !importSource,
          cached: false,
        };
        if (state.route === "live-config" && state.data.liveAgent === agent.id && state.data.liveEngine === engine && formElement.isConnected)
          await liveConfig();
      } catch (error) {
        if (accountData !== state.data || !formElement.isConnected) return;
        liveSaveUncertain = !error.deployPreflight && submitted &&
          Boolean(persisted || !error.status || error.status === 409 || error.status >= 500);
        const message = `${persisted ? `配置 v${persisted.version} 已保存，后续任务或页面刷新未完成：` : ""}${diagnosticError(error.message)}${liveSaveUncertain ? " 当前内容已保留，请重新读取并核对结果后再提交。" : ""}`;
        status.textContent = message;
        status.setAttribute("role", "alert");
        notify(message, "error");
      } finally {
        liveSaving = false;
        delete formElement.dataset.saving;
        formElement.removeAttribute("aria-busy");
        controls.forEach(([element, disabled]) => { element.disabled = disabled || liveSaveUncertain && element.matches("[data-live-intent]"); });
        if (submitter) submitter.textContent = label;
        if (status.getAttribute("role") !== "alert") status.remove();
      }
  });
  if (
    !current &&
    !unsupportedReason &&
    agent.status === "online" &&
    readAction &&
    !source?.error
  )
    void readCurrentConfig(agent, engine, sourceKey, readAction, preferCachedRead);
}

async function requestCurrentConfigSnapshot(
  agent,
  engine,
  readAction,
  { preferCached = false, isCurrent = () => true, onTask = () => {} } = {},
) {
  if (!isCurrent()) return null;
  const createReadTask = (allowCached) => api("/tasks", {
    method: "POST",
    body: JSON.stringify({
      agent_id: agent.id,
      engine,
      action: readAction,
      ...(allowCached ? { prefer_cached: true } : {}),
    }),
  });
  const finishReadTask = async (task) => {
    if (!isCurrent()) return null;
    onTask(task.id);
    const finished = ["succeeded", "failed", "canceled"].includes(task.status)
      ? task
      : await waitForTask(task.id, isCurrent);
    if (!finished || !isCurrent()) return null;
    if (finished.status !== "succeeded")
      throw new Error(finished.error || "节点未能读取当前配置");
    return finished;
  };
  let task = await createReadTask(preferCached);
  let cacheHit = Boolean(preferCached && task.reused && task.status === "succeeded");
  let finished = await finishReadTask(task);
  if (!finished || !isCurrent()) return null;
  let snapshot;
  try {
    snapshot = await api(`/tasks/${encodeURIComponent(finished.id)}/config-snapshot`);
  } catch (error) {
    // Another tab or a mutation can retire any read snapshot before its GET,
    // including a fresh preflight result. Retry once without the cache, but
    // never create work for an abandoned page or a different account.
    if (!isCurrent()) return null;
    if (error?.status !== 404) throw error;
    task = await createReadTask(false);
    cacheHit = false;
    finished = await finishReadTask(task);
    if (!finished || !isCurrent()) return null;
    snapshot = await api(`/tasks/${encodeURIComponent(finished.id)}/config-snapshot`);
  }
  if (!isCurrent()) return null;
  if (!snapshot.content)
    throw new Error("节点返回的配置快照已失效，请重新读取");
  return {
    content: snapshot.content,
    taskId: finished.id,
    cached: cacheHit,
    readAt: Number.isFinite(Date.parse(finished.finished_at || ""))
      ? Date.parse(finished.finished_at)
      : Date.now(),
  };
}

async function readCurrentConfig(agent, engine, sourceKey, readAction, preferCached = false) {
  if (state.data.liveSources?.[sourceKey]?.reading) return;
  const request = ++liveReadRequest;
  const data = state.data;
  const epoch = state.navigationEpoch, sourceMode = data.liveConfigSource;
  const reading = { reading: true };
  const isCurrent = () =>
    data === state.data &&
    epoch === state.navigationEpoch &&
    request === liveReadRequest &&
    state.route === "live-config" &&
    state.data.liveAgent === agent.id &&
    state.data.liveEngine === engine &&
    state.data.liveConfigSource === sourceMode;
  const discardReading = () => {
    if (data.liveSources?.[sourceKey] === reading)
      delete data.liveSources[sourceKey];
  };
  state.data.staleReadTasks ||= {};
  const staleTaskId = state.data.staleReadTasks[sourceKey];
  if (staleTaskId) {
    delete state.data.staleReadTasks[sourceKey];
    try {
      for (let attempt = 0; attempt < 600; attempt += 1) {
        if (!isCurrent()) return;
        const staleTask = await api(`/tasks/${encodeURIComponent(staleTaskId)}`);
        if (!isCurrent()) return;
        if (["succeeded", "failed", "canceled"].includes(staleTask.status)) break;
        await new Promise((resolve) => setTimeout(resolve, 600));
      }
    } catch { /* best effort drain */ }
    if (!isCurrent()) return;
  }
  state.data.liveSources ||= {};
  state.data.liveSources[sourceKey] = reading;
  try {
    const snapshot = await requestCurrentConfigSnapshot(agent, engine, readAction, {
      preferCached,
      isCurrent,
      onTask: taskId => {
        if (isCurrent() && data.liveSources?.[sourceKey] === reading)
          reading.pendingTaskId = taskId;
      },
    });
    if (!snapshot || !isCurrent()) return discardReading();
    state.data.liveSources[sourceKey] = {
      content: snapshot.content,
      // Keep the Agent bytes separate from later saved-but-not-deployed edits.
      // Deployment preflight compares against this immutable editor baseline.
      agentContent: snapshot.content,
      taskId: snapshot.taskId,
      readAt: snapshot.readAt,
      reading: false,
      cached: snapshot.cached,
    };
  } catch (error) {
    if (error?.name === "AbortError" || !isCurrent())
      return discardReading();
    state.data.liveSources[sourceKey] = {
      error: error.message,
      reading: false,
    };
  }
  if (
    state.route === "live-config" &&
    state.data.liveAgent === agent.id &&
    state.data.liveEngine === engine
  )
    await liveConfig();
}

async function waitForTask(taskID, isCurrent = () => true) {
  for (let attempt = 0; attempt < 200; attempt += 1) {
    if (!isCurrent()) return null;
    const task = await api(`/tasks/${encodeURIComponent(taskID)}`);
    if (!isCurrent()) return null;
    if (["succeeded", "failed", "canceled"].includes(task.status)) return task;
    await new Promise((resolve) => setTimeout(resolve, attempt < 5 ? 200 : 600));
  }
  throw new Error("等待节点返回配置超时");
}

  return { agentConfig, liveConfig, archiveConfigs, capturePresetDrafts, presetHasUnsavedChanges, configHasUnsavedChanges };
}
