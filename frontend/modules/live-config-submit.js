import { updateFeedback } from "./presence-motion.js";
import { bindEvent } from "./refresh.js";
import { diagnosticError } from "./errors.js";
import { submitLiveConfigChange } from "./live-config-state.js";
import { taskTerminal } from "./task-monitor.js";

export function bindLiveConfigSubmit({api, state, can, notify, confirmAction, submitTask}, {
  agent, engine, accountData, sourceKey, source, privateWorkspace, importSource, configFiles, beforeDeploy,
  workspaceElement, liveConfig, deployments, workspace, workspaces, status,
}) {
  const formElement = workspaceElement?.querySelector("#live-config-form");
  if (!formElement) return;
  const key = workspace.operationKey, epoch = state.navigationEpoch;
  const current = () => accountData === state.data && epoch === state.navigationEpoch && !state.routeSignal?.aborted && formElement.isConnected &&
    state.route === "live-config" && state.data.liveAgent === agent.id && state.data.liveEngine === engine && state.data.liveConfigSource === workspace.sourceMode;
  const report = (message, tone = "", task, intent) => {
    const operation = status.saveOperation({key, message, tone, task, intent, version:workspace.saved?.version});
    workspace.operation = operation;
    status.renderPresetStatus();
    if (task) status.watchPresetOperation(operation);
  };
  bindEvent(formElement, "submit", async event => {
    event.preventDefault();
    if (!current() || workspace.submitting || workspace.uncertain) return;
    const intent = event.submitter?.dataset.liveIntent || (privateWorkspace ? "save" : "validate");
    if (!["save", "validate", "deploy", "import"].includes(intent) || !can("agent-config.write") ||
      intent !== "save" && (!can("tasks.execute") || agent.status !== "online") || importSource !== (intent === "import")) return;
    if (workspace.conflict) { report("服务端版本已变化，草稿已保留。请核对新版本后再提交。", "error"); return; }
    const form = new FormData(formElement);
    try { form.set("content", configFiles ? configFiles.content() : form.get("content")); }
    catch (error) { report(error.message, "error"); return; }
    form.set("version", workspace.saved?.version || 0);
    const ongoing = status.operationFor(key);
    if (intent !== "save" && ongoing?.task?.id && !taskTerminal(ongoing.task) && ongoing.intent === intent &&
        ongoing.version === workspace.saved?.version && form.get("content") === workspace.saved?.content) {
      notify("此版本的任务正在执行，请等待结果后重试。", "error"); return;
    }
    const controls = [...workspaceElement.querySelectorAll("button, input, select, textarea")].map(element => [element, element.disabled]);
    const submitter = event.submitter, label = submitter?.textContent;
    let submitted = false, persisted, result;
    workspace.submitting = true; workspace.phase = "submitting";
    formElement.dataset.saving = "1"; formElement.setAttribute("aria-busy", "true");
    controls.forEach(([element]) => { element.disabled = true; });
    try {
      workspaces.capture();
      if (workspace.formDrafts?.length && !(await confirmAction("协议表单还有未保存修改。继续保存当前源码会清除这些表单草稿，确定继续？", "保存完整配置"))) return;
      if (intent === "import" && !(await confirmAction("导入当前系统服务快照并迁移到 QAgent？原服务将在迁移时停止，失败会恢复原服务。", "导入并迁移"))) return;
      if (intent === "deploy" && !(await confirmAction(agent.can_manage === false
        ? "保存并部署到本人的独立实例？失败时保留草稿并恢复上一有效配置。"
        : "保存当前配置并部署到此节点的所选内核？部署会启动或重启服务。", "保存并部署"))) return;
      if (!current()) return;
      submitted = true;
      report("正在保存配置…");
      if (submitter) updateFeedback(submitter, "正在提交…");
      result = await submitLiveConfigChange({api, submitTask, agent, engine, intent, form, source,
        existingAvailable:importSource, savedConfig:workspace.saved, isCurrent:current,
        beforeDeploy:beforeDeploy ? async () => {
          report("正在核验节点当前配置…");
          await beforeDeploy();
        } : null,
        onSavedConfig:config => {
          persisted = config;
          if (accountData !== state.data) return;
          state.data.presetDrafts?.clear(`${workspace.agentId}|${engine}|${workspace.sourceMode}|`);
          workspaces.commit(workspace, config);
          state.data.liveSources[sourceKey] = {...source, content:config.content, saved:!importSource, cached:false};
        },
        onDeployTask:taskId => {
          if (accountData !== state.data) return;
          deployments.recordPendingDeploy(taskId, agent.id, engine);
          void deployments.monitorDeployTask(taskId, agent.id, engine);
        },
      });
      if (accountData !== state.data) return;
      if (!persisted) workspaces.commit(workspace, result.saved);
      state.data.liveSources[sourceKey] = {...source, content:result.content, saved:!importSource, cached:false};
      workspace.phase = result.task ? "tracking" : "ready";
      report(intent === "save" ? `配置 v${result.saved.version} 已保存，尚未部署。`
        : `配置 v${result.saved.version} 已保存，${intent === "deploy" ? "部署" : intent === "import" ? "迁移" : "校验"}任务已提交。`, "", result.task, intent);
      if (current()) await liveConfig();
    } catch (error) {
      if (accountData !== state.data || error.name === "AbortError") return;
      workspace.uncertain = !error.deployPreflight && submitted && !persisted && (!error.status || error.status >= 500 || error.status === 409);
      workspace.phase = workspace.uncertain ? "uncertain" : "ready";
      const message = `${persisted ? `配置 v${persisted.version} 已保存，后续任务未完成：` : ""}${diagnosticError(error.message)}${workspace.uncertain ? " 草稿已保留，请刷新并核对保存结果。" : persisted ? " 可以重新提交任务，已保存的内容仍然保留。" : ""}`;
      report(message, "error");
      if (current()) notify(message, "error");
      if (persisted && current()) {
        try { await liveConfig(); } catch { /* Durable saved state and feedback are retained for the next render. */ }
      }
    } finally {
      workspace.submitting = false;
      delete formElement.dataset.saving; formElement.removeAttribute("aria-busy");
      if (formElement.isConnected) {
        controls.forEach(([element, disabled]) => { element.disabled = disabled || workspace.uncertain && element.matches("[data-live-intent]"); });
        if (submitter) updateFeedback(submitter, label);
      }
    }
  });
}
