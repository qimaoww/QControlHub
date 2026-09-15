import { bindEvent } from "./refresh.js";
import { batchAgentEligibility, batchSelectAllState } from "./agent-batch.js";

export function createAgentBatchController({ api, state, esc, notify, confirmAction, engineName, actionName }, { renderAgentPage, cancelCardDrag }) {
  let syncActiveBatchSnapshot = null;
  function bind(agentsByID) {
  const setNodeBatchMode = async (enabled, trigger) => {
    if (trigger?.disabled) return;
    if (trigger) trigger.disabled = true;
    state.data.nodeBatchMode = enabled;
    cancelCardDrag();
    try {
      await renderAgentPage();
    } catch (error) {
      state.data.nodeBatchMode = !enabled;
      if (trigger?.isConnected) trigger.disabled = false;
      notify(error.message, "error");
    }
  };
  document.querySelectorAll("[data-node-batch-toggle]").forEach((button) => {
    button.onclick = () =>
      setNodeBatchMode(!Boolean(state.data.nodeBatchMode), button);
  });
  const batchForm = document.querySelector("#batch-form");
  const updateBatch = () => {
    if (!batchForm) return;
    const engine = batchForm.elements.engine.value;
    const action = batchForm.elements.action.value;
    const busy = batchForm.dataset.busy === "1";
    const inputs = [...batchForm.querySelectorAll("[data-batch-checkbox]")];
    inputs.forEach((input) => {
      const agent = agentsByID.get(input.value);
      const eligibility = batchAgentEligibility(agent, action, engine);
      input.dataset.batchEligible = eligibility.eligible ? "1" : "0";
      input.disabled = busy || !eligibility.eligible;
      const card = input.closest(".node-card");
      const control = input.closest(".node-card-select");
      if (control) control.title = eligibility.reason;
      if (card) card.dataset.batchEligible = eligibility.eligible ? "1" : "0";
      if (!eligibility.eligible) input.checked = false;
      card?.classList.toggle("selected", input.checked);
    });
    const selection = batchSelectAllState(inputs);
    const selectAll = batchForm.querySelector("[data-batch-select-all]");
    if (selectAll) {
      selectAll.disabled = busy || selection.eligible === 0;
      selectAll.checked = selection.checked;
      selectAll.indeterminate = selection.indeterminate;
      selectAll.setAttribute(
        "aria-checked",
        selection.indeterminate ? "mixed" : String(selection.checked),
      );
    }
    const selectAllLabel = batchForm.querySelector(
      "[data-batch-select-all-label]",
    );
    if (selectAllLabel)
      selectAllLabel.textContent = selection.checked ? "取消全选" : "全选";
    const button = batchForm?.querySelector("button[type=submit]");
    if (button)
      button.disabled = selection.selected === 0 || batchForm.dataset.busy === "1";
    const clear = batchForm.querySelector("[data-batch-clear]");
    if (clear) clear.disabled = busy || selection.selected === 0;
    const label = batchForm?.querySelector("[data-batch-count]");
    if (label)
      label.textContent = `已选择 ${selection.selected} 个节点 · 当前可选 ${selection.eligible} 个`;
    const engineWrap = batchForm.querySelector("[data-batch-engine-wrap]");
    if (engineWrap) engineWrap.hidden = action === "upgrade-agent";
    batchForm.elements.action.disabled = busy;
    batchForm.elements.engine.disabled = busy;
    const close = batchForm.querySelector("[data-close-node-batch]");
    if (close) close.disabled = busy;
    batchForm
      .querySelectorAll("[data-batch-retry]")
      .forEach((retry) => {
        const eligibility = batchAgentEligibility(
          agentsByID.get(retry.dataset.batchRetry),
          retry.dataset.batchRetryAction,
          retry.dataset.batchRetryEngine,
        );
        retry.disabled = busy || !eligibility.eligible;
        retry.title = eligibility.eligible ? "重试失败任务" : eligibility.reason;
      });
  };
  const setBatchBusy = (busy) => {
    if (!batchForm) return;
    batchForm.dataset.busy = busy ? "1" : "";
    updateBatch();
  };
  syncActiveBatchSnapshot = (items) => {
    if (!batchForm?.isConnected) return;
    agentsByID.clear();
    items.forEach((agent) => agentsByID.set(agent.id, agent));
    updateBatch();
  };
  batchForm
    ?.querySelectorAll("[data-batch-checkbox]")
    .forEach((input) => (input.onchange = updateBatch));
  batchForm?.querySelectorAll("[data-node-batch-card]").forEach((card) => {
    card.onclick = (event) => {
      if (event.target.closest("input,button,label,select")) return;
      const input = card.querySelector("[data-batch-checkbox]");
      if (!input || input.disabled || batchForm.dataset.busy === "1") return;
      input.checked = !input.checked;
      updateBatch();
    };
  });
  const selectAll = batchForm?.querySelector("[data-batch-select-all]");
  if (selectAll)
    selectAll.onchange = () => {
      const shouldSelect = selectAll.checked;
      [...batchForm.querySelectorAll("[data-batch-checkbox]")]
        .filter((input) => input.dataset.batchEligible === "1")
        .forEach((input) => (input.checked = shouldSelect));
      updateBatch();
    };
  const clearBatch = batchForm?.querySelector("[data-batch-clear]");
  if (clearBatch)
    clearBatch.onclick = () => {
      batchForm
        .querySelectorAll("[data-batch-checkbox]")
        .forEach((input) => (input.checked = false));
      updateBatch();
    };
  const closeBatch = batchForm?.querySelector("[data-close-node-batch]");
  if (closeBatch)
    closeBatch.onclick = () => setNodeBatchMode(false, closeBatch);
  bindEvent(batchForm?.elements.engine, "change", updateBatch);
  bindEvent(batchForm?.elements.action, "change", updateBatch);
  updateBatch();
  if (batchForm)
    batchForm.onsubmit = async (event) => {
      event.preventDefault();
      const values = new FormData(batchForm);
      const action = String(values.get("action"));
      const engine = String(values.get("engine"));
      let selected = [
        ...batchForm.querySelectorAll("[data-batch-checkbox]:checked"),
      ].filter((input) =>
        batchAgentEligibility(agentsByID.get(input.value), action, engine)
          .eligible,
      );
      if (!selected.length || batchForm.dataset.busy === "1" || batchForm.dataset.confirming === "1")
        return;
      batchForm.dataset.confirming = "1";
      let confirmed = false;
      try {
        confirmed = await confirmAction(
          action === "upgrade-agent"
            ? `确定在 ${selected.length} 个在线节点上批量更新 Agent？升级期间节点会短暂离线。`
            : `确定在 ${selected.length} 个节点上执行 ${engineName(engine)} ${actionName(action)}？`,
          "提交批量任务",
        );
      } finally {
        batchForm.dataset.confirming = "";
      }
      if (!confirmed || batchForm.dataset.busy === "1")
        return;
      updateBatch();
      selected = [
        ...batchForm.querySelectorAll("[data-batch-checkbox]:checked"),
      ].filter((input) =>
        batchAgentEligibility(agentsByID.get(input.value), action, engine)
          .eligible,
      );
      if (!selected.length) return;
      setBatchBusy(true);
      const results = batchForm.querySelector("[data-batch-results]");
      const settled = [];
      for (const input of selected) {
        const agent = agentsByID.get(input.value);
        const eligibility = batchAgentEligibility(agent, action, engine);
        if (!eligibility.eligible) {
          settled.push({
            agent,
            error: new Error(`节点状态已变化：${eligibility.reason}`),
            ok: false,
          });
          continue;
        }
        try {
          const task = await api("/tasks", {
            method: "POST",
            body: JSON.stringify({
              agent_id: input.value,
              ...(action === "upgrade-agent" ? {} : { engine }),
              action,
            }),
          });
          settled.push({ agent, task, ok: true });
        } catch (error) {
          settled.push({ agent, error, ok: false });
        }
      }
      if (results) {
        results.hidden = false;
        results.innerHTML = `<header><b>批量结果</b><small>${settled.filter((item) => item.ok).length}/${settled.length} 成功</small></header>${settled.map((item) => `<div class="batch-result-row ${item.ok ? "ok" : "error"}"><span><b>${esc(item.agent?.name || item.agent?.id || "节点")}</b><small>${item.ok ? `任务 ${esc(item.task?.id || "已提交")}` : esc(item.error?.message || "提交失败")}</small></span>${item.ok ? "" : `<button type="button" class="button small" data-batch-retry="${esc(item.agent?.id || "")}" data-batch-retry-action="${esc(action)}" data-batch-retry-engine="${esc(engine)}">重试</button>`}</div>`).join("")}`;
      }
      setBatchBusy(false);
      const success = settled.filter((item) => item.ok).length;
      notify(success === settled.length ? `已提交 ${success} 个任务` : `已提交 ${success}/${settled.length} 个任务`, success === settled.length ? "success" : "error");
      bindBatchRetries(
        batchForm,
        action,
        engine,
        agentsByID,
        setBatchBusy,
      );
    };

    return batchForm;
  }
function bindBatchRetries(form, action, engine, agentsByID, setBatchBusy) {
  form.querySelectorAll("[data-batch-retry]").forEach((button) => {
    button.onclick = async () => {
      if (form.dataset.busy === "1") return;
      const agentID = button.dataset.batchRetry;
      const agent = agentsByID.get(agentID);
      if (!agent) return;
      const eligibility = batchAgentEligibility(agent, action, engine);
      if (!eligibility.eligible) {
        notify(`无法重试：${eligibility.reason}`, "error");
        return;
      }
      setBatchBusy(true);
      try {
        const currentAgent = agentsByID.get(agentID);
        const currentEligibility = batchAgentEligibility(
          currentAgent,
          action,
          engine,
        );
        if (!currentEligibility.eligible) {
          notify(`无法重试：${currentEligibility.reason}`, "error");
          return;
        }
        const task = await api("/tasks", {
          method: "POST",
          body: JSON.stringify({
            agent_id: agentID,
            ...(action === "upgrade-agent" ? {} : { engine }),
            action,
          }),
        });
        button.closest(".batch-result-row").className = "batch-result-row ok";
        button.closest(".batch-result-row").querySelector("small").textContent = `任务 ${task?.id || "已提交"}`;
        button.remove();
        notify("重试任务已提交");
      } catch (error) {
        notify(error.message, "error");
      } finally {
        setBatchBusy(false);
      }
    };
  });
}


  return {
    bind,
    reset() { syncActiveBatchSnapshot = null; },
    syncSnapshot(items) { syncActiveBatchSnapshot?.(items); },
  };
}
