import { bindEvent } from "./refresh.js";

// In-flight settings writes survive a render and keep newer user input intact.
export function createAgentSettings({ api, can: permission, engineName, notify, confirmAction }, { refreshAgentPage }) {
  const pendingAgentNames = new Set();
  const pendingEngineCapabilities = new Set();
  const pendingAgentDeletes = new Map();
  return (agentsByID) => {
  const syncDeleteButton = (button) => {
    const agentID = button.dataset.delete;
    const agent = agentsByID.get(agentID);
    const deleting = pendingAgentDeletes.get(agentID) === "deleting";
    button.disabled = pendingAgentDeletes.has(agentID) || !permission("agents.manage", agent) || agent?.can_manage === false;
    button.textContent = deleting ? "正在删除…" : "删除节点";
    if (deleting) button.setAttribute("aria-busy", "true");
    else button.removeAttribute("aria-busy");
  };
  document.querySelectorAll("[data-delete]").forEach((button) => {
    syncDeleteButton(button);
    button.onclick = async () => {
      const agentID = button.dataset.delete;
      if (button.disabled || pendingAgentDeletes.has(agentID)) return;
      pendingAgentDeletes.set(agentID, "confirming");
      syncDeleteButton(button);
      try {
        if (!(await confirmAction(
          "确定删除此节点？控制面会断开连接并清理关联配置，节点上的 QAgent 不会被远程卸载；以后可通过新的添加节点命令重新安装。",
          "删除节点",
        ))) return;
        pendingAgentDeletes.set(agentID, "deleting");
        syncDeleteButton(button);
        await api(`/agents/${encodeURIComponent(agentID)}`, {
          method: "DELETE", signal: AbortSignal.timeout(45000),
        });
        try {
          await refreshAgentPage();
          notify("节点已删除");
        } catch (error) {
          notify(`节点已删除，但列表刷新失败：${error.message}`, "error");
        }
      } catch (error) {
        notify(error.message, "error");
      } finally {
        pendingAgentDeletes.delete(agentID);
        syncDeleteButton(button);
        // A refresh or navigation may have replaced the initiating button.
        document.querySelectorAll("[data-delete]").forEach((current) => {
          if (current.dataset.delete === agentID) syncDeleteButton(current);
        });
      }
    };
  });
  document.querySelectorAll("[data-agent-visibility]").forEach((input) => {
    input.onchange = async () => {
      input.disabled = true;
      try {
        await api(`/agents/${encodeURIComponent(input.dataset.agentVisibility)}/visibility`, {
          method: "PUT",
          body: JSON.stringify({ admin_hidden: input.checked }),
        });
        notify(input.checked ? "已对管理员隐藏此节点" : "此节点已对管理员可见");
        await refreshAgentPage();
      } catch (error) {
        input.checked = !input.checked;
        input.disabled = false;
        notify(error.message, "error");
      }
    };
  });
  document.querySelectorAll("[data-node-capabilities]").forEach((section) => {
    const agentID = section.dataset.nodeCapabilities;
    const can = (capability) => permission(capability, agentsByID.get(agentID)) && agentsByID.get(agentID)?.can_manage !== false;
    section.querySelectorAll("[data-engine-capability]").forEach((input) => {
      const engine = input.dataset.engineCapability;
      const key = `${agentID}|${engine}`;
      // These are immediate-action controls, not an unsaved form draft.
      // Reconciliation updates attributes but a checkbox's dirty-checkedness
      // flag can retain its old property after an asynchronous service result.
      if (!pendingEngineCapabilities.has(key)) {
        input.checked = (agentsByID.get(agentID)?.capabilities || []).includes(engine);
        input.defaultChecked = input.checked;
      }
      if (pendingEngineCapabilities.has(key)) input.disabled = true;
      bindEvent(input, "change", async () => {
        if (!can("agents.manage") || pendingEngineCapabilities.has(key)) return;
        const enabled = input.checked;
        let awaitingService = false;
        pendingEngineCapabilities.add(key);
        input.disabled = true;
        try {
          const saved = await api(`/agents/${encodeURIComponent(agentID)}/capabilities/${engine}`, {
            method: "PUT", body: JSON.stringify({ enabled }),
          });
          input.checked = saved.enabled;
          input.defaultChecked = saved.enabled;
          awaitingService = Boolean(saved.task_id);
          notify(saved.task_id
            ? `${engineName(engine)} ${enabled ? "启动" : "停止"}任务已提交，成功后更新能力；离线节点上线后执行`
            : `${engineName(engine)} 能力已${enabled ? "开启" : "关闭"}（未安装内核，不执行启停）`);
        } catch (error) {
          input.checked = !enabled;
          notify(error.message, "error");
        } finally {
          pendingEngineCapabilities.delete(key);
          input.disabled = awaitingService || !can("agents.manage");
          try {
            await refreshAgentPage();
          } catch (error) {
            notify(error.message, "error");
          }
        }
      });
    });
  });
  document.querySelectorAll("[data-agent-name-form]").forEach((form) => {
    const agentID = form.dataset.agentNameForm;
    const can = (capability) => permission(capability, agentsByID.get(agentID)) && agentsByID.get(agentID)?.can_manage !== false;
    const button = form.querySelector("button[type=submit]");
    if (button) button.disabled = !can("agents.manage") || pendingAgentNames.has(agentID);
    bindEvent(form, "submit", async (event) => {
      event.preventDefault();
      if (!can("agents.manage") || pendingAgentNames.has(agentID)) return;
      const name = String(new FormData(form).get("name") || "").trim();
      if (!name || [...name].length > 100 || /[\u0000-\u001f\u007f-\u009f]/u.test(name)) {
        notify("节点名称须为 1–100 个字符，且不能包含控制字符", "error");
        return;
      }
      pendingAgentNames.add(agentID);
      form.dataset.busy = "1";
      if (button) button.disabled = true;
      try {
        const saved = await api(`/agents/${encodeURIComponent(agentID)}/name`, {
          method: "PUT", body: JSON.stringify({ name }),
        });
        const agent = agentsByID.get(agentID);
        if (agent) agent.name = saved.name;
        const input = form.elements.namedItem("name");
        // Keep any newer draft typed while the save request was in flight.
        if (input.value.trim() === name) input.value = saved.name;
        input.defaultValue = saved.name;
        await refreshAgentPage();
        notify("节点名称已保存");
      } catch (error) {
        notify(error.message, "error");
      } finally {
        pendingAgentNames.delete(agentID);
        delete form.dataset.busy;
        if (button) button.disabled = !can("agents.manage");
        document.querySelectorAll("[data-agent-name-form]").forEach((current) => {
          if (current.dataset.agentNameForm === agentID) current.querySelector("button[type=submit]").disabled = !can("agents.manage");
        });
      }
    });
  });
  document.querySelectorAll("[data-komari-form]").forEach((form) => {
    const can = (capability) => permission(capability, agentsByID.get(form.dataset.komariForm)) && agentsByID.get(form.dataset.komariForm)?.can_manage !== false;
    form.onsubmit = async (event) => {
      event.preventDefault();
      if (!can("agents.manage")) return;
      const uuid = String(new FormData(form).get("uuid") || "").trim();
      const agentID = form.dataset.komariForm;
      const button = form.querySelector("button[type=submit]");
      if (button) button.disabled = true;
      try {
        await api(`/agents/${encodeURIComponent(agentID)}/komari`, {
          method: "PUT",
          body: JSON.stringify({ uuid }),
        });
        const agent = agentsByID.get(agentID);
        if (agent) {
          agent.labels = { ...(agent.labels || {}) };
          if (uuid) agent.labels.komari_uuid = uuid;
          else delete agent.labels.komari_uuid;
        }
        await refreshAgentPage();
        notify(uuid ? "Komari 服务器已关联" : "Komari 服务器关联已清除");
      } catch (error) {
        notify(error.message, "error");
        if (button) button.disabled = false;
      }
    };
  });

  };
}
