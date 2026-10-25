import { bindConfigMenu } from "./config-menu.js";
import { closePopup } from "./popup.js";

export function renderConfigWorkspaceTools({can, agent, engine, sourceMode}) {
  const shared = agent.can_manage === false;
  return `<nav class="config-workspace-tools" aria-label="配置工具"><button class="button" type="button" data-config-refresh>${sourceMode === "personal" ? "刷新配置" : "重新读取"}</button><details class="config-tools-menu"><summary class="button" aria-haspopup="menu">更多</summary><div class="config-inbound-menu-items" role="menu" aria-label="配置工具">
    ${sourceMode !== "import" && can("agent-config.write") ? '<button type="button" role="menuitem" data-config-new>新建空白配置</button>' : ""}
    ${sourceMode !== "import" && can("agent-config.write") ? '<button type="button" role="menuitem" data-inbound-action="advanced">高级字段</button>' : ""}
    ${sourceMode !== "import" && can("configs.read") ? '<button type="button" role="menuitem" data-inbound-action="history">版本历史</button>' : ""}
    ${sourceMode !== "import" && can("deployments.read") && can("configs.read") ? '<button type="button" role="menuitem" data-inbound-action="diff">配置差异</button>' : ""}
    ${shared ? `<a role="menuitem" href="#settings-node-${encodeURIComponent(agent.id)}">实例管理</a>` : ""}
    ${shared && can("tasks.execute") && can("agent-config.read") ? '<button type="button" role="menuitem" data-instance-read>读取实例文件</button>' : ""}
    ${shared && can("core-logs.read") ? `<a role="menuitem" href="#core-logs?agent=${encodeURIComponent(agent.id)}&instance=${encodeURIComponent(agent.runtime?.[engine]?.instance?.id || "")}&engine=${encodeURIComponent(engine)}">实例日志</a>` : ""}
    ${can("client-access.read") ? '<a role="menuitem" href="#client-access" data-config-client>客户端配置</a>' : ""}
  </div></details></nav>`;
}

export function renderConfigObjectActions(can, sourceMode) {
  return `<div class="config-selection-actions" data-config-object-tools${!can("agent-config.write") || !can("tasks.execute") || sourceMode === "import" ? " hidden" : ""}><details class="config-inbound-menu" data-config-inbound-menu><summary class="button" aria-haspopup="menu"><span data-config-operation-label>配置对象操作</span></summary><div class="config-inbound-menu-items" role="menu"><div data-common-actions role="group" aria-label="公共配置操作" hidden><button type="button" role="menuitem" data-common-action="add">增加公共配置项</button><button type="button" role="menuitem" data-common-action="modify">修改公共配置项</button><button type="button" role="menuitem" class="danger-text" data-common-action="delete">删除公共配置项</button></div><button type="button" role="menuitem" data-inbound-action="modify">编辑入站</button><button type="button" role="menuitem" class="danger-text" data-inbound-action="delete">删除入站</button></div></details></div>`;
}

export function composeConfigWorkspaceToolbar({container}) {
  const tools = container.querySelector(".config-workspace-tools");
  const menu = tools?.querySelector("details");
  if (menu) {
    bindConfigMenu(menu);
    menu.addEventListener("click", event => {
      if (event.target.closest('[role="menuitem"]')) closePopup(menu);
    });
  }
  return tools;
}
