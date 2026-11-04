import { systemBBRState, validateTCPSelection, validateTCPAvailability } from "../modules/system-bbr-model.js";
import { node, button, actions, stack, section, table, badge, field, muted, list, code } from "./ui.js";
import { date, taskDetail } from "./operations.js";

export async function tcpPage(ctx) {
  const [agents, rules, tasks] = await Promise.all([
    ctx.api("/agents"), ctx.api("/system-tcp/parameters"),
    ctx.can("tasks.read") ? ctx.api("/system-tcp/tasks") : [],
  ]);
  const latest = new Map(list(tasks).map(task => [task.agent_id, task]));
  return section(null, table(["节点", "状态", "拥塞控制", "队列", "最近任务", "操作"], list(agents).map(agent => {
    const current = systemBBRState(agent), metrics = agent.metrics?.bbr || {};
    const busy = ["pending", "running"].includes(latest.get(agent.id)?.status);
    const editable = ctx.can("agents.manage", agent) && ctx.can("tasks.execute") && current.controllable && !busy;
    const submit = async (control, action, settings) => {
      if (!editable || !await ctx.confirm(`对 ${agent.name} 执行 TCP 参数变更？会影响此主机的新连接。`, "提交 TCP 变更") || !ctx.current()) return;
      await ctx.mutate(control, async () => {
        const fresh = list(await ctx.api("/agents")).find(item => item.id === agent.id);
        if (!fresh || !systemBBRState(fresh).controllable || !ctx.can("agents.manage", fresh)) throw new Error("节点状态已变化，请刷新后重试。");
        if (settings) validateTCPAvailability(settings, fresh.metrics?.bbr?.parameters);
        const task = await ctx.api("/tasks", { method: "POST", body: { agent_id: agent.id, action, tcp_settings: settings } });
        latest.set(agent.id, task);
        ctx.announce("TCP 调优任务已提交");
        if (ctx.can("tasks.read")) await taskDetail(ctx, task.id);
      });
    };
    return [
      agent.name, badge(current.text, current.tone), metrics.congestion_control || "—", metrics.default_qdisc || "—",
      latest.has(agent.id) ? date(latest.get(agent.id).created_at) : "—",
      actions(button("启用 BBR", event => submit(event.currentTarget, "enable-bbr"), { disabled: !editable }),
        button("切换 CUBIC", event => submit(event.currentTarget, "disable-bbr"), { disabled: !editable }),
        button("自定义参数", () => {
          const parameters = metrics.parameters || {};
          const rows = list(rules).map(rule => {
            const enabled = field(rule.label || rule.key, `select:${rule.key}`, false, { type: "checkbox", disabled: !Object.hasOwn(parameters, rule.key) });
            const input = field(rule.key, rule.key, parameters[rule.key] || "", { options: rule.choices?.length ? rule.choices : undefined,
              disabled: !Object.hasOwn(parameters, rule.key), hint: rule.description });
            return stack(enabled, input);
          });
          const form = ctx.form(rows, "提交参数", async (values, element) => {
            const selected = Object.fromEntries(list(rules).filter(rule => element.elements.namedItem(`select:${rule.key}`).checked).map(rule => [rule.key, values[rule.key]]));
            const settings = validateTCPSelection(selected, rules);
            const fresh = list(await ctx.api("/agents")).find(item => item.id === agent.id);
            if (!fresh || !systemBBRState(fresh).controllable) throw new Error("节点状态已变化，请刷新后重试。");
            validateTCPAvailability(settings, fresh.metrics?.bbr?.parameters);
            if (!await ctx.confirm("应用所选 TCP 参数？更改会影响此主机的新连接。", "提交 TCP 变更") || !ctx.current()) return;
            const task = await ctx.api("/tasks", { method: "POST", body: { agent_id: agent.id, action: "configure-tcp", tcp_settings: settings } });
            box.close();
            ctx.announce("TCP 调优任务已提交");
            if (ctx.can("tasks.read")) await taskDetail(ctx, task.id);
          });
          const box = ctx.dialog(`${agent.name} · TCP 参数`, form, { wide: true });
        }, { disabled: !editable }),
        button("查看参数", () => ctx.dialog(`${agent.name} · 系统参数`, code(JSON.stringify(metrics.parameters || {}, null, 2))))),
    ];
  }), { label: "TCP 调优" }));
}
