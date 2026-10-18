import { updateFeedback, removePresented } from "./presence-motion.js";
import { bindEvent } from "./refresh.js";
import { diagnosticError } from "./errors.js";
import { submitLiveConfigChange } from "./live-config-state.js";

export function bindLiveConfigSubmit({ api, state, notify, confirmAction, submitTask }, {
  agent, engine, accountData, sourceKey, source, saved, privateWorkspace, importSource,
  configFiles, beforeDeploy, workspaceElement, liveConfig, deployments, sourceMode, status: operationStatus,
}) {
  const { recordPendingDeploy, monitorDeployTask } = deployments;
  const root = workspaceElement || document.querySelector("#live-config-form");
  const epoch = state.navigationEpoch;
  const current = () => accountData === state.data && epoch === state.navigationEpoch &&
    state.route === "live-config" && root?.isConnected &&
    !root.dataset.configSwitching && !root.dataset.configRefreshing &&
    state.data.liveAgent === agent.id && state.data.liveEngine === engine && state.data.liveConfigSource === sourceMode;
  let liveSaving = false, liveSaveUncertain = false;
  bindEvent(document.querySelector("#live-config-form"), "submit", async (event) => {
      event.preventDefault();
      const formElement = event.currentTarget;
      if (!current() || liveSaving || liveSaveUncertain) return;
      const form = new FormData(formElement);
      const intent = event.submitter?.dataset.liveIntent || (privateWorkspace ? "save" : "validate");
      if (!["save", "import", "deploy", "validate"].includes(intent)) return;
      const controls = [...(workspaceElement || formElement).querySelectorAll("button, input, select, textarea")]
        .map(element => [element, element.disabled]);
      const submitter = event.submitter, label = submitter?.textContent;
      let submitted = false, persisted;
      liveSaving = true;
      formElement.dataset.saving = "1";
      formElement.setAttribute("aria-busy", "true");
      controls.forEach(([element]) => { element.disabled = true; });
      removePresented(formElement.querySelector("[data-live-save-status]"));
      const status = document.createElement("span");
      status.dataset.liveSaveStatus = "";
      status.setAttribute("role", "status");
      updateFeedback(status, "正在检查配置…");
      formElement.querySelector(".code-workspace>footer").prepend(status);
      try {
        if (configFiles) form.set("content", configFiles.content());
        if (
          intent === "import" &&
          !(await confirmAction(
            "确定导入当前快照并迁移服务？Agent 将停止并禁用原服务，启动 QAgent 专用服务；迁移任一步失败都会自动恢复原服务。",
            "手动导入并迁移",
          ))
        )
          return;
        if (
          intent === "deploy" &&
          !(await confirmAction(
            agent.can_manage === false ? "确定保存并部署到本人的独立实例？部署后启动实例并检查运行结果；失败时保留草稿并恢复上一有效配置。" : "确定保存当前源码、替换此主机该内核的基础服务配置并启动服务？",
            "保存并部署",
          ))
        )
          return;
        if (!current()) return;
        submitted = true;
        if (submitter) updateFeedback(submitter, "正在提交…");
        updateFeedback(status, "正在保存配置并提交任务…");
        const result = await submitLiveConfigChange({
          api,
          submitTask,
          agent,
          engine,
          intent,
          form,
          source,
          existingAvailable: importSource,
          savedConfig: saved,
          beforeDeploy: beforeDeploy ? async () => {
            updateFeedback(status, "正在核验 Agent 当前配置…");
            if (submitter) updateFeedback(submitter, "正在核验…");
            await beforeDeploy();
            updateFeedback(status, "正在保存配置并提交任务…");
            if (submitter) updateFeedback(submitter, "正在提交…");
          } : null,
          isCurrent: current,
          onSavedConfig: value => { persisted = value; },
          onDeployTask: (taskId) => {
            recordPendingDeploy(taskId, agent.id, engine);
            monitorDeployTask(taskId, agent.id, engine);
          },
        });
        if (accountData !== state.data) return;
        const key = `${agent.id}|${engine}`;
        const operation = operationStatus.saveOperation({
          key, task: result.task, version: result.saved.version, intent: result.task?.action || intent, sourceMode,
          message: intent === "save" ? `配置 v${result.saved.version} 已保存，尚未部署。` :
            `配置 v${result.saved.version} 已保存，${intent === "import" ? "迁移" : intent === "deploy" ? "部署" : "校验"}任务已提交。`,
          tone: intent === "save" ? "success" : "",
        });
        operationStatus.watchPresetOperation(operation);
        if (intent === "save") notify("已保存，尚未部署");
        if (intent === "import") {
          notify("配置已保存，服务迁移任务已提交");
        }
        state.data.liveSources[sourceKey] = {
          ...source,
          content: result.content,
          saved: !importSource,
          cached: false,
        };
        if (state.route === "live-config" && state.data.liveAgent === agent.id && state.data.liveEngine === engine && formElement.isConnected)
          await liveConfig({resetEditor:true});
      } catch (error) {
        if (accountData !== state.data || !formElement.isConnected) return;
        liveSaveUncertain = Boolean(persisted) || !error.deployPreflight && submitted &&
          Boolean(!error.status || error.status === 409 || error.status >= 500);
        const message = `${persisted ? `配置 v${persisted.version} 已保存，后续任务或页面刷新未完成：` : ""}${diagnosticError(error.message)}${liveSaveUncertain ? " 当前内容已保留，请重新读取并核对结果后再提交。" : ""}`;
        updateFeedback(status, message);
        status.setAttribute("role", "alert");
        notify(message, "error");
      } finally {
        liveSaving = false;
        delete formElement.dataset.saving;
        formElement.removeAttribute("aria-busy");
        controls.forEach(([element, disabled]) => { element.disabled = disabled || liveSaveUncertain && element.matches("[data-live-intent]"); });
        if (submitter) updateFeedback(submitter, label);
        if (status.getAttribute("role") !== "alert") removePresented(status);
      }
  });

}
