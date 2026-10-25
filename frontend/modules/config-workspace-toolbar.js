import { bindConfigMenu } from "./config-menu.js";
import { closePopup } from "./popup.js";

// One source toolbar owns contextual actions; secondary workspace tools live
// in the header, outside the editing and submission flow.
export function composeConfigWorkspaceToolbar({ can, container, form, navigation, sourceActions, sourceMode, agent, engine }) {
  const toolbar = form?.querySelector(".code-editor-toolbar");
  if (toolbar) {
    const selectionActions = document.createElement("div");
    selectionActions.className = "config-selection-actions";
    [...navigation.children].filter(element => !element.matches(".config-file-list"))
      .forEach(element => selectionActions.append(element));
    navigation.append(sourceActions);
    const editorActions = document.createElement("div");
    editorActions.className = "config-editor-actions";
    editorActions.setAttribute("role", "group");
    editorActions.setAttribute("aria-label", "当前文件操作");
    form.querySelectorAll("[data-code-reset], [data-code-format]")
      .forEach(button => editorActions.append(button));
    toolbar.append(selectionActions, editorActions);
    const draft = form.querySelector(".config-draft-summary");
    if (draft) form.querySelector(".code-workspace>footer").prepend(draft);
    form.querySelector(".config-file-navigation")?.remove();
  }

  const tools = document.createElement("nav");
  tools.className = "config-workspace-tools";
  tools.setAttribute("aria-label", "配置工具");
  const shared = agent.can_manage === false;
  const sharedDeploy = shared && can("agent-config.write") && can("tasks.execute") && sourceMode === "personal" &&
    agent.runtime?.[engine]?.installed && !agent.runtime[engine].existing_config_unsupported_reason;
  tools.innerHTML = `${sharedDeploy ? '<button class="button primary" type="submit" form="live-config-form" data-live-intent="deploy">保存并部署</button>' : `<button class="button" type="button" data-config-refresh>${sourceMode === "personal" ? "刷新配置" : agent.runtime?.[engine]?.installed ? "重新读取" : "刷新配置"}</button>`}
    <details class="config-tools-menu"><summary class="button" aria-haspopup="menu">更多 <span aria-hidden="true">▾</span></summary>
      <div class="config-inbound-menu-items" role="menu" aria-label="配置工具">
        ${sharedDeploy ? '<button type="button" role="menuitem" data-config-refresh>刷新配置</button>' : ""}
        ${shared ? `<a role="menuitem" href="#settings-node-${encodeURIComponent(agent.id)}">实例管理</a>` : ""}
        ${shared && can("tasks.execute") && can("agent-config.read") ? '<button type="button" role="menuitem" data-instance-read>读取实例文件</button>' : ""}
        ${shared && can("core-logs.read") ? `<a role="menuitem" href="#core-logs?agent=${encodeURIComponent(agent.id)}&instance=${encodeURIComponent(agent.runtime?.[engine]?.instance?.id || "")}&engine=${encodeURIComponent(engine)}">实例日志</a>` : ""}
        <button type="button" role="menuitem" data-inbound-action="advanced">高级字段</button>
        ${can("configs.read") ? '<button type="button" role="menuitem" data-inbound-action="history">版本历史</button>' : ""}
        ${can("deployments.read") && can("configs.read") ? '<button type="button" role="menuitem" data-inbound-action="diff">配置差异</button>' : ""}
        ${can("client-access.read") ? '<a role="menuitem" href="#client-access" data-config-client>客户端配置</a>' : ""}
      </div>
    </details>`;
  const menu = tools.querySelector("details");
  bindConfigMenu(menu);
  menu.addEventListener("click", event => {
    if (event.target.closest('[role="menuitem"]')) closePopup(menu);
  });
  container.querySelector(".editor-toolbar-state").append(tools);
  return tools;
}
