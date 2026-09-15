import { bindEvent } from "./refresh.js";
import { validateTCPSelection, systemBBRState, systemBBRActions, actionLabel, dialogID } from "./system-bbr-model.js";
import { prepareTCPPreset } from "./system-bbr-presets.js";
export function createSystemBBREditor({ api, state, can, notify, confirmAction }, { lifecycle, editable, render, systemBBR }) {
  function editorError(agentID, message) {
    lifecycle.editorErrors.set(agentID, message);
    const dialog = document.getElementById(dialogID(agentID, "editor"));
    const label = dialog?.querySelector("[data-tcp-error]");
    if (label) { label.textContent = message; label.hidden = !message; }
    // Keep errors in the active modal. An outside notice is obscured by the
    // backdrop and inserting/removing it can move the modal's DOM ancestors.
    if (message && !dialog?.open) notify(message, "error");
  }

  function bind({ agents, focused }) {
    bindEvent(document.querySelector("[data-bbr-refresh]"), "click", () => systemBBR());
    document.querySelectorAll("[data-bbr-dialog-open]").forEach((button) => {
      bindEvent(button, "click", () => {
        if (!state.confirmOpen) document.getElementById(button.dataset.bbrDialogOpen)?.showModal();
      });
    });
    document.querySelectorAll("[data-bbr-dialog-close]").forEach((button) => {
      bindEvent(button, "click", () => {
        if (!state.confirmOpen) button.closest("dialog")?.close();
      });
    });
    document.querySelectorAll(".bbr-dialog").forEach((dialog) => {
      // Moving a keyed card (or removing a preceding warning) can detach its
      // native modal from the top layer while leaving `open` set. Restore it
      // only after the shared confirmation is closed so it stays on top.
      if (dialog.open && !dialog.matches(":modal") && !state.confirmOpen) {
        dialog.close();
        dialog.showModal();
        if (dialog.contains(focused)) focused.focus({ preventScroll: true });
      }
      const outside = (event) => {
        const rect = dialog.getBoundingClientRect();
        return event.target === dialog && (event.clientX < rect.left || event.clientX > rect.right || event.clientY < rect.top || event.clientY > rect.bottom);
      };
      // A refresh can rebind handlers between pointerdown and click. Retain
      // the gesture on the dialog, and don't dismiss a drag from inside it.
      bindEvent(dialog, "pointerdown", (event) => {
        if (outside(event)) lifecycle.backdropStarts.add(dialog);
        else lifecycle.backdropStarts.delete(dialog);
      });
      bindEvent(dialog, "click", (event) => {
        if (lifecycle.backdropStarts.has(dialog) && outside(event) && !state.confirmOpen) dialog.close();
        lifecycle.backdropStarts.delete(dialog);
      });
      bindEvent(dialog, "pointercancel", () => lifecycle.backdropStarts.delete(dialog));
      bindEvent(dialog, "close", () => {
        // close() queues this event; modal restoration may already have
        // reopened the dialog and started a new gesture before it arrives.
        if (!dialog.open) lifecycle.backdropStarts.delete(dialog);
      });
      bindEvent(dialog, "cancel", (event) => { if (state.confirmOpen) event.preventDefault(); });
    });
    document.querySelectorAll("[data-bbr-action]").forEach((button) => {
      bindEvent(button, "click", () => submitChange(
        agents.find((entry) => entry.id === button.dataset.bbrAgent), button.dataset.bbrAction,
      ));
    });
    document.querySelectorAll("[data-tcp-form]").forEach((form) => {
      const agent = agents.find((entry) => entry.id === form.dataset.tcpForm);
      // Attribute reconciliation alone does not clear the browser's dirty
      // value/checked flags. The per-node draft is the authoritative editor
      // state; synchronize properties after an explicit clear or submission.
      form.querySelectorAll("[data-tcp-selected]").forEach((input) => {
        input.checked = Object.hasOwn(lifecycle.drafts[agent.id] || {}, input.dataset.tcpSelected);
      });
      form.querySelectorAll("[data-tcp-value]").forEach((input) => {
        const desired = lifecycle.drafts[agent.id]?.[input.dataset.tcpValue] ?? agent.metrics?.bbr?.parameters?.[input.dataset.tcpValue] ?? "";
        if (input.value !== desired) input.value = desired;
      });
      const capture = () => {
        const draft = {};
        form.querySelectorAll("[data-tcp-selected]").forEach((checkbox) => {
          if (checkbox.checked) draft[checkbox.dataset.tcpSelected] = form.querySelector(`[data-tcp-value="${checkbox.dataset.tcpSelected}"]`).value;
        });
        lifecycle.drafts[agent.id] = draft;
        editorError(agent.id, "");
        const draftLabel = document.querySelector(`[data-bbr-draft-label="${agent.id}"]`);
        if (draftLabel) draftLabel.hidden = !Object.keys(draft).length;
        const label = form.querySelector("[data-tcp-draft-status]");
        if (label) label.textContent = `${Object.keys(draft).length} 项待提交 · 草稿已保留`;
      };
      form.querySelectorAll("[data-tcp-value]").forEach((input) => {
        bindEvent(input, "input", () => {
          form.querySelector(`[data-tcp-selected="${input.dataset.tcpValue}"]`).checked = true;
          capture();
        });
        bindEvent(input, "change", () => {
          form.querySelector(`[data-tcp-selected="${input.dataset.tcpValue}"]`).checked = true;
          capture();
        });
      });
      form.querySelectorAll("[data-tcp-selected]").forEach((input) => bindEvent(input, "change", capture));
      form.querySelectorAll("[data-tcp-preset]").forEach((button) => {
        bindEvent(button, "click", () => {
          const current = lifecycle.lastAgents?.find((entry) => entry.id === agent.id);
          if (button.disabled || state.route !== "system-bbr" || lifecycle.accountData !== state.data || state.confirmOpen || !current || !editable(current) || !systemBBRState(current).controllable || lifecycle.submitting.has(agent.id) || ["pending", "running"].includes(lifecycle.localTasks.get(agent.id)?.status)) return;
          try {
            lifecycle.drafts[agent.id] = prepareTCPPreset(button.dataset.tcpPreset, lifecycle.rules, current.metrics?.bbr?.parameters, lifecycle.drafts[agent.id]);
            lifecycle.editorErrors.delete(agent.id);
            render(lifecycle.lastAgents);
          } catch (error) {
            editorError(agent.id, error.message);
          }
        });
      });
      bindEvent(form.querySelector("[data-tcp-reset]"), "click", async () => {
        const epoch = state.navigationEpoch;
        const accepted = !Object.keys(lifecycle.drafts[agent.id] || {}).length || await confirmAction("确定清空此节点未提交的 TCP 参数选择？已保存的系统配置不受影响。", "清空选择");
        if (state.route !== "system-bbr" || epoch !== state.navigationEpoch) return;
        if (accepted) {
          delete lifecycle.drafts[agent.id];
          lifecycle.editorErrors.delete(agent.id);
        }
        // Also restore modal state on cancellation: background reconciliation
        // may have moved this dialog while the confirmation was on top.
        // Local editor state must settle even when the network is unavailable.
        render(lifecycle.lastAgents);
      });
      bindEvent(form, "submit", async (event) => {
        event.preventDefault();
        capture();
        try {
          const settings = validateTCPSelection(lifecycle.drafts[agent.id], lifecycle.rules);
          await submitChange(agent, "configure-tcp", settings);
        } catch (error) {
          editorError(agent.id, error.message);
        }
      });
    });
  }

  async function submitChange(agent, action, settings) {
    if (!agent || !editable(agent) || !systemBBRActions.includes(action) || !systemBBRState(agent).controllable || lifecycle.submitting.has(agent.id) || ["pending", "running"].includes(lifecycle.localTasks.get(agent.id)?.status)) return;
    lifecycle.submitting.add(agent.id);
    document.querySelectorAll("[data-bbr-action]").forEach((entry) => {
      if (entry.dataset.bbrAgent === agent.id) entry.disabled = true;
    });
    document.querySelectorAll("[data-tcp-form]").forEach((form) => {
      if (form.dataset.tcpForm === agent.id) form.querySelectorAll("input, select, button:not([data-bbr-dialog-close])").forEach((entry) => (entry.disabled = true));
    });
    const epoch = state.navigationEpoch, data = state.data;
    try {
      const changes = settings || {
        "net.ipv4.tcp_congestion_control": action === "enable-bbr" ? "bbr" : "cubic",
        "net.core.default_qdisc": "fq",
      };
      const summary = Object.entries(changes).map(([key, value]) => `${key}: ${agent.metrics?.bbr?.parameters?.[key] || "未知"} → ${value}`).join("\n");
      if (!(await confirmAction(`确定对「${agent.name}」应用以下系统参数？\n${summary}\n保存到 /etc/sysctl.d/90-qcontrolhub-bbr.conf；未选参数保持不变，不重启网络或重置现有连接、网卡队列。请确认这些值适合该节点。`, actionLabel(action)))) return;
      if (data !== state.data || state.route !== "system-bbr" || epoch !== state.navigationEpoch) return;
      const current = lifecycle.lastAgents?.find((entry) => entry.id === agent.id);
      if (!editable(current) || !current || !systemBBRState(current).controllable || ["pending", "running"].includes(lifecycle.localTasks.get(agent.id)?.status)) {
        editorError(agent.id, "节点或任务状态已变化，请刷新核对后重新操作。");
        return;
      }
      const task = await api("/tasks", { method: "POST", body: JSON.stringify({
        agent_id: agent.id, action, engine: "", ...(settings ? { tcp_settings: settings } : {}),
      }) });
      if (data !== state.data) return;
      lifecycle.localTasks.set(agent.id, can("tasks.read") ? task : { ...task, status: "submitted" });
      if (settings) {
        delete lifecycle.drafts[agent.id];
        lifecycle.editorErrors.delete(agent.id);
        document.getElementById(dialogID(agent.id, "editor"))?.close();
      }
      notify("TCP 调优任务已提交，等待 Agent 执行；实际参数以采集结果为准。");
    } catch (error) {
      if (data === state.data) editorError(agent.id, error.message);
    } finally {
      if (data === state.data) lifecycle.submitting.delete(agent.id);
      if (data === state.data && state.route === "system-bbr" && epoch === state.navigationEpoch) {
        // Cancel/failure/submission must update controls before an optional
        // network refresh, which can be slow or fail entirely.
        render(lifecycle.lastAgents);
        await systemBBR({ background: true });
      }
    }
  }

  return { bind };
}
