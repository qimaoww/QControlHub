import { submitConfigImport } from "../modules/config-import.js";
import { loadingForm, loadingTable, readRegion, dialogContext, preparedDialog } from "./loading.js";
import { prepareConfigDeployment, configDeployBlockReason } from "../modules/core-install-state.js";
import { recoverConfigInstance } from "./config-status-recovery.js";
import { orderNodesBySavedOrder, savedNodeOrder } from "../modules/node-order.js";
import { requestConfigSnapshot } from "../modules/config-snapshot.js";
import { createConfigMutations } from "../modules/config-mutations.js";
import { liveConfigReadAction, submitLiveConfigChange } from "../modules/live-config-state.js";
import { readPresetRoute } from "../modules/preset-route.js";
import { diagnosticError } from "../modules/errors.js";
import { configStudio } from "./config-studio.js";
import { nodeName } from "./node-region.js";
import { formatConfigContent } from "../modules/code-format.js";
import { formatSharedPorts } from "../modules/user-model.js";
import { filterbar, node, button, actions, stack, section, table, badge, field, pathID, list, muted, notice, formData, empty, code, link } from "./ui.js";
import { date, status, taskDetail } from "./operations.js";

const engineIDs = ["mihomo", "xray", "sing-box", "ss-rust"];
const terminal = new Set(["succeeded", "failed", "canceled"]);
const defaultSource = engine => engine === "mihomo" ? "listeners: []\nrules:\n  - MATCH,DIRECT\n" : "{}\n";

async function waitTask(ctx, id) {
  for (let attempt = 0; attempt < 180; attempt++) {
    const task = await ctx.api(`/tasks/${pathID(id)}?view=status`);
    if (terminal.has(task.status)) return task;
    await new Promise((resolve, reject) => {
      const abort = () => { clearTimeout(timer); reject(new DOMException("已取消", "AbortError")); };
      const timer = setTimeout(() => { ctx.signal.removeEventListener("abort", abort); resolve(); }, attempt < 5 ? 250 : 600);
      if (ctx.signal.aborted) abort();
      else ctx.signal.addEventListener("abort", abort, { once: true });
    });
  }
  throw new Error("等待节点结果超时。请到任务页核对任务状态。");
}

