import { updateFeedback, removePresented } from "./presence-motion.js";
import { bindEvent } from "./refresh.js";
import { diagnosticError } from "./errors.js";
import { submitLiveConfigChange } from "./live-config-state.js";

export function bindLiveConfigSubmit({ api, state, notify, confirmAction, submitTask }, {
  agent, engine, accountData, sourceKey, source, saved, privateWorkspace, importSource,
  configFiles, beforeDeploy, workspaceElement, liveConfig, deployments,
}) {
  const { recordPendingDeploy, monitorDeployTask } = deployments;
  const sourceMode = state.data.liveConfigSource;
  let liveSaving = false, liveSaveUncertain = false;
  bindEvent(document.querySelector("#live-config-form"), "submit", async (event) => {
      event.preventDefault();
      const formElement = event.currentTarget;
      if (accountData !== state.data || liveSaving || liveSaveUncertain || !formElement.isConnected ||
          state.data.liveAgent !== agent.id || state.data.liveEngine !== engine) return;
      const form = new FormData(formElement);
      const intent = event.submitter?.dataset.liveIntent || (privateWorkspace ? "save" : "validate");
      if (!["save", "import", "deploy", "validate"].includes(intent)) return;
      const controls = [...(workspaceElement || formElement).querySelectorAll("button, input, select, textarea")]
        .map(element => [element, element.disabled]);
      const submitter = event.submitter, label = submitter?.textContent;
      let submitted = false, persisted, completed;
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
        if (accountData !== state.data || !formElement.isConnected) return;
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
          beforeDeploy,
          onSavedConfig: value => { persisted = value; },
          onDeployTask: (taskId) => {
            recordPendingDeploy(taskId, agent.id, engine);
            monitorDeployTask(taskId, agent.id, engine);
          },
        });
        completed = result;
        if (accountData !== state.data ||
            state.data.presetOperations?.[`${agent.id}|${engine}`]?.version > result.saved.version) return;
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
          await liveConfig();
      } catch (error) {
        if (accountData !== state.data || !formElement.isConnected) return;
        liveSaveUncertain = !error.deployPreflight && submitted &&
          Boolean(persisted || error.recoverConfigSubmission || !error.status || error.status === 408 || error.status === 409 || error.status >= 500);
        const message = `${completed ? `配置已保存${completed.task?.id ? "且任务已提交" : ""}，页面刷新失败：` : persisted ? "配置已保存，后续任务未确认：" : ""}${diagnosticError(error.message)}${liveSaveUncertain ? " 当前内容已保留，请核对结果后继续。" : ""}`;
        updateFeedback(status, message);
        status.setAttribute("role", "alert");
        if (completed || error.recoverConfigSubmission) {
          const retry = document.createElement("button");
          retry.type = "button";
          retry.className = "button small";
          retry.textContent = completed ? "重新加载配置" : "核对保存结果";
          const current = () => accountData === state.data && formElement.isConnected &&
            state.route === "live-config" && state.data.liveAgent === agent.id &&
            state.data.liveEngine === engine && state.data.liveConfigSource === sourceMode;
          retry.onclick = async () => {
            if (!current()) return;
            retry.disabled = true;
            try {
              if (!completed) {
                const result = await error.recoverConfigSubmission();
                if (!current()) return;
                completed = {saved:result.config, task:result.task};
              }
              await liveConfig();
            } catch (recoveryError) {
              if (!current()) return;
              updateFeedback(status, `${completed ? "配置已保存，页面刷新失败" : "保存结果暂未确认"}：${diagnosticError(recoveryError.message)}`);
              status.append(retry);
            } finally { retry.disabled = false; }
          };
          status.append(retry);
        }
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
