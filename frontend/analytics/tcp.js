import { pageSizePreference, recordPages } from './page-size.js';
import { readRegion, loadingTable } from "./loading.js";
import { systemBBRState, validateTCPSelection, validateTCPAvailability, hasTCPParameter, taskLabel, actionLabel } from '../modules/system-bbr-model.js';
import { systemTCPPresets, prepareTCPPreset } from '../modules/system-bbr-presets.js';
import { orderNodesBySavedOrder } from '../modules/node-order.js';
import { diagnosticError } from '../modules/errors.js';
import { patch, poll } from './live-dom.js';
import { nodeName } from './node-region.js';
import { node, button, actions, stack, grid, section, table, badge, field, muted, list, notice } from './ui.js';
import { date, taskDetail } from './operations.js';

export async function tcpPage(ctx) {
  let agents = [], rules = [], tasks = [], order = null, agentsReady = false, rulesReady = false, tasksReady = !ctx.can('tasks.read');
  const latest = new Map();
  const drafts = ctx.state?.data ? (ctx.state.data.analyticsTCPDrafts ||= new Map()) : new Map();
  const submitting = new Set();
  const results = node('div');
  const resultTable = loadingTable(['节点', '状态', '拥塞控制', '队列', '持久配置', '最近任务', '操作'], 'TCP 调优', { rowKey: row => row.refreshKey, motion: false, paginate: false });
  results.append(resultTable);
  const paging = recordPages(() => draw());
  const pageSize = pageSizePreference(ctx, 'tcp', size => paging.setSize(size));
  const pageCount = muted('');
  const previous = button('上一页', () => paging.setPage(paging.page - 1));
  const next = button('下一页', () => paging.setPage(paging.page + 1));
  const pages = node('div', { className: 'a-pagination', hidden: true }, pageCount, actions(previous, next));
  const refreshStatus = muted('正在读取节点状态…');
  refreshStatus.setAttribute('role', 'status');
  let serial = 0;
  const dialogs = new Set(), confirmations = new Map();
  const observe = refresh => { dialogs.add(refresh); };
  const syncDialogs = () => {
    for (const [id, box] of confirmations) if (!owned(currentAgent(id))) box?.close();
    for (const refresh of dialogs) if (!refresh()) dialogs.delete(refresh);
  };
  async function confirmFor(id, message, label) {
    const before = new Set(document.querySelectorAll('dialog'));
    const result = ctx.confirm(message, label);
    const box = [...document.querySelectorAll('dialog')].find(dialog => !before.has(dialog));
    confirmations.set(id, box);
    try { return await result; } finally { if (confirmations.get(id) === box) confirmations.delete(id); }
  }
  const owned = agent => agent && ctx.can('agents.manage', agent) && agent.can_manage !== false;
  const selectedID = location.hash.startsWith('#system-bbr-agent-') ? decodeURIComponent(location.hash.slice('#system-bbr-agent-'.length)) : '';
  const selected = () => orderNodesBySavedOrder(list(agents), order?.ids).filter(agent => agent.can_manage !== false && (!selectedID || agent.id === selectedID));
  const setTasks = values => { if (!ctx.can('tasks.read')) return; latest.clear(); for (const task of list(values)) if (!latest.has(task.agent_id)) latest.set(task.agent_id, task); };
  setTasks(tasks);
  const currentAgent = id => list(agents).find(agent => agent.id === id);
  const busy = id => submitting.has(id) || ['pending', 'running'].includes(latest.get(id)?.status);
  const editable = agent => tasksReady && owned(agent) && ctx.can('tasks.execute') && systemBBRState(agent).controllable && !busy(agent.id);
  async function reload() {
    const sequence = ++serial;
    let fresh, taskItems, savedOrder;
    try { [fresh, taskItems, savedOrder] = await Promise.all([ctx.api('/agents'), ctx.can('tasks.read') ? ctx.api('/system-tcp/tasks') : [], ctx.api('/preferences/node-order')]); }
    catch (error) {
      if (ctx.current() && sequence === serial) refreshStatus.textContent = agentsReady ? '状态刷新失败，保留上次数据并重试。' : '节点状态读取失败，请重试。';
      if (ctx.current() && sequence === serial && [401, 403].includes(error.status)) { agents = []; latest.clear(); drafts.clear(); draw(); syncDialogs(); }
      throw error;
    }
    if (!ctx.current() || sequence !== serial) return;
    agents = fresh; agentsReady = tasksReady = true; order = savedOrder;
    setTasks(taskItems);
    refreshStatus.textContent = '正在实时更新';
    draw(); syncDialogs();
    return true;
  }
  async function submit(id, action, settings) {
    const initial = currentAgent(id);
    if (!editable(initial)) throw new Error('节点状态或权限已变化，请刷新后重试。');
    const changes = settings || { 'net.ipv4.tcp_congestion_control': action === 'enable-bbr' ? 'bbr' : 'cubic', 'net.core.default_qdisc': 'fq' };
    validateTCPAvailability(changes, initial.metrics?.bbr?.parameters);
    const normalize = value => String(value).trim().replace(/\s+/g, ' ');
    const baseline = Object.fromEntries(Object.keys(changes).map(key => [key, normalize(initial.metrics.bbr.parameters[key])]));
    const summary = Object.entries(changes).map(([key, value]) => `${key}: ${baseline[key]} → ${value}`).join('\n');
    submitting.add(id); draw(); syncDialogs();
    try {
      if (!await confirmFor(id, `应用到「${initial.name}」？\n${summary}\n\n保存到 /etc/sysctl.d/90-qcontrolhub-bbr.conf。${Object.keys(changes).some(key => /rmem|wmem/.test(key)) ? '\n缓冲区上限请结合节点内存与并发连接数评估。' : ''}`, actionLabel(action)) || !ctx.current()) return;
      const refreshed = await reload();
      if (!ctx.current()) return;
      if (!refreshed) throw new Error('节点状态正在更新，请核对后重试。');
      const agent = currentAgent(id);
      if (!owned(agent) || !ctx.can('tasks.execute') || !systemBBRState(agent).controllable || ['pending', 'running'].includes(latest.get(id)?.status)) throw new Error('节点或任务状态已变化，请刷新核对后重新操作。');
      validateTCPAvailability(changes, agent.metrics?.bbr?.parameters);
      if (Object.keys(changes).some(key => normalize(agent.metrics.bbr.parameters[key]) !== baseline[key])) throw new Error('参数已变化，请核对当前值后重新确认。');
      const task = await ctx.api('/tasks', { method: 'POST', body: { agent_id: id, action, engine: '', ...(settings ? { tcp_settings: settings } : {}) } });
      if (!ctx.current()) return;
      latest.set(id, ctx.can('tasks.read') ? task : { ...task, status: 'submitted' });
      if (settings) drafts.delete(id);
      ctx.announce('TCP 调优任务已提交，等待 Agent 执行。');
      return task;
    } finally { submitting.delete(id); if (ctx.current()) { draw(); syncDialogs(); } }
  }
  async function action(id, name) {
    const task = await submit(id, name);
    if (task && ctx.can('tasks.read')) await taskDetail(ctx, task.id);
  }
  function editor(id) {
    const agent = currentAgent(id);
    if (!editable(agent)) return;
    const parameters = agent.metrics?.bbr?.parameters || {};
    const draft = drafts.get(id) || {};
    const currentLabels = new Map();
    const rows = list(rules).map(rule => {
      const value = field(rule.key, rule.key, draft[rule.key] ?? parameters[rule.key] ?? '', {
        options: rule.choices?.length ? rule.choices : undefined, disabled: !hasTCPParameter(parameters, rule.key),
        hint: `当前：${hasTCPParameter(parameters, rule.key) ? parameters[rule.key] : '未上报'}`,
      });
      currentLabels.set(rule.key, value.querySelector('.a-field-hint'));
      return stack(field(rule.label || rule.key, `select:${rule.key}`, Object.hasOwn(draft, rule.key), {
        type: 'checkbox', disabled: !hasTCPParameter(parameters, rule.key) && !Object.hasOwn(draft, rule.key),
      }), value, rule.description && muted(rule.description));
    });
    const error = notice('');
    const draftState = muted('');
    const capture = () => {
      if (submitting.has(id) || clearing || !ctx.current()) return drafts.get(id) || {};
      const values = {};
      for (const rule of list(rules)) if (form.elements.namedItem(`select:${rule.key}`).checked) values[rule.key] = form.elements.namedItem(rule.key).value;
      if (submitting.has(id) || !ctx.current()) return drafts.get(id) || {};
      drafts.set(id, values);
      draftState.textContent = `${Object.keys(values).length} 项待应用`;
      draw(); reconcile();
      return values;
    };
    const form = ctx.form([error, draftState, grid(rows)], '提交参数', async () => {
      const settings = validateTCPSelection(capture(), rules);
      const task = await submit(id, 'configure-tcp', settings);
      if (!task) return;
      box.close();
      if (ctx.can('tasks.read')) await taskDetail(ctx, task.id);
    });
    const presetButtons = systemTCPPresets.map(preset => button(preset.name, () => {
      try {
        const fresh = currentAgent(id);
        if (!editable(fresh)) throw new Error('节点状态或权限已变化，请刷新后重试。');
        const next = prepareTCPPreset(preset.id, rules, fresh.metrics?.bbr?.parameters, capture());
        for (const rule of list(rules)) {
          form.elements.namedItem(`select:${rule.key}`).checked = Object.hasOwn(next, rule.key);
          if (Object.hasOwn(next, rule.key)) form.elements.namedItem(rule.key).value = next[rule.key];
        }
        form.querySelectorAll('.a-notice').forEach(item => { item.textContent = ''; }); capture();
      } catch (failure) { error.textContent = failure.message; }
    }, { 'data-tcp-preset': preset.id }));
    let clearing = false;
    const reset = button('清空选择', async () => {
      if (clearing || !editable(currentAgent(id))) return;
      const values = capture();
      clearing = true; reconcile();
      try {
      if (Object.keys(values).length && !await confirmFor(id, '清空此节点未提交的 TCP 参数选择？已保存的系统配置不受影响。', '清空选择')) return;
      if (!ctx.current() || !box.isConnected) return;
      for (const rule of list(rules)) {
        form.elements.namedItem(`select:${rule.key}`).checked = false;
        form.elements.namedItem(rule.key).value = currentAgent(id)?.metrics?.bbr?.parameters?.[rule.key] ?? '';
      }
      form.querySelectorAll('.a-notice').forEach(item => { item.textContent = ''; }); clearing = false; capture();
      } finally { clearing = false; reconcile(); }
    }, { 'data-tcp-reset': '' });
    form.querySelector('fieldset').prepend(actions(presetButtons, reset));
    for (const rule of list(rules)) for (const type of ['input', 'change']) form.elements.namedItem(rule.key).addEventListener(type, () => { form.elements.namedItem(`select:${rule.key}`).checked = true; error.textContent = ''; form.querySelectorAll('.a-notice').forEach(item => { item.textContent = ''; }); capture(); });
    form.addEventListener('change', () => { form.querySelectorAll('.a-notice').forEach(item => { item.textContent = ''; }); capture(); });
    const liveStatus = muted('');
    form.prepend(liveStatus);
    const box = ctx.dialog(`${agent.name} · TCP 参数`, form, { wide: true });
    function reconcile() {
      if (!ctx.current() || !box.isConnected) return false;
      const fresh = currentAgent(id);
      if (!fresh || !owned(fresh)) { box.close(); drafts.delete(id); return false; }
      const locked = clearing || !editable(fresh), current = fresh.metrics?.bbr?.parameters || {}, values = drafts.get(id) || {};
      liveStatus.textContent = [systemBBRState(fresh).text, latest.has(id) && taskLabel(latest.get(id).status)].filter(Boolean).join(' · ');
      for (const rule of list(rules)) {
        const select = form.elements.namedItem(`select:${rule.key}`), input = form.elements.namedItem(rule.key), selected = Object.hasOwn(values, rule.key), available = hasTCPParameter(current, rule.key);
        select.checked = selected; select.disabled = locked || !available && !selected;
        input.disabled = locked || !available;
        const label = currentLabels.get(rule.key), text = `当前：${available ? current[rule.key] : '未上报'}`;
        if (label.textContent !== text) label.textContent = text;
        const desired = values[rule.key] ?? current[rule.key] ?? '';
        if (input.value !== desired) input.value = desired;
      }
      presetButtons.forEach((control, index) => {
        let unavailable = '';
        try { prepareTCPPreset(systemTCPPresets[index].id, rules, current); } catch (failure) { unavailable = failure.message; }
        control.disabled = locked || Boolean(unavailable); control.title = unavailable;
      });
      reset.disabled = locked;
      form.querySelector('[type=submit]').disabled = locked;
      return true;
    }
    capture(); observe(reconcile);
  }
  function parameters(id) {
    const content = stack(), box = ctx.dialog(`${currentAgent(id).name} · 系统参数`, content, { wide: true });
    let signature;
    const refresh = () => {
      const agent = currentAgent(id);
      if (!ctx.current() || !box.isConnected) return false;
      if (!agent || agent.can_manage === false) { box.close(); return false; }
      const metrics = agent.metrics?.bbr || {}, next = JSON.stringify(metrics);
      if (next === signature) return true;
      signature = next;
      patch(content, stack(
        metrics.error && notice(diagnosticError(metrics.error)),
        section('已加载算法', muted(list(metrics.available_algorithms).join(' · ') || '尚未上报')),
        section('内核当前参数', table(['参数', '当前值', '面板保存值'], Object.entries(metrics.parameters || {}).map(([key, value]) => [key, value, metrics.configured_parameters?.[key] ?? '—']), { paginate: 0, label: '内核当前参数' })),
        section('网卡实际队列', table(['网卡', '队列', '层级'], list(metrics.qdiscs).map(value => [value.device, value.kind, `${value.root ? '根队列' : value.parent || '—'} ${value.handle || ''}`]), { paginate: 0, label: '网卡实际队列' })),
        metrics.qdisc_error && notice(diagnosticError(metrics.qdisc_error)), muted(`参数采集：${date(metrics.collected_at)}`),
      ));
      return true;
    };
    refresh(); observe(refresh);
  }
  function draw() {
    if (!agentsReady) return;
    const ordered = paging.slice(selected());
    pageCount.textContent = paging.summary(); pages.hidden = paging.last <= 1;
    previous.disabled = paging.page <= 1; next.disabled = paging.page >= paging.last;
    const rows = ordered.map(agent => {
      const current = systemBBRState(agent), metrics = agent.metrics?.bbr || {};
      const task = latest.get(agent.id);
      const drift = metrics.persistence === 'managed' && Object.entries(metrics.configured_parameters || {}).some(([key, value]) => String(metrics.parameters?.[key] || '').trim().replace(/\s+/g, ' ') !== value);
      const row = [stack(nodeName(ctx, agent), muted([metrics.kernel_release, agent.os, agent.arch].filter(Boolean).join(' · '))), stack(actions(badge(current.text, current.tone)), metrics.error && muted('部分参数读取失败'), muted(date(metrics.collected_at))), metrics.congestion_control || '—', metrics.default_qdisc || '—', stack(actions(badge(metrics.persistence === 'managed' ? `托管 ${Object.keys(metrics.configured_parameters || {}).length} 项` : metrics.persistence === 'error' ? '配置冲突' : '未托管')), drift && notice('当前生效参数与已保存配置不一致')),
        task ? stack(date(task.created_at), badge(taskLabel(task.status)), task.error && muted(diagnosticError(task.error))) : '—',
        actions(owned(agent) && ctx.can('tasks.execute') && button('启用 BBR', event => void ctx.mutate(event.currentTarget, () => action(agent.id, 'enable-bbr')), { disabled: !editable(agent) || !hasTCPParameter(metrics.parameters, 'net.core.default_qdisc') || !hasTCPParameter(metrics.parameters, 'net.ipv4.tcp_congestion_control') }),
          owned(agent) && ctx.can('tasks.execute') && button('切换 CUBIC', event => void ctx.mutate(event.currentTarget, () => action(agent.id, 'disable-bbr')), { disabled: !editable(agent) || !hasTCPParameter(metrics.parameters, 'net.core.default_qdisc') || !hasTCPParameter(metrics.parameters, 'net.ipv4.tcp_congestion_control') }),
          owned(agent) && ctx.can('tasks.execute') && button(drafts.has(agent.id) && Object.keys(drafts.get(agent.id)).length ? '自定义参数 · 有草稿' : '自定义参数', () => editor(agent.id), { disabled: !editable(agent) || !rulesReady }),
          button('查看参数', () => parameters(agent.id)),
          task && ctx.can('tasks.read') && button('任务详情', event => void ctx.mutate(event.currentTarget, () => taskDetail(ctx, task.id))))];
      row.refreshKey = agent.id; return row;
    });
    if (resultTable) resultTable.updateRows(rows);
    else {
      resultTable = table(['节点', '状态', '拥塞控制', '队列', '持久配置', '最近任务', '操作'], rows, { label: 'TCP 调优', rowKey: row => row.refreshKey, motion: false });
      results.append(resultTable);
    }
  }
  const agentRead = readRegion(ctx, '节点状态', async signal => { try { return await ctx.api('/agents', { signal }); } catch (error) { if (ctx.current()) refreshStatus.textContent = agentsReady ? '状态刷新失败，保留上次数据并重试。' : '节点状态读取失败，请重试。'; throw error; } }, value => { agents = value; agentsReady = true; draw(); syncDialogs(); refreshStatus.textContent = '正在实时更新'; }, { view: resultTable, clear() { agents = []; agentsReady = true; drafts.clear(); draw(); syncDialogs(); } });
  const rulesRead = readRegion(ctx, 'TCP 参数选项', signal => ctx.api('/system-tcp/parameters', { signal }), value => { rules = value; rulesReady = true; draw(); }, { clear() { rulesReady = false; draw(); } });
  const taskRead = readRegion(ctx, 'TCP 任务状态', signal => ctx.can('tasks.read') ? ctx.api('/system-tcp/tasks', { signal }) : [], value => { tasks = value; tasksReady = true; setTasks(tasks); draw(); syncDialogs(); }, { clear() { tasksReady = false; latest.clear(); draw(); syncDialogs(); } });
  const orderRead = readRegion(ctx, '节点排序', signal => ctx.api('/preferences/node-order', { signal }), value => { order = value; draw(); });
  const refreshView = () => Promise.all([agentRead.load(), taskRead.load(), !rulesReady && rulesRead.load(), orderRead.load()]);
  ctx.onRefresh(refreshView);
  poll(ctx, refreshView, 5000);
  void refreshView();
  return stack(agentRead.element, taskRead.element, rulesRead.element, orderRead.element, pageSize.status, node('div', { className: 'a-probe-toolbar' }, refreshStatus, pageSize.element), section(null, results), pages);
}
