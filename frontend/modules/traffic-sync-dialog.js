import { protocolName } from "./traffic-model.js";
import { bindDialogBackdrop, closeRetiringDialog } from "./popup.js";
import { updateFeedback, setVisible, updateFeedbackHTML } from "./presence-motion.js";
export function createTrafficSyncDialog({ api, state, esc, engineName, notify }, { traffic }) {
  let trafficSyncPending = false;
  function openTrafficSync() {
    if (trafficSyncPending) return;
    trafficSyncPending = true;
    const navigationEpoch = state.navigationEpoch;
    const accountData = state.data;
    const routeSignal = state.routeSignal;
    const isCurrent = () => state.route === "traffic" && state.navigationEpoch === navigationEpoch && state.data === accountData;
    const controller = new AbortController();
    // Keep this dialog outside the reconciled workspace so polling cannot reset
    // selection, focus or scroll while the operator reviews candidates.
    const dialog = document.createElement("dialog");
    dialog.className = "traffic-edit-dialog traffic-sync-dialog";
    dialog.dataset.trafficSyncDialog = "";
    dialog.setAttribute("aria-labelledby", "traffic-sync-title");
    dialog.innerHTML = `<header><span class="traffic-edit-icon" aria-hidden="true">↻</span><div><p class="eyebrow">端口监控</p><h2 id="traffic-sync-title">同步端口</h2><p>核对已生效端口与待部署变更</p></div><button class="deploy-command-close" type="button" data-sync-close aria-label="关闭同步弹窗">×</button></header><form data-sync-form><div class="traffic-edit-body"><p class="traffic-sync-help">按确认生效的配置同步。待部署端口不会提前计量；旧端口保留历史和共享累计，端口预留由节点所有者管理。</p><div data-sync-error role="alert" hidden></div><div data-sync-list aria-live="polite">正在读取端口…</div></div><footer><span data-sync-count>已选 0 项</span><button class="button" type="button" data-sync-retry>重新扫描</button><button class="button" type="button" data-sync-close>取消</button><button class="button primary" type="submit" data-sync-submit disabled>同步所选</button></footer></form>`;
    document.body.append(dialog);
    const list = dialog.querySelector("[data-sync-list]");
    const errorBox = dialog.querySelector("[data-sync-error]");
    const submit = dialog.querySelector("[data-sync-submit]");
    const retry = dialog.querySelector("[data-sync-retry]");
    let candidates = [];
    let saving = false;
    let loading = false;
    let snapshot = "";
    let closed = false;
    const selectedInputs = () => [...list.querySelectorAll("[data-sync-choice]:checked")];
    const updateSelection = () => {
      const count = selectedInputs().length;
      updateFeedback(dialog.querySelector("[data-sync-count]"), `已选 ${count} 项`);
      submit.disabled = saving || loading || count === 0 || count > 4096;
      const all = list.querySelector("[data-sync-all]");
      const available = candidates.filter(candidate => !candidate.blocked_reason).length;
      if (all) { all.checked = available > 0 && count === available; all.indeterminate = count > 0 && count < available; all.disabled = loading || saving || !available; }
    };
    const showError = (message) => { errorBox.className = "alert error"; updateFeedback(errorBox, message); };
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
    const navigate = () => { closeRetiringDialog(dialog); cleanup(); };
    const close = () => { if (!saving) { closeRetiringDialog(dialog); cleanup(); } };
    window.addEventListener("hashchange", navigate);
    routeSignal?.addEventListener("abort", navigate, { once: true });
    dialog.addEventListener("close", cleanup);
    dialog.addEventListener("cancel", event => { event.preventDefault(); close(); });
    dialog.querySelectorAll("[data-sync-close]").forEach(button => { button.onclick = close; });
    bindDialogBackdrop(dialog, close);
    async function loadCandidates() {
      if (loading || saving || closed) return;
      loading = true;
      candidates = []; snapshot = "";
      retry.disabled = true;
      setVisible(errorBox, false);
      updateFeedback(list, "正在扫描配置与部署版本…");
      updateSelection();
      try {
        const resource = await api("/traffic-endpoints/sync", { signal: controller.signal });
        if (closed || !isCurrent()) { cleanup(); return; }
        candidates = resource.candidates || [];
        snapshot = resource.snapshot || "";
        const agentNames = new Map((state.data.agents || []).map(agent => [agent.id, agent.name]));
        const labels = { new: "新增", changed: "修改", removed: "移除 · 历史保留", unchanged: "保持不变", deleted: "已删除 · 可恢复" };
        updateFeedbackHTML(list, candidates.length ? `<label class="traffic-sync-all"><input type="checkbox" data-sync-all>全选可同步项 · ${candidates.filter(candidate => !candidate.blocked_reason).length} 个端口</label><div class="traffic-sync-list">${candidates.map((candidate, index) => `<label class="traffic-sync-row"><input type="checkbox" data-sync-choice="${index}" ${candidate.blocked_reason ? "disabled" : ""} aria-label="${esc(agentNames.get(candidate.agent_id) || candidate.agent_id)} :${Number(candidate.port)} ${esc(labels[candidate.kind] || "同步")}"><span class="traffic-sync-source"><strong>${esc(candidate.name || "未命名端口")} · ${candidate.previous_port ? `${Number(candidate.previous_port)} → ` : ""}${Number(candidate.port)}</strong><small>${esc(agentNames.get(candidate.agent_id) || candidate.agent_id)} · ${esc(engineName(candidate.engine))} · ${esc(protocolName(candidate.protocol))}</small><small>${candidate.share_id ? `实例 ${esc(candidate.share_id)}` : "节点基础实例"} · ${candidate.config_version ? `配置 v${Number(candidate.config_version)} · ` : ""}${candidate.deployment_status === "pending" ? "待部署" : candidate.deployment_status === "uncertain" ? "部署未确认" : candidate.deployment_status === "retained" ? "旧端口" : "已部署"}</small>${candidate.blocked_reason ? `<small>${esc(candidate.blocked_reason)}</small>` : ""}</span><span class="status-label ${candidate.blocked_reason ? "warn" : "muted"}">${esc(labels[candidate.kind] || "未监控 · 可添加")}</span></label>`).join("")}</div>` : '<div class="empty compact"><strong>暂无需要同步的端口</strong></div>');
        const all = list.querySelector("[data-sync-all]");
        if (all) all.onchange = () => { list.querySelectorAll("[data-sync-choice]:not(:disabled)").forEach(input => { input.checked = all.checked; }); updateSelection(); };
        list.querySelectorAll("[data-sync-choice]").forEach(input => { input.onchange = updateSelection; });
        updateSelection();
      } catch (error) {
        if (closed || !isCurrent()) { cleanup(); return; }
        updateFeedback(list, "无法读取待同步端口");
        showError(error.message);
      } finally {
        loading = false;
        if (!closed) { retry.disabled = false; updateSelection(); }
      }
    }
    retry.onclick = loadCandidates;
    dialog.querySelector("[data-sync-form]").onsubmit = async event => {
      event.preventDefault();
      if (closed || saving || !isCurrent()) return;
      const chosen = selectedInputs().map(input => candidates[Number(input.dataset.syncChoice)]);
      if (!chosen.length || chosen.length > 4096) return;
      saving = true;
      setVisible(errorBox, false);
      dialog.querySelectorAll("button, input").forEach(control => { control.disabled = true; });
      submit.textContent = "同步中…";
      try {
        await api("/traffic-endpoints/sync", { method: "POST", body: JSON.stringify({ snapshot, selections: chosen.map(({ agent_id, port }) => ({ agent_id, port })) }) });
        if (closed || !isCurrent()) { cleanup(); return; }

        closeRetiringDialog(dialog);
        cleanup();
        const refreshed = await traffic();
        if (isCurrent()) notify(`所选 ${chosen.length} 项已核验并同步${refreshed ? "" : "；列表刷新失败，请重新进入流量页"}`);
      } catch (error) {
        if (closed || !isCurrent()) cleanup();
        else {
          showError(error.message);
          if (error.status === 409 || /重新扫描|refresh|changed/i.test(error.message)) {
            candidates = []; snapshot = "";
            updateFeedback(list, "候选已失效，请重新扫描后选择端口");
          }
        }
      } finally {
        saving = false;
        if (!closed) {
          dialog.querySelectorAll("button").forEach(control => { control.disabled = false; });
          list.querySelectorAll("[data-sync-choice]").forEach(control => { control.disabled = Boolean(candidates[Number(control.dataset.syncChoice)]?.blocked_reason); });
          submit.textContent = "同步所选";
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
