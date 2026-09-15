import { bindEvent } from "./refresh.js";
import { diagnosticError } from "./errors.js";
import { submitLiveConfigChange } from "./live-config-state.js";

export function bindLiveConfigSubmit({ api, state, notify, confirmAction, submitTask }, {
  agent, engine, accountData, sourceKey, source, saved, privateWorkspace, importSource,
  configFiles, beforeDeploy, workspaceElement, liveConfig, deployments,
}) {
  const { recordPendingDeploy, monitorDeployTask } = deployments;
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
      let submitted = false, persisted;
      liveSaving = true;
      formElement.dataset.saving = "1";
      formElement.setAttribute("aria-busy", "true");
      controls.forEach(([element]) => { element.disabled = true; });
      formElement.querySelector("[data-live-save-status]")?.remove();
      const status = document.createElement("span");
      status.dataset.liveSaveStatus = "";
      status.setAttribute("role", "status");
      status.textContent = "正在检查配置…";
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
            "确定保存当前源码、替换此主机该内核的当前配置并重启服务？同一内核只运行一份配置，可能影响其他用户的已部署服务。",
            "保存并部署",
          ))
        )
          return;
        if (accountData !== state.data || !formElement.isConnected) return;
        submitted = true;
        if (submitter) submitter.textContent = "正在提交…";
        status.textContent = "正在保存配置并提交任务…";
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
            status.textContent = "正在核验 Agent 当前配置…";
            if (submitter) submitter.textContent = "正在核验…";
            await beforeDeploy();
            status.textContent = "正在保存配置并提交任务…";
            if (submitter) submitter.textContent = "正在提交…";
          } : null,
          onSavedConfig: value => { persisted = value; },
          onDeployTask: (taskId) => {
            recordPendingDeploy(taskId, agent.id, engine);
            monitorDeployTask(taskId, agent.id, engine);
          },
        });
        if (accountData !== state.data) return;
        if (intent === "save") notify("个人配置已保存");
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
          Boolean(persisted || !error.status || error.status === 409 || error.status >= 500);
        const message = `${persisted ? `配置 v${persisted.version} 已保存，后续任务或页面刷新未完成：` : ""}${diagnosticError(error.message)}${liveSaveUncertain ? " 当前内容已保留，请重新读取并核对结果后再提交。" : ""}`;
        status.textContent = message;
        status.setAttribute("role", "alert");
        notify(message, "error");
      } finally {
        liveSaving = false;
        delete formElement.dataset.saving;
        formElement.removeAttribute("aria-busy");
        controls.forEach(([element, disabled]) => { element.disabled = disabled || liveSaveUncertain && element.matches("[data-live-intent]"); });
        if (submitter) submitter.textContent = label;
        if (status.getAttribute("role") !== "alert") status.remove();
      }
  });

}
