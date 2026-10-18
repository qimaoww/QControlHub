import { trafficBatchActions, trafficBatchState, trafficBatchQuotaMarkup } from "./traffic-batch-view.js";
import { gibibyte } from "./traffic-model.js";
import { bindDialogBackdrop } from "./popup.js";

export function createTrafficBatch({ api, state, notify, confirmAction }, { renderTraffic, traffic, cancelCardDrag }) {
  const render = () => renderTraffic(state.data.agents || [], state.data.trafficPolicies || [], state.data.trafficEndpoints || []);
  let resizeObserver;
  function bind({ eligibleIDs }) {
    resizeObserver?.disconnect();
    const data = state.data, batch = trafficBatchState(state);
    const isCurrent = () => state.data === data && state.route === "traffic";
    const update = () => {
      if (!isCurrent()) return;
      document.querySelectorAll("[data-traffic-choice]").forEach(input => { input.checked = batch.selected.includes(input.dataset.trafficChoice); input.disabled = batch.busy || !eligibleIDs.has(input.dataset.trafficChoice); });
      const all = document.querySelector("[data-traffic-select-all]");
      if (all) { all.checked = eligibleIDs.size > 0 && batch.selected.length === eligibleIDs.size; all.indeterminate = batch.selected.length > 0 && !all.checked; all.setAttribute("aria-checked", all.indeterminate ? "mixed" : String(all.checked)); all.disabled = batch.busy || !eligibleIDs.size; }
      const count = document.querySelector("[data-traffic-selected-count]"); if (count) count.textContent = `已选 ${batch.selected.length}`;
      document.querySelectorAll("[data-traffic-batch-toggle], [data-traffic-batch-close], [data-traffic-selection-clear], [data-traffic-batch-action], [data-traffic-batch-retry], [data-traffic-filter], [data-traffic-filter-reset]").forEach(control => { control.disabled = batch.busy || control.matches("[data-traffic-selection-clear]") && !batch.selected.length || control.matches("[data-traffic-filter-reset]") && !Object.entries(state.data.trafficFilters || {}).some(([key,value]) => value && (key !== "user_scope" || value === "all")); });
      const submit = document.querySelector("[data-traffic-batch-submit]"); if (submit) { submit.disabled = batch.busy || !batch.selected.length; submit.textContent = batch.busy ? "处理中…" : "执行"; }
    };
    const setMode = enabled => { if (batch.busy) return; cancelCardDrag(); batch.mode = enabled; batch.selected = []; batch.results = []; render(); };
    document.querySelectorAll("[data-traffic-batch-toggle]").forEach(button => { button.onclick = () => setMode(!batch.mode); });
    const form = document.querySelector("[data-traffic-batch-form]");
    if (!form) return;
    form.querySelector("[data-traffic-batch-close]").onclick = () => setMode(false);
    form.querySelector("[data-traffic-selection-clear]").onclick = () => { batch.selected = []; update(); };
    form.querySelector("[data-traffic-select-all]").onchange = event => { batch.selected = event.target.checked ? [...eligibleIDs] : []; update(); };
    form.querySelector("[data-traffic-batch-action]").onchange = event => { batch.action = event.target.value; render(); };
    form.querySelectorAll("[data-traffic-choice]").forEach(input => { input.onchange = () => { batch.selected = batch.selected.filter(id => id !== input.dataset.trafficChoice); if (input.checked && eligibleIDs.has(input.dataset.trafficChoice)) batch.selected.push(input.dataset.trafficChoice); update(); }; });
    const bar = form.querySelector(".node-batch-bar");
    if (globalThis.ResizeObserver) { resizeObserver = new ResizeObserver(() => form.style.setProperty("--batch-bar-height", `${Math.ceil(bar.getBoundingClientRect().height)}px`)); resizeObserver.observe(bar); }
    async function execute(input) {
      batch.busy = true; update();
      const labels = new Map((data.trafficPolicies || []).map(p => [p.id, `${(data.agents || []).find(a => a.id === p.agent_id)?.name || p.agent_id} :${p.port}`]));
      try {
        const response = await api("/traffic-policies/batch", { method: "POST", body: JSON.stringify(input) });
        if (state.data !== data) return;
        batch.uncertainRequest = null;
        batch.results = response.results.map(result => ({ ...result, label: labels.get(result.id) }));
        batch.last = input;
        const success = new Set(batch.results.filter(item => item.status === "succeeded").map(item => item.id));
        batch.selected = batch.selected.filter(id => !success.has(id));
        if (isCurrent()) notify(`成功 ${success.size} · 失败 ${batch.results.filter(item => item.status === "failed").length} · 未执行 ${batch.results.filter(item => item.status === "not_executed").length}`);
      } catch (error) {
        if (state.data !== data) return;
        // Replaying this exact request retrieves atomic receipts if the response
        // was lost after commit; no successful reset can run twice.
        batch.uncertainRequest = input;
        batch.results = input.ids.map(id => ({ id, label: labels.get(id), status: "failed", message: error.message }));
      } finally {
        batch.busy = false;
        if (isCurrent()) { const refreshed = await traffic(); if (!refreshed && isCurrent()) render(); }
      }
    }
    form.onsubmit = async event => {
      event.preventDefault();
      if (batch.busy || !batch.selected.length || !isCurrent()) return;
      const ids = batch.selected.filter(id => eligibleIDs.has(id)); if (!ids.length) return;
      const action = batch.action;
      batch.busy = true; update();
      try {
        const quota = action === "quota" ? await quotaDialog(ids.length, state.routeSignal) : undefined;
        if (!isCurrent() || action === "quota" && !quota) return;
        if (action !== "quota" && !await confirmAction(`${trafficBatchActions[action]} · ${ids.length} 条记录${action === "delete" ? "\n删除当前记录及每日历史" : action === "reset" ? "\n清零当前周期并解除封禁" : "\n解除自动封禁，继续统计"}`, trafficBatchActions[action])) return;
        if (!isCurrent()) return;
        await execute({ request_id: crypto.randomUUID(), action, ids, ...(quota ? { quota } : {}) });
      } finally { batch.busy = false; update(); }
    };
    const retry = form.querySelector("[data-traffic-batch-retry]");
    if (retry) retry.onclick = async () => {
      if (batch.busy || !isCurrent()) return;
      if (batch.uncertainRequest) { await execute(batch.uncertainRequest); return; }
      const failed = batch.results.filter(item => item.status === "failed" && eligibleIDs.has(item.id)).map(item => item.id);
      if (!failed.length) return;
      await execute({ ...batch.last, request_id: crypto.randomUUID(), ids: failed });
    };
    update();
  }
  return { bind, cancel() { resizeObserver?.disconnect(); } };
}

