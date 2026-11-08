import { node, button, actions, stack, section, table, badge, field, muted, notice } from "./ui.js";
import { date } from "./operations.js";

const types = [["telegram", "Telegram Bot"], ["webhook", "Webhook"], ["smtp", "SMTP 邮件"], ["bark", "Bark"], ["ntfy", "ntfy"], ["gotify", "Gotify"]];
const fields = {
  telegram: [["token", "Bot Token", true], ["chat_id", "Chat ID"], ["message_thread_id", "会话主题 ID"], ["direct_messages_topic_id", "私信主题 ID"]],
  webhook: [["url", "接收地址", true], ["authorization", "Authorization", true], ["secret", "HMAC-SHA256 签名密钥", true]],
  smtp: [["host", "SMTP 主机"], ["port", "端口"], ["tls", "安全连接"], ["username", "用户名"], ["password", "密码", true], ["from", "发件邮箱"], ["to", "收件邮箱"]],
  bark: [["url", "服务地址", true], ["device_key", "设备 Key", true]],
  ntfy: [["url", "服务地址", true], ["topic", "主题"], ["token", "访问 Token", true]],
  gotify: [["url", "服务地址", true], ["token", "应用 Token", true]],
};
const statusNames = { pending: "等待投递", sending: "投递中", accepted: "平台已接受", failed: "投递失败", canceled: "已取消" };
const id = () => crypto.randomUUID().replaceAll("-", "");

