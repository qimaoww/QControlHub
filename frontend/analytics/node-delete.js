import { button, pathID } from './ui.js';

export function nodeDeleteControl(ctx, agent, { available = () => ctx.can('agents.manage', agent), onDeleted, refresh }) {
  let busy = false, deleted = false;
  const control = button('删除节点', async () => {
    if (busy || deleted || !ctx.current() || !available()) return;
    busy = true; control.disabled = true;
    try {
      if (!await ctx.confirm(`删除“${agent.name}”？关联数据将被清理，节点上的 QAgent 不会被远程卸载。`, '删除节点') || !ctx.current() || !available()) return;
      await ctx.mutate(null, async () => {
        control.textContent = '正在删除…'; control.setAttribute('aria-busy', 'true');
        await ctx.api(`/agents/${pathID(agent.id)}`, { method: 'DELETE', signal: AbortSignal.any([ctx.signal, AbortSignal.timeout(45000)]) });
        if (!ctx.current()) return;
        deleted = true; control.textContent = '已删除'; control.removeAttribute('aria-busy');
        onDeleted(); ctx.announce('节点已删除');
        try { await refresh(); }
        catch (error) { if (ctx.current() && error.name !== 'AbortError') ctx.announce(`节点已删除，但列表刷新失败：${error.message}`, 'error'); }
      });
    } finally {
      busy = false;
      if (!deleted) { control.textContent = '删除节点'; control.removeAttribute('aria-busy'); }
      control.disabled = deleted || !ctx.current() || !available();
    }
  }, { tone: 'danger' });
  return control;
}
