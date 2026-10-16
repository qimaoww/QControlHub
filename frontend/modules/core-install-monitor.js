import { createTaskMonitor, taskTerminal } from "./task-monitor.js";

export function createCoreInstallMonitor({ api, state, can, notify, refreshAgentPage }) {
  const monitorInstall = createTaskMonitor({
    read: id => api(`/tasks/${encodeURIComponent(id)}?view=status`),
    session: () => state.data,
  });
  const installFeedback = (agentID, engine, message) => {
    const form = [...document.querySelectorAll(".core-version-form")].find(
      item => item.dataset.versionAgent === agentID && item.dataset.versionEngine === engine,
    );
    const status = form?.querySelector("[data-core-install-status]");
    state.data.coreInstallFeedback ||= {};
    state.data.coreInstallFeedback[`${agentID}|${engine}`] = message;
    if (status) {
      status.textContent = message;
      status.hidden = !message;
    }
  };
  const watchInstall = (task, agentID, engine, sessionData) => {
    if (!can("tasks.read")) {
      installFeedback(agentID, engine, "安装任务已提交；当前账号无任务查看权限，可刷新节点状态确认结果。");
      return;
    }
    const key = `${agentID}|${engine}`;
    sessionData.coreInstallTasks ||= {};
    sessionData.coreInstallTasks[key] = task.id;
    const waiting = taskTerminal(task) ? Promise.resolve(task) : monitorInstall(task.id, (latest, error) => {
      installFeedback(agentID, engine, error ? "任务状态暂不可用，正在重试…" : latest.status === "running" ? "内核安装正在执行…" : "内核安装正在排队…");
    });
    void waiting.then(latest => {
      if (state.data !== sessionData) return;
      const message = latest.status === "succeeded"
        ? "内核安装成功，正在刷新节点状态。"
        : `内核安装${latest.status === "canceled" ? "已取消" : "失败"}：${latest.error || "请查看执行记录"}`;
      installFeedback(agentID, engine, message);
      notify(message, latest.status === "succeeded" ? "success" : "error");
    }).catch(error => {
      if (state.data === sessionData && error.name !== "AbortError") {
        installFeedback(agentID, engine, `任务状态读取失败：${error.message}`);
      }
    }).finally(async () => {
      if (state.data !== sessionData) return;
      if (sessionData.coreInstallTasks?.[key] === task.id) delete sessionData.coreInstallTasks[key];
      if (["node-settings", "agents"].includes(state.route)) {
        try {
          await refreshAgentPage?.();
        } catch (error) {
          notify(`刷新内核状态失败：${error.message}`, "error");
        }
      }
    });
  };
  return { watchInstall, installFeedback };
}
