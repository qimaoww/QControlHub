import { actions, button, muted, node, section, stack, table } from './ui.js';
import { date } from './operations.js';
import { patch } from './live-dom.js';

const states = { missing: '未安装', unmanaged: '未托管', inactive: '已停止', failed: '服务失败', pending: '待切换', active: '运行中' };

function presentation(agent, row) {
  if (!agent.features?.includes('managed-proxies-v1')) return '需升级 QAgent';
  if (!row) return '未知';
  if (row.outcome) return row.outcome;
  if (row.state === 'active' && row.latest && row.running) return row.running === row.latest ? '已最新' : '版本不同';
  return states[row.state] || '未知';
}

export function nodeProxyManager(ctx, agent, current, refresh) {
  const pending = new Map();
  const feedback = new Map();
  const online = () => agent.status === 'online' && agent.features?.includes('managed-proxies-v1');
  async function act(mode, label, update) {
    if (!current() || !online() || pending.has(mode)) return;
    const report = agent.managed_proxies?.[mode];
    if (update) {
      if ((!ctx.can('host.manage', agent) || !ctx.can('agents.manage', agent)) || !report?.managed) return;
      const message = `更新 ${label} 会切换本节点的 ${report.listeners} 个托管监听，涉及 ${report.shared_instances} 个共享实例。切换期间连接可能短暂中断。`;
      if (!await ctx.confirm(message, `更新 ${label}`) || !current() || !online()) return;
    }
    pending.set(mode, update ? '更新中' : '检查中');
    feedback.delete(mode);
    render();
    try {
      const action = `${update ? 'update' : 'check'}-${mode}`;
      let task = update ? await ctx.api('/tasks', { method: 'POST', body: { agent_id: agent.id, action } }) : await ctx.api(`/agents/${encodeURIComponent(agent.id)}/proxies/${mode}/check`, { method: 'POST' });
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
  function content() {
    return table(['组件', '托管状态', '版本', '操作'], ['nginx', 'caddy'].map(mode => {
      const nginx = mode === 'nginx';
      const label = nginx ? 'Nginx' : 'Caddy';
      const row = agent.managed_proxies?.[mode];
      const last = row?.checked_at ? date(row.checked_at) : '未知';
      return [
        stack(node('strong', {}, label), muted(nginx ? 'nginx.org 主线' : 'Caddy 官方稳定版')),
        stack(pending.get(mode) || presentation(agent, row), row && muted(row.managed ? '已托管' : '未托管'),
          agent.status !== 'online' && muted(`离线 · ${date(agent.last_seen)}`)),
        stack(`已装　${row?.installed || '未知'}`, `运行　${row?.running || '未知'}`,
          `官方　${row?.latest || '未知'}`, muted(`${row?.outcome === '检查失败' ? '上次成功 · 已过期' : '上次成功检查'}　${last}`)),
        stack(actions(ctx.can('agents.read') && button('检查更新', () => act(mode, label, false), { disabled: !online() || pending.has(mode), 'aria-label': `检查 ${label} 更新` }),
          ctx.can('host.manage', agent) && ctx.can('agents.manage', agent) && button('更新', () => act(mode, label, true), { disabled: !online() || pending.has(mode) || !row?.managed || !['pending','active'].includes(row.state), 'aria-label': `更新 ${label}` })),
          feedback.has(mode) && node('span', { role: 'status' }, feedback.get(mode))),
      ];
    }), { paginate: false, motion: false, label: '节点代理版本' });
  }
  const body = content();
  function render() { patch(body, content()); }
  return { element: section('Nginx/Caddy 管理器', body), render };
}
