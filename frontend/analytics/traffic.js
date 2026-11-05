import { trafficBatch } from './traffic-batch.js';
import { trafficSync } from './traffic-sync.js';
import { trafficDetails } from './traffic-details.js';
import { trafficTable } from './traffic-table.js';
import { mergeTrafficPorts, trafficPolicyEditable, eligibleAgents, policyStatus, policyStatusKey, trafficRateForDisplay, protocolName, dateInputValue } from '../modules/traffic-model.js';
import { createPoller } from '../modules/poller.js';
import { filterbar, node, button, actions, stack, section, badge, field, muted, list, pathID } from './ui.js';
import { bytes, date } from './operations.js';

export async function trafficPage(ctx) {
  const agents = [], endpoints = [];
  let byID = new Map();
  const policies = [], results = trafficTable(['节点 / 归属', '端口 / 协议', '状态', '用量 / 配额', '速率', '操作']);
  const initial = ctx.state.data.analyticsTrafficFilters || {};
  const route = new URLSearchParams(location.hash.split('?')[1] || '');
  const search = field('搜索端口', 'search', initial.search || '', { type: 'search', placeholder: '节点、名称、内核或端口' });
  const scope = field('用户范围', 'scope', initial.scope || 'current', { options: [['current', '当前用户'], ['all', '全部获授权资源']] });
  let firstAgent = route.get('agent') || initial.agent || '';
  const agentFilter = field('节点', 'agent', '', { options: [['', '全部节点']] });
  const engineFilter = field('内核', 'engine', initial.engine || '', { options: [['', '全部内核'], ...['mihomo', 'xray', 'sing-box', 'ss-rust']] });
  const statusFilter = field('状态', 'status', initial.status || '', { options: [['', '全部状态'], ['active', '已设置配额'], ['metering', '持续统计'], ['blocked', '已封禁'], ['waiting', '等待同步'], ['stopped', '服务已停止'], ['offline', '节点离线'], ['error', '监控异常']] });
  const controls = [search, scope, agentFilter, engineFilter, statusFilter];
  const refreshStatus = muted('');
  let readSequence = 0, readController, lastError = false;
  function currentFilters() { return Object.fromEntries(controls.map(root => { const input = root.querySelector('input,select'); return [input.name, input.value]; })); }
  function matchesFilter(value) {
    const filters = currentFilters(), agent = byID.get(value.agent_id);
    const policy = policies.find(policy => policy.agent_id === value.agent_id && policy.engine === value.engine && policy.port === value.port);
    return (filters.scope === 'all' || value.is_current_user === true) && (!filters.agent || value.agent_id === filters.agent) &&
      (!filters.engine || value.engine === filters.engine) && (!filters.status || (policy ? policyStatusKey(policy, agent) : 'waiting') === filters.status) &&
      [value.agent_id, agent?.name, value.name, value.engine, value.port].join(' ').toLowerCase().includes(filters.search.trim().toLowerCase());
  }
  function keepObjects(target, received, key) {
    const old = new Map(target.map(value => [key(value), value]));
    target.splice(0, target.length, ...received.map(value => {
      const retained = old.get(key(value)) || {};
      for (const name of Object.keys(retained)) if (!(name in value)) delete retained[name];
      return Object.assign(retained, value);
    }));
  }
  async function reload({ quiet = false } = {}) {
    readController?.abort(); readController = new AbortController();
    const expected = ++readSequence;
    const signal = AbortSignal.any([ctx.signal, readController.signal]);
    try {
      const canReadNodes = ctx.can('agents.read');
      const [page, ports, nodes] = await Promise.all([
        ctx.api(canReadNodes ? '/traffic-policies' : '/traffic-policies?view=page', { signal }),
        ctx.api('/traffic-endpoints', { signal }), canReadNodes ? ctx.api('/agents', { signal }) : [],
      ]);
      if (!ctx.current() || expected !== readSequence) return false;
      keepObjects(agents, list(canReadNodes ? nodes : page.agents), value => value.id); byID = new Map(agents.map(agent => [agent.id, agent]));
      keepObjects(policies, list(canReadNodes ? page : page.policies), value => value.id);
      keepObjects(endpoints, list(ports), value => `${value.agent_id}:${value.engine}:${value.port}:${value.protocol}:${value.owner_id}`);
      const select = agentFilter.querySelector('select'), selected = firstAgent ?? select.value;
      firstAgent = null;
      const options = [['', '全部节点'], ...agents.map(agent => [agent.id, agent.name])];
      const fingerprint = JSON.stringify(options);
      if (select.dataset.options !== fingerprint) {
        select.replaceChildren(select.querySelector('button'), ...options.map(([value, label]) => node('option', { value }, label)));
        select.value = options.some(([value]) => value === selected) ? selected : '';
        select.dataset.options = fingerprint;
      }
      refreshStatus.textContent = ''; lastError = false;
      if (!document.querySelector('dialog.a-dialog[open]')) draw();
      return true;
    } catch (error) {
      if (!ctx.current() || expected !== readSequence || error.name === 'AbortError') return false;
      if (!quiet) throw error;
      refreshStatus.textContent = '刷新失败，保留上次数据'; refreshStatus.title = error.message;
      if (!lastError) ctx.announce(error.message, 'error');
      lastError = true; return false;
    }
  }
  const poller = createPoller({ run: () => reload({ quiet: true }), isActive: () => ctx.current() && !document.hidden && !document.querySelector('dialog.a-dialog[open]'), delay: () => 2000 });
  const stop = () => { readController?.abort(); poller.stop(); };
  ctx.signal.addEventListener('abort', stop, { once: true });
  document.addEventListener('visibilitychange', () => document.hidden ? poller.stop() : void poller.trigger(), { signal: ctx.signal });
  document.addEventListener('close', () => { if (ctx.current() && !document.querySelector('dialog.a-dialog[open]')) { draw(); poller.start(); } }, { capture: true, signal: ctx.signal });
  async function afterWrite(message) {
    const refreshed = await reload({ quiet: true });
    if (ctx.current()) ctx.announce(refreshed ? message : `${message}，列表刷新失败，请重试。`, refreshed ? '' : 'error');
  }
  function edit(policy = null, endpoint = null) {
    const target = policy || endpoint;
    const available = (policy ? [byID.get(policy.agent_id)].filter(Boolean) : eligibleAgents(agents)).filter(agent => ctx.can('traffic.manage', agent));
    if (policy && !trafficPolicyEditable(policy, byID.get(policy.agent_id), ctx.can)) return;
    const agentSelect = field('节点', 'agent_id', target?.agent_id || available[0]?.id || '', { options: available.map(agent => [agent.id, agent.name]), disabled: !!policy, required: true });
    const engineSelect = field('内核', 'engine', target?.engine || '', { options: [], required: true });
    function engines() {
      const input = engineSelect.querySelector('select'), selected = input.value || target?.engine;
      const agent = byID.get(agentSelect.querySelector('select').value);
      input.replaceChildren(input.querySelector('button'), ...(agent?.capabilities || []).map(engine => node('option', { value: engine }, engine)));
      input.value = (agent?.capabilities || []).includes(selected) ? selected : agent?.capabilities?.[0] || '';
    }
    agentSelect.addEventListener('change', engines); engines();
    let changed = false;
    const form = ctx.form([
      node('div', { className: 'a-form-grid' }, agentSelect, engineSelect,
        field('监听端口', 'port', target?.port || '', { type: 'number', min: 1, max: 65535, required: true }),
        field('名称', 'name', target?.name || '', { required: true, maxlength: 100 }),
        field('协议', 'protocol', target?.protocol || 'both', { options: [['both', 'TCP + UDP'], ['tcp', 'TCP'], ['udp', 'UDP']] }),
        field('周期', 'cycle', policy?.cycle || 'monthly', { options: [['monthly', '每月'], ['yearly', '每年']] }),
        field('周期起始日', 'cycle_anchor', dateInputValue(policy?.cycle_anchor || new Date()), { type: 'date', required: true, max: dateInputValue() }),
        field('配额（GiB）', 'limit_gb', policy?.quota_enabled === false ? 0 : (policy?.limit_bytes || 0) / 1024 ** 3, { type: 'number', min: 0, max: 8388607, step: 'any', hint: '填 0 则仅统计流量' })),
      field('超额自动封禁', 'auto_block', policy?.auto_block ?? true, { type: 'checkbox' }),
    ], '保存配额', async (values, element) => {
      const limit = Math.round(Number(values.limit_gb) * 1024 ** 3);
      if (!Number.isSafeInteger(limit) || limit < 0) throw new Error('请输入有效配额。');
      const saved = await ctx.api(policy ? `/traffic-policies/${pathID(policy.id)}` : '/traffic-policies', { method: policy ? 'PUT' : 'POST', body: {
        agent_id: policy?.agent_id || values.agent_id, engine: values.engine, port: Number(values.port), name: values.name.trim(), protocol: values.protocol, cycle: values.cycle,
        cycle_anchor: `${values.cycle_anchor}T00:00:00Z`, limit_bytes: limit, auto_block: element.elements.auto_block.checked,
      } });
      if (!ctx.current() || !box.open) return;
      changed = false; ctx.setDirty(false); box.close();
      await afterWrite('配额已保存');
      // Write responses do not carry list visibility/ownership projections.
      // Only the subsequent authorized collection read updates the table.
      if (ctx.current() && saved.id && !policies.some(value => value.id === saved.id)) ctx.announce('配额已保存，端口成功部署后显示。');
    });
    form.addEventListener('input', () => { changed = true; ctx.setDirty(true); });
    form.addEventListener('change', () => { changed = true; ctx.setDirty(true); });
    const box = ctx.dialog(policy ? '编辑端口配额' : '添加端口配额', form);
    box.onRequestClose = async () => { if (!changed || await ctx.confirm('存在未保存的更改，确定关闭？', '放弃更改')) { changed = false; ctx.setDirty(false); box.close(); } };
    const close = () => box.close();
    ctx.signal.addEventListener('abort', close, { once: true });
    box.addEventListener('close', () => { ctx.setDirty(false); ctx.signal.removeEventListener('abort', close); }, { once: true });
  }
  async function change(policy, control, kind) {
    const options = {
      reset: ['清零并解封', '清零当前周期用量并解除此端口的自动封禁？', '/reset', 'POST', '计数已清零，正在同步 Agent'],
      quota: ['取消配额', '取消配额并解除自动封禁？此端口继续统计流量。', '', 'DELETE', '配额已取消'],
      monitoring: ['删除流量记录', '停止监控并永久删除此端口当前记录和每日历史？', '/monitoring', 'DELETE', '流量记录已删除'],
    }[kind];
    if (!await ctx.confirm(options[1], options[0]) || !ctx.current()) return;
    await ctx.mutate(control, async () => { await ctx.api(`/traffic-policies/${pathID(policy.id)}${options[2]}`, { method: options[3] }); await afterWrite(options[4]); });
  }
  function draw(reset = false) {
    const all = mergeTrafficPorts(policies, endpoints);
    const items = all.filter(item => matchesFilter(item.policy || item.endpoint));
    results.update(items.map(item => {
      const value = item.policy || item.endpoint, agent = byID.get(value.agent_id);
      const writable = !!item.policy && trafficPolicyEditable(value, agent, ctx.can);
      const sampledAt = value.last_collected_at || value.last_reported_at;
      const rate = number => value.enforcement_available === false ? 0 : trafficRateForDisplay(number, sampledAt, agent?.status);
      return { key: item.key, cells: [
        stack(agent?.name || value.agent_id, muted(value.owner_name || (value.is_current_user ? '当前用户' : '其他用户')), muted(value.shared_quota || value.share_id ? '共享实例' : '主实例')),
        stack(node('strong', {}, `:${value.port}`), muted(`${value.engine} · ${protocolName(value.protocol)}`), muted(value.name)),
        item.policy ? stack(badge(...policyStatus(value, agent)), muted(date(sampledAt))) : badge('等待同步', 'warn'),
        item.policy ? stack(`${bytes(value.used_bytes)} / ${value.quota_enabled === false ? '持续统计' : bytes(value.limit_bytes)}`, muted(`接收 ${bytes(value.received_bytes)} · 发送 ${bytes(value.sent_bytes)}`), muted(value.period_start ? `${dateInputValue(value.period_start)} 至 ${dateInputValue(value.period_end)}` : '等待周期确认')) : '—',
        item.policy ? stack(`接收 ${bytes(rate(value.receive_bps))}/s`, muted(`发送 ${bytes(rate(value.send_bps))}/s`)) : '—',
        actions(item.policy && button('统计详情', () => trafficDetails(ctx, value, agent)),
          writable && button(value.quota_enabled === false ? '设置配额' : '编辑配额', () => edit(value)),
          !item.policy && eligibleAgents([agent].filter(Boolean)).length > 0 && ctx.can('traffic.manage', agent) && button('设置配额', () => edit(null, value)),
          writable && button('清零', event => void change(value, event.currentTarget, 'reset')),
          writable && value.quota_enabled !== false && button('取消配额', event => void change(value, event.currentTarget, 'quota')),
          writable && button('删除', event => void change(value, event.currentTarget, 'monitoring'), { tone: 'danger' })),
      ] };
    }), reset, all.length ? '没有符合筛选条件的端口' : '暂无已部署端口，成功部署配置后显示流量监控');
  }
  await reload();
  const toolbar = filterbar(node('div', { className: 'a-filter-fields a-traffic-filters' }, controls), actions(
    ctx.can('traffic.manage') && button('同步配置端口', () => trafficSync(ctx, { agentName: id => byID.get(id)?.name || id, matches: matchesFilter, onSaved: () => reload({ quiet: true }) })),
    ctx.can('traffic.manage') && button('批量操作', () => trafficBatch(ctx, policies.filter(policy => matchesFilter(policy) && trafficPolicyEditable(policy, byID.get(policy.agent_id), ctx.can)), id => byID.get(id)?.name || id, () => reload({ quiet: true }))),
    ctx.can('traffic.manage') && button('添加配额', () => edit(), { tone: 'primary', disabled: !eligibleAgents(agents).some(agent => ctx.can('traffic.manage', agent)) })));
  const reset = [...toolbar.querySelectorAll('button')].find(button => button.textContent === '重置');
  reset?.replaceWith(button('重置', () => {
    const defaults = { search: '', scope: 'current', agent: '', engine: '', status: '' };
    for (const root of controls) { const input = root.querySelector('input,select'); input.value = defaults[input.name]; }
    ctx.state.data.analyticsTrafficFilters = currentFilters(); draw(true);
  }));
  for (const root of controls) root.addEventListener(root === search ? 'input' : 'change', () => { ctx.state.data.analyticsTrafficFilters = currentFilters(); draw(true); });
  poller.start();
  const content = stack(toolbar, refreshStatus, section(null, results.root));
  content.classList.add('a-traffic-page');
  return content;
}
