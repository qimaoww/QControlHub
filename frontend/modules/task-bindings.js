import { bindEvent } from "./refresh.js";
export function createTaskBindings({ api, state, notify, confirmAction }, { tasks }) {
  function bindTaskActions(root) {
    root.querySelectorAll("[data-cancel]").forEach(
      (button) =>
        (button.onclick = async () => {
          if (!(await confirmAction("确定取消这个待执行任务？", "取消任务")))
            return;
          try {
            await api(`/tasks/${button.dataset.cancel}`, {
              method: "DELETE",
            });
            tasks({ background: true });
          } catch (error) {
            notify(error.message, "error");
          }
        }),
    );
    root.querySelectorAll("[data-retry]").forEach(
      (button) =>
        (button.onclick = async () => {
          if (
            !(await confirmAction(
              button.dataset.retryVersion
                ? `确定重试配置 v${button.dataset.retryVersion}？仅缺少内核时安装稳定版，已安装的内核不会切换版本。配置已有新版本时会拒绝本次重试。`
                : "确定使用当前配置重新提交这个任务？",
              "重试任务",
            ))
          )
            return;
          try {
            await api(`/tasks/${button.dataset.retry}/retry`, {
              method: "POST",
            });
            tasks({ background: true });
          } catch (error) {
            notify(error.message, "error");
          }
        }),
    );
  }

  function bindTaskFilters() {
    bindEvent(document.querySelector("[data-apply-task-filter]"), "click", () => {
      state.data.taskFilters = {
        agent_id: document.querySelector("#task-agent")?.value || "",
        status: document.querySelector("#task-status")?.value || "",
        action: document.querySelector("#task-action")?.value || "",
        limit: Number(document.querySelector("#task-limit")?.value || 100),
      };
      tasks({ syncFilters: true });
    });
    document.querySelectorAll("[data-task-status-filter]").forEach((link) => {
      link.onclick = (event) => {
        event.preventDefault();
        state.data.taskFilters = {
          ...(state.data.taskFilters || {}),
          status: link.dataset.taskStatusFilter,
        };
        tasks({ syncFilters: true });
      };
    });
    bindEvent(document.querySelector("[data-reset-task-filter]"), "click", (event) => {
      event.preventDefault();
      state.data.taskFilters = {};
      tasks({ syncFilters: true });
    });

  }
  return { bindTaskActions, bindTaskFilters };
}
