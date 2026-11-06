import { agentShareStatus, sharedPortsLabel } from '../modules/user-model.js';
import { node, button, actions, stack, grid, section, badge, muted, notice, pathID, list } from './ui.js';
import { bytes } from './operations.js';
import { patch } from './live-dom.js';

export async function quotaInvitations(ctx) {
  const data = ctx.state.data;
  const model = data.quotaInvitations ||= { serial: 0, access: null, pending: false, openID: null, listeners: new Set() };
  const current = () => ctx.current() && ctx.state.data === data;
  const view = stack();
  let box = null, loading = false, drawDetails = () => {};
  const feedback = notice('');
  const own = () => list(model.access?.shares);
  const actionable = share => share?.enabled && ['pending', 'accepted'].includes(share.status);
  function close() {
    if (model.pending || loading) return;
    model.openID = null; box?.close(); box = null;
  }
  function open(share) {
    if (!current() || !actionable(share) || model.pending || loading) return;
    model.openID = share.id;
    const detail = stack();
    box = ctx.dialog(`${share.agent_name || '共享节点'} · ${share.status === 'pending' ? '共享邀请' : '共享详情'}`, detail);
    box.onRequestClose = close;
    function draw() {
      if (!current() || !box?.isConnected) return;
      const latest = own().find(item => item.id === share.id);
      if (!actionable(latest)) { model.openID = null; box.close(); box = null; return; }
      share = latest;
      const facts = [
        ['所有者', share.owner_username || '管理员'], ['状态', badge(agentShareStatus(share))],
        ['可用内核', list(share.engines).join(' / ') || '未分配'], ['授权端口', sharedPortsLabel(share.ports)],
        ['总额度', share.limit_bytes ? bytes(share.limit_bytes) : '不限量'], ['已用流量', bytes(share.used_bytes)],
      ];
      const terms = stack(node('dl', { className: 'a-traffic-facts' }, facts.map(([label, value]) =>
        node('div', {}, node('dt', {}, label), node('dd', {}, value)))),
      node('div', { className: 'a-field' }, node('span', { className: 'a-field-label' }, 'HTTP-01 允许域名'),
        node('span', {}, list(share.http_domains).join(' · ') || '未授权')));
      const reload = button('重新读取', () => void read(draw), { disabled: model.pending || loading });
      const accept = share.status === 'pending' && button('接受邀请', () => void respond('accept', share, draw), { tone: 'primary', disabled: model.pending || loading || !list(share.engines).length });
      const reject = button(share.status === 'pending' ? '拒绝邀请' : '退出共享', () => void respond('reject', share, draw), { tone: 'danger', disabled: model.pending || loading });
      patch(detail, stack(terms, notice(feedback.textContent), actions(reload, accept, reject)));
    }
    drawDetails = draw;
    draw();
  }
  function drawCards() {
    if (!current()) return;
    if (!own().length) {
      patch(view, stack(model.access ? stack(node('strong', {}, '暂无共享记录'),
        muted('收到共享邀请后，可在此查看节点与可用额度。')) : loading && muted('正在读取共享记录…')));
      return;
    }
    patch(view, stack(grid(own().map(share => {
      const card = section(share.agent_name || share.agent_id,
      node('div', { className: 'a-card-body a-stack' },
        node('dl', { className: 'a-traffic-facts' }, [
          ['已用 / 总额度', node('strong', {}, `${bytes(share.used_bytes)} / ${share.limit_bytes ? bytes(share.limit_bytes) : '不限量'}`)],
          ['可用内核', list(share.engines).join(' / ') || '未分配'],
          ['授权端口', sharedPortsLabel(share.ports)],
        ].map(([label, value]) => node('div', {}, node('dt', {}, label), node('dd', {}, value))))),
      badge(agentShareStatus(share)), `所有者 · ${share.owner_username || '管理员'}`);
      if (actionable(share)) card.append(node('footer', { className: 'a-probe-footer' },
        actions(button(share.status === 'pending' ? '查看邀请' : '共享详情', () => open(share), { disabled: model.pending || loading }))));
      card.dataset.refreshKey = share.id;
      return card;
    }))));
  }
  const update = () => { drawCards(); drawDetails(); };
  model.listeners.add(update);
  ctx.signal.addEventListener('abort', () => model.listeners.delete(update), { once: true });
  const notify = () => model.listeners.forEach(listener => listener());
  async function read(redraw) {
    if (!current() || model.pending || loading) return;
    loading = true; const serial = ++model.serial;
    drawCards(); redraw?.();
    try {
      const received = await ctx.api('/agent-access');
      if (!current() || serial !== model.serial || model.pending) return;
      model.access = received; feedback.textContent = '';
    } catch (error) { if (current() && error.name !== 'AbortError') feedback.textContent = error.message; }
    finally {
      loading = false;
      if (current()) { drawCards(); redraw?.(); }
    }
  }
  async function respond(decision, share, redraw) {
    if (!current() || model.pending || loading || !actionable(share) || decision === 'accept' && !list(share.engines).length) return;
    model.pending = true; ++model.serial;
    drawCards(); redraw();
    try {
      if (decision === 'reject' && share.status === 'accepted' && !await ctx.confirm('确定退出此共享？', '退出共享') || !current()) return;
      await ctx.mutate(null, async () => {
        try {
          const received = await ctx.api(`/agent-access/${pathID(share.id)}/response`, { method: 'POST', body: { decision, revision: share.invitation_revision } });
          if (ctx.state.data !== data) return;
          model.access = received; feedback.textContent = ''; model.openID = null;
          if (current()) { box?.close(); box = null; ctx.announce('共享状态已更新'); }
        } catch (error) { if (current() && error.name !== 'AbortError') feedback.textContent = error.message; }
      });
    } catch (error) { if (current() && error.name !== 'AbortError') feedback.textContent = error.message; }
    finally {
      model.pending = false;
      notify();
    }
  }
  await read();
  if (!current()) return view;
  // An explicit page refresh can resume an open invitation, while route and
  // account changes dispose its dialog through the existing theme lifecycle.
  const previous = own().find(share => share.id === model.openID);
  if (previous && !model.pending) open(previous);
  const result = section('我使用的共享与额度', node('div', { className: 'a-card-body a-stack' }, view, feedback));
  result.refresh = () => read(drawDetails);
  return result;
}
