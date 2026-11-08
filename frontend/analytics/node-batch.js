import { batchAgentEligibility, batchTaskOptions, batchCoreVersionLabel } from '../modules/agent-batch.js';
import { coreInstallBlockReason } from '../modules/core-install-state.js';
import { coreSourceForInstall } from '../modules/core-install-source.js';
import { createServiceActionGuard } from '../modules/agent-service-state.js';
import { poll } from './live-dom.js';
import { node, button, actions, stack, table, field, muted, notice, list, pathID } from './ui.js';
import { actionName, taskDetail } from './operations.js';

export function openNodeBatch(ctx, getAgents) {
  const scope = (agent, engine) => JSON.stringify([agent.can_manage !== false,
    agent.shared_allocation?.owner_id || '', agent.runtime?.[engine]?.instance?.id || '']);
  const openedScopes = new Map();
  const guard = createServiceActionGuard(ctx.can);
  const engine = field('内核', 'engine', 'mihomo', { options: ['mihomo', 'xray', 'sing-box', 'ss-rust'] });
  const action = field('操作', 'action', 'status', { options: [['status', '查询状态'], ['start', '启动'], ['restart', '重启'], ['stop', '停止'], ['install', '更新内核'], ['upgrade-agent', '更新 Agent']] });
  const channel = field('发行通道', 'release_channel', 'stable', { options: [['stable', '稳定版'], ['development', '开发版'], ['custom', '指定版本']] });
  const version = field('指定版本号', 'custom_version', '', { maxlength: 100 });
  const source = field('开发版来源', 'core_source', 'official', { options: [['official', 'MetaCubeX 官方'], ['mirror', 'vernesong 镜像']] });
  const operationFields = node('div', { className: 'a-filter-fields' }, action, engine, channel, version, source);
  const summary = notice('', 'ok'), stale = notice('');
  const selection = muted('');
  selection.setAttribute('role', 'status');
  let agents = getAgents(), busy = false, box, targetTable, refreshSequence = 0;
  const active = () => ctx.current() && box?.isConnected;
  const terminal = task => ['succeeded', 'failed', 'canceled'].includes(task?.status);
  // This model includes unmounted pages. No state is shared with the parent page.
  const rows = new Map();
  const element = control => control.querySelector('input,select');
  const readOptions = values => {
    const options = batchTaskOptions({ get: name => values[name] });
    if (options.action === 'install') options.core_source = coreSourceForInstall(options.engine, values.release_channel, values.core_source);
    return options;
  };
  function eligibility(agent, options) {
    if (!agent) return '节点已不在当前授权范围';
    if (scope(agent, options.engine) !== openedScopes.get(agent.id)?.get(options.engine)) return '目标实例已变化，请关闭后重新选择';
    if (!ctx.can('tasks.execute')) return '需要任务执行权限';
    if (!(agent.capabilities || []).includes(options.engine) && options.action !== 'upgrade-agent') return '内核能力未启用或不受支持';
    if (agent.can_manage === false && ['status', 'start', 'restart', 'stop'].includes(options.action)) {
      const runtime = agent.runtime?.[options.engine] || {};
      return guard(options.action, agent.status === 'online', runtime.installed, runtime.service_status, agent, options.engine) ? '本人实例当前状态或权限不支持此操作' : '';
    }
    const base = batchAgentEligibility(agent, options.action, options.engine);
    if (!base.eligible) return base.reason;
    if (options.action === 'install') {
      if (options.core_source === 'mirror' && !(agent.features || []).includes('mihomo-development-source-v1')) return 'Agent 不支持此开发版来源';
      return coreInstallBlockReason(agent, options.engine, ctx.can);
    }
    if (options.action === 'upgrade-agent') return ctx.can('agents.manage', agent) && ctx.can('host.manage', agent) ? '' : '需要此节点的管理授权';
    const runtime = agent.runtime?.[options.engine] || {};
    return guard(options.action, true, runtime.installed, runtime.service_status, agent, options.engine) ? '内核状态或权限不支持此操作' : '';
  }
  const all = button('全选', () => {
    if (busy) return;
    const eligible = [...rows.values()].filter(row => !row.reason);
    const selected = eligible.length > 0 && eligible.every(row => row.selected);
    eligible.forEach(row => { row.selected = !selected; });
    controls();
  });
  function controls() {
    form.querySelector('fieldset').disabled = busy;
    const installing = element(action).value === 'install';
    const upgrading = element(action).value === 'upgrade-agent';
    const development = element(engine).value === 'mihomo';
    element(channel).querySelector('[value=development]').disabled = !development;
    if (!development && element(channel).value === 'development') element(channel).value = 'stable';
    ctx.visible(engine, !upgrading);
    ctx.visible(channel, installing);
    ctx.visible(version, installing && element(channel).value === 'custom');
    element(version).required = installing && element(channel).value === 'custom';
    ctx.visible(source, installing && development && element(channel).value === 'development');
    // Keep eligibility available while a required version is still being drafted.
    const options = { action: element(action).value, engine: element(engine).value,
      core_source: coreSourceForInstall(element(engine).value, element(channel).value, element(source).value) };
    let eligible = 0, selected = 0;
    for (const [id, row] of rows) {
      row.reason = eligibility(agents.find(agent => agent.id === id), options);
      if (row.reason && !busy) row.selected = false;
      if (!row.reason) { eligible++; if (row.selected) selected++; }
    }
    all.textContent = eligible > 0 && selected === eligible ? '取消全选' : '全选';
    all.disabled = busy || !eligible;
    const text = `已选 ${selected} / 可选 ${eligible} 个节点`;
    if (selection.textContent !== text) selection.textContent = text;
    form.querySelector('[type=submit]').disabled = busy || !selected;
    targetTable?.updateRows([...rows.values()].map(renderRow));
    // Reconciliation preserves input properties; synchronize from the model.
    for (const input of form.querySelectorAll('[name^="batch:"]')) input.checked = rows.get(input.name.slice(6)).selected;
  }
  function count() {
    const values = [...rows.values()].map(row => row.result).filter(Boolean);
    const submitted = values.filter(value => value.task).length;
    const failed = values.filter(value => value.error).length;
    const text = values.length ? `已提交 ${submitted}，提交失败 ${failed}${busy ? ' · 提交中' : ''}` : '';
    if (summary.textContent !== text) summary.textContent = text;
    summary.classList.toggle('error', failed > 0);
    summary.classList.toggle('ok', failed === 0);
  }
  function syncCandidates() {
    for (const agent of agents) {
      if (!rows.has(agent.id)) {
        openedScopes.set(agent.id, new Map([undefined, 'mihomo', 'xray', 'sing-box', 'ss-rust'].map(engine => [engine, scope(agent, engine)])));
        rows.set(agent.id, { id: agent.id, name: agent.name, selected: false, reason: '', result: null });
      } else rows.get(agent.id).name = agent.name;
    }
    // Clear revoked data, including previous receipts. A removed target never
    // becomes selected again if its ID reappears in a later response.
    for (const [id, row] of rows) if (!agents.some(agent => agent.id === id)) {
      row.selected = false; row.name = '节点已不在当前授权范围'; row.result = null;
    }
    controls(); count();
  }
  async function refresh() {
    const sequence = ++refreshSequence;
    try {
      const fresh = list(await ctx.api('/agents'));
      if (!active() || sequence !== refreshSequence) return false;
      agents = getAgents(fresh); stale.textContent = ''; syncCandidates(); return true;
    } catch (error) {
      if (active() && sequence === refreshSequence) {
        if (error.status === 403) { agents = []; syncCandidates(); }
        stale.textContent = '状态刷新失败，暂时无法核实节点状态。';
      }
      throw error;
    }
  }
  async function submitOne(id, options) {
    const row = rows.get(id);
    if (!active()) return;
    const reason = eligibility(agents.find(agent => agent.id === id), options);
    row.result = { options: { ...options }, error: reason || '' };
    if (!reason) {
      try {
        const task = await ctx.api('/tasks', { method: 'POST', body: { agent_id: id, ...options } });
        if (!active()) return;
        row.result.task = task;
      } catch (error) {
        if (error.name === 'AbortError') throw error;
        if (!active()) return;
        row.result.error = error.message;
      }
    }
    controls(); count();
  }
  const form = ctx.form([
    operationFields, stale,
    muted('全部授权节点；全选覆盖所有分页中符合当前操作的节点。'), actions(all, selection),
  ], '执行批量操作', async values => {
    if (busy) return;
    const options = readOptions(values);
    const targets = [...rows.values()].filter(row => row.selected).map(row => row.id);
    busy = true; controls();
    try {
      if (!await refresh()) return;
      if (!targets.length || targets.some(id => eligibility(agents.find(agent => agent.id === id), options))) throw new Error('所选节点状态已变化，请检查选择后重试');
      const hint = ({install:'更新后对应内核服务会重启。', 'upgrade-agent':'更新期间 Agent 短暂离线。', stop:'现有连接会立即中断。',restart:'连接会短暂中断。'})[options.action] || '';
      const context = [options.engine, options.core_version && batchCoreVersionLabel(options.core_version), options.core_source && ({official:'MetaCubeX 官方',mirror:'vernesong 镜像'}[options.core_source])].filter(Boolean).join(' · ');
      if (!await ctx.confirm(`对 ${targets.length} 个节点执行“${actionName(options.action)}”${context ? ` · ${context}` : ''}？${hint}`, '执行批量操作') || !active()) return;
      if (!await refresh()) return;
      for (const row of rows.values()) row.result = null;
      for (const id of targets) {
        if (!active()) break;
        const original = JSON.parse(openedScopes.get(id).get(options.engine));
        await submitOne(id, { ...options, expected_instance_id: original[2] });
      }
    } finally { busy = false; if (active()) { controls(); count(); } }
  });
  function renderRow(row) {
    return Object.assign(() => {
      const result = row.result;
      const text = result?.task ? ({pending:'等待执行',running:'执行中',succeeded:'执行成功',failed:'执行失败',canceled:'已取消'}[result.task.status] || '已提交') : result?.error || (result ? '正在提交' : '');
      return [field('选择', `batch:${row.id}`, row.selected, { type: 'checkbox', disabled: busy || Boolean(row.reason), 'aria-label': `批量操作：${row.name}`,
        onchange(event) { row.selected = event.target.checked; controls(); } }), row.name, muted(row.reason || '可执行'), stack(node('span', {}, text), notice(result?.task?.error || ''), actions(
        button('重试提交', () => void ctx.mutate(null, async () => {
          if (busy || !row.result?.error) return;
          const options = { ...row.result.options };
          busy = true; controls();
          try { if (await refresh()) await submitOne(row.id, options); }
          finally { busy = false; if (active()) { controls(); count(); } }
        }), { hidden: !result?.error, disabled: busy || Boolean(result && eligibility(agents.find(agent => agent.id === row.id), result.options)) }),
        button('任务详情', event => void ctx.mutate(event.currentTarget, () => taskDetail(ctx, row.result.task.id)), { hidden: !result?.task || !ctx.can('tasks.read') }),
      ))];
    }, { id: row.id });
  }
  targetTable = table(['', '节点', '条件', '执行结果'], [], {
    label: '批量操作节点与结果', rowKey: row => row.id, emptyText: '暂无授权节点',
  });
  form.querySelector('fieldset').append(node('div', { className: 'a-traffic-list' }, targetTable));
  form.append(summary);
  for (const control of [engine, action, channel, source]) element(control).addEventListener('change', controls);
  form.querySelector('[type=submit]').addEventListener('mutationend', controls);
  box = ctx.dialog('批量操作', form, { wide: true });
  syncCandidates();
  poll({ ...ctx, current: active, onRefreshError: () => { if (!stale.textContent) stale.textContent = '任务状态刷新失败，保留已有结果。'; } }, async () => {
    if (busy) return;
    const sequence = refreshSequence;
    if (!await refresh() || busy) return;
    if (!ctx.can('tasks.read')) return;
    for (const row of rows.values()) {
      if (!row.result?.task || terminal(row.result.task)) continue;
      const result = row.result, id = result.task.id;
      try {
        const task = await ctx.api(`/tasks/${pathID(id)}?view=status`);
        if (!active() || busy || sequence + 1 !== refreshSequence) return;
        if (row.result === result) { result.task = task; controls(); }
      } catch (error) {
        if (active() && error.status === 403) { row.result = null; controls(); count(); }
        throw error;
      }
    }
  });
  return box;
}