export async function liveConfigPage(ctx) {
  const request = ctx.api, recordFailure = ctx.recordFailure;
  let recoverPortConflict;
  const mutations = createConfigMutations({state:ctx.state, api:(path, options={})=>request(path, {
    ...options, body:options.body === undefined ? undefined : JSON.parse(options.body),
  })}, {onTask:()=>{}, waitForTerminal:id=>waitTask(ctx,id), recoverPortConflict:error=>recoverPortConflict?.(error)});
  ctx = {...ctx, api:(path, options={})=>mutations.request(path, {
    ...options, body:options.body === undefined ? undefined : JSON.stringify(options.body),
  })};
  const selection = readPresetRoute(location.hash) || {};
  const params = new URLSearchParams(location.hash.split("?")[1]);
  const requested = selection.agentId || location.hash.match(/^#preset-node-(.+)$/)?.[1] || "";
  const selectedEngine = selection.engine || "";
  const targetField = field('节点', 'agent', '', { options: [['', '正在读取节点…']], disabled: true });
  const engineField = field('内核', 'engine', '', { options: [['', '正在读取内核…']], disabled: true });
  const modeField = field('配置来源', 'source', '', { options: [['', '正在读取配置来源…']], disabled: true });
  const sourceField = field('系统服务', 'source_id', '', { options: [['', '请选择服务']], disabled: true });
  sourceField.hidden = true;
  let selectedAgentID = '', selectedEngineID = '', selectedMode = '', selectedSourceID = '';
  const select = node('form', { className: 'a-target-form', onsubmit(event) {
    event.preventDefault();
    const values = formData(event.currentTarget);
    if (values.agent !== selectedAgentID) { delete values.engine; delete values.source; delete values.source_id; }
    else if (values.engine !== selectedEngineID) { delete values.source; delete values.source_id; }
    else if (values.source !== 'import') delete values.source_id;
    location.hash = `#live-config?${new URLSearchParams(values)}`;
    targetField.querySelector('select').value = selectedAgentID;
    engineField.querySelector('select').value = selectedEngineID;
    modeField.querySelector('select').value = selectedMode;
    sourceField.querySelector('select').value = selectedSourceID;
  } }, targetField, engineField, modeField);
  select.addEventListener('change', () => select.requestSubmit());
  const flagsHost = actions();
  const studioHost = stack(section('入站配置', loadingTable(['端口', '监听', '协议', '状态', '操作'], '入站配置')),
    section('出站配置', loadingTable(['名称', '类型', '目标状态', '操作'], '出站配置')));
  const sourceBody = node('div', { className: 'a-card-body' }, loadingForm(['名称', '说明', '配置正文']));
  const sourceActions = actions(button('保存历史', () => {}, { disabled: true }));
  const page = stack(filterbar(select, flagsHost), studioHost, section('配置源码', sourceBody, sourceActions));
  ctx.mount?.(page);
  const setOptions = (root, options, value, disabled = false) => {
    const input = root.querySelector('select'); input.replaceChildren(input.querySelector('button'), ...options.map(([id, label]) => node('option', { value: id }, label))); input.value = value; input.disabled = disabled;
  };
  const initialRead = readRegion(ctx, '节点配置', initialize, () => ctx.restoreTargetFocus?.(), {view:studioHost});
  page.prepend(initialRead.element);
  ctx.onRefresh(initialRead.load);
  void initialRead.load();
  return page;
  async function initialize() {
  const preferenceRead = ctx.preferences?.ready();
  let agents = ctx.can('agents.read') ? orderNodesBySavedOrder(list(await ctx.api('/agents')), savedNodeOrder()) : [];
  await preferenceRead;
  if (!ctx.current()) return page;
  const remembered = ctx.preferences?.get('live-config').agent || '';
  const preferred = requested || remembered;
  const orderRead = readRegion(ctx, '节点排序', signal => ctx.api('/preferences/node-order', { signal }), value => {
    if (!agents.length || !selectedAgentID) return;
    agents = orderNodesBySavedOrder(agents, value.ids);
    const input = targetField.querySelector('select');
    if (input.matches(':open')) return;
    setOptions(targetField, agents.filter(agent => agent.capabilities?.length).map(item => [item.id, nodeName(ctx, item)]), selectedAgentID);
  });
  page.prepend(orderRead.element); void orderRead.load();
  let workspace;
  let workspaceEngine = selectedEngine;
  if (!ctx.can('agents.read') && preferred) {
    for (const candidate of engineIDs.includes(selectedEngine) ? [selectedEngine] : engineIDs) {
      try {
        workspace = await ctx.api(`/agents/${pathID(preferred)}/configs/${candidate}/workspace`);
        if (!ctx.current()) return page;
        agents = [workspace.agent].filter(Boolean); workspaceEngine = candidate; break;
      } catch (error) {
        if (requested || ![403, 404].includes(error.status)) throw error;
      }
    }
  }
  if (!ctx.can('agents.read') && !requested && preferred && !workspace) ctx.preferences?.patch('live-config', { agent: '' });
  const available = agents.filter(agent => agent.capabilities?.length);
  if (requested && !available.some(agent => agent.id === requested))
    { studioHost.replaceChildren(notice('此节点不可用，或当前账号无权访问。')); sourceBody.replaceChildren(); return page; }
  const target = available.find(agent => agent.id === preferred) || available.find(agent => agent.status === "online" && agent.capabilities.some(id => agent.runtime?.[id]?.installed)) || available.find(agent => agent.status === "online") || available[0];
  if (!target) { if (remembered && !requested && ctx.can('agents.read')) ctx.preferences?.patch('live-config', { agent: '' }); studioHost.replaceChildren(section('节点配置', empty('没有可配置的节点'))); sourceBody.replaceChildren(); return page; }
  const engines = target.capabilities;
  const engine = engines.includes(workspaceEngine) ? workspaceEngine : engines.find(id => target.runtime?.[id]?.installed) || engines[0];
  const base = `/agents/${pathID(target.id)}/configs/${pathID(engine)}`;
  if (ctx.can("tasks.read")) void mutations.recover(target.id, engine, target.shared_allocation?.id || "");
  selectedAgentID = target.id; selectedEngineID = engine;
  setOptions(targetField, available.map(item => [item.id, nodeName(ctx, item)]), target.id);
  setOptions(engineField, engines.map(id => [id, id]), engine);
  workspace ||= await ctx.api(`${base}/workspace`);
  if (!ctx.current()) return page;
  // Reaching this route means unsaved-change navigation was accepted. A
  // cancelled switch never gets here and cannot update the account choice.
  if (requested || remembered && remembered !== target.id) ctx.preferences?.patch('live-config', { agent: target.id });
  const agent = workspace.agent || target;
  const record = recordFailure;
  if (record) ctx = {...ctx,recordFailure:(error,operation,stage) => record(Object.assign(error,{agentID:agent.id,engine,resource:`/agents/${agent.id}/configs/${engine}`}),operation,stage)};
  const runtime = agent.runtime?.[engine] || {};
  let saved = workspace.config || null;
  let source = null;
  const editable = ctx.can("agent-config.write");
  const execute = ctx.can("tasks.execute") && agent.status === "online";
  const canReadHost = ctx.can("host.manage", agent) && execute && ctx.can("tasks.read");
  const canReadCurrent = canReadHost || Boolean(agent.shared_allocation?.enabled && execute && ctx.can("tasks.read") && ["managed-config-read-v1","shared-core-instances-v1","shared-core-lifecycle-v1"].every(feature=>agent.features?.includes(feature)));
  const canImport = Boolean(runtime.existing_config_available && canReadHost);
  const hostManaged = Boolean(runtime.installed && (agent.can_manage !== false || agent.shared_allocation?.enabled));
  const mode = params.get("source") === "import" && canReadHost ? "import" : params.get("source") === "managed" && hostManaged && canReadCurrent ? "managed" : "personal";
  const importSource = mode === "import";
  const sources = canReadHost ? runtime.existing_sources || [] : [];
  const selectedSource = sources.find(source => source.id === params.get('source_id')) || (sources.length === 1 ? sources[0] : null);
  selectedSourceID = selectedSource?.id || '';
  sourceField.hidden = !importSource || !sources.length;
  if (sourceField.hidden) sourceField.remove();
  else select.append(sourceField);
  const serviceStates = { active: '运行中', inactive: '已停止', unknown: '未知', failed: '失败' };
  setOptions(sourceField, [['', '请选择服务'], ...sources.map(source => [source.id, `${source.service} · ${serviceStates[source.status] || '未知'}`])], selectedSourceID, !importSource);

  const readAction = liveConfigReadAction({
    sourceMode: mode, managedReadSupported: agent.features?.includes("managed-config-read-v1"),
    existingAvailable: Boolean(runtime.existing_config_available),
  });
  const flags = actions(status(agent.status), badge(agent.shared_allocation ? "共享实例" : "主实例"),
    agent.shared_allocation && muted(`本人端口 · ${formatSharedPorts(agent.shared_allocation.ports) || "未分配"}`),
    agent.shared_allocation && ctx.can("agents.read") && link("实例管理", `#settings-node-${pathID(agent.id)}`));
  selectedMode = mode;
  setOptions(modeField, [['personal', '面板保存的配置'], ...(hostManaged && canReadCurrent ? [['managed', 'QAgent 托管配置']] : []), ...(canReadHost ? [['import', '导入系统配置']] : [])], mode);
  flagsHost.replaceChildren(flags);
  const liveNotice = node("p", { className: "a-save-state", role: "status" }, saved ? `已保存 · ${date(saved.updated_at)}` : "尚未保存");
  const errorText = runtime.existing_config_unsupported_reason;
  if(importSource && errorText)void ctx.recordFailure?.(new Error(errorText),`${agent.name} (${agent.id}) · ${engine} 系统配置导入`,"import_discovery");
  const api = (path, options) => ctx.api(path, { method: options?.method, body: options?.body ? JSON.parse(options.body) : undefined });
  const beforeDeploy = () => {
    if (!ctx.current()) throw Object.assign(new Error("页面目标已变化，请重新打开配置后提交。"), { deployPreflight: true });
  };
  const snapshot = async (preferCached = false) => {
    if (importSource && !canImport) throw new Error(errorText || "当前未发现可导入的系统服务配置。");
    if (!readAction || !canReadCurrent) throw new Error("当前节点或账号无法读取托管配置。");
    if (importSource && sources.length > 1 && !selectedSourceID) throw new Error("请选择要读取的系统服务。");
    const result = await requestConfigSnapshot({ api, waitForTask: id => waitTask(ctx, id) }, agent, engine, readAction, {
      preferCached, isCurrent: ctx.current, existingSourceID: importSource ? selectedSourceID : "",
    });
    if (!result || !ctx.current()) throw new DOMException("目标已切换", "AbortError");
    return result;
  };
  let canDeploy = !configDeployBlockReason(agent, engine, ctx.can);
  const intent = node("input", {type:"hidden", name:"intent", value:importSource ? "import" : "deploy"});
  const content = field(importSource ? "系统配置快照" : "配置正文", "content", importSource ? "" : workspace.source_content ?? saved?.content ?? defaultSource(engine), {
    multiline: 20, required: true, readonly: importSource || !editable, spellcheck: false,
  });
  const dependencyPreview = node("details", {}, node("summary", {}, "依赖资源"));
  dependencyPreview.hidden = true;
  const sourcePaths = importSource && selectedSource && table(["来源", "路径"], [
    selectedSource.config_path && ["配置文件", selectedSource.config_path],
    selectedSource.config_directory && ["配置目录", selectedSource.config_directory],
  ].filter(Boolean), {paginate: 0, label: "系统服务配置来源"});
  let sourceBlocked = importSource && !canImport;
  const recovery = stack();
  const form = ctx.form([
    node("div", { className: "a-grid" },
      field("名称", "name", saved?.name || `${agent.name} ${engine}`, { required: true, maxlength: 100, readonly: !editable }),
      field("说明", "description", saved?.description || "", { maxlength: 300, readonly: !editable }), intent),
    sourcePaths, node("div", { className: "a-editor" }, content), dependencyPreview, liveNotice, recovery,
  ], importSource ? "导入并迁移" : "保存并部署", async (values, element) => {
    if (!editable || sourceBlocked) return;
    if (!importSource && !canDeploy) throw new Error("没有配置部署权限，草稿已保留。");
    values.intent = importSource ? "import" : "deploy";
    if (values.intent === "import") {
      if (!source?.content) throw new Error("请先读取可导入的系统服务配置。");
      if (!await ctx.confirm(`导入此配置并迁移${selectedSource ? " " + selectedSource.service : "现有系统服务"}？操作成功后由 QAgent 管理对应内核。`, "导入并迁移")) return;
    }
    if (values.intent === "deploy" && !await prepareConfigDeployment(ctx, base, agent, engine)) return;
    if (!ctx.current()) return;
    let pendingTask;
    try {
      await (importSource ? submitConfigImport : submitLiveConfigChange)({
        api, agent, engine, intent: values.intent, source, existingAvailable: importSource, savedConfig: saved,
        form: new Map(Object.entries({ ...values, version: saved?.version || 0 })),
        beforeDeploy,
        async submitTask(body) {
          pendingTask = await ctx.api("/tasks", { method: "POST", body });
          return pendingTask;
        },
        onSavedConfig(config) {
          if (!ctx.current()) return;
          acceptSavedSource(config);
          inbounds.acceptConfig(config);
        },
        onDeployTask() { source = null; },
      }).then(result => { pendingTask ||= result.task; });
    } catch (error) {
      if (error.name === "AbortError") throw error;
      if (error.code === "port_conflict" && error.recoveryEngine && ctx.can("tasks.read")) {
        const feedback = notice('草稿已保留。');
        recovery.replaceChildren(feedback,actions(button('核对实例状态',event=>void ctx.mutate(event.currentTarget,async()=>{
          feedback.textContent='正在核对实例状态…';
          await recoverConfigInstance(ctx,agent,error.recoveryEngine);
          if(ctx.current())feedback.textContent='状态已核对，可修正后继续提交。草稿已保留。';
        }))));
      }
      if (!error.deployPreflight && error.code!=="port_conflict" && (!error.status || error.status===200 || error.status===408 || error.status===409 || error.status>=500)) blockSource(error);
      error.message = diagnosticError(error.message);
      throw error;
    }
    if (!ctx.current()) return;
    if (importSource) {
      ctx.announce("迁移任务已提交，完成前保留当前已保存配置");
      if (pendingTask && ctx.can("tasks.read")) await taskDetail(ctx,pendingTask.id);
      return;
    }
    ctx.setDirty(false);
    try { await inbounds.reload(); }
    catch (error) {
      if (error.name !== "AbortError" && ctx.current()) {
        blockSource(error, saved, Boolean(pendingTask));
        ctx.announce("配置已保存，部署状态读取失败，可稍后重试。", "error");
      }
    }
    if (pendingTask) {
      ctx.announce("配置已保存，任务已提交");
      if (ctx.can("tasks.read")) await taskDetail(ctx, pendingTask.id);
    }
    element.elements.content.defaultValue = element.elements.content.value;
  });
  function blockSource(error, committed, hasTask = false) {
    if (!ctx.current()) return;
    sourceBlocked = true;
    recovery.replaceChildren(notice(committed ? `配置已保存${hasTask ? "且任务已提交" : ""}，状态读取失败。` : "保存结果需重新核对，当前草稿已保留。请勿重复提交。"),
      actions(button(error.configSubmissionRejected ? "核对最新配置并保留草稿" : error.recoverConfigSubmission ? "核对保存结果" : "重新读取配置", async event=>{
        const control=event.currentTarget;
        if (!await ctx.confirm(error.configSubmissionRejected ? "核对最新已保存配置并保留当前草稿？再次提交前请核对期间的配置修改。" : "核对并读取已保存配置会替换当前源码草稿，确定继续？", "核对保存结果") || !ctx.current()) return;
        await ctx.mutate(control, async()=>{
          const draft=()=>JSON.stringify([form.elements.name.value,form.elements.description.value,form.elements.content.value]);
          const original=draft();
          const result = error.recoverConfigSubmission ? await error.recoverConfigSubmission() : {config:(await ctx.api(`${base}/workspace`)).config};
          if (!ctx.current() || !result.config) return;
          const fresh=await inbounds.reload();
          if (!ctx.current()) return;
          if (draft()!==original) throw new Error("核对期间源码已修改，已保留当前草稿，请再次核对。");
          if (!fresh || fresh.config?.id!==result.config.id || fresh.config?.version!==result.config.version)
            throw new Error("配置又有更新，请重新核对保存结果。");
          saved=result.config;inbounds.acceptConfig(saved);
          if (!error.configSubmissionRejected) {
            form.elements.name.value=saved.name;
            form.elements.description.value=saved.description||"";
            form.elements.content.value=saved.content;
          }
          form.elements.content.defaultValue=saved.content;
          liveNotice.textContent=`已保存 · ${date(saved.updated_at)}`;
          sourceBlocked=false;recovery.replaceChildren();ctx.setDirty(false);syncDraft();
          const feedback=form.querySelector(':scope > .a-notice');
          if (feedback) feedback.textContent="";
          form.querySelector('[type=submit]').disabled=!importSource && !canDeploy;
          ctx.announce("保存结果已核对");
        });
      })));
  }
  const dirty = () => form.elements.content.value !== form.elements.content.defaultValue || form.elements.name.value !== (saved?.name || `${agent.name} ${engine}`) || form.elements.description.value !== (saved?.description || "");
  const syncDraft = () => {
    const changed = dirty();
    ctx.setDirty(changed);
    if (reset) reset.disabled = !changed;
    liveNotice.textContent = changed ? "有未保存的修改" : source ? `已读取 · ${date(source.readAt)}` : saved ? `已保存 · ${date(saved.updated_at)}` : "尚未保存";
  };
  function acceptSavedSource(config, sourceContent = config.content) {
    saved = config; workspace.config = config; workspace.source_content = sourceContent; source = null;
    form.elements.name.value = config.name;
    form.elements.description.value = config.description || "";
    form.elements.content.value = form.elements.content.defaultValue = sourceContent;
    sourceBlocked = false; recovery.replaceChildren();
    const feedback = form.querySelector(':scope > .a-notice');
    if (feedback) feedback.textContent = "";
    const submit = form.querySelector('[type=submit]');
    if (submit) submit.disabled = !importSource && !canDeploy;
    if (readHistory) readHistory.disabled = false;
    syncDraft();
  }
  recoverPortConflict = async error => {
    if (!execute || !ctx.can("tasks.read") || !ctx.current()) return false;
    const draft = JSON.stringify([form.elements.name.value,form.elements.description.value,form.elements.content.value]);
    liveNotice.textContent = "正在核对实例状态…";
    try { await recoverConfigInstance(ctx,agent,error.recoveryEngine); }
    finally { if (ctx.current()) syncDraft(); }
    if (!ctx.current()) throw new DOMException("目标已切换", "AbortError");
    if (draft !== JSON.stringify([form.elements.name.value,form.elements.description.value,form.elements.content.value]))
      throw new Error("核对期间配置已修改，已保留当前草稿，请再次提交。");
    return true;
  };
  const reset = editable && !importSource && button("重置修改", () => {
    if (form.getAttribute("aria-busy") === "true") return;
    form.elements.name.value = saved?.name || `${agent.name} ${engine}`;
    form.elements.description.value = saved?.description || "";
    form.elements.content.value = form.elements.content.defaultValue;
    syncDraft();
  }, { disabled: true });
  form.addEventListener("input", syncDraft);
  form.querySelector('button[type="submit"]').addEventListener("mutationend", syncDraft);
  form.querySelector('button[type="submit"]').addEventListener("mutationend", ()=>{if(sourceBlocked || !importSource && !canDeploy)form.querySelector('[type=submit]').disabled=true;});
  form.addEventListener("submit", () => { if (reset && form.getAttribute("aria-busy") === "true") reset.disabled = true; });
  const deployNotice = notice(configDeployBlockReason(agent,engine,ctx.can));
  deployNotice.hidden = !deployNotice.textContent || importSource;
  if (!editable) form.querySelector('button[type="submit"]').remove();
  else if (sourceBlocked || !importSource && !canDeploy) form.querySelector('button[type="submit"]').disabled = true;
  form.append(deployNotice);
  const read = canReadCurrent && readAction && button(mode === "import" ? "读取系统配置" : "读取托管配置", async event => {
    const control = event.currentTarget;
    if (form.elements.content.value !== form.elements.content.defaultValue &&
        !await ctx.confirm("读取节点配置会替换当前编辑内容，确定继续？", "重新读取")) return;
    if (!ctx.current()) return;
    await ctx.mutate(control, async () => {
      const draft = JSON.stringify([form.elements.name.value, form.elements.description.value, form.elements.content.value]);
      liveNotice.textContent = "等待节点返回配置…";
      try {
        const result = await snapshot();
        if (draft !== JSON.stringify([form.elements.name.value, form.elements.description.value, form.elements.content.value]))
          throw new Error("读取期间配置已修改，已保留当前草稿，请重新读取。");
        if (!importSource) await inbounds.previewSnapshot(result.taskId,()=>draft === JSON.stringify([form.elements.name.value,form.elements.description.value,form.elements.content.value]));
        if (!ctx.current()) return;
        if (draft !== JSON.stringify([form.elements.name.value,form.elements.description.value,form.elements.content.value]))
          throw new Error("读取期间配置已修改，已保留当前草稿，请重新读取。");
        source = result;
        dependencyPreview.hidden = !importSource || !source.dependencies?.length;
        if (!dependencyPreview.hidden) dependencyPreview.replaceChildren(
          node("summary", {}, `依赖资源 · ${source.dependencies.length}`),
          table(["配置字段", "路径映射"], source.dependencies.map(item => [item.field,
            stack(muted("原资源"), node("span", {}, item.source), muted("托管去向"), node("span", {}, item.destination)),
          ])),
        );
        form.elements.content.value = source.content;
        form.elements.content.defaultValue = source.content;
        syncDraft();
        liveNotice.textContent = `已读取 · ${date(source.readAt)}`;
      } catch (error) {
        if (error.name === "AbortError" || !ctx.current()) throw new DOMException("目标已切换", "AbortError");
        liveNotice.textContent = saved ? `已保存 · ${date(saved.updated_at)}` : "尚未保存";
        throw new Error(diagnosticError(error.message), { cause: error });
      }
    });
  }, {disabled: importSource && !canImport});
  const format = editable && !importSource && engine !== "mihomo" && button("格式化 JSON", () => {
    if (form.getAttribute("aria-busy") === "true") return;
    try {
      const editor = form.elements.content;
      editor.value = formatConfigContent(editor.value, "JSON");
      syncDraft();
    } catch(error) { void ctx.recordFailure?.(error,`${agent.name || agent.id} · ${engine} JSON 格式化`,"format_validation");ctx.announce(`JSON 格式无效，请检查配置。\n${error.message}`,"error"); }
  });
  const readHistory = ctx.can("configs.read") && button("保存历史", () => preparedDialog(ctx, "节点配置历史", ["保存时间", "状态", "操作", "配置预览"], async ctx => {
    if (!saved) return;
    const records = list(await ctx.api(`/configs/${pathID(saved.id)}/revisions?limit=100`));
    const preview = node("div", {}, muted("选择一份记录预览"));
    const historyBox = ctx.dialog("节点配置历史", stack(table(["保存时间", "状态", "操作"], records.map(record => [
      date(record.updated_at), record.version === saved.version ? badge("当前") : "历史档案",
      button("预览", () => {
        const previewRead = readRegion(dialogContext(ctx, historyBox), "配置预览", async () => {
        const revision = await ctx.api(`/configs/${pathID(saved.id)}/revisions/${record.version}`);
        if (!ctx.current() || !historyBox.isConnected) return;
        preview.replaceChildren(stack(code(revision.content), ctx.can("configs.restore") && record.version !== saved.version && button("恢复此档案", async event => {
          const control = event.currentTarget;
          if (!await ctx.confirm(`恢复 ${date(revision.updated_at)} 的配置？将按当前时间新增记录，原档案保留。`, "恢复配置") || !ctx.current()) return;
          await ctx.mutate(control, async () => {
            const restored = await ctx.api(`/configs/${pathID(saved.id)}/revisions/${revision.version}/restore`, { method: "POST", body: { expected_version: saved.version } });
            if (!ctx.current()) return;
            acceptSavedSource(restored);
            inbounds.acceptConfig(restored);
            try {
              const fresh = await inbounds.reload();
              if (fresh?.config?.id === restored.id && fresh.config.version === restored.version)
                acceptSavedSource(fresh.config, fresh.source_content);
            }
            catch (error) { blockSource(error, restored); throw error; }
            historyBox.close();
            ctx.announce("配置已恢复，节点部署保持原状");
          });
        }, { tone: "primary" })));
        }, () => {});
        preview.prepend(previewRead.element); void previewRead.load();
      }),
    ]), { label: "节点配置历史" }), preview), { wide: true });
  }));
  if (readHistory) readHistory.disabled = !saved;
  const sourceTools = actions();
  const inbounds = configStudio(ctx, {
    onAgent(fresh) {
      canDeploy = !configDeployBlockReason(fresh, engine, ctx.can);
      deployNotice.textContent = configDeployBlockReason(fresh,engine,ctx.can);
      deployNotice.hidden = !deployNotice.textContent || importSource;
      const submit = form.querySelector("button[type=submit]");
      if (submit && !sourceBlocked && form.getAttribute("aria-busy") !== "true") submit.disabled = !importSource && !canDeploy;
    },
    sourceTools, readOnly: importSource,
    agent, engine, base, workspace, getDraft: () => JSON.stringify([form.elements.name.value,form.elements.description.value,form.elements.content.value]),
    dirty,
    accept(config, sourceContent) {
      acceptSavedSource(config, sourceContent);
    },
    beforeDeploy,
  });
  studioHost.replaceChildren(...(importSource && !canImport ? [notice(errorText || "当前未发现可导入的系统服务配置。")] : []), inbounds);
  sourceBody.replaceChildren(form);
  sourceActions.replaceChildren(actions(sourceTools, read, reset, format, readHistory));
  return page;
  }
}
