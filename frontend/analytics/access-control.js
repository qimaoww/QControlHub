import { node, button, actions, stack, section, table, badge, field, muted, list, notice } from "./ui.js";
import { status, taskDetail } from "./operations.js";

export async function accessControlPage(ctx) {
  const entries = list(await ctx.api("/access-controls"));
  const search = field("搜索入站", "search", "", { type: "search", placeholder: "节点、内核、入站或端口" });
  const results = node("div");
  function draw() {
    const value = search.querySelector("input").value.toLowerCase();
    const shown = entries.filter(entry => [entry.agent_name, entry.engine, entry.tag, entry.port].join(" ").toLowerCase().includes(value));
    results.replaceChildren(table(["节点", "内核", "入站", "端口", "状态", "大陆目标", "大陆来源", "操作"], shown.map(entry => [
      entry.agent_name || entry.agent_id, entry.engine, entry.tag, entry.port, status(entry.agent_status),
      badge(entry.block_mainland_destination ? "禁止" : "允许"), badge(entry.block_mainland_source ? "禁止" : "允许"),
      button("设置限制", () => edit(entry)),
    ]), { label: "访问限制" }));
  }
  function edit(entry) {
    const editable = ctx.can("agent-config.write") && ctx.can("tasks.execute") && entry.agent_status === "online";
    const form = ctx.form([
      actions(badge(entry.engine), badge(`端口 ${entry.port}`), status(entry.agent_status)),
      muted(entry.engine === "ss-rust" ? "目标限制会应用于同一 ssserver 的全部入站；来源限制按当前端口生效。" : "限制仅作用于当前入站或端口。"),
      field("禁止访问大陆目标", "block_mainland_destination", entry.block_mainland_destination, { type: "checkbox", disabled: !editable }),
      field("禁止大陆来源连接", "block_mainland_source", entry.block_mainland_source, { type: "checkbox",
        disabled: !editable || (entry.kind === "wireguard" && !entry.block_mainland_source) }),
      entry.kind === "wireguard" && notice("WireGuard 仅能识别隧道内来源地址，公网来源限制请在节点防火墙配置。", "warn"),
      field("执行方式", "intent", "validate", { options: [["validate", "保存并校验"], ["deploy", "保存并部署"]], disabled: !editable }),
    ], "提交", async (values, element) => {
      if (!editable) return;
      if (values.intent === "deploy" && !await ctx.confirm(`保存并部署 ${entry.tag} 的访问限制？`, "保存并部署")) return;
      if (!ctx.current()) return;
      const result = await ctx.api("/access-controls", { method: "PUT", body: {
        agent_id: entry.agent_id, engine: entry.engine, tag: entry.tag, port: entry.port,
        expected_version: entry.config_version, intent: values.intent,
        block_mainland_destination: element.elements.block_mainland_destination.checked,
        block_mainland_source: element.elements.block_mainland_source.checked,
      } });
      ctx.setDirty(false);
      box.close();
      ctx.announce("访问限制已保存，任务已提交");
      if (ctx.can("tasks.read")) await taskDetail(ctx, result.task.id);
    });
    if (!editable) form.querySelector('button[type="submit"]').remove();
    form.addEventListener("input", () => ctx.setDirty(true));
    const box = ctx.dialog(`${entry.agent_name} · ${entry.tag}`, form);
    box.onRequestClose = async () => {
      const changed = form.elements.block_mainland_destination.checked !== entry.block_mainland_destination ||
        form.elements.block_mainland_source.checked !== entry.block_mainland_source;
      if (changed && !await ctx.confirm("放弃未保存的访问限制更改？", "放弃更改")) return;
      ctx.setDirty(false);
      box.close();
    };
  }
  draw();
  search.querySelector("input").addEventListener("input", draw);
  return stack(section("入站筛选", node("div", { className: "a-card-body" }, search)), section("访问限制", results));
}
