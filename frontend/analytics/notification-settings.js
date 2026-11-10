import { readRegion, loadingTable, loadingForm } from "./loading.js";
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

export function notificationsPage(ctx) {
  let agents = [], policies = [], resource, ready = false, targetsReady = !ctx.can('agents.read') && !ctx.can('traffic.read');
  const channelHost = node('div', {}, loadingTable(['名称', '类型', '状态', '操作'], '通知渠道'));
  const ruleHost = node('div', {}, loadingTable(['名称', '事件 / 范围', '接收渠道', '状态', '操作'], '通知规则'));
  const templateHost = node('div', { className: 'a-card-body' }, loadingForm(['事件', '格式', '标题', '正文'], '保存模板'));
  const serviceHost = loadingTable(['项目', '状态'], '通知服务状态');
  let openChannel = () => {}, openRule = () => {}, refreshRecords = () => {}, acceptRecords;
  let deliverySnapshot;
  const addChannel = ctx.can('settings.manage') && button('添加渠道', () => openChannel(), { disabled: true });
  const addRule = ctx.can('settings.manage') && button('添加规则', () => openRule(), { disabled: true });
  const recordTable = loadingTable(['时间', '事件', '渠道', '状态', '尝试', '反馈'], '通知投递');
  const deliveriesRead = readRegion(ctx, '投递记录', signal => ctx.api('/settings/notifications/deliveries', {signal}), value => {
    deliverySnapshot = value;
    if (acceptRecords) acceptRecords(value);
    else recordTable.updateRows(value.map(d => [date(d.created_at), d.event.type, '正在读取渠道…', badge(statusNames[d.status] || d.status), String(d.attempts), d.error || '—']));
  }, {clear() { deliverySnapshot = null; recordTable.updateRows([]); }});
  const recordHost = stack(deliveriesRead.element, recordTable);
  void deliveriesRead.load();
  const page = stack(section('通知渠道', channelHost, addChannel), section('通知规则', ruleHost, addRule),
    section('通知模板', templateHost), section('投递记录', recordHost, button('刷新记录', () => void refreshRecords())), section('通知服务状态', serviceHost));
  ctx.mount?.(page);
  const targetRead = readRegion(ctx, '通知目标选项', async signal => {
    await Promise.all([
      ctx.can('agents.read') && ctx.api('/agents', { signal }).then(value => { if (ctx.current()) agents = value; }),
      ctx.can('traffic.read') && ctx.api('/traffic-policies', { signal }).then(value => { if (ctx.current()) policies = value; }),
    ]);
  }, () => { targetsReady = true; if (addRule) addRule.disabled = !ready; });
  const configRead = readRegion(ctx, '通知配置', signal => ctx.api('/settings/notifications', { signal }), value => { resource = value; initialize(); }, { clear() { ready = false; if (addChannel) addChannel.disabled = true; if (addRule) addRule.disabled = true; channelHost.replaceChildren(); ruleHost.replaceChildren(); templateHost.replaceChildren(); } });
  page.prepend(configRead.element, targetRead.element);
  void targetRead.load();
  void configRead.load();
  ctx.onRefresh(() => Promise.all([configRead.load(), targetRead.load()]));
  return page;
  function initialize() {
  if (!ctx.current() || !resource) return;
  openChannel = editChannel; openRule = editRule; refreshRecords = loadRecords;
  ready = true; if (addChannel) addChannel.disabled = false; if (addRule) addRule.disabled = !targetsReady;
  let config = resource.configuration;
  const writable = ctx.can("settings.manage");
  const feedback = notice("", "info");
  const channelList = node("div"), ruleList = node("div");
  const records = recordTable;
  let recordSequence = 0, refreshSequence = 0, templateDirty = false, modalDirty = false, activeModal = null, pollTimer, activeTestID, hasPending = false, recordError = false;
  ctx.signal.addEventListener("abort", () => clearTimeout(pollTimer), { once: true });
  const syncDirty = () => { if (ctx.current()) ctx.setDirty(templateDirty || modalDirty); };
  function protectDraft(box, form) {
    activeModal = box;
    const changed = () => { modalDirty = true; syncDirty(); };
    form.addEventListener("input", changed); form.addEventListener("change", changed);
    box.onRequestClose = async () => { if (!modalDirty || await ctx.confirm("放弃当前草稿？", "关闭编辑")) box.close(); };
    box.addEventListener("close", () => { activeModal = null; modalDirty = false; syncDirty(); }, { once: true });
  }
  const save = async next => {
    let saved;
    try { saved = await ctx.api("/settings/notifications", { method: "PUT", body: next }); }
    catch (error) {
      if (error.status === 409) throw new Error("通知配置已变化，请刷新后重试。当前草稿已保留。");
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
  async function testSaved(channelID, event) {
    const result = await ctx.api("/settings/notifications/test", { method: "POST", body: { channel_id: channelID, revision: config.revision, ...(event ? { event } : {}) } });
    if (!ctx.current()) return;
    activeTestID = result.id; hasPending = true; feedback.textContent = "测试已入队，正在等待投递结果。"; await loadRecords();
  }
  function draw() {
    channelList.replaceChildren(table(["名称", "类型", "状态", "操作"], config.channels.map(ch => [ch.name, badge(types.find(([t]) => t === ch.type)?.[1]), badge(ch.enabled ? "启用" : "停用"), writable ? actions(
      button("编辑", () => editChannel(ch)), button(ch.enabled ? "停用" : "启用", event => void ctx.mutate(event.currentTarget, () => change(c => { c.channels.find(v => v.id === ch.id).enabled = !ch.enabled; }))),
      button("测试已保存配置", event => void ctx.mutate(event.currentTarget, () => testSaved(ch.id)), { disabled: !ch.enabled }), button("删除", event => void removeChannel(ch, event.currentTarget), { tone: "danger" })) : null,
    ]), { label: "通知渠道", emptyText: "尚未添加通知渠道" }));
    ruleList.replaceChildren(table(["名称", "事件 / 范围", "接收渠道", "状态", "操作"], config.rules.map(r => [r.name,
      stack(resource.events[r.event], muted(r.policy_id ? (() => { const p = policies.find(p => p.id === r.policy_id); return p ? `${agents.find(a => a.id === p.agent_id)?.name || p.agent_id} · ${p.engine} · ${p.port} · ${p.name}` : "统计对象已不可用"; })() : agents.find(a => a.id === r.agent_id)?.name || (r.agent_id ? "节点已不可用" : r.event === "panel.update" ? "面板" : "有权接收的资源"))),
      r.channels.map(key => config.channels.find(c => c.id === key)?.name || "已删除").join("、") || "未选择", badge(r.enabled ? "启用" : "停用"), writable ? actions(
        button("编辑", () => editRule(r), { disabled: !targetsReady }), button(r.enabled ? "停用" : "启用", event => void ctx.mutate(event.currentTarget, () => change(c => { c.rules.find(v => v.id === r.id).enabled = !r.enabled; }))),
        button("删除", async event => { const control = event.currentTarget; if (await ctx.confirm(`删除“${r.name}”？`, "删除通知规则") && ctx.current()) await ctx.mutate(control, () => change(c => { c.rules = c.rules.filter(v => v.id !== r.id); })); }, { tone: "danger" })) : null,
    ]), { label: "通知规则", emptyText: "尚未添加通知规则" }));
    const selected = testChannel.value;
    testChannel.replaceChildren(node("option", { value: "" }, "选择测试渠道"), ...config.channels.filter(ch => ch.enabled).map(ch => node("option", { value: ch.id }, ch.name)));
    testChannel.value = config.channels.some(ch => ch.enabled && ch.id === selected) ? selected : "";
  }
  const templateEvent = field("事件", "template_event", "agent.offline", { options: Object.entries(resource.events) });
  const selection = templateEvent.querySelector("select");
  const title = field("标题", "title", "", { maxlength: 400, required: true, disabled: !writable });
  const body = field("正文", "body", "", { multiline: 8, maxlength: 12000, required: true, disabled: !writable });
  const formatField = field("格式", "template_format", "text", { options: [["markdown", "Markdown"], ["text", "纯文本"]], disabled: !writable });
  const format = formatField.querySelector("select");
  const preview = node("div", { className: "a-stack", role: "status" });
  const testChannelField = field("测试渠道", "template_test_channel", "", { options: [["", "选择测试渠道"]] });
  const testChannel = testChannelField.querySelector("select");
  let previewSequence = 0;
  function loadTemplate() { previewSequence++; const t = config.templates?.[selection.value] || resource.defaults[selection.value]; title.querySelector("input").value = t.title; body.querySelector("textarea").value = t.body; format.value = t.format || "text"; preview.textContent = ""; templateDirty = false; syncDirty(); }
  selection.addEventListener("change", async () => { if (templateDirty && !await ctx.confirm("放弃当前模板草稿？", "切换模板")) { selection.value = templateForm.dataset.event; return; } templateForm.dataset.event = selection.value; loadTemplate(); });
  function showPreview(result) {
    let offset = result.title.length + 2;
    const lines = result.body.split("\n").map(line => {
      const parts = []; let cursor = 0;
      for (const entity of result.entities || []) {
        const start = Math.max(0, entity.offset - offset), end = Math.min(line.length, entity.offset + entity.length - offset);
        if (end <= start) continue;
        parts.push(line.slice(cursor, start));
        const tag = { bold: "strong", italic: "em", strikethrough: "s", code: "code", pre: "code", text_link: "a" }[entity.type] || "span";
        const attrs = tag === "a" && /^https?:\/\//i.test(entity.url || "") ? { href: entity.url, target: "_blank", rel: "noopener noreferrer" } : {};
        parts.push(node(tag, attrs, line.slice(start, end))); cursor = end;
      }
      parts.push(line.slice(cursor)); offset += line.length + 1;
      return node("p", {}, line ? parts : node("br"));
    });
    preview.replaceChildren(muted("脱敏示例"), node("p", {}, node("strong", {}, result.title)), node("div", {}, lines));
  }
  const templateForm = ctx.form([templateEvent, formatField, title, body,
    node("details", { className: "a-disclosure" }, node("summary", {}, "模板变量"), node("div", { className: "a-disclosure-body" }, table(["变量", "含义"], resource.variables.map(v => [`{{${v}}}`, resource.variable_descriptions[v]]), { label: "模板变量" }))),
    muted("Markdown 支持 **粗体**、*斜体*、~~删除线~~、行内代码、代码块和链接。Telegram 保留格式，其他渠道使用纯文本。不适用的变量显示为 —。"),
    actions(button("渲染预览", event => void ctx.mutate(event.currentTarget, async () => {
      const sequence = ++previewSequence, selected = selection.value;
      const result = await ctx.api("/settings/notifications/preview", { method: "POST", body: { event: selected, template: { title: title.querySelector("input").value, body: body.querySelector("textarea").value, format: format.value } } });
      if (ctx.current() && sequence === previewSequence && selected === selection.value) showPreview(result);
    })), writable && button("恢复默认", event => void ctx.mutate(event.currentTarget, async () => {
      if (await change(c => { delete c.templates[selection.value]; })) { loadTemplate(); ctx.announce("已恢复默认模板"); }
    }))), preview,
    writable && stack(testChannelField, actions(button("测试已保存模板", event => void ctx.mutate(event.currentTarget, async () => {
      if (templateDirty) throw new Error("请先保存模板，再测试。");
      if (!testChannel.value) throw new Error("请选择测试渠道。");
      await testSaved(testChannel.value, selection.value);
    })))),
  ], "保存模板", async values => { if (await change(c => { c.templates ||= {}; c.templates[selection.value] = { title: values.title, body: values.body, format: values.template_format }; })) { templateDirty = false; syncDirty(); ctx.announce("模板已保存"); } });
  templateForm.dataset.event = selection.value;
  templateForm.addEventListener("input", event => { if (!event.target.matches('[name="title"],[name="body"],[name="template_format"]')) return; templateDirty = true; syncDirty(); previewSequence++; preview.textContent = ""; });
  if (!writable) templateForm.querySelector('button[type="submit"]').remove();
  loadTemplate();
  function renderRecords(result) {
      records.updateRows(result.map(d => [node("div", { "data-delivery-id": d.id }, node("div", {}, date(d.created_at)), muted(`更新：${date(d.updated_at)}`)), badge(d.event.test ? `测试${resource.events[d.event.type] ? ` · ${resource.events[d.event.type]}` : ""}` : resource.events[d.event.type] || d.event.type),
        config.channels.find(c => c.id === d.channel_id)?.name || "已删除", stack(node("div", {}, badge(statusNames[d.status] || d.status, d.status === "failed" ? "bad" : d.status === "accepted" ? "ok" : "warn")),
          d.status === "pending" && d.attempts > 0 ? muted(`重试：${date(d.next_at)}`) : null), String(d.attempts), d.error || "—"]));
      if (recordError && !activeTestID) feedback.textContent = "";
      recordError = false;
      const test = result.find(d => d.id === activeTestID);
      if (test) {
        feedback.textContent = test.status === "accepted" ? "测试消息已被平台接受。" : test.status === "failed" || test.status === "canceled" ? `测试${statusNames[test.status]}：${test.error || "请检查渠道配置"}` : test.attempts > 0 && test.status === "pending" ? `测试暂未成功，将于 ${date(test.next_at)} 重试。` : "测试正在投递，请稍候。";
        if (!["pending", "sending"].includes(test.status)) activeTestID = null;
      }
      hasPending = result.some(d => ["pending", "sending"].includes(d.status));
      if (hasPending) pollTimer = setTimeout(() => void loadRecords(), 3000);
  }
  acceptRecords = renderRecords;
  if (deliverySnapshot) renderRecords(deliverySnapshot);
  async function loadRecords() {
    clearTimeout(pollTimer);
    const sequence = ++recordSequence;
    try {
      await deliveriesRead.load();
      if (!ctx.current() || sequence !== recordSequence) return;
      // The region applies this independently of configuration and targets.
    } catch (error) {
      if (!ctx.current() || sequence !== recordSequence) return;
      if (error.status === 403) { records.updateRows([]); hasPending = false; activeTestID = null; }
      else if (hasPending) pollTimer = setTimeout(() => void loadRecords(), 10000);
      recordError = true; feedback.textContent = `读取投递记录失败：${error.message}`;
    }
  }
  async function refreshConfiguration() {
    const sequence = ++refreshSequence, revision = config.revision;
    const latest = await Promise.all([
      ctx.api("/settings/notifications"), ctx.can("agents.read") ? ctx.api("/agents") : [], ctx.can("traffic.read") ? ctx.api("/traffic-policies") : [],
    ]);
    if (!ctx.current() || sequence !== refreshSequence || config.revision !== revision) return;
    if (latest[0].configuration.revision !== config.revision) {
      if ((templateDirty || activeModal) && !await ctx.confirm("通知配置已更新，载入最新配置将放弃当前草稿。", "刷新通知配置")) return loadRecords();
      if (!ctx.current() || sequence !== refreshSequence || config.revision !== revision) return;
      activeModal?.close();
      [resource, agents, policies] = latest; config = resource.configuration;
      draw(); loadTemplate(); templateForm.querySelector(":scope > .a-notice").textContent = "";
    } else {
      [resource, agents, policies] = latest;
    }
    await loadRecords();
  }
  ctx.onRefresh(refreshConfiguration);
  draw();
  const rendered = stack(
    section("通知渠道", node("div", { className: "a-card-body a-stack" }, feedback, channelList), writable && button("添加渠道", () => editChannel())),
    section("通知规则", node("div", { className: "a-card-body a-stack" }, muted("新规则先建立当前状态基线；速度异常与恢复均需满足持续窗口。"), ruleList), writable && button("添加规则", () => editRule())),
    section("通知模板", node("div", { className: "a-card-body" }, node("details", { className: "a-disclosure" }, node("summary", {}, "编辑事件模板"), node("div", { className: "a-disclosure-body" }, templateForm)))),
    section("投递记录", node("div", { className: "a-card-body a-stack" }, muted("保留最近 14 天记录，每个账号最多 5000 条，当前显示最近 100 条。"), records), button("刷新记录", event => void ctx.mutate(event.currentTarget, loadRecords))),
    section("通知服务状态", node("div", { className: "a-card-body" }, table(["项目", "状态"], [
      ["原有 Webhook 默认签名", badge(resource.webhook_signing_configured ? "已配置" : "未配置", resource.webhook_signing_configured ? "ok" : "warn")],
    ], { label: "通知服务状态" }))),
  );
  const sections = [...rendered.children];
  channelHost.replaceChildren(...sections[0].querySelector('.a-card-body').childNodes);
  ruleHost.replaceChildren(...sections[1].querySelector('.a-card-body').childNodes);
  templateHost.replaceChildren(...sections[2].querySelector('.a-card-body').childNodes);
  recordHost.replaceChildren(deliveriesRead.element, ...sections[3].querySelector('.a-card-body').childNodes);
  serviceHost.replaceWith(sections[4].querySelector('.a-card-body'));
  }
}
