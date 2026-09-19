// Floating controls for selecting nodes and submitting batch tasks.
export function agentBatchBarMarkup({ engines, engineName, esc }) {
  return `<aside class="node-batch-bar" aria-label="批量操作栏">
    <div class="batch-selection-head">
      <label class="batch-select-all"><input type="checkbox" data-batch-select-all aria-label="全选当前合格节点" aria-checked="false"><span data-batch-select-all-label>全选</span></label>
      <strong data-batch-count aria-live="polite">已选择 0 个节点</strong>
      <button class="button small" type="button" data-batch-clear disabled>清空</button>
      <button class="node-batch-close" type="button" data-close-node-batch aria-label="退出批量操作" title="退出批量操作">×</button>
    </div>
    <div class="batch-controls">
      <label><span>批量动作</span><select name="action"><option value="upgrade-agent">更新 Agent</option><option value="install">更新内核版本</option><option value="restart">重启服务</option><option value="status">查询状态</option><option value="start">启动服务</option><option value="stop">停止服务</option></select></label>
      <label data-batch-engine-wrap hidden><span>目标内核</span><select name="engine">${engines.map((engine) => `<option value="${esc(engine)}">${esc(engineName(engine))}</option>`).join("")}</select></label>
      <label data-batch-version-wrap hidden><span>目标版本</span><select name="release_channel" disabled><option value="stable">最新稳定版</option><option value="development">最新开发版</option><option value="custom">指定版本</option></select></label>
      <label data-batch-custom-wrap hidden><span>指定版本号</span><input name="custom_version" maxlength="64" autocomplete="off" placeholder="填写 Release 版本号" disabled></label>
    </div>
    <div class="batch-submit-row"><small data-batch-hint>仅可选择在线且支持远程升级的节点。</small><button class="button small primary" type="submit" disabled>提交批量任务</button></div>
    <section class="batch-results" data-batch-results aria-live="polite" hidden></section>
  </aside>`;
}