export async function notificationsPage(ctx) {
  const [resource, agents, policies] = await Promise.all([
    ctx.api("/settings/notifications"), ctx.can("agents.read") ? ctx.api("/agents") : [], ctx.can("traffic.read") ? ctx.api("/traffic-policies") : [],
  ]);
  let config = resource.configuration;
  const writable = ctx.can("settings.manage");
  const feedback = notice("", "info");
  const channelList = node("div"), ruleList = node("div");
  const records = table(["时间", "事件", "渠道", "状态", "尝试", "反馈"], [], { label: "通知投递", emptyText: "暂无投递记录" });
  let recordSequence = 0, templateDirty = false, modalDirty = false;
  const syncDirty = () => { if (ctx.current()) ctx.setDirty(templateDirty || modalDirty); };
  function protectDraft(box, form) {
    const changed = () => { modalDirty = true; syncDirty(); };
    form.addEventListener("input", changed); form.addEventListener("change", changed);
    box.onRequestClose = async () => { if (!modalDirty || await ctx.confirm("放弃当前草稿？", "关闭编辑")) box.close(); };
    box.addEventListener("close", () => { modalDirty = false; syncDirty(); }, { once: true });
  }
  const save = async next => {
    let saved;
    try { saved = await ctx.api("/settings/notifications", { method: "PUT", body: next }); }
    catch (error) {
      if (error.status === 409 && ctx.current()) { const latest = await ctx.api("/settings/notifications"); if (ctx.current()) { config = latest.configuration; draw(); } }
      throw error;
    }
    if (!ctx.current()) return false;
    config = saved; draw(); return true;
  };
  const change = mutate => { const next = structuredClone(config); mutate(next); return save(next); };
  function editChannel(channel = null) {
    const typeField = field("渠道类型", "type", channel?.type || "telegram", { options: types, disabled: Boolean(channel) });
    const dynamic = node("div", { className: "a-stack" });
    const type = typeField.querySelector("select");
    function drawFields() {
      dynamic.replaceChildren(...fields[type.value].map(([name, label, secret]) => stack(
        field(label, name, secret ? "" : channel?.values?.[name] || (name === "port" ? "587" : ""), {
          ...(name === "tls" ? { options: [["starttls", "STARTTLS"], ["tls", "TLS"]] } : { type: secret ? "password" : "text", maxlength: secret ? 2000 : 500, autocomplete: "off" }),
          hint: secret && channel?.configured?.includes(name) ? "已配置，留空保留" : "",
        }), secret && channel?.configured?.includes(name) ? field(`清除${label}`, `clear:${name}`, false, { type: "checkbox" }) : null)));
    }
    type.addEventListener("change", drawFields); drawFields();
    const form = ctx.form([
      field("名称", "name", channel?.name || "", { required: true, maxlength: 100 }), typeField,
      field("启用", "enabled", channel?.enabled ?? true, { type: "checkbox" }), dynamic,
    ], "保存渠道", async (values, element) => {
      const next = { id: channel?.id || id(), name: values.name, type: type.value, enabled: element.elements.enabled.checked, values: {}, secrets: {} };
      for (const [key, , secret] of fields[type.value]) {
        if (secret) { if (element.elements.namedItem(`clear:${key}`)?.checked) next.secrets[key] = ""; else if (values[key]) next.secrets[key] = values[key]; }
        else next.values[key] = values[key];
      }
      if (await change(c => { c.channels = [...c.channels.filter(v => v.id !== next.id), next]; })) { box.close(); ctx.announce("渠道已保存"); }
    });
    const box = ctx.dialog(channel ? "编辑通知渠道" : "添加通知渠道", form); protectDraft(box, form);
  }
  function editRule(rule = null) {
    const eventField = field("事件", "event", rule?.event || "agent.offline", { options: Object.entries(resource.events).filter(([key]) => key !== "speed.recovered") });
    const event = eventField.querySelector("select");
    const specific = node("div", { className: "a-stack" });
    function drawSpecific() {
      const speed = event.value.startsWith("speed.");
      specific.replaceChildren(...[
        event.value !== "panel.update" ? field("节点", "agent_id", rule?.agent_id || "", { required: speed, options: [["", speed ? "选择节点" : "所有有权接收的节点"], ...agents.filter(a => !speed || a.can_manage).map(a => [a.id, a.name])], hint: speed ? "主机网卡速率，需 QAgent 支持有效速率采样" : "" }) : null,
        ["traffic.warning", "traffic.quota"].includes(event.value) ? field("端口统计对象", "policy_id", rule?.policy_id || "", { required: event.value === "traffic.warning", options: [["", "选择统计对象"], ...policies.filter(p => p.monitoring_enabled).map(p => [p.id, `${agents.find(a => a.id === p.agent_id)?.name || p.agent_id} · ${p.engine} · ${p.port} · ${p.name}`])], hint: "按端口计费口径；共享端口使用所属分配的用量和限额" }) : null,
        event.value === "traffic.warning" ? field("预警比例（%）", "threshold", rule?.threshold || 80, { type: "number", min: 1, max: 100, step: "any", required: true }) : null,
        speed ? field("方向", "direction", rule?.direction || "upload", { options: [["upload", "上传（TX）"], ["download", "下载（RX）"], ["total", "上传 + 下载"]] }) : null,
        speed ? field("异常阈值（B/s）", "threshold", rule?.threshold || (event.value === "speed.low" ? 1000 : 10000000), { type: "number", min: 1, step: "any", required: true }) : null,
        speed ? field("恢复阈值（B/s）", "recovery", rule?.recovery ?? (event.value === "speed.low" ? 2000 : 8000000), { type: "number", min: 0, step: "any", required: true }) : null,
        speed ? muted("1 KB/s = 1000 B/s；1 MB/s = 1000000 B/s；1 bit/s = 0.125 B/s。") : null,
        speed ? field("持续时间（秒）", "duration_seconds", rule?.duration_seconds || 60, { type: "number", min: 10, max: 86400 }) : null,
        speed ? field("冷却时间（秒）", "cooldown_seconds", rule?.cooldown_seconds || 300, { type: "number", min: 60, max: 604800 }) : null,
        event.value === "panel.update" ? field("检查周期（秒）", "interval_seconds", rule?.interval_seconds || 86400, { type: "number", min: 3600, max: 604800 }) : null,
      ].filter(Boolean));
    }
    event.addEventListener("change", drawSpecific); drawSpecific();
    const form = ctx.form([
      field("名称", "name", rule?.name || "", { required: true, maxlength: 100 }), eventField,
      field("启用", "enabled", rule?.enabled ?? false, { type: "checkbox" }), specific,
      section("接收渠道", node("div", { className: "a-card-body a-stack" }, config.channels.map(c => field(c.name, `channel:${c.id}`, rule?.channels?.includes(c.id), { type: "checkbox" })))),
    ], "保存规则", async (values, element) => {
      const next = { id: rule?.id || id(), name: values.name, event: event.value, enabled: element.elements.enabled.checked,
        agent_id: values.agent_id || "", policy_id: values.policy_id || "", direction: values.direction || "", channels: config.channels.filter(c => element.elements.namedItem(`channel:${c.id}`).checked).map(c => c.id),
        threshold: Number(values.threshold || 0), recovery: Number(values.recovery || 0), duration_seconds: Number(values.duration_seconds || 30), cooldown_seconds: Number(values.cooldown_seconds || 300), interval_seconds: Number(values.interval_seconds || 86400) };
      if (await change(c => { c.rules = [...c.rules.filter(v => v.id !== next.id), next]; })) { box.close(); ctx.announce("规则已保存"); }
    });
    const box = ctx.dialog(rule ? "编辑通知规则" : "添加通知规则", form); protectDraft(box, form);
  }
  async function removeChannel(ch, control) {
    if (!await ctx.confirm(`删除“${ch.name}”？仅引用此渠道的规则将停用。`, "删除通知渠道") || !ctx.current()) return;
    await ctx.mutate(control, async () => { await change(c => { c.channels = c.channels.filter(v => v.id !== ch.id); c.rules = c.rules.map(r => ({ ...r, channels: r.channels.filter(id => id !== ch.id), enabled: r.enabled && r.channels.some(id => id !== ch.id) })); }); });
  }
  function draw() {
    channelList.replaceChildren(table(["名称", "类型", "状态", "操作"], config.channels.map(ch => [ch.name, badge(types.find(([t]) => t === ch.type)?.[1]), badge(ch.enabled ? "启用" : "停用"), writable ? actions(
      button("编辑", () => editChannel(ch)), button(ch.enabled ? "停用" : "启用", event => void ctx.mutate(event.currentTarget, () => change(c => { c.channels.find(v => v.id === ch.id).enabled = !ch.enabled; }))),
      button("测试已保存配置", event => void ctx.mutate(event.currentTarget, async () => {
        const result = await ctx.api("/settings/notifications/test", { method: "POST", body: { channel_id: ch.id, revision: config.revision } });
        if (!ctx.current()) return; feedback.textContent = `测试已入队（${result.id}），投递结果见下方记录。`; await loadRecords();
      }), { disabled: !ch.enabled }), button("删除", event => void removeChannel(ch, event.currentTarget), { tone: "danger" })) : null,
    ]), { label: "通知渠道", emptyText: "尚未添加通知渠道" }));
    ruleList.replaceChildren(table(["名称", "事件 / 范围", "接收渠道", "状态", "操作"], config.rules.map(r => [r.name,
      stack(resource.events[r.event], muted(r.policy_id ? (() => { const p = policies.find(p => p.id === r.policy_id); return p ? `${agents.find(a => a.id === p.agent_id)?.name || p.agent_id} · ${p.engine} · ${p.port} · ${p.name}` : "统计对象已不可用"; })() : agents.find(a => a.id === r.agent_id)?.name || (r.agent_id ? "节点已不可用" : r.event === "panel.update" ? "面板" : "有权接收的资源"))),
      r.channels.map(key => config.channels.find(c => c.id === key)?.name || "已删除").join("、") || "未选择", badge(r.enabled ? "启用" : "停用"), writable ? actions(
        button("编辑", () => editRule(r)), button(r.enabled ? "停用" : "启用", event => void ctx.mutate(event.currentTarget, () => change(c => { c.rules.find(v => v.id === r.id).enabled = !r.enabled; }))),
        button("删除", async event => { const control = event.currentTarget; if (await ctx.confirm(`删除“${r.name}”？`, "删除通知规则") && ctx.current()) await ctx.mutate(control, () => change(c => { c.rules = c.rules.filter(v => v.id !== r.id); })); }, { tone: "danger" })) : null,
    ]), { label: "通知规则", emptyText: "尚未添加通知规则" }));
  }
  const templateEvent = field("事件", "template_event", "agent.offline", { options: Object.entries(resource.events) });
  const selection = templateEvent.querySelector("select");
  const title = field("标题", "title", "", { maxlength: 400, required: true, disabled: !writable });
  const body = field("正文", "body", "", { multiline: 8, maxlength: 12000, required: true, disabled: !writable });
  const preview = node("pre", { className: "a-code", role: "status" });
  let previewSequence = 0;
  function loadTemplate() { previewSequence++; const t = config.templates?.[selection.value] || resource.defaults[selection.value]; title.querySelector("input").value = t.title; body.querySelector("textarea").value = t.body; preview.textContent = ""; templateDirty = false; syncDirty(); }
  selection.addEventListener("change", async () => { if (templateDirty && !await ctx.confirm("放弃当前模板草稿？", "切换模板")) { selection.value = templateForm.dataset.event; return; } templateForm.dataset.event = selection.value; loadTemplate(); });
  const templateForm = ctx.form([templateEvent, title, body,
    node("details", { className: "a-disclosure" }, node("summary", {}, "模板变量"), node("div", { className: "a-disclosure-body" }, table(["变量", "含义"], resource.variables.map(v => [`{{${v}}}`, resource.variable_descriptions[v]]), { label: "模板变量" }))),
    muted("不适用的变量显示为 —。标题和正文按渠道长度限制截断，均使用纯文本。"),
    actions(button("渲染预览", event => void ctx.mutate(event.currentTarget, async () => {
      const sequence = ++previewSequence, selected = selection.value;
      const result = await ctx.api("/settings/notifications/preview", { method: "POST", body: { event: selected, template: { title: title.querySelector("input").value, body: body.querySelector("textarea").value } } });
      if (ctx.current() && sequence === previewSequence && selected === selection.value) preview.textContent = `脱敏示例\n${result.title}\n${result.body}`;
    })), writable && button("恢复默认", event => void ctx.mutate(event.currentTarget, async () => {
      if (await change(c => { delete c.templates[selection.value]; })) { loadTemplate(); ctx.announce("已恢复默认模板"); }
    }))), preview,
  ], "保存模板", async values => { if (await change(c => { c.templates ||= {}; c.templates[selection.value] = { title: values.title, body: values.body }; })) { templateDirty = false; syncDirty(); ctx.announce("模板已保存"); } });
  templateForm.dataset.event = selection.value;
  templateForm.addEventListener("input", () => { templateDirty = true; syncDirty(); previewSequence++; });
  if (!writable) templateForm.querySelector('button[type="submit"]').remove();
  loadTemplate();
  async function loadRecords() {
    const sequence = ++recordSequence;
    try {
      const result = await ctx.api("/settings/notifications/deliveries");
      if (!ctx.current() || sequence !== recordSequence) return;
      records.updateRows(result.map(d => [date(d.created_at), d.event.test ? "测试" : resource.events[d.event.type] || d.event.type,
        config.channels.find(c => c.id === d.channel_id)?.name || "已删除", badge(statusNames[d.status] || d.status, d.status === "failed" ? "bad" : d.status === "accepted" ? "ok" : "warn"), String(d.attempts), d.error || "—"]));
    } catch (error) { if (!ctx.current() || sequence !== recordSequence) return; if (error.status === 403) records.updateRows([]); feedback.textContent = `读取投递记录失败：${error.message}`; }
  }
  ctx.onRefresh(loadRecords);
  draw(); await loadRecords();
  return stack(
    section("通知渠道", node("div", { className: "a-card-body a-stack" }, feedback, channelList), writable && button("添加渠道", () => editChannel())),
    section("通知规则", node("div", { className: "a-card-body a-stack" }, muted("新规则先建立当前状态基线；速度异常与恢复均需满足持续窗口。"), ruleList), writable && button("添加规则", () => editRule())),
    section("通知模板", node("div", { className: "a-card-body" }, node("details", { className: "a-disclosure" }, node("summary", {}, "编辑事件模板"), node("div", { className: "a-disclosure-body" }, templateForm)))),
    section("投递记录", node("div", { className: "a-card-body a-stack" }, muted("保留最近 14 天记录，每个账号最多 5000 条，当前显示最近 100 条。"), records), button("刷新记录", event => void ctx.mutate(event.currentTarget, loadRecords))),
    section("通知服务状态", node("div", { className: "a-card-body" }, table(["项目", "状态"], [
      ["原有 Webhook 默认签名", badge(resource.webhook_signing_configured ? "已配置" : "未配置", resource.webhook_signing_configured ? "ok" : "warn")],
    ], { label: "通知服务状态" }))),
  );
}
