import { actions, badge, button, muted, node, section } from './ui.js';
import { date } from './operations.js';
import { patch } from './live-dom.js';

const states = { missing: '未安装', unmanaged: '未托管', inactive: '已停止', failed: '服务失败', pending: '待切换', active: '运行中' };

function presentation(agent, row) {
  if (!agent.features?.includes('managed-proxies-v1')) return '需升级 QAgent';
  if (!row) return '未知';
  if (row.outcome) return row.outcome;
  if (row.state === 'active' && row.latest && row.running) return row.running === row.latest ? '已最新' : row.newer_installed ? '已装版本较新' : '可更新';
  return states[row.state] || '未知';
}

export function nodeProxyManager(ctx, agent, current, refresh) {
  const pending = new Map();
  const feedback = new Map();
  const online = () => agent.status === 'online' && agent.features?.includes('managed-proxies-v1');
  const canUpdate = () => ctx.can('tasks.execute') && ctx.can('host.manage', agent) && ctx.can('agents.manage', agent);
  async function act(mode, label, update) {
    if (!current() || !online() || pending.has(mode)) return;
    const report = agent.managed_proxies?.[mode];
    if (update) {
      if (!canUpdate() || !report?.managed) return;
      const message = `更新 ${label} 会切换本节点的 ${report.listeners} 个托管监听，涉及 ${report.shared_instances} 个共享实例。切换期间连接可能短暂中断。`;
      if (!await ctx.confirm(message, `更新 ${label}`) || !current() || !online() || !canUpdate() || pending.has(mode)) return;
    }
    pending.set(mode, update ? '更新中' : '检查中');
    feedback.delete(mode);
    render();
    try {
      const endpoint = `/agents/${encodeURIComponent(agent.id)}/proxies/${mode}/${update ? 'update' : 'check'}`;
      let task = await ctx.api(endpoint, { method: 'POST' });
      if (!current()) return;
      if (!ctx.can('tasks.read')) { feedback.set(mode, '任务已提交'); return; }
      const deadline = Date.now() + 7 * 60 * 1000;
      while (['pending', 'running'].includes(task.status) && Date.now() < deadline) {
        await new Promise(resolve => setTimeout(resolve, 1200));
        if (!current()) return;
        task = await ctx.api(`/tasks/${encodeURIComponent(task.id)}`);
        if (!current()) return;
      }
      feedback.set(mode, task.status === 'succeeded' ? task.output?.trim() || '已完成' : task.error || '等待节点回报');
      await refresh();
    } catch (error) {
      if (current()) feedback.set(mode, error.message);
    } finally {
      pending.delete(mode);
      if (current()) render();
    }
  }
  function component(mode) {
    const nginx = mode === 'nginx';
    const label = nginx ? 'Nginx' : 'Caddy';
    const row = agent.managed_proxies?.[mode];
    const status = pending.get(mode) || presentation(agent, row);
    const tone = row?.outcome ? 'bad' : ['待切换', '可更新'].includes(status) ? 'warn' : status === '已最新' ? 'good' : '';
    const source = nginx ? 'nginx.org 主线' : 'Caddy 官方稳定版';
    const managed = row?.managed ? '已托管' : '未托管';
    const versions = [['运行版本', row?.running], ['磁盘版本', row?.installed], ['官方最新版', row?.latest]];
    const checked = row?.checked_at ? date(row.checked_at) : '尚无记录';
    return node('article', { className: 'a-proxy-row', 'data-refresh-key': mode, 'aria-label': label },
      node('div', { className: 'a-proxy-identity' },
        node('div', { className: 'a-proxy-title' }, node('strong', {}, label), badge(status, tone)),
        muted(row ? `${source} · ${managed}` : source),
        agent.status !== 'online' && muted(`离线 · ${date(row?.observed_at || agent.last_seen)}`)),
      node('dl', { className: 'a-proxy-versions' }, versions.map(([title, value]) =>
        node('div', {}, node('dt', {}, title), node('dd', value ? {} : { className: 'a-muted', 'aria-label': '未知' }, value || '—')))),
      node('div', { className: 'a-proxy-checked' }, muted(`${row?.outcome === '检查失败' ? '上次成功检查（已过期）' : '上次成功检查'} · ${checked}`)),
      node('div', { className: 'a-proxy-controls' }, actions(
        ctx.can('agents.read') && button('检查更新', () => act(mode, label, false), { disabled: !online() || pending.has(mode), 'aria-label': `检查 ${label} 更新` }),
        canUpdate() && button('更新', () => act(mode, label, true), { disabled: !online() || pending.has(mode) || !row?.managed || !['pending', 'active'].includes(row.state), 'aria-label': `更新 ${label}` }))),
      feedback.has(mode) && node('div', { className: 'a-proxy-feedback', role: 'status' }, feedback.get(mode)));
  }
  function content() {
    return node('div', { className: 'a-proxy-list' }, component('nginx'), component('caddy'));
  }
  const body = content();
  function render() { patch(body, content()); }
  const element = section('Nginx/Caddy 管理器', body);
  element.classList.add('a-node-proxies');
  return { element, render };
}
