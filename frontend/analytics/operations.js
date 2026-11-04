import { formatDate } from "./appearance.js";
import { aggregateDashboardTrafficDays, utcMonth } from "../modules/dashboard-model.js";
import { diagnosticError, redactDiagnostic } from "../modules/errors.js";
import { node, button, actions, stack, grid, section, table, badge, field, code, muted, link, query, pathID, list, notice } from "./ui.js";

export const bytes = value => {
  const amount = Math.max(0, Number(value) || 0);
  const exponent = amount ? Math.max(0, Math.min(4, Math.floor(Math.log(amount) / Math.log(1024)))) : 0;
  return `${(amount / 1024 ** exponent).toLocaleString("zh-CN", { maximumFractionDigits: exponent ? 1 : 0 })} ${["B", "KB", "MB", "GB", "TB"][exponent]}`;
};
export const date = formatDate;
export const status = value => badge(({ online: "在线", offline: "离线", active: "运行中", inactive: "已停止", pending: "准备中", running: "执行中", succeeded: "成功", failed: "失败", canceled: "已取消", accepted: "已接受", rejected: "已拒绝" })[value] || value || "未知",
  ["online", "active", "succeeded", "accepted"].includes(value) ? "ok" : ["pending", "running"].includes(value) ? "warn" : ["offline", "failed"].includes(value) ? "bad" : "");
export const actionName = value => ({
  validate: "校验配置", deploy: "部署并重启", start: "启动服务", stop: "停止服务", restart: "重启服务", status: "查询状态",
  install: "安装内核", uninstall: "卸载内核", "read-config": "读取可导入配置", "read-managed-config": "读取托管配置",
  "import-existing": "导入现有服务", "upgrade-agent": "升级 Agent", "enable-bbr": "启用 BBR",
  "disable-bbr": "关闭 BBR", "configure-tcp": "配置 TCP", "ip-quality": "IP 质量检测",
})[value] || value;

function responsiveChart(days, signal) {
  const container = node("div", { className: "a-chart-container" });
  const render = width => container.replaceChildren(chart(days, Math.max(280, width)));
  render(Math.max(280, innerWidth - (innerWidth > 896 ? 256 : 0) - 96));
  const observer = new ResizeObserver(entries => render(entries[0].contentRect.width));
  observer.observe(container);
  signal.addEventListener("abort", () => observer.disconnect(), { once: true });
  return container;
}

function chart(days, width) {
  const height = 235, left = 62, right = 18, top = 16, bottom = 27;
  const max = Math.max(3, ...days.map(day => day.received_bytes + day.sent_bytes));
  const points = days.map((day, index) => [left + index / Math.max(1, days.length - 1) * (width - left - right),
    height - bottom - (day.received_bytes + day.sent_bytes) / max * (height - top - bottom)]);
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("viewBox", `0 0 ${width} ${height}`);
  svg.setAttribute("class", "a-chart");
  svg.setAttribute("role", "img");
  svg.setAttribute("aria-label", "本月每日接收与发送流量总和");
  const add = (tag, attrs, text) => {
    const element = document.createElementNS(svg.namespaceURI, tag);
    Object.entries(attrs).forEach(([key, value]) => element.setAttribute(key, value));
    if (text) element.textContent = text;
    svg.append(element);
  };
  for (let i = 0; i < 4; i++) {
    const y = top + i / 3 * (height - top - bottom);
    add("path", { d: `M${left} ${y}H${width - right}`, stroke: "#e4e4e7", "stroke-dasharray": "3 4" });
    add("text", { x: left - 8, y: y + 4, "text-anchor": "end" }, bytes(max * (1 - i / 3)));
  }
  if (points.length) {
    const line = points.map(([x, y], index) => `${index ? "L" : "M"}${x.toFixed(1)} ${y.toFixed(1)}`).join(" ");
    add("path", { d: `${line}L${points.at(-1)[0]} ${height - bottom}H${left}Z`, fill: "#e4e4e7", opacity: ".7" });
    add("path", { d: line, stroke: "#3f3f46", "stroke-width": "2", fill: "none" });
    days.forEach((day, index) => {
      if (index % 5 === 0 || index === days.length - 1)
        add("text", { x: points[index][0], y: height - 6, "text-anchor": "middle" }, day.day.slice(5));
    });
  }
  return svg;
}

