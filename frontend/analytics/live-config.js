import { readOrderedNodes } from "../modules/node-order.js";
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
  const request = ctx.api;
  const mutations = createConfigMutations({state:ctx.state, api:(path, options={})=>request(path, {
    ...options, body:options.body === undefined ? undefined : JSON.parse(options.body),
  })}, {onTask:()=>{}, waitForTerminal:id=>waitTask(ctx,id)});
  ctx = {...ctx, api:(path, options={})=>mutations.request(path, {
    ...options, body:options.body === undefined ? undefined : JSON.stringify(options.body),
  })};
  const selection = readPresetRoute(location.hash) || {};
  const params = new URLSearchParams(location.hash.split("?")[1]);
  const requested = selection.agentId || location.hash.match(/^#preset-node-(.+)$/)?.[1] || "";
  const selectedEngine = selection.engine || "";
  let agents = ctx.can("agents.read") ? await readOrderedNodes(ctx.api) : [];
  let workspace;
  if (!ctx.can("agents.read") && requested && engineIDs.includes(selectedEngine)) {
    workspace = await ctx.api(`/agents/${pathID(requested)}/configs/${pathID(selectedEngine)}/workspace`);
    agents = [workspace.agent].filter(Boolean);
  }
  const available = agents.filter(agent => agent.capabilities?.length);
  if (requested && !available.some(agent => agent.id === requested))
    return notice("此节点不可用，或当前账号无权访问。");
  const target = available.find(agent => agent.id === requested) || available.find(agent => agent.status === "online" && agent.capabilities.some(id => agent.runtime?.[id]?.installed)) || available.find(agent => agent.status === "online") || available[0];
  if (!target) return section("节点配置", empty("没有可配置的节点"));
  const engines = target.capabilities;
  const engine = engines.includes(selectedEngine) ? selectedEngine : engines.find(id => target.runtime?.[id]?.installed) || engines[0];
  const base = `/agents/${pathID(target.id)}/configs/${pathID(engine)}`;
  if (ctx.can("tasks.read")) void mutations.recover(target.id, engine);
  workspace ||= await ctx.api(`${base}/workspace`);
  const agent = workspace.agent || target;
  const runtime = agent.runtime?.[engine] || {};
  let saved = workspace.config || null;
  let source = null;
  const editable = ctx.can("agent-config.write");
  const execute = ctx.can("tasks.execute") && agent.status === "online";
  const canReadHost = ctx.can("host.manage", agent) && execute && ctx.can("tasks.read");
  const canReadCurrent = canReadHost || Boolean(agent.shared_allocation?.enabled && execute && ctx.can("tasks.read") && ["managed-config-read-v1","shared-core-instances-v1","shared-core-lifecycle-v1"].every(feature=>agent.features?.includes(feature)));
  const canImport = Boolean(runtime.existing_config_available && canReadHost);
  const hostManaged = Boolean(runtime.installed && (agent.can_manage !== false || agent.shared_allocation?.enabled));
  const mode = params.get("source") === "import" && canImport ? "import" : params.get("source") === "managed" && hostManaged && canReadCurrent ? "managed" : "personal";
  const importSource = mode === "import";
  const readAction = liveConfigReadAction({
    sourceMode: mode, managedReadSupported: agent.features?.includes("managed-config-read-v1"),
    existingAvailable: Boolean(runtime.existing_config_available),
  });
  const flags = actions(status(agent.status), badge(agent.shared_allocation ? "共享实例" : "主实例"),
    agent.shared_allocation && muted(`本人端口 · ${formatSharedPorts(agent.shared_allocation.ports) || "未分配"}`),
    agent.shared_allocation && ctx.can("agents.read") && link("实例管理", `#settings-node-${pathID(agent.id)}`));
  const targetField = field("节点", "agent", target.id, { options: available.map(item => [item.id, nodeName(ctx, item)]) });
  const engineField = field("内核", "engine", engine, { options: engines });
  const modeField = field("配置来源", "source", mode, { options: [
    ["personal", "面板保存的配置"],
    ...(hostManaged && canReadCurrent ? [["managed", "QAgent 托管配置"]] : []),
    ...(canImport ? [["import", "可导入的系统服务配置"]] : []),
  ] });
  const select = node("form", { className: "a-target-form", onsubmit(event) {
    event.preventDefault();
    const values = formData(event.currentTarget);
    if (values.agent !== agent.id) { delete values.engine; delete values.source; }
    else if (values.engine !== engine) delete values.source;
    location.hash = `#live-config?${new URLSearchParams(values)}`;
    // The route guard owns draft confirmation. Until navigation is accepted,
    // keep the controls bound to the configuration actually being edited.
    targetField.querySelector("select").value = agent.id;
    engineField.querySelector("select").value = engine;
    modeField.querySelector("select").value = mode;
  } }, targetField, engineField, modeField);
  select.addEventListener("change", () => select.requestSubmit());
  const liveNotice = node("p", { className: "a-save-state", role: "status" }, saved ? `已保存 · ${date(saved.updated_at)}` : "尚未保存");
  const errorText = runtime.existing_config_unsupported_reason;
  const api = (path, options) => ctx.api(path, { method: options?.method, body: options?.body ? JSON.parse(options.body) : undefined });
  const beforeDeploy = () => {
    if (!ctx.current()) throw Object.assign(new Error("页面目标已变化，请重新打开配置后提交。"), { deployPreflight: true });
  };
  const snapshot = async (preferCached = false) => {
    if (!readAction || !canReadCurrent) throw new Error("当前节点或账号无法读取托管配置。");
    const result = await requestConfigSnapshot({ api, waitForTask: id => waitTask(ctx, id) }, agent, engine, readAction, {
      preferCached, isCurrent: ctx.current,
    });
    if (!result || !ctx.current()) throw new DOMException("目标已切换", "AbortError");
    return result;
  };
  const canDeploy = execute && !errorText && (runtime.installed || agent.shared_allocation?.enabled);
  const intent = node("input", {type:"hidden", name:"intent", value:importSource ? "import" : "deploy"});
  const content = field(importSource ? "系统配置快照" : "配置正文", "content", importSource ? "" : saved?.content || defaultSource(engine), {
    multiline: 20, required: true, readonly: importSource || !editable, spellcheck: false,
  });
  let sourceBlocked = false;
  const recovery = stack();
  const form = ctx.form([
    node("div", { className: "a-grid" },
      field("名称", "name", saved?.name || `${agent.name} ${engine}`, { required: true, maxlength: 100, readonly: !editable }),
      field("说明", "description", saved?.description || "", { maxlength: 300, readonly: !editable }), intent),
    node("div", { className: "a-editor" }, content), liveNotice, recovery,
  ], importSource ? "导入并迁移" : "保存并部署", async (values, element) => {
    if (!editable || sourceBlocked) return;
    if (!importSource && !canDeploy) throw new Error("当前节点或内核不可部署，请核对在线及安装状态。");
    values.intent = importSource ? "import" : "deploy";
    if (values.intent === "import") {
      if (!source?.content) throw new Error("请先读取可导入的系统服务配置。");
      if (!await ctx.confirm("导入此配置并迁移现有系统服务？操作成功后由 QAgent 管理对应内核。", "导入并迁移")) return;
    }
    if (values.intent === "deploy" && !await ctx.confirm(`部署到 ${agent.name} · ${engine} · ${agent.shared_allocation ? "当前共享实例" : "当前主实例"} 并重启此实例内核？`, "部署并重启")) return;
    if (!ctx.current()) return;
    let pendingTask;
    try {
      await submitLiveConfigChange({
        api, agent, engine, intent: values.intent, source, existingAvailable: importSource, savedConfig: saved,
        form: new Map(Object.entries({ ...values, version: saved?.version || 0 })),
        beforeDeploy,
        async submitTask(body) {
          pendingTask = await ctx.api("/tasks", { method: "POST", body });
          return pendingTask;
        },
        onSavedConfig(config) {
          saved = config;
          inbounds.acceptConfig(config);
          if (readHistory) readHistory.disabled = false;
          ctx.setDirty(false);
          liveNotice.textContent = `已保存 · ${date(config.updated_at)}`;
        },
        onDeployTask() { source = null; },
      }).then(result => { pendingTask ||= result.task; });
    } catch (error) {
      if (error.name === "AbortError") throw error;
      if (!error.deployPreflight && (!error.status || error.status===200 || error.status===408 || error.status===409 || error.status>=500)) blockSource(error);
      error.message = diagnosticError(error.message);
      throw error;
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
      actions(button(error.recoverConfigSubmission ? "核对保存结果" : "重新读取配置", async event=>{
        const control=event.currentTarget;
        if (!await ctx.confirm("核对并读取已保存配置会替换当前源码草稿，确定继续？", "核对保存结果") || !ctx.current()) return;
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
          form.elements.name.value=saved.name;
          form.elements.description.value=saved.description||"";
          form.elements.content.value=form.elements.content.defaultValue=saved.content;
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
  const syncDraft = () => { ctx.setDirty(dirty()); if (reset) reset.disabled = !dirty(); };
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
  if (!editable) form.querySelector('button[type="submit"]').remove();
  else if (!importSource && !canDeploy) {
    form.querySelector('button[type="submit"]').disabled = true;
    form.append(notice(!execute ? "当前节点离线或没有部署权限，草稿可继续编辑。" : "当前内核不可部署，请先安装内核或检查节点状态。"));
  }
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
  });
  const format = editable && !importSource && engine !== "mihomo" && button("格式化 JSON", () => {
    if (form.getAttribute("aria-busy") === "true") return;
    try {
      const editor = form.elements.content;
      editor.value = formatConfigContent(editor.value, "JSON");
      syncDraft();
    } catch { ctx.announce("JSON 格式无效，请检查配置。", "error"); }
  });
  const readHistory = ctx.can("configs.read") && button("保存历史", event => void ctx.mutate(event.currentTarget, async () => {
    if (!saved) return;
    const records = list(await ctx.api(`/configs/${pathID(saved.id)}/revisions?limit=100`));
    const preview = node("div", {}, muted("选择一份记录预览"));
    const historyBox = ctx.dialog("节点配置历史", stack(table(["保存时间", "状态", "操作"], records.map(record => [
      date(record.updated_at), record.version === saved.version ? badge("当前") : "历史档案",
      button("预览", event => void ctx.mutate(event.currentTarget, async () => {
        const revision = await ctx.api(`/configs/${pathID(saved.id)}/revisions/${record.version}`);
        if (!ctx.current() || !historyBox.isConnected) return;
        preview.replaceChildren(stack(code(revision.content), ctx.can("configs.restore") && record.version !== saved.version && button("恢复此档案", async event => {
          const control = event.currentTarget;
          if (!await ctx.confirm(`恢复 ${date(revision.updated_at)} 的配置？将按当前时间新增记录，原档案保留。`, "恢复配置") || !ctx.current()) return;
          await ctx.mutate(control, async () => {
            const restored = await ctx.api(`/configs/${pathID(saved.id)}/revisions/${revision.version}/restore`, { method: "POST", body: { expected_version: saved.version } });
            if (!ctx.current()) return;
            saved = restored;
            inbounds.acceptConfig(restored);
            await inbounds.reload();
            form.elements.name.value = restored.name;
            form.elements.description.value = restored.description || "";
            form.elements.content.value = form.elements.content.defaultValue = restored.content;
            ctx.setDirty(false); historyBox.close();
            syncDraft();
            liveNotice.textContent = `已保存 · ${date(restored.updated_at)}`;
            ctx.announce("配置已恢复，节点部署保持原状");
          });
        }, { tone: "primary" })));
      })),
    ]), { label: "节点配置历史" }), preview), { wide: true });
  }));
  if (readHistory) readHistory.disabled = !saved;
  const sourceTools = actions();
  const inbounds = configStudio(ctx, {
    sourceTools, readOnly: importSource,
    agent, engine, base, workspace, getDraft: () => JSON.stringify([form.elements.name.value,form.elements.description.value,form.elements.content.value]),
    dirty,
    accept(config) {
      saved = config; workspace.config = config;
      if (readHistory) readHistory.disabled = false;
      form.elements.name.value = config.name;
      form.elements.description.value = config.description || "";
      form.elements.content.value = form.elements.content.defaultValue = config.content;
      liveNotice.textContent = `已保存 · ${date(config.updated_at)}`;
      syncDraft();
    },
    beforeDeploy,
  });
  return stack(filterbar(select, flags),
    errorText && notice(errorText),
    inbounds,
    section("配置源码", node("div", { className: "a-card-body" }, form), actions(sourceTools, read, reset, format, readHistory)));
}
