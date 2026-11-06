import { actions, badge, field, muted, notice, node } from "./ui.js";

export function inboundAccessState({ agent, engine, entry, siblings, can }) {
  const shared = Boolean(agent.shared_allocation);
  const editable = can('agent-config.write') && can('tasks.execute') && agent.status === 'online' && !(shared && engine === 'ss-rust');
  const multiRust = engine === 'ss-rust' && siblings.length > 1;
  return { shared, editable, multiRust, destinationEditable: editable && !multiRust,
    sourceEditable: editable && (entry.kind !== 'wireguard' || Boolean(entry.block_mainland_source)) };
}

// Entry is read from the authenticated API and matched to the current saved
// workspace before opening; never derive a resource from labels or drafts.
export function editInboundAccess(ctx, { agent, engine, entry, siblings, beforeDeploy, onSaved }) {
  const { shared, editable, multiRust, destinationEditable, sourceEditable } = inboundAccessState({ agent, engine, entry, siblings, can: ctx.can });
  const form = ctx.form([
    actions(badge(agent.name), badge(engine), badge(shared ? "共享实例" : "主实例"), badge(`端口 ${entry.port}`)),
    muted(engine === "ss-rust" ? "大陆来源限制通过节点防火墙按端口生效；目标限制由同一 ssserver 进程统一加载。" : "限制作用于当前实例的此入站，保存后需部署生效。"),
    shared && engine === "ss-rust" && notice("Shadowsocks Rust 共享实例不支持节点范围的大陆访问策略，请由节点所有者管理。"),
    multiRust && notice("Shadowsocks Rust 多端口配置不能启用大陆目标限制；需拆分为单独进程。"),
    field("禁止访问大陆目标", "block_mainland_destination", entry.block_mainland_destination, { type: "checkbox", disabled: !destinationEditable }),
    field("禁止大陆来源连接", "block_mainland_source", entry.block_mainland_source, { type: "checkbox", disabled: !sourceEditable }),
    entry.kind === "wireguard" && notice("WireGuard 仅能识别隧道内来源地址，已有来源限制请关闭；公网来源限制须在节点防火墙配置。", "warn"),
    node("input", {type:"hidden", name:"intent", value:"deploy"}),
  ], "保存并部署", async (values, element) => {
    if (!editable) return;
    if (!await ctx.confirm(`保存并部署 ${agent.name} · ${engine} · ${shared ? "当前共享实例" : "当前主实例"} 的访问限制？会重启此实例内核${engine === "ss-rust" ? "并短暂影响同一 ssserver 的全部端口" : ""}。`, "保存并部署")) return;
    await beforeDeploy?.();
    if (!ctx.current()) return;
    const result = await ctx.api("/access-controls", { method: "PUT", body: {
      agent_id: agent.id, engine, tag: entry.tag, port: entry.port,
      expected_version: entry.config_version, intent: "deploy",
      block_mainland_destination: element.elements.block_mainland_destination.checked,
      block_mainland_source: element.elements.block_mainland_source.checked,
    } });
    if (!ctx.current()) return;
    ctx.setDirty(false);
    await onSaved(result, box);
  });
  if (!editable) form.querySelector('button[type="submit"]').remove();
  form.addEventListener("input", () => ctx.setDirty(true));
  const box = ctx.dialog(`入站访问限制 · ${entry.tag}`, form);
  box.onRequestClose = async () => {
    if (form.getAttribute("aria-busy") === "true") return;
    const changed = form.elements.block_mainland_destination.checked !== entry.block_mainland_destination ||
      form.elements.block_mainland_source.checked !== entry.block_mainland_source;
    if (changed && !await ctx.confirm("放弃未保存的访问限制更改？", "放弃更改")) return;
    if (!ctx.current()) return;
    ctx.setDirty(false);
    box.close();
  };
}
