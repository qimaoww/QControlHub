import { taskTerminal } from "./task-monitor.js";

// Operation feedback remains account-scoped and follows the same deployment
// poller used by the source editor.
export function createPresetStatus({ state, can, confirmAction }, {
  editor, drafts, presetVisible, refreshPresetPage, renderLiveConfig, deployments, onRuntimeChange,
}) {
  const { waitForDeployTerminal, monitorDeployTask } = deployments;
const operationFor = (key = `${state.data.agentId}|${state.data.engine}`) =>
  state.data.presetOperations?.[key] || (state.data.presetOperation?.key === key ? state.data.presetOperation : undefined);
function saveOperation(operation) {
  state.data.presetOperations ||= {};
  state.data.presetOperations[operation.key] = operation;
  state.data.presetOperation = operation;
  return operation;
}

function renderPresetStatus() {
  const key = state.route === "live-config" ? `${state.data.liveAgent}|${state.data.liveEngine}` : `${state.data.agentId}|${state.data.engine}`;
  if (state.route !== "live-config" && !presetVisible()) return;
  const operation = operationFor(key);
  if (!operation) return;
  const root = editor.host?.root || document;
  let status = root.querySelector("[data-preset-status]");
  if (!status) {
    status = document.createElement("div");
    status.dataset.presetStatus = "";
    const anchor = root.querySelector(".config-command-bar, .live-config-details");
    if (anchor) anchor.after(status);
    else if (editor.host) root.prepend(status);
  }
  status.className = `alert preset-submit-status ${operation.tone}`;
  status.setAttribute("role", operation.tone === "error" ? "alert" : "status");
  status.setAttribute("aria-live", "polite");
  status.textContent = operation.message;
  if (operation.task?.id) {
    const link = document.createElement("a");
    link.href = "#tasks";
    link.textContent = " 查看执行记录 →";
    status.append(link);
  }
  if (operation.reload) {
    const retry = document.createElement("button");
    retry.type = "button";
    retry.className = "button small";
    retry.textContent = "重新加载配置";
    retry.onclick = async () => {
      if (editor.savePending || editor.context?.generating) return;
      const sessionData = state.data;
      const epoch = state.navigationEpoch;
      retry.disabled = true;
      try {
        if (drafts().dirty() && !(await confirmAction("重新加载将放弃当前未保存的草稿，是否继续？", "重新加载配置"))) return;
        if (state.data !== sessionData || state.navigationEpoch !== epoch || operationFor(key) !== operation) return;
        drafts().clear(operation.key + "|");
        // Same-version reconciliation normally preserves inputs. An explicit
        // discard must instead mount fresh controls with saved defaults.
        state.data.presetDraftReset = (state.data.presetDraftReset || 0) + 1;
        if (presetVisible()) await refreshPresetPage();
        else {
          operation.reload = false;
          await renderLiveConfig();
        }
      }
      catch (error) {
        if (operationFor(key) !== operation) return;
        operation.reload = true;
        operation.message = `页面加载失败：${error.message}。请重新加载后继续编辑。`;
        renderPresetStatus();
        editor.syncControls();
      }
      finally { retry.disabled = false; }
    };
    status.append(retry);
  }
}

function watchPresetOperation(operation) {
  if (!operation?.task?.id || operation.monitoring || taskTerminal(operation.task) || !can("tasks.read")) return;
  const sessionData = state.data;
  const {key, intent, version, task} = operation;
  const [agentId, engine] = key.split("|");
  const tracked = () => sessionData === state.data && operationFor(key) === operation;
  operation.monitoring = true;
  const progress = (latest, error) => {
    if (!tracked() || operation.reload) return;
    const message = error ? `配置 v${version} 已保存，任务状态连接暂时中断，正在重试…` :
      `配置 v${version} 已保存，${task.install_if_missing ? "稳定版安装及" : ""}${intent === "deploy" ? "部署" : "校验"}任务${latest.status === "running" ? "正在执行" : "正在排队"}…`;
    if (operation.message === message) return;
    operation.message = message;
    operation.tone = "";
    renderPresetStatus();
  };
  const waiting = waitForDeployTerminal(task.id, progress);
  if (intent === "deploy") void monitorDeployTask(task.id, agentId, engine);
  void waiting.then(latest => {
    if (!tracked()) return;
    operation.task = latest;
    operation.message = latest.status === "succeeded"
      ? `配置 v${version} ${intent === "deploy" ? "部署" : "校验"}成功。`
      : `配置已保存，任务${latest.status === "canceled" ? "已取消" : "失败"}：${latest.error || "请查看执行记录"}`;
    if (operation.reload) operation.message += " 请重新加载配置后继续编辑。";
    operation.tone = latest.status === "succeeded" ? "success" : "error";
    if (task.install_if_missing) {
      onRuntimeChange(agentId, engine);
    }
    renderPresetStatus();
  }).catch(error => {
    if (!tracked() || error.name === "AbortError") return;
    operation.message = "配置已保存，任务状态暂不可读取，请查看执行记录。";
    operation.tone = "error";
    renderPresetStatus();
  }).finally(() => { operation.monitoring = false; });
}


  return { operationFor, saveOperation, renderPresetStatus, watchPresetOperation };
}
