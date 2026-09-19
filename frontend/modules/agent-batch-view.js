// Floating controls for selecting nodes and submitting batch tasks.
export function agentBatchBarMarkup({ engines, engineName, esc }) {
  return `<aside class="node-batch-bar" aria-label="批量操作栏" data-batch-action="upgrade-agent">
    <div class="batch-selection-head">
      <label class="batch-select-all"><input type="checkbox" data-batch-select-all aria-label="全选当前合格节点" aria-checked="false"><span data-batch-select-all-label>全选</span></label>
      <strong data-batch-count aria-live="polite">已选 0</strong>
    </div>
    <div class="batch-operation-row"><div class="batch-controls">
      <label><select name="action" aria-label="批量操作"><option value="upgrade-agent">更新 Agent</option><option value="install">更新内核</option><option value="restart">重启服务</option><option value="status">查询状态</option><option value="start">启动服务</option><option value="stop">停止服务</option></select></label>
      <label data-batch-engine-wrap hidden><select name="engine" aria-label="内核">${engines.map((engine) => `<option value="${esc(engine)}">${esc(engineName(engine))}</option>`).join("")}</select></label>
      <label data-batch-version-wrap hidden><select name="release_channel" aria-label="版本" disabled><option value="stable">最新稳定版</option><option value="development">最新开发版</option><option value="custom">指定版本</option></select></label>
      <label data-batch-custom-wrap hidden><input name="custom_version" aria-label="版本号" aria-describedby="batch-version-error" maxlength="64" autocomplete="off" placeholder="版本号" disabled></label>
    </div>
    <button class="button small primary" type="submit" disabled>执行</button></div>
      <button class="node-batch-close" type="button" data-close-node-batch aria-label="退出批量操作" title="退出批量操作">×</button>
    <p class="batch-field-error" id="batch-version-error" data-batch-error role="alert" hidden></p>
    <section class="batch-results" data-batch-results aria-live="polite" hidden></section>
  </aside>`;
}
