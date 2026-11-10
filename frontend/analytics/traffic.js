import { readRegion, loadingTable } from "./loading.js";
import { trafficBatch } from './traffic-batch.js';
import { trafficSync } from './traffic-sync.js';
import { trafficDetails } from './traffic-details.js';
import { trafficTable } from './traffic-table.js';
import { metricTrack } from './metric-track.js';
import { bindNodeOrdering } from './node-ordering.js';
import { nodeName } from './node-region.js';
import { mergeTrafficPorts, orderTrafficItems, trafficCardIdentity, trafficPolicyEditable, trafficCardOrderKey, eligibleAgents, policyStatus, policyStatusKey, trafficRateForDisplay, protocolName, dateInputValue, reusableTrafficSnapshot } from '../modules/traffic-model.js';
import { createNodeOrderSync, orderNodesBySavedOrder } from '../modules/node-order.js';
import { accountStorage } from '../modules/account-storage.js';
import { createPoller } from '../modules/poller.js';
import { filterbar, node, button, dragHandle, actions, stack, section, badge, field, muted, list, pathID } from './ui.js';
import { bytes } from './operations.js';

export async function trafficPage(ctx) {
  const data = ctx.state.data, session = ctx.session;
  const epoch = data.analyticsTrafficEpoch = (data.analyticsTrafficEpoch || 0) + 1;
  const agents = [], endpoints = [];
  let byID = new Map(), policiesReady = false, agentsReady = false, endpointsReady = false;
  let details;
  const ordering = createNodeOrderSync((path, options = {}) => ctx.api(path, { ...options, body: options.body ? JSON.parse(options.body) : undefined }), accountStorage, ctx.current);
  const portOrdering = createNodeOrderSync((path, options = {}) => ctx.api(path, { ...options, body: options.body ? JSON.parse(options.body) : undefined }), accountStorage, ctx.current, true, { endpoint: "/preferences/traffic-order", key: trafficCardOrderKey,
    migrationIDs: ids => { const visible = new Set(mergeTrafficPorts(policies, endpoints).map(trafficCardIdentity)); return ids.filter(id => visible.has(id)); },
  });
  const savedTrafficCardOrder = () => portOrdering.ids();
  let savedView = 'cards';
  try { savedView = accountStorage.getItem('qcontrolhub:traffic-view') === 'list' ? 'list' : 'cards'; } catch { /* The view remains usable without storage. */ }
  const policies = [], results = trafficTable(['节点 / 归属', '端口 / 协议', '状态', '累计流量', '累计接收', '累计发送', '操作'], {
    view: savedView, canSwitch: () => !dragging.active(), onViewChange: view => { try { accountStorage.setItem('qcontrolhub:traffic-view', view); } catch { /* In-memory view remains selected. */ } },
  });
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
  const allItems = () => orderTrafficItems(mergeTrafficPorts(policies, endpoints), savedTrafficCardOrder(), ordering.ids());
  const dragging = bindNodeOrdering(ctx, results.root, {
    enabled: () => !document.querySelector('dialog.a-dialog[open]'),
    order: () => allItems().filter(item => matchesFilter(item.policy || item.endpoint)).map(item => item.key),
    save: async ids => {
      const selected = new Set(ids), incoming = [...ids];
      const all = allItems(), identities = new Map(all.map(item => [item.key, trafficCardIdentity(item)]));
      const complete = all.map(item => identities.get(selected.has(item.key) ? incoming.shift() : item.key));
      await portOrdering.save(complete);
    }, redraw: () => draw(), saved: '端口顺序已保存',
  });
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
  function updateAgentFilter() {
      const select = agentFilter.querySelector('select'), selected = firstAgent ?? select.value;
      firstAgent = null;
      const ordered = orderNodesBySavedOrder(agents, ordering.ids());
      const fingerprint = JSON.stringify(ordered.map(agent => [agent.id, agent.name, agent.labels?.region_code, agent.region_code]));
      if (select.dataset.options !== fingerprint && !select.matches(':open')) {
        select.replaceChildren(select.querySelector('button'), node('option', { value: '' }, '全部节点'),
          ...ordered.map(agent => node('option', { value: agent.id }, nodeName(ctx, agent))));
        select.value = ordered.some(agent => agent.id === selected) ? selected : '';
        select.dataset.options = fingerprint;
      }
  }
  async function reload({ quiet = false, initial = false } = {}) {
    readController?.abort(); readController = new AbortController();
    const expected = ++readSequence;
    const signal = AbortSignal.any([ctx.signal, readController.signal]);
    try {
      const canReadNodes = ctx.can('agents.read');
      const cached = initial && reusableTrafficSnapshot(data.analyticsTrafficSnapshot, { data, session, canRead: canReadNodes, epoch });
      const publish = () => {
        if (!ctx.current() || expected !== readSequence) return;
        addQuota && (addQuota.disabled = !policiesReady || !agentsReady || !eligibleAgents(agents).some(agent => ctx.can('traffic.manage', agent)));
        updateAgentFilter();
        const available = policies.length > 0 || endpoints.length > 0 || policiesReady && endpointsReady;
        firstRegion.hidden = available; results.root.hidden = !available; if (available) draw();
      };
      const optional = read => { void read.then(() => { if (ctx.current() && expected === readSequence) { updateAgentFilter(); draw(); } }).catch(() => {}); };
      optional(ordering.load()); optional(portOrdering.load());
      const [page, ports, nodes] = await Promise.all(cached ? [cached.policies, cached.endpoints, cached.agents] : [
        ctx.api(canReadNodes ? '/traffic-policies' : '/traffic-policies?view=page', { signal }).then(page => {
          if (ctx.current() && expected === readSequence) {
            keepObjects(policies, list(canReadNodes ? page : page.policies), value => value.id); policiesReady = true;
            if (!canReadNodes) { keepObjects(agents, list(page.agents), value => value.id); byID = new Map(agents.map(agent => [agent.id, agent])); agentsReady = true; }
            publish();
          } return page;
        }),
        ctx.api('/traffic-endpoints', { signal }).then(ports => { if (ctx.current() && expected === readSequence) { keepObjects(endpoints, list(ports), value => `${value.agent_id}:${value.engine}:${value.port}:${value.protocol}:${value.owner_id}`); endpointsReady = true; publish(); } return ports; }),
        canReadNodes ? ctx.api('/agents', { signal }).then(nodes => { if (ctx.current() && expected === readSequence) { keepObjects(agents, list(nodes), value => value.id); byID = new Map(agents.map(agent => [agent.id, agent])); agentsReady = true; publish(); } return nodes; }) : [],
      ]);
      if (!ctx.current() || expected !== readSequence) return false;
      keepObjects(agents, list(cached || canReadNodes ? nodes : page.agents), value => value.id); byID = new Map(agents.map(agent => [agent.id, agent]));
      keepObjects(policies, list(cached || canReadNodes ? page : page.policies), value => value.id);
      keepObjects(endpoints, list(ports), value => `${value.agent_id}:${value.engine}:${value.port}:${value.protocol}:${value.owner_id}`);
      policiesReady = endpointsReady = agentsReady = true;
      publish();
      if (!ctx.current() || expected !== readSequence) return false;
      if (!cached) data.analyticsTrafficSnapshot = { data, session, canRead: canReadNodes, epoch, at: Date.now(),
        agents: structuredClone(agents), policies: structuredClone(policies), endpoints: structuredClone(endpoints) };
      updateAgentFilter();
      refreshStatus.textContent = ''; lastError = false;
      if (details?.box.open) {
        const policy = policies.find(value => value.id === details.policyID);
        if (policy) details.update(policy, byID.get(policy.agent_id));
        else { details.box.close(); ctx.announce('此端口已不可访问，请重新选择。', 'error'); }
      }
      if (!dragging.active() && !document.querySelector('dialog.a-dialog[open]')) draw();
      return true;
    } catch (error) {
      if (!ctx.current() || expected !== readSequence || error.name === 'AbortError') return false;
      if ([401, 403, 404].includes(error.status)) {
        delete data.analyticsTrafficSnapshot;
        readSequence++; readController.abort();
        agents.length = policies.length = endpoints.length = 0; byID.clear();
        details?.box.close(); draw();
      }
      if (!quiet) throw error;
      refreshStatus.textContent = [401, 403, 404].includes(error.status) ? '流量访问权限已变化' : policiesReady || endpointsReady ? '刷新失败，保留已读取的数据' : '流量读取失败，请重试'; refreshStatus.title = error.message;
      if (!lastError) ctx.announce(error.message, 'error');
      lastError = true; return false;
    }
  }
  const poller = createPoller({ run: () => reload({ quiet: true }), isActive: () => ctx.current() && !document.hidden && !document.querySelector('dialog.a-dialog[open]:not([data-traffic-details]):not([data-traffic-sync])'), delay: () => 2000 });
  const stop = () => { readController?.abort(); poller.stop(); };
  ctx.signal.addEventListener('abort', stop, { once: true });
  document.addEventListener('visibilitychange', () => document.hidden ? poller.stop() : void poller.trigger(), { signal: ctx.signal });
  document.addEventListener('close', () => { if (ctx.current() && !document.querySelector('dialog.a-dialog[open]')) { draw(); poller.start(); } }, { capture: true, signal: ctx.signal });
  async function afterWrite(message) {
    const refreshed = await reload({ quiet: true });
    if (ctx.current()) ctx.announce(refreshed ? message : `${message}，列表刷新失败，请重试。`, refreshed ? '' : 'error');
  }
  function edit(policy = null, endpoint = null) {
    if (!policiesReady || !agentsReady) return;
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
    if (dragging.active() || !policiesReady && !endpointsReady) return;
    const all = allItems();
    const items = all.filter(item => matchesFilter(item.policy || item.endpoint));
    results.update(items.map(item => {
      const value = item.policy || item.endpoint, agent = byID.get(value.agent_id);
      const writable = !!item.policy && trafficPolicyEditable(value, agent, ctx.can);
      const sampledAt = value.last_collected_at || value.last_reported_at;
      const rate = number => value.enforcement_available === false ? 0 : trafficRateForDisplay(number, sampledAt, agent?.status);
      const controls = () => actions(
        item.policy && button('统计详情', () => { details = trafficDetails(ctx, value, agent, { canManage: policy => trafficPolicyEditable(policy, byID.get(policy.agent_id), ctx.can), change }); }),
        writable && button(value.quota_enabled === false ? '设置配额' : '编辑配额', () => edit(value)),
        !item.policy && eligibleAgents([agent].filter(Boolean)).length > 0 && ctx.can('traffic.manage', agent) && button('设置配额', () => edit(null, value), { disabled: !policiesReady || !agentsReady }),
        dragHandle(item.key, `端口 ${value.port}`));
      const state = () => item.policy ? badge(...policyStatus(value, agent)) : badge(policiesReady ? '等待同步' : '正在读取配额…', 'warn');
      const identity = () => stack(node('span', {}, agent?.name || value.agent_id), !value.is_current_user && muted(value.owner_name || '其他用户'), (value.shared_quota || value.share_id) && muted('共享实例'));
      const cumulative = () => item.policy && value.used_bytes != null && Number.isFinite(Number(value.used_bytes)) ? bytes(value.used_bytes) : '—';
      const quota = () => {
        const shared = value.shared_quota, source = shared || value;
        const limit = Number(source.limit_bytes), used = Number(source.used_bytes);
        const limited = item.policy && (shared || value.quota_enabled !== false) && Number.isFinite(limit) && limit > 0;
        const available = limited && source.used_bytes != null && Number.isFinite(used) && used >= 0;
        const percent = available ? used / limit * 100 : 0;
        const formatted = percent > 0 && percent < 0.1 ? '不足0.1%' : `${percent.toLocaleString('zh-CN', { maximumFractionDigits: 1, notation: percent >= 10000 ? 'compact' : 'standard' })}%`;
        const label = shared ? '共享配额' : '配额', usage = shared ? '共享已用' : '已用';
        const track = metricTrack(percent, available);
        if (available) {
          track.removeAttribute('aria-hidden');
          track.setAttribute('role', 'meter'); track.setAttribute('aria-label', `${label}用量`);
          track.setAttribute('aria-valuemin', '0'); track.setAttribute('aria-valuemax', '100');
          track.setAttribute('aria-valuenow', String(Math.min(100, percent)));
          track.setAttribute('aria-valuetext', `${usage}${formatted}，${bytes(used)} / ${bytes(limit)}`);
        }
        return stack(node('div', { className: 'a-stat-heading' },
          muted(limited ? `${label} ${bytes(limit)}` : shared ? '共享配额不限量' : item.policy ? '未设置配额' : '等待同步'),
          muted(available ? `${usage}${formatted}` : limited ? '用量待同步' : '')), track);
      };
      const transfer = (label, counter, speed) => stack(muted(label), node('b', {}, item.policy ? bytes(value[counter]) : '—'), muted(item.policy ? `当前 ${bytes(rate(value[speed]))}/s` : '等待采样'));
      const [amount, unit = ''] = cumulative().split('\u00a0');
      return { key: item.key, card: () => node('article', { className: 'a-card a-probe', 'data-traffic-key': item.key },
        node('div', { className: 'a-probe-head' }, node('div', {},
          node('h2', {}, agent?.name || value.agent_id),
          actions(node('b', {}, String(value.port)), muted(`${value.engine} · ${protocolName(value.protocol)}`)),
          !value.is_current_user && muted(value.owner_name || '其他用户'),
          (value.shared_quota || value.share_id) && muted('共享实例')), node('div', {}, state())),
        node('div', { className: 'a-stat' },
          muted('累计流量'), actions(node('strong', {}, amount), muted(unit)),
          quota(),
          node('div', { className: 'a-probe-network' }, transfer('累计接收', 'received_bytes', 'receive_bps'), transfer('累计发送', 'sent_bytes', 'send_bps'))),
        node('div', { className: 'a-probe-footer' }, controls())), cells: [
        identity(),
        stack(node('strong', {}, `${value.port}`), muted(`${value.engine} · ${protocolName(value.protocol)}`)),
        item.policy ? actions(badge(...policyStatus(value, agent))) : badge(policiesReady ? '等待同步' : '正在读取配额…', 'warn'),
        stack(node('strong', {}, cumulative()), quota()),
        item.policy ? stack(bytes(value.received_bytes), muted(`${bytes(rate(value.receive_bps))}/s`)) : '—',
        item.policy ? stack(bytes(value.sent_bytes), muted(`${bytes(rate(value.send_bps))}/s`)) : '—',
        controls(),
      ] };
    }), reset, all.length ? '没有符合筛选条件的端口' : '暂无已部署端口，成功部署配置后显示流量监控');
  }
  const initialRead = readRegion(ctx, "端口流量", () => reload({ initial: true }), () => {}, {view:()=>firstTable});
  const advanced = node('div', { className: 'a-filter-fields a-traffic-filters', hidden: true }, scope, engineFilter, statusFilter);
  const manage = actions();
  manage.hidden = true;
  const addQuota = ctx.can('traffic.manage') && button('添加配额', () => edit(), { tone: 'primary', disabled: true });
  const toolbar = filterbar(node('div', { className: 'a-filter-fields a-traffic-filters' }, search, agentFilter), actions(
    button('更多筛选', event => { advanced.hidden = !advanced.hidden; event.currentTarget.setAttribute('aria-expanded', String(!advanced.hidden)); }, { 'aria-expanded': 'false' }),
    button('管理', event => { manage.hidden = !manage.hidden; event.currentTarget.setAttribute('aria-expanded', String(!manage.hidden)); }, { 'aria-expanded': 'false' }),
    addQuota));
  manage.append(
    ...(ctx.can('traffic.manage') ? [button('同步配置端口', event => {
      const control = event.currentTarget;
      if (control.disabled) return;
      control.disabled = true;
      const box = trafficSync(ctx, { agentName: id => byID.get(id)?.name || id, matches: matchesFilter, onSaved: () => reload({ quiet: true }) });
      box.addEventListener('close', () => { control.disabled = false; }, { once: true });
    })] : []),
    ...(ctx.can('traffic.manage') ? [button('批量操作', () => trafficBatch(ctx, policies.filter(policy => matchesFilter(policy) && trafficPolicyEditable(policy, byID.get(policy.agent_id), ctx.can)), id => byID.get(id)?.name || id, () => reload({ quiet: true })))] : []));
  const reset = [...toolbar.querySelectorAll('button')].find(button => button.textContent === '重置');
  reset?.replaceWith(button('重置', () => {
    const defaults = { search: '', scope: 'current', agent: '', engine: '', status: '' };
    for (const root of controls) { const input = root.querySelector('input,select'); input.value = defaults[input.name]; }
    ctx.state.data.analyticsTrafficFilters = currentFilters(); draw(true);
  }));
  for (const root of controls) root.addEventListener(root === search ? 'input' : 'change', () => { ctx.state.data.analyticsTrafficFilters = currentFilters(); draw(true); });
  ctx.onRefresh(() => reload({ quiet: true }));
  const firstTable = loadingTable(['节点 / 归属', '端口 / 协议', '状态', '累计流量', '累计接收', '累计发送', '操作'], '端口流量');
  const firstRegion = section('端口流量', firstTable);
  results.root.hidden = true;
  const content = stack(toolbar, advanced, manage, initialRead.element, refreshStatus, firstRegion, results.root);
  void initialRead.load().finally(() => { if (ctx.current()) poller.start(); });
  content.classList.add('a-traffic-page');
  return content;
}