function quotaDialog(count, routeSignal) {
  return new Promise(resolve => {
    const dialog = document.createElement("dialog");
    dialog.className = "traffic-edit-dialog traffic-create-dialog";
    dialog.setAttribute("aria-labelledby", "traffic-batch-quota-title");
    dialog.innerHTML = trafficBatchQuotaMarkup(count);
    document.body.append(dialog);
    let done = false;
    const close = value => { if (done) return; done = true; routeSignal?.removeEventListener("abort", abort); dialog.close(); dialog.remove(); resolve(value); };
    const abort = () => close(null);
    routeSignal?.addEventListener("abort", abort, { once: true });
    dialog.querySelectorAll("[data-quota-close]").forEach(button => { button.onclick = () => close(null); });
    dialog.addEventListener("cancel", event => { event.preventDefault(); close(null); });
    dialog.addEventListener("close", () => close(null));
    bindDialogBackdrop(dialog, () => close(null));
    const form = dialog.querySelector("form");
    form.querySelectorAll("[data-quota-field]").forEach(input => { input.onchange = () => { input.closest("label").querySelector("input[name],select[name]").disabled = !input.checked; form.querySelector("[type=submit]").disabled = !form.querySelector("[data-quota-field]:checked"); }; });
    form.onsubmit = event => {
      event.preventDefault();
      const fields = new Set([...form.querySelectorAll("[data-quota-field]:checked")].map(input => input.dataset.quotaField));
      if (!fields.size) return;
      const values = new FormData(form), patch = {};
      if (fields.has("limit_bytes")) patch.limit_bytes = Math.round(Number(values.get("limit_gb")) * gibibyte);
      if (fields.has("cycle")) patch.cycle = values.get("cycle");
      if (fields.has("cycle_anchor")) patch.cycle_anchor = `${values.get("cycle_anchor")}T00:00:00Z`;
      if (fields.has("auto_block")) patch.auto_block = values.get("auto_block") === "true";
      close(patch);
    };
    dialog.showModal();
  });
}
