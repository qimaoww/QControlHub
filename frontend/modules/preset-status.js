import { enterSurface } from "./motion.js";
import { taskTerminal } from "./task-monitor.js";
import { diagnosticError } from "./errors.js";

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
const hasTaskFeedback = taskID => Object.values(state.data.presetOperations || {}).some(operation => operation.task?.id === taskID);

// Failure feedback carries a Chinese explanation followed by the original log,
// so newlines become real line breaks without injecting markup.
function fillStatusText(element, message) {
  const text = String(message ?? "");
  if (!text.includes("\n") || typeof document.createTextNode !== "function") {
    element.textContent = text;
    return;
  }
  const nodes = [];
  text.split("\n").forEach((line, index) => {
    if (index) nodes.push(document.createElement("br"));
    nodes.push(document.createTextNode(line));
  });
  element.replaceChildren(...nodes);
}

function renderPresetStatus() {
  const key = state.route === "live-config" ? `${state.data.liveAgent}|${state.data.liveEngine}|${state.data.liveConfigSource}` : `${state.data.agentId}|${state.data.engine}`;
  if (state.route !== "live-config" && !presetVisible()) return;
  const operation = operationFor(key);
  if (!operation) return;
  const root = editor.host?.root || document;
  let status = root.querySelector("[data-preset-status], [data-workspace-result]");
  if (status) status.dataset.presetStatus = "";
  if (!status) {
    status = document.createElement("div");
    status.dataset.presetStatus = "";
    const anchor = root.querySelector("[data-workspace-result], .config-command-bar, .live-config-details");
    if (anchor) anchor.after(status);
    else if (editor.host) root.prepend(status);
  }
  const changed = status.dataset.motionMessage !== operation.message;
  status.dataset.motionMessage = operation.message;
  status.className = `alert preset-submit-status ${operation.tone}`;
  status.setAttribute("role", operation.tone === "error" ? "alert" : "status");
  status.setAttribute("aria-live", "polite");
  fillStatusText(status, operation.message);
  if (changed) enterSurface(status, { token: "--motion-feedback" });
  if (operation.task?.id && can("tasks.read") && operation.tone !== "error") {
    const link = document.createElement("a");
    link.href = "#tasks";
    link.textContent = " 查看任务";
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
        if (state.route === "live-config" && !editor.host) {
          if (await renderLiveConfig({refresh:true, operationKey:key})) { operation.reload = false; renderPresetStatus(); }
          return;
        }
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
        operation.message = diagnosticError(error.message) || "配置加载失败";
        renderPresetStatus();
        editor.syncControls();
      }
      finally { retry.disabled = false; }
    };
    status.append(retry);
  }
}

function watchPresetOperation(operation) {
  if (!operation?.task?.id || operation.monitoring) return;
  const sessionData = state.data;
  const {key, intent, task} = operation;
  const verb = intent === "deploy" ? "部署" : intent === "import" ? "迁移" : "校验";
  const [agentId, engine] = key.split("|");
  const tracked = () => sessionData === state.data && operationFor(key) === operation;
  const finish = latest => {
    if (!tracked()) return;
    operation.task = latest;
    operation.message = latest.status === "succeeded"
      ? `${verb}成功`
      : diagnosticError(latest.error) || `${verb}${latest.status === "canceled" ? "已取消" : "失败"}`;
    operation.tone = latest.status === "succeeded" ? "success" : "error";
    if (task.install_if_missing || intent === "deploy") onRuntimeChange(agentId, engine);
    renderPresetStatus();
  };
  if (taskTerminal(task)) { finish(task); return; }
  if (!can("tasks.read")) return;
  operation.monitoring = true;
  const progress = (latest, error) => {
    if (!tracked() || operation.reload) return;
    const action = `${task.install_if_missing ? "安装并" : ""}${verb}`;
    const message = error ? "正在重新连接…" : latest.status === "running" ? `正在${action}…` : `等待${action}…`;
    if (operation.message === message) return;
    operation.message = message;
    operation.tone = "";
    renderPresetStatus();
  };
  const waiting = waitForDeployTerminal(task.id, progress);
  if (intent === "deploy") void monitorDeployTask(task.id, agentId, engine);
  void waiting.then(latest => {
    finish(latest);
  }).catch(error => {
    if (!tracked() || error.name === "AbortError") return;
    operation.message = diagnosticError(error.message) || "无法获取任务结果";
    operation.tone = "error";
    renderPresetStatus();
  }).finally(() => { operation.monitoring = false; });
}


  return { operationFor, saveOperation, renderPresetStatus, watchPresetOperation, hasTaskFeedback };
}
