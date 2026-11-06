import { batchAgentEligibility, batchTaskOptions, batchCoreVersionLabel } from '../modules/agent-batch.js';
import { coreInstallBlockReason } from '../modules/core-install-state.js';
import { coreSourceForInstall } from '../modules/core-install-source.js';
import { createServiceActionGuard } from '../modules/agent-service-state.js';
import { poll } from './live-dom.js';
import { node, button, actions, stack, grid, table, field, muted, notice, list, pathID } from './ui.js';
import { actionName, taskDetail } from './operations.js';

export function openNodeBatch(ctx, getAgents, selected) {
  const chosen = getAgents().filter(agent => selected.has(agent.id));
  if (!chosen.length) { ctx.announce('请先选择节点', 'error'); return; }
  const scope = (agent, engine) => JSON.stringify([agent.can_manage !== false,
    agent.shared_allocation?.owner_id || '', agent.runtime?.[engine]?.instance?.id || '']);
  const openedScopes = new Map(chosen.map(agent => [agent.id, new Map([undefined, 'mihomo', 'xray', 'sing-box', 'ss-rust'].map(engine => [engine, scope(agent, engine)]))]));
  const guard = createServiceActionGuard(ctx.can);
  const engine = field('内核', 'engine', 'mihomo', { options: ['mihomo', 'xray', 'sing-box', 'ss-rust'] });
  const action = field('操作', 'action', 'status', { options: [['status', '查询状态'], ['start', '启动'], ['restart', '重启'], ['stop', '停止'], ['install', '更新内核'], ['upgrade-agent', '更新 Agent']] });
  const channel = field('发行通道', 'release_channel', 'stable', { options: [['stable', '稳定版'], ['development', '开发版'], ['custom', '指定版本']] });
  const version = field('指定版本号', 'custom_version', '', { maxlength: 100 });
  const source = field('开发版来源', 'core_source', 'official', { options: [['official', 'MetaCubeX 官方'], ['mirror', 'vernesong 镜像']] });
  const operationFields = grid(engine, action), versionFields = grid(channel, version);
  const summary = notice('');
  const stale = notice('');
  let agents = getAgents(), busy = false, box;
  const terminal = task => ['succeeded', 'failed', 'canceled'].includes(task?.status);
  const rows = new Map();
  const element = control => control.querySelector('input,select');
  const readOptions = values => {
    const options = batchTaskOptions({ get: name => values[name] });
    if (options.action === 'install') options.core_source = coreSourceForInstall(options.engine, values.release_channel, values.core_source);
    return options;
  };
  function eligibility(agent, options) {
    if (!agent) return '节点已不在当前授权范围';
    if (scope(agent, options.engine) !== openedScopes.get(agent.id)?.get(options.engine)) return '目标实例已变化，请重新选择节点';
    if (!ctx.can('tasks.execute')) return '需要任务执行权限';
    if (agent.can_manage === false && ['status', 'start', 'restart', 'stop'].includes(options.action)) {
      const runtime = agent.runtime?.[options.engine] || {};
      return guard(options.action, agent.status === 'online', runtime.installed, runtime.service_status, agent, options.engine) ? '本人实例当前状态或权限不支持此操作' : '';
    }
    const base = batchAgentEligibility(agent, options.action, options.engine);
    if (!base.eligible) return base.reason;
    if (options.action === 'install') return coreInstallBlockReason(agent, options.engine, ctx.can);
    if (options.action === 'upgrade-agent') return ctx.can('agents.manage', agent) && ctx.can('host.manage', agent) ? '' : '需要此节点的管理授权';
    const runtime = agent.runtime?.[options.engine] || {};
    return guard(options.action, true, runtime.installed, runtime.service_status, agent, options.engine) ? '内核状态或权限不支持此操作' : '';
  }
  function controls() {
    form.querySelector('fieldset').disabled = busy;
    form.querySelector('[type=submit]').disabled = busy;
    const installing = element(action).value === 'install';
    const upgrading = element(action).value === 'upgrade-agent';
    const development = element(engine).value === 'mihomo';
    const option = element(channel).querySelector('[value=development]');
    option.disabled = !development;
    if (!development && element(channel).value === 'development') element(channel).value = 'stable';
    ctx.visible(engine, !upgrading);
    operationFields.classList.toggle('a-grid', !upgrading);
    ctx.visible(versionFields, installing);
    ctx.visible(channel, installing);
    ctx.visible(version, installing && element(channel).value === 'custom');
    element(version).required = installing && element(channel).value === 'custom';
    ctx.visible(source, installing && development && element(channel).value === 'development');
    const options = { action: element(action).value, engine: element(engine).value };
    for (const [id, row] of rows) {
      const reason = eligibility(agents.find(agent => agent.id === id), options);
      if (reason && !busy) row.choose.checked = false;
      row.choose.disabled = busy || Boolean(reason);
      const label = reason || '可执行';
      if (row.availability.textContent !== label) row.availability.textContent = label;
      row.retry.disabled = busy || Boolean(row.result && eligibility(agents.find(agent => agent.id === id), row.result.options));
    }
  }
  function receipt(row) {
    const result = row.result;
    const text = result.task ? ({pending:'等待执行',running:'执行中',succeeded:'成功',failed:'失败',canceled:'已取消'}[result.task.status] || '已提交') : result.error || '正在提交';
    if (row.status.textContent !== text) row.status.textContent = text;
    const details = result.task?.error || '';
    if (row.error.textContent !== details) row.error.textContent = details;
    row.retry.hidden = !result.error;
    row.detail.hidden = !result.task || !ctx.can('tasks.read');
  }
  function count() {
    const values = [...rows.values()].map(row => row.result).filter(Boolean);
    const submitted = values.filter(value => value.task).length;
    const failed = values.filter(value => value.error).length;
    const text = values.length ? `已提交 ${submitted}，提交失败 ${failed}${busy ? ' · 提交中' : ''}` : '';
    if (summary.textContent !== text) summary.textContent = text;
  }
  async function submitOne(id, options) {
    const row = rows.get(id);
    if (!ctx.current() || !box.isConnected) return;
    const reason = eligibility(agents.find(agent => agent.id === id), options);
    row.result = { options: { ...options }, error: reason || '' };
    receipt(row);
    if (!reason) {
      try {
        const task = await ctx.api('/tasks', { method: 'POST', body: { agent_id: id, ...options } });
        if (!ctx.current() || !box.isConnected) return;
        row.result.task = task;
      } catch (error) {
        if (error.name === 'AbortError') throw error;
        row.result.error = error.message;
      }
      receipt(row);
    }
    count();
  }
  const form = ctx.form([
    operationFields, versionFields, source, stale,
  ], '执行批量操作', async values => {
    const options = readOptions(values);
    agents = list(await ctx.api('/agents'));
    const targets = [...rows].filter(([id, row]) => row.choose.checked && !eligibility(agents.find(agent => agent.id === id), options)).map(([id]) => id);
    if (!targets.length) throw new Error('没有符合当前动作和权限的节点');
    const hint = ({install:'更新后对应内核服务会重启。', 'upgrade-agent':'更新期间 Agent 短暂离线。', stop:'现有连接会立即中断。',restart:'连接会短暂中断。'})[options.action] || '';
    const context = [options.engine, options.core_version && batchCoreVersionLabel(options.core_version)].filter(Boolean).join(' · ');
    if (!await ctx.confirm(`对 ${targets.length} 个节点执行“${actionName(options.action)}”${context ? ` · ${context}` : ''}？${hint}`, '执行批量操作') || !ctx.current() || !box.isConnected) return;
    for (const row of rows.values()) {
      row.result = null; row.status.textContent = ''; row.error.textContent = '';
      row.retry.hidden = row.detail.hidden = true;
    }
    busy = true;
    try {
      agents = list(await ctx.api('/agents'));
      controls();
      for (const id of targets) { if (!ctx.current() || !box.isConnected) break; await submitOne(id, options); }
    } finally { busy = false; controls(); count(); }
  });
  const targetRows = chosen.map(agent => {
    const choose = field('选择', `batch:${agent.id}`, true, { type: 'checkbox', 'aria-label': `批量操作：${agent.name}` });
    const row = { choose: element(choose), availability: muted(''), status: node('span'), error: notice(''), result: null };
    row.retry = button('重试提交', () => void ctx.mutate(null, async () => {
      if (busy || !row.result) return;
      const options = { ...row.result.options };
      busy = true;
      try { agents = list(await ctx.api('/agents')); controls(); await submitOne(agent.id, options); }
      finally { busy = false; controls(); count(); }
    }));
    row.detail = button('任务详情', event => void ctx.mutate(event.currentTarget, () => taskDetail(ctx, row.result.task.id)));
    row.retry.hidden = row.detail.hidden = true;
    rows.set(agent.id, row);
    return [choose, agent.name, row.availability, stack(row.status, row.error, actions(row.retry, row.detail))];
  });
  // Freeze selection together with the request controls during submission.
  form.querySelector('fieldset').append(node('div', { className: 'a-traffic-list' }, table(['选择', '节点', '条件', '执行结果'], targetRows, {
    label: '批量操作节点与结果', rowKey: row => row[0].querySelector('input').name,
  })));
  form.append(summary);
  for (const control of [engine, action, channel]) element(control).addEventListener('change', controls);
  box = ctx.dialog('批量操作', form, { wide: true });
  controls();
  poll({ ...ctx, current: () => ctx.current() && box.isConnected, onRefreshError: () => { stale.textContent = '状态刷新失败，保留已有结果并重试。'; } }, async () => {
    if (busy) return;
    const fresh = list(await ctx.api('/agents'));
    if (!ctx.current() || !box.isConnected) return;
    agents = fresh;
    stale.textContent = '';
    controls();
    if (!ctx.can('tasks.read')) return;
    for (const row of rows.values()) {
      if (!row.result?.task || terminal(row.result.task)) continue;
      const id = row.result.task.id;
      const task = await ctx.api(`/tasks/${pathID(id)}?view=status`);
      if (!ctx.current() || !box.isConnected) return;
      if (row.result?.task?.id === id) { row.result.task = task; receipt(row); }
    }
  });
}
