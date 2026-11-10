import { createPoller } from "../modules/poller.js";
import { taskDetail } from "./operations.js";
import { node, actions, badge, button, muted, notice, stack, code, pathID } from "./ui.js";

// The workspace resolves ownership, exact saved revision and actual deployment
// receipts on every visit; polling only paints these small, persistent nodes.
export function deploymentStatus(ctx, { base, workspace, getConfig, isPreview=()=>false, agent, engine, onWorkspace }) {
  const state = badge(""), service = badge(""), feedback = notice("");
  const configState = actions(muted("配置部署"), state);
  const element = node("div", {className:"a-actions", "data-deployment-status":""},
    configState, actions(muted("内核运行"), service));
  feedback.hidden = true;
  const ports = new Set();
  let latest = workspace.deployment || {}, displayedConfig = workspace.config;
  function paintPort({control, normal, enabled}) {
    const current = displayedConfig?.id === getConfig()?.id && displayedConfig?.version === getConfig()?.version;
    const failed = current && latest.state === "failed";
    control.hidden = !failed;
    normal.hidden = failed;
    const labels = {pending:"待部署", deploying:latest.task_status === "pending" ? "等待执行" : "部署中", canceled:"部署已取消", unknown:"部署结果待确认"};
    const label = !current ? "部署结果待确认" : latest.state === "applied" ? enabled === undefined ? "—" : enabled ? "启用" : "停用" : labels[latest.state] || "尚未部署";
    if (normal.textContent !== label) normal.textContent = label;
    normal.className = `a-badge${current && latest.state === "applied" ? "" : " warn"}`;
    configState.hidden = ports.size > 0 || latest.state === "applied";
  }
  async function failure(port) {
    const config = getConfig(), taskID = latest.task_id;
    if (!taskID || !ctx.can("tasks.read")) return;
    const task = await ctx.api(`/tasks/${pathID(taskID)}`);
    if (!ctx.current()) return;
    if (config !== getConfig() || taskID !== latest.task_id || latest.state !== "failed" ||
        task.status !== "failed" || task.action !== "deploy" || task.agent_id !== agent.id || task.engine !== engine ||
        task.config_id !== config?.id || task.config_version !== config?.version ||
        Boolean(task.shared_instance) !== Boolean(agent.shared_allocation))
      throw new Error("部署状态已变化，请重新核对当前端口的结果。");
    await taskDetail(ctx, taskID, {title:`端口 ${port || "无固定端口"} · 部署失败`});
  }
  function paint(value = {}) {
    if (isPreview()) value = {...value,state:"pending",task_id:undefined,task_status:undefined};
    latest = value; displayedConfig = getConfig();
    const label = { pending: "待部署", deploying: "部署中", failed: "部署失败", canceled: "部署已取消", applied: "已部署", unknown: "部署结果待确认" }[value.state] || "尚未部署";
    const taskLabel = { pending: "等待执行", running: "执行中" }[value.task_status];
    const next = taskLabel ? `${label} · ${taskLabel}` : label;
    if (state.textContent !== next) state.textContent = next;
    state.className = `a-badge${value.state === "applied" ? " ok" : value.state === "failed" ? " bad" : ["pending", "deploying", "canceled", "unknown"].includes(value.state) ? " warn" : ""}`;
    const running = { active: "运行中", inactive: "已停止", failed: "服务异常" }[value.service_status] || "状态未知";
    if (service.textContent !== running) service.textContent = running;
    service.className = `a-badge${value.service_status === "active" ? " ok" : value.service_status === "failed" ? " bad" : ""}`;
    configState.hidden = ports.size > 0 || value.state === "applied";
    ports.forEach(paintPort);
  }
  paint(workspace.deployment);
  const poller = createPoller({
    isActive: ctx.current, delay: () => 3000,
    async run() {
      if (document.hidden) return;
      const config = getConfig();
      try {
        const fresh = await ctx.api(`${base}/workspace`);
        if (!ctx.current() || config !== getConfig()) return;
        // Concurrent source updates must not claim the editor's unsaved or old
        // source was deployed. Keep the draft and require an explicit reload.
        if (fresh.config?.id !== config?.id || fresh.config?.version !== config?.version) {
          feedback.textContent = "配置已由其他操作更新，请保存或重新读取后核对部署状态。";
          feedback.hidden = false;
          return;
        }
        onWorkspace?.(fresh);
        paint(fresh.deployment);
        if (feedback.textContent) feedback.textContent = "";
        feedback.hidden = true;
      } catch (error) {
        if (ctx.current() && error.name !== "AbortError") {
          feedback.textContent = `部署状态更新失败，保留上次结果，稍后重试。${error.message}`;
          feedback.hidden = false;
        }
      }
    },
  });
  ctx.signal.addEventListener("abort", () => poller.stop(), { once: true });
  poller.start();
  return {
    element, feedback, refresh: () => poller.trigger(), resetPorts: () => ports.clear(),
    inboundState(port, enabled) {
      const normal = badge("");
      const control = ctx.can("tasks.read")
        ? button("部署失败", event => void ctx.mutate(event.currentTarget, () => failure(port)), {tone:"danger", "aria-label":`端口 ${port || "无固定端口"}部署失败，查看原因`})
        : badge("部署失败", "bad");
      control.className = "a-badge bad";
      const entry = {control, normal, enabled};
      ports.add(entry); paintPort(entry);
      return actions(normal, control);
    },
  };
}
