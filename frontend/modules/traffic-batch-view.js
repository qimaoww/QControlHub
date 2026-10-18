import { dateInputValue } from "./traffic-model.js";

export const trafficBatchState = state => state.data.trafficBatch ||= { mode: false, selected: [], action: "delete", busy: false, results: [] };
export const trafficBatchActions = { delete: "删除流量记录", reset: "清零当前周期", quota: "设置配额", "remove-quota": "取消配额" };

export function clearTrafficBatchResults(batch) {
  batch.results = [];
  batch.last = null;
  batch.uncertainRequest = null;
}

export function trafficBatchBarMarkup(batch, esc) {
  const success = batch.results.filter(item => item.status === "succeeded").length;
  const failed = batch.results.filter(item => item.status === "failed").length;
  const skipped = batch.results.length - success - failed;
  return `<aside class="node-batch-bar" aria-label="流量批量操作栏">
    <div class="batch-selection-head"><label class="batch-select-all"><input type="checkbox" data-traffic-select-all aria-label="全选当前可操作记录"><span>全选</span></label><strong data-traffic-selected-count aria-live="polite">已选 ${batch.selected.length}</strong><button class="button small" type="button" data-traffic-selection-clear>取消选择</button></div>
    <div class="batch-operation-row"><div class="batch-controls"><label>操作<select data-traffic-batch-action aria-label="流量批量操作">${Object.entries(trafficBatchActions).map(([value,label]) => `<option value="${value}" ${value === batch.action ? "selected" : ""}>${label}</option>`).join("")}</select></label></div><button class="button small primary" type="submit" data-traffic-batch-submit ${batch.busy || !batch.selected.length ? "disabled" : ""}>${batch.busy ? "处理中…" : "执行"}</button></div>
    <button class="node-batch-close" type="button" data-traffic-batch-close aria-label="退出批量操作" ${batch.busy ? "disabled" : ""}>×</button>
    <section class="batch-results" aria-live="polite" ${batch.results.length ? "" : "hidden"}><p class="batch-result-summary" role="status">成功 ${success} · 失败 ${failed} · 未执行 ${skipped}</p><div class="batch-result-list">${batch.results.map(item => `<div class="batch-result-row ${item.status === "succeeded" ? "" : "error"}"><span><b>${esc(item.label || item.id)}</b><small>${esc(item.status === "succeeded" ? "成功" : item.message || "未执行")}</small></span></div>`).join("")}</div>${failed ? `<button class="button small" type="button" data-traffic-batch-retry ${batch.busy ? "disabled" : ""}>重试失败项（${failed}）</button>` : ""}</section>
  </aside>`;
}

export function trafficBatchQuotaMarkup(count) {
  return `<header><span class="traffic-edit-icon" aria-hidden="true">✎</span><div><p class="eyebrow">批量配额</p><h2 id="traffic-batch-quota-title">设置 ${count} 条记录的配额</h2></div><button class="deploy-command-close" type="button" data-quota-close aria-label="关闭配额弹窗">×</button></header>
    <form data-batch-quota-form><div class="traffic-edit-body"><div class="traffic-form-grid">
      <label><span><input type="checkbox" data-quota-field="limit_bytes" checked> 周期额度（GiB）</span><input name="limit_gb" type="number" min="0" max="8388607" step="0.000001" required placeholder="0：仅监控"></label>
      <label><span><input type="checkbox" data-quota-field="cycle"> 统计周期</span><select name="cycle" disabled><option value="monthly">每月</option><option value="yearly">每年</option></select></label>
      <label><span><input type="checkbox" data-quota-field="cycle_anchor"> 周期起始日期</span><input name="cycle_anchor" type="date" value="${dateInputValue()}" max="${dateInputValue()}" required disabled></label>
      <label><span><input type="checkbox" data-quota-field="auto_block"> 超额自动封禁</span><select name="auto_block" disabled><option value="true">开启</option><option value="false">关闭</option></select></label>
    </div><p class="settings-hint">仅修改勾选的字段</p></div><footer><span></span><button class="button" type="button" data-quota-close>取消</button><button class="button primary" type="submit">保存配额</button></footer></form>`;
}
