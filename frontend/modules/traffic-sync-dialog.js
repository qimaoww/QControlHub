import { protocolName } from "./traffic-model.js";
export function createTrafficSyncDialog({ api, state, esc, engineName, notify }, { traffic }) {
  let trafficSyncPending = false;
  function openTrafficSync() {
    if (trafficSyncPending) return;
    trafficSyncPending = true;
    const navigationEpoch = state.navigationEpoch;
    const routeSignal = state.routeSignal;
    const isCurrent = () => state.route === "traffic" && state.navigationEpoch === navigationEpoch;
    const controller = new AbortController();
    // Keep this dialog outside the reconciled workspace so polling cannot reset
    // selection, focus or scroll while the operator reviews candidates.
    const dialog = document.createElement("dialog");
    dialog.className = "traffic-edit-dialog traffic-sync-dialog";
    dialog.dataset.trafficSyncDialog = "";
    dialog.setAttribute("aria-labelledby", "traffic-sync-title");
    dialog.innerHTML = `<header><span class="traffic-edit-icon" aria-hidden="true">↻</span><div><p class="eyebrow">端口监控</p><h2 id="traffic-sync-title">同步端口</h2><p>选择已删除或尚未监控的端口</p></div><button class="deploy-command-close" type="button" data-sync-close aria-label="关闭同步弹窗">×</button></header><form data-sync-form><div class="traffic-edit-body"><p class="traffic-sync-help">仅处理勾选项，保留已有监控。恢复后仅计新流量，已删除的历史和配额不恢复。新端口取自已保存配置。</p><div data-sync-error role="alert" hidden></div><div data-sync-list aria-live="polite">正在读取端口…</div></div><footer><span data-sync-count>已选 0 项</span><button class="button" type="button" data-sync-retry hidden>重新读取</button><button class="button" type="button" data-sync-close>取消</button><button class="button primary" type="submit" data-sync-submit disabled>恢复 / 添加所选</button></footer></form>`;
    document.body.append(dialog);
    const list = dialog.querySelector("[data-sync-list]");
    const errorBox = dialog.querySelector("[data-sync-error]");
    const submit = dialog.querySelector("[data-sync-submit]");
    const retry = dialog.querySelector("[data-sync-retry]");
    let candidates = [];
    let saving = false;
    let closed = false;
    const selectedInputs = () => [...list.querySelectorAll("[data-sync-choice]:checked")];
    const updateSelection = () => {
      const count = selectedInputs().length;
      dialog.querySelector("[data-sync-count]").textContent = `已选 ${count} 项`;
      submit.disabled = saving || count === 0 || count > 4096;
      const all = list.querySelector("[data-sync-all]");
      if (all) { all.checked = count === candidates.length; all.indeterminate = count > 0 && count < candidates.length; }
    };
    const showError = (message) => { errorBox.hidden = false; errorBox.className = "alert error"; errorBox.textContent = message; };
    const cleanup = () => {
      if (closed) return;
      closed = true;
      controller.abort();
      window.removeEventListener("hashchange", navigate);
      routeSignal?.removeEventListener("abort", navigate);
      dialog.remove();
      trafficSyncPending = false;
      const button = document.querySelector("[data-traffic-sync]");
      if (button) button.disabled = false;
    };
    const navigate = () => { dialog.close(); cleanup(); };
    const close = () => { if (!saving) { dialog.close(); cleanup(); } };
    window.addEventListener("hashchange", navigate);
    routeSignal?.addEventListener("abort", navigate, { once: true });
    dialog.addEventListener("close", cleanup);
    dialog.addEventListener("cancel", event => { event.preventDefault(); close(); });
    dialog.querySelectorAll("[data-sync-close]").forEach(button => { button.onclick = close; });
    dialog.onclick = event => { if (event.target === dialog) close(); };
    async function loadCandidates() {
      errorBox.hidden = true;
      retry.hidden = true;
      try {
        const resource = await api("/traffic-endpoints/sync", { signal: controller.signal });
        if (closed || !isCurrent()) { cleanup(); return; }
        candidates = resource.candidates || [];
        const agentNames = new Map((state.data.agents || []).map(agent => [agent.id, agent.name]));
        list.innerHTML = candidates.length ? `<label class="traffic-sync-all"><input type="checkbox" data-sync-all>全选 · ${candidates.length} 个端口</label><div class="traffic-sync-list">${candidates.map((candidate, index) => `<label class="traffic-sync-row"><input type="checkbox" data-sync-choice="${index}" aria-label="${esc(agentNames.get(candidate.agent_id) || candidate.agent_id)} :${Number(candidate.port)} ${candidate.kind === "deleted" ? "恢复" : "添加"}"><span class="traffic-sync-source"><strong>${esc(candidate.name || "未命名端口")}</strong><small>${esc(agentNames.get(candidate.agent_id) || candidate.agent_id)} · ${esc(engineName(candidate.engine))} · :${Number(candidate.port)} · ${esc(protocolName(candidate.protocol))}</small></span><span class="status-label ${candidate.kind === "deleted" ? "warn" : "muted"}">${candidate.kind === "deleted" ? "已删除 · 可恢复" : "未监控 · 可添加"}</span></label>`).join("")}</div>` : '<div class="empty compact"><strong>暂无需要同步的端口</strong></div>';
        const all = list.querySelector("[data-sync-all]");
        if (all) all.onchange = () => { list.querySelectorAll("[data-sync-choice]").forEach(input => { input.checked = all.checked; }); updateSelection(); };
        list.querySelectorAll("[data-sync-choice]").forEach(input => { input.onchange = updateSelection; });
        updateSelection();
      } catch (error) {
        if (closed || !isCurrent()) { cleanup(); return; }
        list.textContent = "无法读取待同步端口";
        showError(error.message);
        retry.hidden = false;
      }
    }
    retry.onclick = loadCandidates;
    dialog.querySelector("[data-sync-form]").onsubmit = async event => {
      event.preventDefault();
      if (closed || saving || !isCurrent()) return;
      const chosen = selectedInputs().map(input => candidates[Number(input.dataset.syncChoice)]);
      if (!chosen.length || chosen.length > 4096) return;
      saving = true;
      errorBox.hidden = true;
      dialog.querySelectorAll("button, input").forEach(control => { control.disabled = true; });
      submit.textContent = "同步中…";
      try {
        await api("/traffic-endpoints/sync", { method: "POST", body: JSON.stringify({ selections: chosen.map(({ agent_id, port }) => ({ agent_id, port })) }) });
        if (closed || !isCurrent()) { cleanup(); return; }
        const restored = chosen.filter(candidate => candidate.kind === "deleted").length;
        dialog.close();
        cleanup();
        const refreshed = await traffic();
        if (isCurrent()) notify(`所选端口已同步：恢复 ${restored} 项，添加 ${chosen.length - restored} 项${refreshed ? "" : "；列表刷新失败，请重新进入流量页"}`);
      } catch (error) {
        if (closed || !isCurrent()) cleanup();
        else showError(error.message);
      } finally {
        saving = false;
        if (!closed) {
          dialog.querySelectorAll("button, input").forEach(control => { control.disabled = false; });
          submit.textContent = "恢复 / 添加所选";
          updateSelection();
        }
      }
    };
    const button = document.querySelector("[data-traffic-sync]");
    if (button) button.disabled = true;
    dialog.showModal();
    loadCandidates();
  }

  return { openTrafficSync, isPending: () => trafficSyncPending };
}
