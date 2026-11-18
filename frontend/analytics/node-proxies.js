import { actions, badge, button, muted, node, section } from './ui.js';
import { date } from './operations.js';
import { patch } from './live-dom.js';

const states = { activating: '启动中', deactivating: '停止中', reloading: '重载中', missing: '未安装', unmanaged: '未托管', inactive: '已停止', failed: '服务失败', pending: '待切换', active: '运行中' };
const updateStates = { blocked: '更新受阻', failed: '更新失败', recovery_failed: '恢复失败', rolled_back: '已回滚', interrupted: '更新中断' };
const busy = task => ['pending', 'running'].includes(task?.status);

function runtime(agent, row) {
  if (!agent.features?.includes('managed-proxies-v1')) return '需升级 QAgent';
  if (row?.state === 'active' && row.latest && row.running) return row.running === row.latest ? '已最新' : row.newer_installed ? '已装版本较新' : '可更新';
  return states[row?.state] || '未知';
}

function taskMessage(task, label, attemptAt, receipt) {
  if (receipt && task?.created_at === receipt) return '';
  if (!task?.finished_at || Date.parse(attemptAt) > Date.parse(task.finished_at)) return '';
  if (task.status === 'canceled') return label + '任务已取消';
  if (task.status === 'succeeded' && !(Date.parse(attemptAt) >= Date.parse(task.created_at))) {
    return label + (Date.now() - Date.parse(task.finished_at) < 120000 ? '任务已完成，等待节点上报' : '任务已完成，节点尚未确认本次结果');
  }
  if (task.status === 'failed' && !(Date.parse(attemptAt) >= Date.parse(task.created_at))) return label + '任务失败，节点尚无本次执行结果';
  return '';
}

