import { proxyIngressEditor, proxyIngressLabel } from "./proxy-ingresses.js";
import { preparedDialog, readRegion, loadingTable } from "./loading.js";
import { bytes } from "./operations.js";
import { parseSharedPorts, sharedLimitBytes, sharedLimitGiB, formatSharedPorts, sharedPortsLabel, agentShareStatus, mergeUserAllocation } from '../modules/user-model.js';
import { node, button, actions, stack, grid, field, muted, notice, pathID, list, table, badge } from './ui.js';

export function editUserAllocations(ctx, user) {
  return preparedDialog(ctx, `${user.username} · 共享资源`, ['节点', '授权范围', '已用 / 总额度', '状态', '操作'], local => editUserAllocationsLoaded(local, user), { submit: '分配节点' });
}
async function editUserAllocationsLoaded(ctx, user) {
  const data = ctx.state.data;
  const current = () => ctx.current() && ctx.state.data === data;
  const path = `/users/${pathID(user.id)}/agent-access`;
  const drafts = data.userAllocationDrafts ||= new Map();
  const pending = data.userAllocationPending ||= new Set();
  if (pending.has(user.id)) return;
  let directory = [], directoryReady = false, initialized = false;
  const directoryRead = readRegion(ctx, '节点选项', async signal => {
    const [visible, other] = await Promise.all([ctx.api('/agents', { signal }), ctx.api('/agent-directory', { signal })]);
    return [...new Map([...list(other), ...list(visible).map(agent => ({ ...agent, shared_proxy_ingress: agent.features?.includes('shared-proxy-ingress-v1') }))].map(agent => [agent.id, agent])).values()];
  }, value => { directory = list(value); directoryReady = true; if (initialized && !editor) { draw(); const draft = drafts.get(user.id); if (draft) open(draft.original, draft.mode, draft); } }, { clear() { directory = []; directoryReady = false; if (initialized && !editor) draw(); } });
  void directoryRead.load();
  let saved = await ctx.api(path);
  if (!current()) return;
  const savedView = stack();
  const feedback = notice('');
  let editor = null, busy = false, closed = false;
  const active = () => current() && !closed;
  const available = () => directory.filter(agent => !list(saved.owned_agent_ids).includes(agent.id) && !list(saved.shares).some(share => share.agent_id === agent.id));
  const name = share => directory.find(agent => agent.id === share.agent_id)?.name || share.agent_name || share.agent_id;
  const remember = () => {
    if (!active() || !editor) return;
    const value = editor.values();
    editor.changed = JSON.stringify(value) !== editor.baseline;
    if (editor.changed) drafts.set(user.id, { ...editor.snapshot, value, baseline: editor.baseline });
    else drafts.delete(user.id);
    ctx.setDirty(editor.changed);
  };
  function closeEditor(discard = false) {
    editor = null;
    content.replaceChildren(directoryRead.element, savedView, feedback); box.classList.add('wide');
    box.querySelector('.a-dialog-head h2').textContent = `${user.username} · 共享资源`;
    if (discard) drafts.delete(user.id);
    ctx.setDirty(false);
  }
  async function lock(run) {
    if (!active() || busy || pending.has(user.id)) return;
    busy = true; pending.add(user.id);
    const controls = [...content.querySelectorAll('button,input,select,textarea')].map(control => [control, control.disabled]);
    controls.forEach(([control]) => { control.disabled = true; });
    try { return await run(); }
    finally {
      pending.delete(user.id); busy = false;
      controls.forEach(([control, disabled]) => { if (control.isConnected) control.disabled = disabled; });
    }
  }
  async function save(access, allocation) {
    const received = await ctx.api(path, { method: 'PUT', body: {
      revision: access.revision, isolated: true, shares: mergeUserAllocation(access.shares, allocation),
    } });
    if (!active()) return;
    saved = received; feedback.textContent = ''; closeEditor(true); draw(); ctx.announce('资源范围已保存');
  }
  function open(share = null, mode = 'edit', draft = null) {
    if (!active() || busy) return;
    if (editor?.changed) { feedback.textContent = '请先保存或取消当前分配。'; return; }
    feedback.textContent = '';
    const access = draft?.access || structuredClone(saved);
    const original = draft?.original || share;
    const value = draft?.value || {
      agent_id: share?.agent_id || '', engines: list(share?.engines),
      ports: formatSharedPorts(share?.ports), limit: sharedLimitGiB(share?.limit_bytes),
      domains: list(share?.http_domains).join('\n'), proxy_ingresses: list(share?.proxy_ingresses),
      enabled: mode === 'invite' ? true : share?.enabled !== false,
      reinvite: mode === 'invite' && share?.status === 'rejected',
    };
    const target = field('节点', 'agent_id', value.agent_id, { options: share ? [[share.agent_id, name(share)]] : [['', '选择节点'], ...available().map(agent => [agent.id, agent.name])], disabled: Boolean(share), required: true });
    const engines = node('div', { className: 'a-actions' });
    const ports = field('授权端口', 'ports', value.ports, { placeholder: '21000-21100, 22000', hint: '留空未分配，0 表示不限端口' });
    const limit = field('总额度 · GiB', 'limit', value.limit, { type: 'number', min: 0, max: 8388607, step: 'any', required: true, hint: `已用 ${bytes(original?.used_bytes)}，0 表示不限量` });
    const domains = field('HTTP-01 允许域名', 'domains', value.domains, { multiline: 2 });
    const ingress = proxyIngressEditor(value.proxy_ingresses, directory.find(item => item.id === value.agent_id)?.shared_proxy_ingress === true);
    const consent = muted('');
    const selected = () => share?.agent_id || target.querySelector('select').value;
    const values = () => ({ agent_id: selected(), engines: [...engines.querySelectorAll('input:checked')].map(input => input.value),
      ports: ports.querySelector('input').value, limit: limit.querySelector('input').value, domains: domains.querySelector('textarea').value,
      proxy_ingresses: ingress.value(), enabled: value.enabled, reinvite: value.reinvite });
    function drawEngines(selectedEngines) {
      const agent = directory.find(item => item.id === selected());
      const supported = list(agent?.supported_capabilities || agent?.capabilities);
      const choices = [...new Set([...supported, ...selectedEngines])];
      engines.replaceChildren(...choices.map(engine => field(supported.includes(engine) ? engine : `${engine} · 不可用`, 'engines', selectedEngines.includes(engine), { type: 'checkbox', value: engine })));
    }
    function updateConsent() {
      consent.textContent = !original || mode === 'invite' ? '用户接受邀请后才能使用节点。'
        : !original.enabled ? '保存修改不会恢复共享。'
        : original.status === 'rejected' ? '保存修改不会重新发送邀请。'
        : original.status === 'accepted' && values().engines.some(engine => !list(original.engines).includes(engine)) ? '增加内核后，用户需重新接受。' : '';
    }
    drawEngines(value.engines);
    const allocationFields = stack(node('div', { className: 'a-stack' }, node('span', { className: 'a-field-label' }, '可用内核'), engines), grid(ports, limit), domains, ingress.element, consent);
    const form = ctx.form([!share && target, allocationFields], mode === 'edit' && share ? '保存分配' : '发送邀请', async () => {
      if (!active() || editor?.form !== form || busy) return;
      remember();
      const row = values();
      if (!row.agent_id) throw new Error('请选择节点。');
      if (row.enabled && !row.engines.length) throw new Error('请至少分配一个内核。');
      const allocation = { agent_id: row.agent_id, engines: row.engines, ports: parseSharedPorts(row.ports),
        limit_bytes: sharedLimitBytes(row.limit), http_domains: row.domains.split(/[\s,，]+/).filter(Boolean), proxy_ingresses: row.proxy_ingresses, enabled: row.enabled, reinvite: row.reinvite };
      await lock(() => save(access, allocation));
    });
    const baselineValue = values();
    if (mode === 'invite') { baselineValue.enabled = original.enabled; baselineValue.reinvite = false; }
    const instance = { form, values, baseline: draft?.baseline || JSON.stringify(baselineValue), changed: false, snapshot: { access, original, mode } };
    editor = instance;
    const selectTarget = () => { allocationFields.hidden = !selected(); form.querySelector('[type=submit]').disabled = !selected(); ingress.setSupported(directory.find(item => item.id === selected())?.shared_proxy_ingress); };
    selectTarget();
    target.querySelector('select').addEventListener('change', () => { drawEngines([]); selectTarget(); updateConsent(); remember(); });
    form.addEventListener('input', () => { remember(); updateConsent(); });
    form.addEventListener('change', () => { remember(); updateConsent(); });
    const cancel = button('取消', () => { if (!busy && form.getAttribute('aria-busy') !== 'true') closeEditor(true); });
    const reload = button('重新读取', () => void reloadSaved(instance));
    form.lastElementChild.prepend(reload, cancel);
    content.replaceChildren(muted(`接收用户 · ${user.display_name || user.username}`), form, feedback); box.classList.remove('wide');
    box.querySelector('.a-dialog-head h2').textContent = `${mode === 'invite' ? '重新邀请' : share ? '编辑分配' : '分配节点'}${share ? ` · ${name(share)}` : ''}`;
    box.scrollTop = 0;
    updateConsent(); remember();
  }
  async function reloadSaved(expected = editor) {
    if (!active() || busy) return;
    await lock(async () => {
      try {
        if (editor?.changed && !await ctx.confirm('重新读取会丢弃未保存的分配。', '重新读取') || !active() || editor !== expected) return;
        const received = await ctx.api(path);
        if (!active() || editor !== expected) return;
        saved = received; closeEditor(true); draw(); feedback.textContent = '';
      } catch (error) { if (active() && error.name !== 'AbortError') feedback.textContent = error.message; }
    });
  }
  function draw() {
    const shares = list(saved.shares);
    const owned = directory.filter(agent => list(saved.owned_agent_ids).includes(agent.id));
    const tools = actions(button('分配节点', () => open(null, 'add'), { disabled: !directoryReady || !available().length }), button('重新读取', () => void reloadSaved()));
    const allocations = table(['节点', '授权范围', '已用 / 总额度', '状态', '操作'], shares.map(share => [
      name(share), stack(muted(list(share.engines).join(' / ') || '未分配内核'), node('span', {}, `端口 · ${sharedPortsLabel(share.ports)}`), muted(`托管入口 · ${proxyIngressLabel(share)}`)),
      `${bytes(share.used_bytes)} / ${share.limit_bytes ? bytes(share.limit_bytes) : '不限量'}`, badge(agentShareStatus(share)),
      actions(button('编辑分配', () => open(share), { disabled: !directoryReady }),
        (!share.enabled || share.status === 'rejected') && button('重新邀请', () => open(share, 'invite'), { disabled: !directoryReady }),
        share.enabled && button('撤销共享', () => void lock(async () => {
          try {
            if (editor?.changed) { feedback.textContent = '请先保存或取消当前分配。'; return; }
            if (!await ctx.confirm(`撤销 ${name(share)} 的共享？端口预留与用量将保留。`, '撤销共享') || !active()) return;
            await ctx.mutate(null, async () => {
              try { await save(saved, { agent_id: share.agent_id, enabled: false }); }
              catch (error) { if (active() && error.name !== 'AbortError') feedback.textContent = error.message; }
            });
          } catch (error) { if (active() && error.name !== 'AbortError') feedback.textContent = error.message; }
        }), { tone: 'danger' })),
    ]), { label: '共享分配' });
    allocations.classList.add('a-port-list');
    const labelCells = () => allocations.querySelectorAll('td').forEach(cell => { cell.dataset.label = cell.dataset.column; });
    labelCells();
    allocations.addEventListener('click', labelCells);
    savedView.replaceChildren(tools, allocations);
    if (owned.length) savedView.append(node('details', { className: 'a-disclosure' }, node('summary', {}, `自有节点 · ${owned.length}`), node('div', { className: 'a-disclosure-body' }, owned.map(agent => node('p', {}, agent.name)))));
  }
  const content = stack(directoryRead.element, savedView, feedback);
  initialized = true; draw();
  const box = ctx.dialog(`${user.username} · 共享资源`, content, { wide: true });
  box.dataset.userAllocation = user.id;
  box.onRequestClose = () => {
    if (busy || editor?.form.getAttribute('aria-busy') === 'true') return;
    remember(); ctx.setDirty(false); box.close();
  };
  box.addEventListener('close', () => { closed = true; if (current()) ctx.setDirty(false); }, { once: true });
  const draft = drafts.get(user.id);
  if (draft && directoryReady) open(draft.original, draft.mode, draft);
}
