import { prepareDisclosure } from "./popup.js";
import { bindEvent } from "./refresh.js";

export function developmentSourceVisible(engine, channel) {
  return engine === "mihomo" && channel === "development";
}

export function coreSourceForInstall(engine, channel, rawSource) {
  return developmentSourceVisible(engine, channel)
    ? rawSource || "official"
    : undefined;
}


export function createAgentCoreActions({ api, state, can, engineName, notify, confirmAction }) {
async function submitTask(payload) {
  try {
    const agent = state.data.agents?.find((item) => item.id === payload.agent_id);
    if (agent?.can_manage === false && !["deploy", "validate", "status"].includes(payload.action))
      throw new Error("共享节点的主机操作仅限所有者");
    const task = await api("/tasks", {
      method: "POST",
      body: JSON.stringify(payload),
    });
    notify("任务已提交");
    return task;
  } catch (error) {
    notify(error.message, "error");
    return null;
  }
}


  function bindCoreActions() {
  document.querySelectorAll("[data-task-action]").forEach((button) => {
    button.onclick = async () => {
      if (
        button.dataset.taskAction === "stop" &&
        !(await confirmAction(
          `确定停止 ${engineName(button.dataset.taskEngine)} 服务？现有连接会立即中断，需再次启动才能恢复。`,
          "停止服务",
        ))
      )
        return;
      await submitTask({
        agent_id: button.dataset.taskAgent,
        engine: button.dataset.taskEngine,
        action: button.dataset.taskAction,
      });
    };
  });
  document.querySelectorAll("[data-core-uninstall]").forEach((button) => {
    button.onclick = async () => {
      if (button.disabled || button.dataset.uninstallBusy) return;
      const agentID = button.dataset.taskAgent;
      const engine = button.dataset.taskEngine;
      const key = `${agentID}|${engine}`;
      button.dataset.uninstallBusy = "1";
      button.disabled = true;
      try {
        if (!(await confirmAction(
          `确定卸载 ${engineName(engine)} 内核？目标服务会停止，当前连接将断开；QAgent 管理的内核程序会删除。已保存配置会保留，可重新安装。`,
          "卸载内核",
        ))) return;
        const agent = state.data.agents?.find((item) => item.id === agentID);
        if (!button.isConnected || state.route !== "node-settings" ||
            agent?.can_manage === false || agent?.status !== "online" ||
            !agent?.runtime?.[engine]?.installed ||
            agent.runtime[engine].existing_config_unsupported_reason ||
            !(agent.features || []).includes("core-uninstall-v1") ||
            state.data.coreUninstallTasks?.[key] ||
            !can("tasks.execute") || !can("host.manage", agent)) {
          notify("节点或内核状态已变化，请刷新后重试", "error");
          return;
        }
        const task = await submitTask({
          agent_id: agentID,
          engine,
          action: "uninstall",
        });
        // Task status reads are a separate permission. Without them, leave
        // retry available; the server reuses an equivalent pending task.
        if (task?.id && can("tasks.read")) {
          state.data.coreUninstallTasks ||= {};
          state.data.coreUninstallTasks[key] = task.id;
          button.title = "卸载任务执行中，请等待状态刷新";
        }
      } finally {
        delete button.dataset.uninstallBusy;
        if (button.isConnected) {
          const agent = state.data.agents?.find((item) => item.id === agentID);
          button.disabled = agent?.status !== "online" ||
            !agent?.runtime?.[engine]?.installed ||
            !(agent?.features || []).includes("core-uninstall-v1") ||
            Boolean(state.data.coreUninstallTasks?.[key]) ||
            Boolean(button.closest(".service-card")?.dataset.existingUnsupported);
        }
      }
    };
  });
  document.querySelectorAll("[data-deploy]").forEach((button) => {
    button.onclick = async () => {
      if (button.disabled) return;
      const label = button.textContent;
      button.disabled = true;
      try {
        if (!(await confirmAction(
          `确定将已保存配置 v${button.dataset.configVersion} 部署到 ${engineName(button.dataset.engine)} 并重启服务？`, label.trim(),
        ))) return;
        button.textContent = "正在提交部署…";
        button.setAttribute("aria-busy", "true");
        await submitTask({
          agent_id: button.dataset.deploy,
          engine: button.dataset.engine,
          action: "deploy",
          config_id: button.dataset.configId,
          expected_config_version: Number(button.dataset.configVersion),
        });
      } catch (error) {
        notify(error.message, "error");
      } finally {
        button.textContent = label;
        button.removeAttribute("aria-busy");
        button.disabled = false;
      }
    };
  });
  document.querySelectorAll("[data-manual-import]").forEach((button) => {
    button.onclick = () => {
      state.data.liveAgent = button.dataset.manualAgent;
      state.data.liveEngine = button.dataset.manualEngine;
      state.data.liveConfigSource = "import";
      location.hash = "#live-config";
    };
  });
  document.querySelectorAll(".core-version-form").forEach((form) => {
    form.onsubmit = async (event) => {
      event.preventDefault();
      const values = new FormData(form);
      const channel = values.get("release_channel");
      const engine = form.dataset.versionEngine;
      const version =
        channel === "custom" ? values.get("custom_version") : channel;
      const payload = {
        agent_id: form.dataset.versionAgent,
        engine,
        action: "install",
        core_version: version,
      };
      const source = coreSourceForInstall(engine, channel, values.get("core_source"));
      if (source !== undefined) payload.core_source = source;
      const sourceNote =
        payload.core_source === "mirror"
          ? "来源：vernesong/mihomo Alpha 镜像（第三方）。"
          : payload.core_source === "official"
            ? "来源：MetaCubeX/mihomo 官方（默认）。"
            : "";
      if (
        !(await confirmAction(
          `确定提交内核安装或版本切换任务？${sourceNote}下载和校验完成后，目标服务会重启。`,
          "提交任务",
        ))
      )
        return;
      await submitTask(payload);
    };
  });
  document.querySelectorAll("[data-open-version-form]").forEach((button) => {
    const drawer = button.closest(".service-card")?.querySelector(".version-drawer");
    button.setAttribute("aria-expanded", String(Boolean(drawer?.open)));
    button.onclick = () => {
      if (drawer) { prepareDisclosure(drawer); drawer.open = true; button.setAttribute("aria-expanded", "true"); }
    };
  });
  document.querySelectorAll(".core-version-form").forEach((form) => {
    const custom = form.querySelector(".custom-version-field");
    const input = custom?.querySelector("input");
    const developmentSource = form.querySelector("[data-development-source]");
    const sync = () => {
      const checked = form.querySelector('input[name="release_channel"]:checked');
      const channel = checked?.value;
      const enabled = channel === "custom";
      custom?.classList.toggle("is-disabled", !enabled);
      if (input) {
        input.disabled = !enabled;
        input.required = enabled;
      }
      if (developmentSource) {
        developmentSource.hidden = !developmentSourceVisible(
          form.dataset.versionEngine,
          channel,
        );
      }
    };
    form
      .querySelectorAll('input[name="release_channel"]')
      .forEach((radio) => bindEvent(radio, "change", sync));
    sync();
  });
  document.querySelectorAll("[data-upgrade-agent]").forEach((button) => {
    button.onclick = async () => {
      if (
        !(await confirmAction(
          "确定升级这个节点的 QAgent？控制面会把当前版本的 Agent 二进制签名下发到节点，原子替换后自动重连；升级期间节点会短暂离线。",
          "升级 Agent",
        ))
      )
        return;
      const task = await submitTask({
        agent_id: button.dataset.upgradeAgent,
        action: "upgrade-agent",
      });
      if (task) location.hash = "#tasks";
    };
  });

  }
  return { submitTask, bindCoreActions };
}