export async function dashboardPage(ctx) {
  const month = utcMonth();
  const [overview, agents, tasks, traffic] = await Promise.all([
    ctx.api("/overview"), ctx.can("agents.read") ? ctx.api("/agents") : [],
    ctx.can("tasks.read") ? ctx.api("/tasks?limit=7") : [],
    ctx.can("traffic.read") ? ctx.api(query("/traffic-usage", { month })) : null,
  ]);
  const stat = (title, value, subtitle, glyph) => node("section", { className: "a-card a-stat" },
    node("div", { className: "a-stat-heading" }, muted(title), badge(glyph)),
    node("strong", {}, value), muted(subtitle));
  const cards = node("div", { className: "a-stat-grid" },
    stat("在线节点", overview.agents_online ?? 0, `共 ${overview.agents ?? 0} 个节点`, "实时"),
    stat("节点配置", overview.node_configs ?? 0, "当前账号可见配置", "配置"),
    stat("执行中任务", overview.tasks_running ?? 0, `等待执行 ${overview.tasks_pending ?? 0} 项`, "任务"),
    stat("失败任务", overview.tasks_failed ?? 0, "可在任务页查看详情", "状态"));
  const days = traffic ? aggregateDashboardTrafficDays(traffic.days, month).filter(day => day.day <= new Date().toISOString().slice(0, 10)) : [];
  const trafficCard = traffic && section("流量趋势", node("div", { className: "a-card-body" },
    responsiveChart(days, ctx.signal), node("div", { className: "a-chart-meta" }, muted(`${month} · UTC`),
      muted(`接收 ${bytes(days.reduce((sum, day) => sum + day.received_bytes, 0))}`),
      muted(`发送 ${bytes(days.reduce((sum, day) => sum + day.sent_bytes, 0))}`))), link("查看流量", "#traffic"));
  const nodes = ctx.can("agents.read") && section("节点状态", table(["节点", "状态", "内核", "最近心跳"], list(agents).map(agent => [
    node("a", { href: `#node-settings?agent=${pathID(agent.id)}` }, agent.name),
    status(agent.status), (agent.capabilities || []).join(" · ") || "—", date(agent.last_seen),
  ]), { label: "节点状态" }), link("管理节点", "#node-settings"));
  const activity = ctx.can("tasks.read") && section("最近任务", table(["操作", "节点", "内核", "状态", "创建时间"],
    list(tasks).map(task => [actionName(task.action), list(agents).find(agent => agent.id === task.agent_id)?.name || task.agent_id,
      task.engine, status(task.status), date(task.created_at)]), { label: "最近任务" }), link("全部任务", "#tasks"));
  return stack(cards, trafficCard, nodes, activity);
}

export async function taskDetail(ctx, taskID) {
  if (!ctx.can("tasks.read")) return;
  let task = await ctx.api(`/tasks/${pathID(taskID)}`);
  if (!ctx.current()) return;
  const state = status(task.status), failure = notice(diagnosticError(task.error || ""));
  const output = code(redactDiagnostic(task.output) || "暂无输出");
  const content = stack(actions(state, badge(task.engine), badge(actionName(task.action))),
    muted(`节点 ${task.agent_id} · ${date(task.created_at)}`), failure, output);
  const box = ctx.dialog("任务详情", content, { wide: true });
  let timer, reading = false;
  const stop = () => clearTimeout(timer);
  const active = () => ctx.current() && box.open;
  const refresh = button("刷新结果", event => void ctx.mutate(event.currentTarget, read));
  content.append(actions(refresh));
  async function read() {
    if (reading || !active()) return;
    reading = true;
    try {
      const latest = await ctx.api(`/tasks/${pathID(taskID)}`);
      if (!active()) return;
      task = latest;
      const next = status(task.status);
      if (state.textContent !== next.textContent) { state.textContent = next.textContent; state.className = next.className; }
      const error = diagnosticError(task.error || "");
      if (failure.textContent !== error) failure.textContent = error;
      if (output.textContent !== (redactDiagnostic(task.output) || "暂无输出")) {
        const follow = output.scrollHeight-output.scrollTop-output.clientHeight<24;
        output.textContent = redactDiagnostic(task.output) || "暂无输出";
        if(follow)output.scrollTop=output.scrollHeight;
      }
    } finally { reading = false; }
  }
  async function poll() {
    try { if (!document.hidden && active()) await read(); }
    catch (error) { if (active() && error.name !== "AbortError") failure.textContent = error.message; }
    finally { if (active() && ["pending","running"].includes(task.status)) timer=setTimeout(poll,2000); }
  }
  box.addEventListener("close", stop, {once:true});
  ctx.signal.addEventListener("abort", stop, {once:true});
  if(["pending","running"].includes(task.status))timer=setTimeout(poll,2000);
}

