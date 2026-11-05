import { node, button, actions, stack, table, field, muted, notice, list } from './ui.js';
import { protocolName } from '../modules/traffic-model.js';

export function trafficSync(ctx, { agentName, matches, onSaved }) {
  const rows = node('div'), feedback = notice('');
  let choices = [], snapshot = '', generation = 0, loading = false, request;
  const selected = new Set();
  const labels = { new: '新增', changed: '变更', removed: '移除，历史保留', unchanged: '无变更', deleted: '已删除，可恢复' };
  const all = field('全选可同步项', 'select-all', false, { type: 'checkbox', onchange(event) {
    choices.forEach((entry, index) => { if (!entry.blocked_reason) event.target.checked ? selected.add(index) : selected.delete(index); });
    rows.querySelectorAll('input:not(:disabled)').forEach(input => { input.checked = event.target.checked; }); selection();
  } });
  function selection() {
    const available = choices.filter(entry => !entry.blocked_reason).length;
    const input = all.querySelector('input');
    input.checked = available > 0 && selected.size === available;
    input.indeterminate = selected.size > 0 && selected.size < available;
    input.disabled = loading || !available;
    form.querySelector('button[type=submit]').disabled = loading || !snapshot || !selected.size;
  }
  const scan = button('重新扫描', () => void load());
  const form = ctx.form([actions(all, scan), rows, feedback], '同步所选端口', async () => {
    if (!snapshot || !selected.size) throw new Error('请选择可同步的端口。');
    try {
      await ctx.api('/traffic-endpoints/sync', { method: 'POST', body: { snapshot, selections: [...selected].map(index => ({ agent_id: choices[index].agent_id, port: choices[index].port })) } });
    } catch (error) {
      if (error.status === 409) { snapshot = ''; selected.clear(); selection(); feedback.textContent = '候选已失效，请重新扫描后选择端口。'; }
      throw error;
    }
    if (!ctx.current() || !box.open) return;
    box.close();
    const refreshed = await onSaved();
    ctx.announce(refreshed ? '端口已同步' : '端口已同步，列表刷新失败，请重试。', refreshed ? '' : 'error');
  });
  form.querySelector('button[type=submit]').addEventListener('mutationend', selection);
  const box = ctx.dialog('同步配置端口', form, { wide: true });
  async function load() {
    const expected = ++generation;
    request?.abort(); request = new AbortController();
    loading = true; choices = []; snapshot = ''; selected.clear();
    scan.disabled = true; feedback.textContent = ''; rows.replaceChildren(muted('正在扫描端口…')); selection();
    try {
      const preview = await ctx.api('/traffic-endpoints/sync', { signal: AbortSignal.any([ctx.signal, request.signal]) });
      if (!ctx.current() || !box.open || expected !== generation) return;
      choices = list(preview.candidates).filter(matches); snapshot = preview.snapshot || '';
      rows.replaceChildren(table(['选择', '节点 / 归属', '端口 / 协议', '内核', '变更', '部署状态'], choices.map((entry, index) => {
        const toggle = field('同步', `port:${index}`, false, { type: 'checkbox', disabled: !!entry.blocked_reason, onchange(event) { event.target.checked ? selected.add(index) : selected.delete(index); selection(); } });
        return [toggle, stack(agentName(entry.agent_id), muted(entry.owner_name || (entry.is_current_user ? '当前用户' : '其他用户')), muted(entry.share_id ? '共享实例' : '主实例')),
          stack(entry.previous_port ? `旧 ${entry.previous_port} · 新 ${entry.port}` : String(entry.port), muted(protocolName(entry.protocol))), entry.engine,
          labels[entry.kind] || entry.kind, entry.blocked_reason ? notice(entry.blocked_reason, 'warn') : ({ pending: '待部署', uncertain: '部署未确认', retained: '旧端口', applied: '已部署' })[entry.deployment_status] || entry.deployment_status || '已部署'];
      }), { label: '端口同步预览', paginate: 0, emptyText: '当前筛选范围内暂无可同步端口' }));
    } catch (error) { if (ctx.current() && box.open && expected === generation && error.name !== 'AbortError') { rows.replaceChildren(muted('无法读取端口')); feedback.textContent = error.message; } }
    finally { if (expected === generation) { loading = false; scan.disabled = false; selection(); } }
  }
  const stop = () => { request?.abort(); box.close(); };
  ctx.signal.addEventListener('abort', stop, { once: true });
  box.addEventListener('close', () => { request?.abort(); ctx.signal.removeEventListener('abort', stop); }, { once: true });
  selection(); void load();
}