export function nodeProxyManager(ctx, agent, current, close) {
  const base = `/agents/${encodeURIComponent(agent.id)}/proxies`;
  const submitting = new Map();
  const errors = new Map();
  const lifetime = new AbortController();
  const requestSignal = () => AbortSignal.any([lifetime.signal, AbortSignal.timeout(15000)]);
  let snapshot, timer, generation = 0, readError = '';
  const online = () => agent.status === 'online' && agent.features?.includes('managed-proxies-v1');
  const canUpdate = () => ctx.can('tasks.execute') && ctx.can('host.manage', agent) && ctx.can('agents.manage', agent);
  function report(mode) {
    const cached = agent.managed_proxies?.[mode], fresh = snapshot?.[mode];
    if (!fresh?.observed_at || Date.parse(cached?.observed_at) > Date.parse(fresh.observed_at)) return cached || fresh;
    return fresh;
  }
  function active(mode) {
    for (const operation of ['update', 'check']) {
      const task = snapshot?.tasks?.[`${operation}-${mode}`];
      if (busy(task)) return task.status === 'pending' ? (operation === 'check' ? '检查排队中' : '更新排队中') : (operation === 'check' ? '检查中' : '更新中');
    }
    return submitting.get(mode) || (report(mode)?.update_started_at && '更新中');
  }
  function updatable(row) {
    if (!row?.managed) return false;
    if (['recovery_failed', 'interrupted'].includes(row.update_state) || row.state === 'pending') return true;
    if (row.state !== 'active') return false;
    return Boolean(row.update_error || row.outcome === '更新失败' || !row.newer_installed && (!row.running || row.running !== row.latest));
  }
  function schedule(delay) {
    clearTimeout(timer);
    if (current()) timer = setTimeout(read, delay);
  }
  async function read() {
    if (!current()) return;
    const request = ++generation;
    try {
      const response = await ctx.api(base, { signal: requestSignal(), fresh: true });
      if (!current() || generation !== request) return;
      if (response.agent_id !== agent.id) throw new Error();
      snapshot = response;
      readError = '';
    } catch (error) {
      if (!current() || generation !== request) return;
      if ([401, 403, 404].includes(error.status)) { close(); return; }
      readError = '节点任务状态暂不可用，将自动重试';
    }
    render();
    schedule(Object.values(snapshot?.tasks || {}).some(busy) || readError ? 2500 : 10000);
  }
  async function act(mode, label, update) {
    if (!current() || !online() || active(mode) || readError) return;
    const row = report(mode);
    if (update) {
      if (!canUpdate() || !updatable(row)) return;
      const message = `更新 ${label} 会切换本节点的 ${row.listeners} 个托管监听，涉及 ${row.shared_instances} 个共享实例。切换期间连接可能短暂中断。`;
      if (!await ctx.confirm(message, `更新 ${label}`) || !current() || !online() || !canUpdate() || !updatable(report(mode)) || active(mode) || readError) return;
      const latest = report(mode);
      if (latest.listeners !== row.listeners || latest.shared_instances !== row.shared_instances || latest.running !== row.running || latest.latest !== row.latest) {
        errors.set(mode, '监听范围或代理版本已变化，请重新确认更新');
        render();
        return;
      }
    }
    const operation = update ? 'update' : 'check';
    submitting.set(mode, update ? '提交更新中' : '提交检查中');
    errors.delete(mode);
    render();
    try {
      const task = await ctx.api(`${base}/${mode}/${operation}`, { method: 'POST', signal: requestSignal() });
      if (!current()) return;
      snapshot = { ...snapshot, tasks: { ...snapshot?.tasks, [`${operation}-${mode}`]: task } };
    } catch (error) {
      if (current()) errors.set(mode, error.name === 'TimeoutError' || error.name === 'AbortError' ? '提交超时，正在核对节点任务' : error.message);
    } finally {
      submitting.delete(mode);
      if (current()) {
        render();
        await read();
      }
    }
  }
  function component(mode) {
    const nginx = mode === 'nginx';
    const label = nginx ? 'Nginx' : 'Caddy';
    const row = report(mode);
    const status = runtime(agent, row);
    const operation = active(mode);
    const check = snapshot?.tasks?.['check-' + mode], update = snapshot?.tasks?.['update-' + mode];
    const updateLabel = updateStates[row?.update_state] || (!row?.update_state && row?.outcome !== '检查失败' && row?.outcome);
    const checkFailed = row?.check_error || row?.outcome === '检查失败';
    const diagnostics = [
      updateLabel && [updateLabel, row.update_error].filter(Boolean).join(' · '),
      checkFailed && ['检查失败', row.check_error].filter(Boolean).join(' · '),
      taskMessage(update, '更新', row?.update_at || row?.checked_at, row?.update_request_at), taskMessage(check, '检查', row?.check_attempt_at || row?.checked_at, row?.check_request_at),
      errors.get(mode), readError,
    ].filter(Boolean);
    const source = nginx ? 'nginx.org 主线' : 'Caddy 官方稳定版';
    const managed = row?.managed ? '已托管' : row?.state && row.state !== 'unknown' ? '未托管' : '';
    const versions = [['运行版本', row?.running], ['磁盘版本', row?.installed], ['官方最新版', row?.latest]];
    const checked = row?.checked_at ? date(row.checked_at) : '尚无记录';
    const enabled = online() && !operation && !readError && snapshot;
    return node('article', { className: 'a-proxy-row', 'data-refresh-key': mode, 'aria-label': label },
      node('div', { className: 'a-proxy-identity' },
        node('div', { className: 'a-proxy-title' }, node('strong', {}, label), badge(status, status === '已最新' ? 'good' : ['待切换', '可更新'].includes(status) ? 'warn' : '')),
        muted([source, managed].filter(Boolean).join(' · ')),
        agent.status !== 'online' && muted(`离线 · ${date(row?.observed_at || agent.last_seen)}`)),
      node('dl', { className: 'a-proxy-versions' }, versions.map(([title, value]) =>
        node('div', {}, node('dt', {}, title), node('dd', value ? {} : { className: 'a-muted', 'aria-label': '未知' }, value || '—')))),
      node('div', { className: 'a-proxy-checked' }, muted(`${checkFailed && row.checked_at ? '上次成功检查（已过期）' : '上次成功检查'} · ${checked}`)),
      node('div', { className: 'a-proxy-controls' }, actions(
        ctx.can('agents.read') && button('检查更新', () => act(mode, label, false), { disabled: !enabled, 'aria-label': `检查 ${label} 更新` }),
        canUpdate() && button(updateLabel ? '重试更新' : '更新', () => act(mode, label, true), { disabled: !enabled || !updatable(row), 'aria-label': `更新 ${label}` }))),
      (operation || diagnostics.length > 0) && node('div', { className: 'a-proxy-feedback', role: 'status' },
        operation && badge(operation, 'warn'), diagnostics.map(text => node('div', {}, text))));
  }
  function content() {
    return node('div', { className: 'a-proxy-list' }, component('nginx'), component('caddy'));
  }
  const body = content();
  function render() { patch(body, content()); }
  const element = section('Nginx/Caddy 管理器', body);
  element.classList.add('a-node-proxies');
  queueMicrotask(read);
  return { element, render, dispose() { clearTimeout(timer); lifetime.abort(); } };
}
