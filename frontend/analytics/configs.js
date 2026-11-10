import { patch } from "./live-dom.js";
import { readRegion, loadingTable, dialogContext } from "./loading.js";
import { configDeployBlockReason, configDeployConfirmation } from "../modules/core-install-state.js";
import { readOrderedNodes } from "../modules/node-order.js";
import { configDefaults } from "../modules/config-defaults.js";
import { createArchiveDraft, archiveDraftDirty } from "../modules/archive-draft-model.js";
import { bindSourceEditorTools } from "./source-editor.js";
import { node, button, actions, stack, grid, section, table, badge, field, code, muted, pathID, list, notice } from "./ui.js";
import { date, taskDetail } from "./operations.js";

const engines = ["mihomo", "xray", "sing-box", "ss-rust"];
const initial = engine => configDefaults[engine];


export async function archivePage(ctx) {
  let items = [], templates = [], agents = [], agentsReady = !ctx.can('agents.read');
  const drafts = ctx.state.data.analyticsArchiveDrafts ||= new Map();
  const results = node("div", {}, loadingTable(["名称", "内核", "节点", "保存时间", "操作"], "配置档案"));
  const templateResults = node("div", {}, loadingTable(["名称", "内核", "保存时间", "操作"], "配置模板"));
  function draw() {
    patch(results, node("div", {}, table(["名称", "内核", "节点", "保存时间", "操作"], list(items).map(config => [
      config.name, badge(config.engine),
      list(agents).find(agent => agent.id === config.agent_id)?.name || (config.agent_id ? config.agent_id : "独立配置"),
      date(config.updated_at),
      actions(button("查看与编辑", () => edit(config)),
        button("历史", () => history(config)),
        ctx.can("tasks.execute") && button("校验与部署", () => deliver(config), { disabled: !agentsReady }),
        ctx.can("configs.delete") && button("删除", async event => {
          const control = event.currentTarget;
          if (!await ctx.confirm(`删除“${config.name}”及其配置档案？相关任务记录会保留。此操作无法恢复。`, "删除配置") || !ctx.current()) return;
          await ctx.mutate(control, async () => {
            await ctx.api(`/configs/${pathID(config.id)}`, { method: "DELETE" });
            items.splice(items.indexOf(config), 1);
            draw();
            ctx.announce("配置已删除");
          });
        }, { tone: "danger" })),
    ]), { label: "配置档案", rowKey: (_, index) => items[index].id })));
  }
  function edit(config = null) {
    let baseline = config || { name: "", description: "", engine: "mihomo", content: initial("mihomo"), version: 0 };
    const draftKey = config?.id || "new";
    let draft = drafts.get(draftKey);
    if (!draft || !archiveDraftDirty(draft)) draft = createArchiveDraft(baseline);
    drafts.set(draftKey, draft);
    if (draft.savedBaseline) baseline = draft.savedBaseline;
    else draft.savedBaseline = { ...baseline };
    const fields = [
      grid(field("名称", "name", draft.name, { required: true, maxlength: 100, readonly: !ctx.can("configs.write") }),
        field("内核", "engine", draft.engine, { options: engines, disabled: Boolean(config) || !ctx.can("configs.write") })),
      field("说明", "description", draft.description, { maxlength: 300, readonly: !ctx.can("configs.write") }),
      node("div", { className: "a-editor" }, field("配置正文", "content", draft.sources.get(draft.engine).content, {
        multiline: 18, required: true, spellcheck: false, readonly: !ctx.can("configs.write"),
      })),
    ];
    const savedStatus = notice(config ? `已保存 · ${date(baseline.updated_at)}` : "", "ok");
    const form = ctx.form([...fields, savedStatus], "保存配置", async (values, element) => {
      const payload = {
        ...values, engine: config ? baseline.engine : values.engine, version: baseline.version,
      };
      const saved = await ctx.api(config ? `/configs/${pathID(config.id)}` : "/configs", {
        method: config ? "PUT" : "POST", body: payload,
      });
      if (!ctx.current()) return;
      // Queued input events can still arrive while the form is disabled.
      capture();
      if (config) Object.assign(config, saved);
      else { config = saved; items.unshift(saved); }
      baseline = { ...saved };
      drafts.delete(draftKey);
      draft.originalName = saved.name; draft.originalDescription = saved.description || "";
      draft.sources.get(activeEngine).original = saved.content;
      // A saved archive has one immutable engine; inactive new-file drafts
      // must not keep that archive dirty or hide a later restored revision.
      draft.sources = new Map([[activeEngine, draft.sources.get(activeEngine)]]);
      draft.savedBaseline = baseline;
      drafts.set(saved.id, draft);
      element.elements.engine.disabled = true;
      syncDraft();
      draw();
      updateSource();
    });
    if (!ctx.can("configs.write")) form.querySelector('button[type="submit"]').remove();
    const engineInput = form.elements.engine;
    let activeEngine = draft.engine;
    function capture() {
      draft.name = form.elements.name.value;
      draft.description = form.elements.description.value;
      draft.engine = engineInput.value;
      draft.sources.get(activeEngine).content = form.elements.content.value;
    }
    function syncDraft() {
      capture();
      const changed = archiveDraftDirty(draft);
      ctx.setDirty(changed);
      savedStatus.className = `a-notice${changed ? "" : " ok"}`;
      savedStatus.textContent = changed ? `有未保存修改${baseline.updated_at ? ` · 上次保存 ${date(baseline.updated_at)}` : ""}` :
        baseline.updated_at ? `已保存 · ${date(baseline.updated_at)}` : "";
    }
    engineInput.addEventListener("change", () => {
      draft.sources.get(activeEngine).content = form.elements.content.value;
      activeEngine = engineInput.value; draft.engine = activeEngine;
      if (!draft.sources.has(activeEngine)) draft.sources.set(activeEngine, { content: initial(activeEngine), original: initial(activeEngine) });
      form.elements.content.value = draft.sources.get(activeEngine).content;
      syncDraft();
    });
    form.addEventListener("input", syncDraft);
    const updateSource = bindSourceEditorTools(ctx, form, () => activeEngine, () => draft.sources.get(activeEngine).original,
      syncDraft, ctx.can("configs.write"));
    syncDraft();
    const editorActions = form.querySelector(":scope > .a-actions");
    editorActions.append(button("保留草稿并关闭", () => { capture(); ctx.setDirty(false); box.close(); }));
    if (ctx.can("tasks.execute")) editorActions.append(button("校验与部署已保存配置", () => {
      if (!config) { ctx.announce("请先保存配置。", "error"); return; }
      const changed = ["name", "description", "content"].some(key => String(form.elements[key].value || "") !== String(baseline[key] || ""));
      if (changed) { ctx.announce("存在未保存修改，请先保存后再校验或部署。", "error"); return; }
      deliver(config);
    }));
    const box = ctx.dialog(config?.name || "新建配置", form, { wide: true });
    box.onRequestClose = requestClose;
    async function requestClose() {
      capture();
      const changed = archiveDraftDirty(draft);
      if (form.getAttribute("aria-busy") === "true") return;
      if (changed && !await ctx.confirm("配置尚未保存，确定放弃更改？", "放弃更改")) return;
      drafts.delete(config?.id || draftKey);
      ctx.setDirty(false);
      box.close();
    }
  }
  function history(config) {
    const recordsView = loadingTable(['保存时间', '名称', '状态', '操作'], '配置保存历史');
    const preview = node('div', {}, muted('选择一份记录预览'));
    const content = stack(recordsView, preview);
    const box = ctx.dialog(`${config.name} · 保存历史`, content, { wide: true });
    const local = dialogContext(ctx, box);
    let previewRead;
    const recordsRead = readRegion(local, '配置历史', signal => local.api(`/configs/${pathID(config.id)}/revisions?limit=100`, { signal }), records => {
      recordsView.updateRows(list(records).map(record => [
        date(record.updated_at), record.name, record.version === config.version ? badge('当前') : muted('历史档案'),
        button('预览', () => {
          previewRead?.stop(); preview.replaceChildren(node('h3', {}, '配置预览'), code('正在读取配置正文…'));
          previewRead = readRegion(local, '配置预览', signal => local.api(`/configs/${pathID(config.id)}/revisions/${record.version}`, { signal }), selected => {
            const restore = ctx.can('configs.restore') && record.version !== config.version && button('恢复此档案', async event => {
              const control = event.currentTarget;
              if (!await ctx.confirm(`恢复 ${date(selected.updated_at)} 的配置？本次恢复会按当前时间新增记录，原档案保留。`, '恢复配置') || !local.current()) return;
              await ctx.mutate(control, async () => {
                const saved = await local.api(`/configs/${pathID(config.id)}/revisions/${selected.version}/restore`, { method: 'POST', body: { expected_version: config.version } });
                if (!local.current()) return;
                Object.assign(config, saved); ctx.announce('配置已恢复'); box.close(); draw();
              });
            }, { tone: 'primary' });
            preview.replaceChildren(stack(node('h3', {}, `${selected.name} · ${date(selected.updated_at)}`), code(selected.content), restore && actions(restore)));
          }, { clear: () => preview.replaceChildren() });
          preview.prepend(previewRead.element); void previewRead.load();
        }),
      ]));
    }, { view: recordsView, clear: () => { recordsView.updateRows([]); preview.replaceChildren(); } });
    content.prepend(recordsRead.element); void recordsRead.load();
  }
  function deliver(config) {
    const eligible = list(agents).filter(agent =>
      (agent.supported_capabilities || agent.capabilities || []).includes(config.engine) &&
      (!config.agent_id || config.agent_id === agent.id));
    const targetLabel = agent => `${agent.name} · ${agent.shared_allocation ? "当前账号共享实例" : "当前账号主实例"}`;
    const form = ctx.form([
      muted(`${config.name} · ${config.engine} · 保存于 ${date(config.updated_at)}`),
      muted(`配置归属：${ctx.session?.username || "当前账号"}`),
      field("目标节点与实例", "agent_id", config.agent_id || "", { required: true, options: [["", "选择节点与实例"], ...eligible.map(agent => [agent.id, targetLabel(agent)])] }),
      field("执行方式", "action", "validate", { options: [["validate", "仅校验"], ["deploy", "部署并重启"]] }),
    ], "提交任务", async values => {
      const target = eligible.find(agent => agent.id === values.agent_id);
      if (!target) throw new Error("请选择当前账号可用的目标节点与实例。");
      const refreshed = list(await readOrderedNodes(ctx.api)).find(agent=>agent.id===target.id);
      if (!ctx.current()) return;
      if (!refreshed || (refreshed.shared_allocation?.id || "") !== (target.shared_allocation?.id || ""))
        throw new Error("目标实例已变化，请重新选择节点，当前选择已保留。");
      const reason = configDeployBlockReason(refreshed, config.engine, ctx.can);
      if (reason) throw new Error(reason);
      if (values.action === "deploy" || !refreshed.runtime?.[config.engine]?.installed) {
        const confirmation=configDeployConfirmation(refreshed,config.engine,values.action);
        if (!await ctx.confirm(confirmation.message,confirmation.title) || !ctx.current()) return;
      }
      const task = await ctx.api(`/configs/${pathID(config.id)}/tasks`, { method: "POST", body: {
        ...values, expected_config_version: config.version,
        expected_instance_id:refreshed.shared_allocation?.id || "",
        ...(!refreshed.runtime?.[config.engine]?.installed ? {install_if_missing:true} : {}),
      } });
      box.close();
      ctx.announce("任务已提交");
      if (ctx.can("tasks.read")) await taskDetail(ctx, task.id);
    });
    const box = ctx.dialog(`${config.name} · 校验与部署`, eligible.length ? form : muted("没有当前账号可用且支持此内核的节点"));
  }
  function drawTemplates() {
    patch(templateResults, node("div", {}, table(["名称", "内核", "保存时间", "操作"], list(templates).map(template => [
      template.name, template.engine, date(template.updated_at),
      actions(button("预览", () => ctx.dialog(template.name, code(template.content), { wide: true })),
        ctx.can("templates.write") && ctx.can("agent-config.write") && button("应用到节点", () => {
          const eligible = list(agents).filter(agent => agent.status === "online" && agent.runtime?.[template.engine]?.installed &&
            (agent.capabilities || []).includes(template.engine));
          const form = ctx.form([
            field("目标节点", "agent_id", "", { required: true, options: [["", "选择节点"], ...eligible.map(agent => [agent.id, agent.name])] }),
          ], "应用模板", async values => {
            await ctx.api(`/templates/${pathID(template.id)}/apply`, { method: "POST", body: values });
            box.close();
            ctx.announce("模板已应用到节点配置");
          });
          const box = ctx.dialog("应用模板", eligible.length ? form : muted("没有可用节点"));
        }, { disabled: !agentsReady }),
        ctx.can("templates.delete") && button("删除", async event => {
          const control = event.currentTarget;
          if (!await ctx.confirm(`删除模板“${template.name}”？`, "删除模板") || !ctx.current()) return;
          await ctx.mutate(control, async () => {
            await ctx.api(`/templates/${pathID(template.id)}`, { method: "DELETE" });
            templates.splice(templates.indexOf(template), 1);
            drawTemplates();
          });
        }, { tone: "danger" })),
    ]), { label: "配置模板", rowKey: (_, index) => templates[index].id })));
  }
  const addTemplate = ctx.can("templates.write") && button("新建模板", () => {
    const draft = ctx.state.data.analyticsTemplateDraft ||= createArchiveDraft();
    let activeEngine = draft.engine;
    const form = ctx.form([
      field("模板名称", "name", draft.name, { required: true, maxlength: 100 }),
      field("内核", "engine", draft.engine, { options: engines }),
      node("div", { className: "a-editor" }, field("模板正文", "content", draft.sources.get(activeEngine).content, { multiline: 12, required: true, spellcheck: false })),
      muted("支持 {{node_name}}、{{node_id}}、{{lan_ip}}、{{random_port}}"),
    ], "保存模板", async values => {
      const saved = await ctx.api("/templates", { method: "POST", body: values });
      templates.unshift(saved);
      ctx.state.data.analyticsTemplateDraft = createArchiveDraft();
      ctx.setDirty(false);
      box.close();
      drawTemplates();
      ctx.announce("模板已保存");
    });
    const capture = () => {
      draft.name = form.elements.name.value;
      draft.sources.get(activeEngine).content = form.elements.content.value;
    };
    form.elements.engine.addEventListener("change", () => {
      capture(); activeEngine = draft.engine = form.elements.engine.value;
      if (!draft.sources.has(activeEngine)) draft.sources.set(activeEngine, { content: initial(activeEngine), original: initial(activeEngine) });
      form.elements.content.value = draft.sources.get(activeEngine).content;
      ctx.setDirty(archiveDraftDirty(draft));
    });
    form.addEventListener("input", () => { capture(); ctx.setDirty(archiveDraftDirty(draft)); });
    bindSourceEditorTools(ctx, form, () => activeEngine, () => draft.sources.get(activeEngine).original,
      () => { capture(); ctx.setDirty(archiveDraftDirty(draft)); }, true);
    form.querySelector(":scope > .a-actions").append(button("保留草稿并关闭", () => { capture(); ctx.setDirty(false); box.close(); }));
    const box = ctx.dialog("新建模板", form, { wide: true });
    box.onRequestClose = async () => {
      capture();
      if (form.getAttribute("aria-busy") === "true") return;
      if (archiveDraftDirty(draft) && !await ctx.confirm("模板尚未保存，确定放弃更改？", "放弃更改")) return;
      ctx.state.data.analyticsTemplateDraft = createArchiveDraft();
      ctx.setDirty(false); box.close();
    };
  });
  ctx.pageActions(...(ctx.can("configs.write") ? [button("新建配置", () => edit(), { tone: "primary" })] : []));
  let configsReady = false, templatesReady = false;
  const configRead = readRegion(ctx, '配置档案', signal => ctx.api('/configs', { signal }), value => { items = list(value); configsReady = true; draw(); }, { view: results, clear: () => { items = []; draw(); } });
  const templateRead = readRegion(ctx, '配置模板', signal => ctx.api('/templates', { signal }), value => { templates = list(value); templatesReady = true; drawTemplates(); }, { view: templateResults, clear: () => { templates = []; drawTemplates(); } });
  const agentRead = readRegion(ctx, '节点选项', () => readOrderedNodes(ctx.api), value => {
    agents = list(value); agentsReady = true; if (configsReady) draw(); if (templatesReady) drawTemplates();
  }, { clear: () => { agents = []; agentsReady = false; if (configsReady) draw(); if (templatesReady) drawTemplates(); } });
  void configRead.load();
  if (ctx.can('templates.read')) void templateRead.load();
  if (ctx.can('agents.read')) void agentRead.load();
  ctx.onRefresh(() => Promise.all([configRead.load(), ctx.can('templates.read') && templateRead.load(), ctx.can('agents.read') && agentRead.load()]));
  return stack(configRead.element, ctx.can('agents.read') && agentRead.element, section(null, results),
    ctx.can('templates.read') && section('配置模板', stack(templateRead.element, templateResults), addTemplate));
}