export async function tasksPage(ctx) {
  const filters = ctx.state.data.taskFilters || { limit: "100" };
  const filter = ctx.form([
    node("div", { className: "a-form-grid" },
      field("状态", "status", filters.status || "", { options: [["", "全部"], ["pending", "准备中"], ["running", "执行中"], ["succeeded", "成功"], ["failed", "失败"], ["canceled", "已取消"]] }),
      field("节点 ID", "agent_id", filters.agent_id || ""),
      field("操作", "action", filters.action || "", { options: [["", "全部操作"], ...["validate", "deploy", "install", "uninstall", "start", "stop", "restart", "status", "upgrade-agent", "ip-quality", "enable-bbr", "disable-bbr", "configure-tcp"].map(action => [action, actionName(action)])] }),
      field("条数", "limit", filters.limit || "100", { type: "number", min: 1, max: 500 })),
  ], "查询", async values => { ctx.state.data.taskFilters = values; draw(await ctx.api(query("/tasks", values))); });
  const results = node("div");
  async function editRetry(task) {
    const resource = await ctx.api(`/tasks/${pathID(task.id)}/retry-editor`);
    if (!ctx.current()) return;
    const { config } = resource;
    const form = ctx.form([
      muted(`${config.name} · ${date(config.updated_at)}`),
      task.error && notice(diagnosticError(task.error)),
      config.agent_id && link("在实时配置中编辑", `#live-config?agent=${pathID(config.agent_id)}&engine=${pathID(config.engine)}`),
      node("div", { className: "a-editor" }, field("当前配置", "content", config.content, { multiline: 18, required: true, spellcheck: false })),
    ], `保存并重试${task.action === "deploy" ? "部署" : "校验"}`, async values => {
      if (!await ctx.confirm("保存当前修改并重新执行任务？部署会重启对应内核。", "保存并重试") || !ctx.current()) return;
      const result = await ctx.api(`/tasks/${pathID(task.id)}/edit-retry`, { method: "POST", body: { content: values.content, expected_version: config.version } });
      if (!ctx.current()) return;
      ctx.setDirty(false); box.close(); ctx.announce("配置已保存，重试任务已提交");
      await taskDetail(ctx, result.task.id);
    });
    form.addEventListener("input", () => ctx.setDirty(true));
    const box = ctx.dialog("编辑后重试", form, { wide: true });
    box.onRequestClose = async () => {
      if (form.elements.content.value !== config.content && !await ctx.confirm("放弃未保存的配置修改？", "放弃更改") || !ctx.current()) return;
      ctx.setDirty(false); box.close();
    };
  }
  function draw(tasks) {
    results.replaceChildren(table(["操作", "节点", "内核", "状态", "创建时间", "操作"], list(tasks).map(task => [
      actionName(task.action), task.agent_id, task.engine, status(task.status), date(task.created_at),
      actions(button("详情", () => void ctx.mutate(null, () => taskDetail(ctx, task.id))),
        ctx.can("tasks.execute") && ["failed", "canceled"].includes(task.status) && button("重试", async event => {
          const control = event.currentTarget;
          if (!await ctx.confirm("重新执行此任务？请先核对原任务结果。", "重试任务") || !ctx.current()) return;
          await ctx.mutate(control, async () => {
            const retry = await ctx.api(`/tasks/${pathID(task.id)}/retry`, { method: "POST", body: {} });
            ctx.announce("已提交重试任务");
            await taskDetail(ctx, retry.id);
          });
        }),
        ctx.can("tasks.execute") && task.config_id && ["validate", "deploy"].includes(task.action) && ["failed", "canceled"].includes(task.status) &&
          (ctx.can("agent-config.write") || ctx.can("configs.write")) && button("编辑后重试", event => void ctx.mutate(event.currentTarget, () => editRetry(task))),
        ctx.can("tasks.execute") && ["pending", "running"].includes(task.status) && button("取消", async event => {
          const control = event.currentTarget;
          if (!await ctx.confirm("取消此任务？已在节点执行的操作可能仍需核实结果。", "取消任务") || !ctx.current()) return;
          await ctx.mutate(control, async () => {
            await ctx.api(`/tasks/${pathID(task.id)}`, { method: "DELETE" });
            ctx.announce("已提交取消请求");
            draw(await ctx.api(query("/tasks", ctx.state.data.taskFilters || filters)));
          });
        }, { tone: "danger" })),
    ]), { label: "任务" }));
  }
  draw(await ctx.api(query("/tasks", filters)));
  return stack(section(null, node("div", { className: "a-card-body" }, filter)), section(null, results));
}

export async function quotaPage(ctx) {
  const access = await ctx.api("/agent-access");
  if (!access.isolated) return section(null, node("div", { className: "a-card-body" }, muted("按账号权限访问节点")));
  return grid(list(access.shares).map(share => section(share.agent_name, node("div", { className: "a-card-body" }, stack(
    muted(`所有者 · ${share.owner_username || "管理员"}`),
    node("strong", {}, `${bytes(share.used_bytes)} / ${share.limit_bytes ? bytes(share.limit_bytes) : "不限量"}`),
    muted(`内核 ${(share.engines || []).join(" · ") || "未分配"}`),
    muted(`端口 ${JSON.stringify(share.ports || [])}`),
    muted(`HTTP-01 域名 ${(share.http_domains || []).join(" · ") || "未授权"}`),
    share.enabled && ["pending", "accepted"].includes(share.status) && actions(
      share.status === "pending" && button("接受邀请", event => respond(event.currentTarget, "accept", share), { tone: "primary" }),
      button(share.status === "pending" ? "拒绝邀请" : "退出共享", event => respond(event.currentTarget, "reject", share), { tone: "danger" })),
  )), status(share.status))));
  async function respond(control, decision, share) {
    if (!await ctx.confirm(`确定${decision === "accept" ? "接受" : "拒绝或退出"}此共享？`, decision === "accept" ? "接受邀请" : "退出共享")) return;
    if (!ctx.current()) return;
    await ctx.mutate(control, async () => {
      await ctx.api(`/agent-access/${pathID(share.id)}/response`, { method: "POST", body: { decision, revision: share.invitation_revision } });
      ctx.announce("共享状态已更新");
    });
  }
}
