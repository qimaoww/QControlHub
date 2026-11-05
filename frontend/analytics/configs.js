import { node, button, actions, stack, grid, section, table, badge, field, code, muted, pathID, list, notice } from "./ui.js";
import { date, taskDetail } from "./operations.js";

const engines = ["mihomo", "xray", "sing-box", "ss-rust"];
const initial = engine => engine === "mihomo" ? "mixed-port: 7890\nmode: rule\n" : "{}\n";

export async function archivePage(ctx) {
  const [configItems, templateItems, agents] = await Promise.all([
    ctx.api("/configs"), ctx.can("templates.read") ? ctx.api("/templates") : [],
    ctx.can("agents.read") ? ctx.api("/agents") : [],
  ]);
  const items = list(configItems), templates = list(templateItems);
  const results = node("div");
  const templateResults = node("div");
  function draw() {
    results.replaceChildren(table(["名称", "内核", "节点", "保存时间", "操作"], list(items).map(config => [
      config.name, badge(config.engine),
      list(agents).find(agent => agent.id === config.agent_id)?.name || (config.agent_id ? config.agent_id : "独立配置"),
      date(config.updated_at),
      actions(button("查看与编辑", () => edit(config)),
        button("历史", event => void ctx.mutate(event.currentTarget, () => history(config))),
        ctx.can("tasks.execute") && button("校验与部署", () => deliver(config)),
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
    ]), { label: "配置档案" }));
  }
  function edit(config = null) {
    let baseline = config || { name: "", description: "", engine: "mihomo", content: initial("mihomo"), version: 0 };
    const fields = [
      grid(field("名称", "name", baseline.name, { required: true, maxlength: 100, readonly: !ctx.can("configs.write") }),
        field("内核", "engine", baseline.engine, { options: engines, disabled: Boolean(config) || !ctx.can("configs.write") })),
      field("说明", "description", baseline.description, { maxlength: 300, readonly: !ctx.can("configs.write") }),
      node("div", { className: "a-editor" }, field("配置正文", "content", baseline.content, {
        multiline: 18, required: true, spellcheck: false, readonly: !ctx.can("configs.write"),
      })),
    ];
    const savedStatus = notice("", "ok");
    const form = ctx.form([...fields, savedStatus], "保存配置", async (values, element) => {
      const payload = {
        ...values, engine: config ? baseline.engine : values.engine, version: baseline.version,
      };
      const saved = await ctx.api(config ? `/configs/${pathID(config.id)}` : "/configs", {
        method: config ? "PUT" : "POST", body: payload,
      });
      if (!ctx.current()) return;
      // Controls are disabled during a save; the server timestamp and version
      // become the new baseline only after a successful response.
      if (config) Object.assign(config, saved);
      else { config = saved; items.unshift(saved); }
      baseline = { ...saved };
      element.elements.engine.disabled = true;
      ctx.setDirty(false);
      savedStatus.textContent = `已保存 · ${date(saved.updated_at)}`;
      draw();
    });
    if (!ctx.can("configs.write")) form.querySelector('button[type="submit"]').remove();
    const sourceDrafts = new Map([[baseline.engine, baseline.content]]);
    const engineInput = form.elements.engine;
    let activeEngine = baseline.engine;
    engineInput.addEventListener("change", () => {
      sourceDrafts.set(activeEngine, form.elements.content.value);
      activeEngine = engineInput.value;
      form.elements.content.value = sourceDrafts.get(activeEngine) ?? initial(activeEngine);
    });
    form.addEventListener("input", () => ctx.setDirty(true));
    const box = ctx.dialog(config?.name || "新建配置", form, { wide: true });
    box.onRequestClose = requestClose;
    async function requestClose() {
      const values = new FormData(form);
      const changed = ["name", "description", "content"].some(key => String(values.get(key) || "") !== String(baseline[key] || ""));
      if (form.getAttribute("aria-busy") === "true") return;
      if (changed && !await ctx.confirm("配置尚未保存，确定放弃更改？", "放弃更改")) return;
      ctx.setDirty(false);
      box.close();
    }
  }
  async function history(config) {
    const records = list(await ctx.api(`/configs/${pathID(config.id)}/revisions?limit=100`));
    if (!ctx.current()) return;
    const preview = node("div", {}, muted("选择一份记录预览"));
    const content = stack(
      table(["保存时间", "名称", "状态", "操作"], records.map(record => [
        date(record.updated_at), record.name, record.version === config.version ? badge("当前") : muted("历史档案"),
        button("预览", event => void ctx.mutate(event.currentTarget, async () => {
          const selected = await ctx.api(`/configs/${pathID(config.id)}/revisions/${record.version}`);
          if (!ctx.current()) return;
          const restore = ctx.can("configs.restore") && record.version !== config.version && button("恢复此档案", async event => {
            const control = event.currentTarget;
            if (!await ctx.confirm(`恢复 ${date(selected.updated_at)} 的配置？本次恢复会按当前时间新增记录，原档案保留。`, "恢复配置") || !ctx.current()) return;
            await ctx.mutate(control, async () => {
              const saved = await ctx.api(`/configs/${pathID(config.id)}/revisions/${selected.version}/restore`, {
                method: "POST", body: { expected_version: config.version },
              });
              Object.assign(config, saved);
              ctx.announce("配置已恢复");
              box.close();
              draw();
            });
          }, { tone: "primary" });
          preview.replaceChildren(stack(node("h3", {}, `${selected.name} · ${date(selected.updated_at)}`),
            code(selected.content), restore && actions(restore)));
        })),
      ]), { label: "配置保存历史" }), preview);
    const box = ctx.dialog(`${config.name} · 保存历史`, content, { wide: true });
  }
  function deliver(config) {
    const eligible = list(agents).filter(agent => agent.status === "online" &&
      (agent.capabilities || []).includes(config.engine) && agent.runtime?.[config.engine]?.installed);
    const form = ctx.form([
      field("目标节点", "agent_id", "", { required: true, options: [["", "选择节点"], ...eligible.map(agent => [agent.id, agent.name])] }),
      field("执行方式", "action", "validate", { options: [["validate", "仅校验"], ["deploy", "部署并重启"]] }),
    ], "提交任务", async values => {
      if (values.action === "deploy" && !await ctx.confirm("部署此配置并重启目标内核？将替换目标实例当前配置。", "部署并重启")) return;
      if (!ctx.current()) return;
      const task = await ctx.api("/tasks", { method: "POST", body: {
        ...values, engine: config.engine, config_id: config.id, expected_config_version: config.version,
      } });
      box.close();
      ctx.announce("任务已提交");
      await taskDetail(ctx, task.id);
    });
    const box = ctx.dialog(`${config.name} · 校验与部署`, eligible.length ? form : muted("没有在线且已安装对应内核的可用节点"));
  }
  function drawTemplates() {
    templateResults.replaceChildren(table(["名称", "内核", "保存时间", "操作"], list(templates).map(template => [
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
        }),
        ctx.can("templates.delete") && button("删除", async event => {
          const control = event.currentTarget;
          if (!await ctx.confirm(`删除模板“${template.name}”？`, "删除模板") || !ctx.current()) return;
          await ctx.mutate(control, async () => {
            await ctx.api(`/templates/${pathID(template.id)}`, { method: "DELETE" });
            templates.splice(templates.indexOf(template), 1);
            drawTemplates();
          });
        }, { tone: "danger" })),
    ]), { label: "配置模板" }));
  }
  draw();
  drawTemplates();
  const addTemplate = ctx.can("templates.write") && button("新建模板", () => {
    const form = ctx.form([
      field("模板名称", "name", "", { required: true, maxlength: 100 }),
      field("内核", "engine", "mihomo", { options: engines }),
      node("div", { className: "a-editor" }, field("模板正文", "content", "", { multiline: 12, required: true, spellcheck: false })),
      muted("支持 {{node_name}}、{{node_id}}、{{lan_ip}}、{{random_port}}"),
    ], "保存模板", async values => {
      const saved = await ctx.api("/templates", { method: "POST", body: values });
      templates.unshift(saved);
      box.close();
      drawTemplates();
      ctx.announce("模板已保存");
    });
    const box = ctx.dialog("新建模板", form, { wide: true });
  });
  ctx.pageActions(...(ctx.can("configs.write") ? [button("新建配置", () => edit(), { tone: "primary" })] : []));
  return stack(section(null, results),
    ctx.can("templates.read") && section("配置模板", templateResults, addTemplate));
}
