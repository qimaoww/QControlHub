import { mergeTrafficPorts, trafficPolicyEditable, eligibleAgents, policyStatus, trafficRateForDisplay, trafficCardIdentity } from "../modules/traffic-model.js";
import { node, button, actions, stack, section, table, badge, field, muted, list, pathID, notice, query } from "./ui.js";
import { bytes, date } from "./operations.js";

export async function trafficPage(ctx) {
  const [agents, policyItems, endpoints] = await Promise.all([
    ctx.can("agents.read") ? ctx.api("/agents") : [], ctx.api("/traffic-policies"), ctx.api("/traffic-endpoints"),
  ]);
  const policies = list(policyItems);
  const byID = new Map(list(agents).map(agent => [agent.id, agent]));
  const search = field("搜索端口", "search", "", { type: "search", placeholder: "节点、名称、内核或端口" });
  const scope = field("用户范围", "scope", "current", { options: [["current", "当前用户"], ["all", "全部获授权资源"]] });
  const results = node("div");
  const summary = node("div", { className: "a-card-body a-actions" });
  function edit(policy = null, endpoint = null) {
    const target = policy || endpoint;
    const available = eligibleAgents(list(agents));
    const agentSelect = field("节点", "agent_id", target?.agent_id || available[0]?.id || "", {
      options: available.map(agent => [agent.id, agent.name]), disabled: Boolean(policy), required: true,
    });
    const engineSelect = field("内核", "engine", target?.engine || "mihomo", { options: ["mihomo", "xray", "sing-box", "ss-rust"], disabled: Boolean(policy), required: true });
    const port = field("端口", "port", target?.port || "", { type: "number", min: 1, max: 65535, required: true, readonly: Boolean(policy) });
    const form = ctx.form([
      node("div", { className: "a-form-grid" }, agentSelect, engineSelect, port,
        field("名称", "name", target?.name || "", { required: true, maxlength: 100 }),
        field("协议", "protocol", target?.protocol || "both", { options: [["both", "TCP + UDP"], ["tcp", "TCP"], ["udp", "UDP"]] }),
        field("周期", "cycle", policy?.cycle || "monthly", { options: [["monthly", "每月"], ["yearly", "每年"]] }),
        field("周期起始日", "cycle_anchor", (policy?.cycle_anchor || new Date().toISOString()).slice(0, 10), { type: "date", required: true }),
        field("配额（GiB）", "limit_gb", (policy?.limit_bytes || 0) / 1024 ** 3, { type: "number", min: 0.000001, step: "any", required: true })),
      field("超额自动封禁", "auto_block", policy?.auto_block ?? true, { type: "checkbox" }),
    ], "保存配额", async (values, element) => {
      const limit = Math.round(Number(values.limit_gb) * 1024 ** 3);
      if (!Number.isSafeInteger(limit) || limit < 1) throw new Error("请输入有效配额。");
      const payload = { agent_id: policy?.agent_id || values.agent_id, engine: policy?.engine || values.engine,
        port: Number(values.port), name: values.name, protocol: values.protocol, cycle: values.cycle,
        cycle_anchor: `${values.cycle_anchor}T00:00:00Z`, limit_bytes: limit, auto_block: element.elements.auto_block.checked };
      const saved = await ctx.api(policy ? `/traffic-policies/${pathID(policy.id)}` : "/traffic-policies", {
        method: policy ? "PUT" : "POST", body: payload,
      });
      if (policy) Object.assign(policy, saved);
      else policies.push(saved);
      box.close();
      draw();
      ctx.announce("配额已保存");
    });
    const box = ctx.dialog(policy ? "编辑端口配额" : "添加端口配额", form);
  }
  function draw() {
    const q = search.querySelector("input").value.toLowerCase();
    const all = scope.querySelector("select").value === "all";
    const items = mergeTrafficPorts(policies, list(endpoints)).filter(item => {
      const value = item.policy || item.endpoint;
      return (all || value.is_current_user === true) &&
        [value.agent_id, byID.get(value.agent_id)?.name, value.name, value.engine, value.port].join(" ").toLowerCase().includes(q);
    });
    summary.replaceChildren(badge(`${items.length} 个端口`), muted(`已用 ${bytes(items.reduce((sum, item) => sum + Number(item.policy?.used_bytes || 0), 0))}`));
    results.replaceChildren(table(["节点 / 归属", "端口", "内核", "状态", "已用 / 配额", "速率", "操作"], items.map(item => {
      const value = item.policy || item.endpoint;
      const agent = byID.get(value.agent_id);
      const writable = item.policy && trafficPolicyEditable(value, agent, ctx.can);
      const rates = item.policy ? {
        receiveBPS: trafficRateForDisplay(value.receive_bps, value.last_reported_at, agent?.status),
        sendBPS: trafficRateForDisplay(value.send_bps, value.last_reported_at, agent?.status),
      } : null;
      return [
        stack(agent?.name || value.agent_id, muted(value.owner_name || (value.is_current_user ? "当前用户" : "其他用户"))),
        stack(node("strong", {}, `:${value.port}`), muted(value.name)), badge(value.engine),
        item.policy ? badge(...policyStatus(value, agent)) : badge("等待同步", "warn"),
        item.policy ? `${bytes(value.used_bytes)} / ${value.quota_enabled === false ? "持续统计" : bytes(value.limit_bytes)}` : "—",
        rates ? stack(`接收 ${bytes(rates.receiveBPS)}/s`, muted(`发送 ${bytes(rates.sendBPS)}/s`)) : "—",
        actions(
          writable && button("编辑配额", () => edit(value)),
          !item.policy && ctx.can("traffic.manage", agent) && button("设置配额", () => edit(null, value)),
          writable && button("重置计数", async event => {
            const control = event.currentTarget;
            if (!await ctx.confirm("重置此端口当前周期的用量计数？", "重置计数") || !ctx.current()) return;
            await ctx.mutate(control, async () => {
              const saved = await ctx.api(`/traffic-policies/${pathID(value.id)}/reset`, { method: "POST" });
              Object.assign(value, saved);
              draw();
              ctx.announce("计数已重置");
            });
          }),
          writable && value.quota_enabled !== false && button("移除配额", async event => {
            const control = event.currentTarget;
            if (!await ctx.confirm("移除此端口配额？流量统计继续保留。", "移除配额") || !ctx.current()) return;
            await ctx.mutate(control, async () => {
              await ctx.api(`/traffic-policies/${pathID(value.id)}`, { method: "DELETE" });
              value.quota_enabled = false;
              draw();
              ctx.announce("配额已移除");
            });
          }, { tone: "danger" }),
          writable && button("停止监控", async event => {
            const control = event.currentTarget;
            if (!await ctx.confirm("停止此端口的流量监控并移除配额？", "停止监控") || !ctx.current()) return;
            await ctx.mutate(control, async () => {
              await ctx.api(`/traffic-policies/${pathID(value.id)}/monitoring`, { method: "DELETE" });
              policies.splice(policies.indexOf(value), 1);
              draw();
            });
          }, { tone: "danger" })),
      ];
    }), { label: "端口流量" }));
  }
  draw();
  for (const control of [search.querySelector("input"), scope.querySelector("select")]) control.addEventListener(control.tagName === "INPUT" ? "input" : "change", draw);
  const sync = ctx.can("traffic.manage") && button("同步配置端口", event => void ctx.mutate(event.currentTarget, async () => {
    const preview = await ctx.api("/traffic-endpoints/sync");
    const choices = list(preview.candidates);
    const selected = new Set();
    const form = ctx.form([
      table(["选择", "节点", "端口", "内核", "变更", "状态"], choices.map((entry, index) => {
        const toggle = field("同步", `port:${index}`, false, { type: "checkbox", disabled: Boolean(entry.blocked_reason) });
        toggle.querySelector("input").addEventListener("change", event => event.target.checked ? selected.add(index) : selected.delete(index));
        return [toggle, byID.get(entry.agent_id)?.name || entry.agent_id, entry.port, entry.engine,
          ({ new: "新增", changed: "变更", removed: "移除", unchanged: "无变更", deleted: "已删除" })[entry.kind] || entry.kind,
          entry.blocked_reason ? notice(entry.blocked_reason, "warn") : muted(entry.deployment_status)];
      }), { label: "端口同步预览" }),
    ], "同步所选端口", async () => {
      if (!selected.size) throw new Error("请选择要同步的端口。");
      const selections = [...selected].map(index => ({ agent_id: choices[index].agent_id, port: choices[index].port }));
      const result = await ctx.api("/traffic-endpoints/sync", { method: "POST", body: { snapshot: preview.snapshot, selections } });
      box.close();
      ctx.announce("端口同步已提交");
    });
    const box = ctx.dialog("同步配置端口", form, { wide: true });
  }));
  return stack(section("流量筛选", node("div", { className: "a-card-body a-form-grid" }, search, scope)),
    section("端口流量", stack(summary, results), actions(sync,
      ctx.can("traffic.manage") && button("添加配额", () => edit(), { tone: "primary", disabled: !eligibleAgents(list(agents)).length }))));
}
