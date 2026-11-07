import { createNodeOrderSync, orderNodesBySavedOrder } from "../modules/node-order.js";
import { accountStorage } from "../modules/account-storage.js";
import { nodeTimeZone } from "./node-time-zone.js";
import { openNodeBatch } from "./node-batch.js";
import { bindNodeOrdering } from "./node-ordering.js";
import { patch, poll } from "./live-dom.js";
import { nodeRegion, nodeRegionFlag } from './node-region.js';
import { nodeMetrics } from './node-metrics.js';
import { metricTrack } from './metric-track.js';
import { nodeDeleteControl } from './node-delete.js';
import { publicAddressRows, manualConnectionAddressNote } from '../modules/agent-address-model.js';
import { geoRegionDetails } from '../modules/region-model.js';
import { coreInstallBlockReason } from '../modules/core-install-state.js';
import { coreSourceForInstall } from '../modules/core-install-source.js';
import { createServiceActionGuard } from '../modules/agent-service-state.js';
import { enrollmentInstallCommand } from '../modules/enrollment-command.js';
import { formatSharedPorts } from '../modules/user-model.js';
import { sharedInstancePresentation } from '../modules/shared-instance-model.js';
import { bindSelectionSurface, copyText, node, button, dragHandle, actions, stack, grid, section, table, field, code, badge, muted, notice, link, pathID, list } from './ui.js';
import { date, bytes, status, actionName, taskDetail } from './operations.js';

const engineIDs = ['mihomo', 'xray', 'sing-box', 'ss-rust'];
const taskTerminal = task => ['succeeded', 'failed', 'canceled'].includes(task.status);
const addressRows = agent => publicAddressRows(agent.metrics || {}, agent.labels || {}, agent.features || []);
export async function nodesPage(ctx) {
  const [receivedAgents, savedView] = await Promise.all([ctx.api('/agents'), ctx.api('/preferences/node-view')]);
  let agents = list(receivedAgents), preference = savedView;
  let liveTable, loadSequence = 0, renderSignature;
  const dialogUpdates = new Set();
  const actionBusy = new Set();
  const uninstallTasks = ctx.state.data.coreUninstallTasks ||= {};
  const coreKey = (agent, engine) => `${agent.id}:${engine}`;
  const refreshState = muted('正在实时更新');
  refreshState.setAttribute('role', 'status');
  const ordering = createNodeOrderSync((path, options = {}) => ctx.api(path, { ...options, body: options.body ? JSON.parse(options.body) : undefined }), accountStorage, ctx.current);
  await ordering.load();
  const canManage = agent => ctx.can('agents.manage', agent) && agent.can_manage !== false;
  const guard = createServiceActionGuard(ctx.can);
  const search = field('搜索节点', 'search', '', { type: 'search', placeholder: '名称、节点 ID 或地址' });
  const results = node('div');
  bindSelectionSurface(results, { selector: 'article[data-agent-id]', active: () => selecting, signal: ctx.signal });
  const dragging = bindNodeOrdering(ctx, results, {
    enabled: () => !selecting,
    order: () => shownAgents().map(agent => agent.id), save: ids => ordering.save(ids), redraw: () => draw(true),
  });
  const selected = new Set();
  let view = preference.view === 'list' ? 'list' : 'cards', selecting = false;
  const batchBar = node('div', {className:'a-probe-batch'});
  const cardToggle = button('卡片',event=>void ctx.mutate(event.currentTarget,()=>setView('cards')));
  const listToggle = button('列表',event=>void ctx.mutate(event.currentTarget,()=>setView('list')));
  const batchToggle = ctx.can('tasks.execute') && button('批量操作', () => { selecting = true; draw(); });
  async function setView(value) {
    if (value === view) return;
    cardToggle.disabled = listToggle.disabled = true;
    try {
      const saved = await ctx.api('/preferences/node-view', { method: 'PUT', body: { view: value, revision: preference.revision } });
      if (!ctx.current()) return;
      preference = saved; view = saved.view; draw();
    } catch (error) {
      if (error.status === 409) {
        const latest = await ctx.api('/preferences/node-view');
        if (!ctx.current()) return;
        preference = latest; view = latest.view; draw();
      }
      throw error;
    } finally {
      if (ctx.current()) cardToggle.disabled = listToggle.disabled = false;
    }
  }
  const shownAgents = () => {
    const query = search.querySelector('input').value.trim().toLowerCase();
    const ordered = orderNodesBySavedOrder(agents, ordering.ids());
    return ordered.filter(agent => [agent.name, agent.id, ...addressRows(agent).map(row => row.value)].some(value => String(value || '').toLowerCase().includes(query)));
  };
  const selectable = agent => ctx.can('tasks.execute') &&
    (agent.can_manage !== false || ctx.can('instance.manage', agent));
  function choice(agent) { return field('选择', `select:${agent.id}`, selected.has(agent.id), {
    type: 'checkbox', 'aria-label': `选择 ${agent.name}`, disabled: !selectable(agent), onchange(event) {
      event.target.checked ? selected.add(agent.id) : selected.delete(agent.id); drawBatch();
    },
  }); }
  function drawBatch() {
    results.querySelectorAll('article[data-agent-id]').forEach(card => {
      const input = card.querySelector('input[type=checkbox]');
      if (input) {
        input.checked = selected.has(card.dataset.agentId);
        card.dataset.selected = String(input.checked);
        input.nextElementSibling.textContent = input.checked ? '已选择' : '选择';
        card.setAttribute('aria-label', `${agents.find(agent => agent.id === card.dataset.agentId)?.name || ''}，${input.checked ? '已选择' : '未选择'}`);
      }
    });
    batchBar.hidden = !selecting;
    if (batchToggle) batchToggle.hidden = selecting;
    if (!selecting) { batchBar.replaceChildren(); return; }
    const eligible = shownAgents().filter(selectable);
    const count = eligible.filter(agent => selected.has(agent.id)).length;
    const all = field('全选', 'select-all', eligible.length > 0 && count === eligible.length, {
      type: 'checkbox', disabled: !eligible.length, onchange(event) {
        eligible.forEach(agent => event.target.checked ? selected.add(agent.id) : selected.delete(agent.id)); draw();
        batchBar.querySelector('[name="select-all"]').focus({ preventScroll: true });
      },
    });
    all.querySelector('input').indeterminate = count > 0 && count < eligible.length;
    batchBar.replaceChildren(all, muted(`已选 ${selected.size} 个节点`), button('执行批量操作', batch, { disabled: !selected.size }),
      button('取消选择', () => { selecting = false; selected.clear(); draw(); }));
  }
  const percentage=(used,total)=>total>0?Math.min(100,Math.max(0,Number(used)/Number(total)*100)):0;
  function meter(label,value,available,detail){
    const percent=Math.min(100,Math.max(0,Number(value)||0));
    return node('div',{className:'a-probe-meter'},node('div',{className:'a-probe-meter-label'},muted(label),node('strong',{},available?`${percent.toFixed(1)}%`:'—')),
      metricTrack(percent, available),detail&&muted(available?detail:'暂无数据'));
  }
  function metrics(agent){const m=agent.metrics||{},online=agent.status==='online';return node('div',{className:'a-probe-meters'},
    meter('CPU',m.cpu_percent,online&&m.cpu_available),
    meter('内存',percentage(m.memory_used_bytes,m.memory_total_bytes),online&&m.memory_available,`${bytes(m.memory_used_bytes)} / ${bytes(m.memory_total_bytes)}`),
    meter('磁盘',percentage(m.disk_used_bytes,m.disk_total_bytes),online&&m.disk_available,`${bytes(m.disk_used_bytes)} / ${bytes(m.disk_total_bytes)}`));}
  function dragControl(agent) { return dragHandle(agent.id, agent.name, selecting); }
  function controls(agent,compact=false){return actions(ctx.can('agent-config.read')&&link(compact?'配置':'实时配置',`#live-config?agent=${pathID(agent.id)}`),
    button(compact?'设置':'查看设置',event=>void ctx.mutate(event.currentTarget,()=>settings(agent)),{title:'查看设置'}),
    dragControl(agent));}
  function network(agent){const m=agent.metrics||{},available=agent.status==='online'&&m.network_available;return node('div',{className:'a-probe-network'},
    stack(muted('接收'),node('strong',{},available?`${bytes(m.network_rx_bps)}/s`:'—'),muted(`累计 ${m.network_available ? bytes(m.network_rx_bytes) : '—'}`)),
    stack(muted('发送'),node('strong',{},available?`${bytes(m.network_tx_bps)}/s`:'—'),muted(`累计 ${m.network_available ? bytes(m.network_tx_bytes) : '—'}`)));}
  function regionFlag(agent) {
    const region = geoRegionDetails(agent.labels?.region_code) || geoRegionDetails(agent.region_code);
    const flag = nodeRegionFlag(ctx, agent);
    if (!canManage(agent)) { flag.title = region?.name || '国家／地区未知'; return flag; }
    const trigger = button('', () => chooseRegion(agent), { className: 'a-region-trigger', 'aria-label': `选择国家／地区：${agent.name}`, 'aria-haspopup': 'dialog', title: `${region?.name || '国家／地区未知'} · 选择国家／地区` });
    trigger.append(flag); return trigger;
  }
  function chooseRegion(agent) {
    if (!canManage(agent)) return;
    return nodeRegion(ctx, agent, async code => {
      agent.labels = { ...(agent.labels || {}) };
      if (code) agent.labels.region_code = code; else { delete agent.labels.region_code; agent.region_code = ''; }
      draw();
      try { await load(); }
      catch (error) { if (ctx.current() && error.name !== 'AbortError') ctx.announce('国家／地区已保存，节点刷新失败，请刷新重试。', 'error'); }
    });
  }
  function addresses(agent) {
    const rows = addressRows(agent).filter(row => row.ok);
    return node('div', { className: 'a-probe-addresses', 'aria-label': '节点 IP 地址', 'data-ip-stack': rows.length === 2 ? 'dual' : rows[0]?.cls || 'none' }, rows.length ? rows.map(row =>
      node('div', { className: 'a-probe-address', 'data-ip-family': row.cls, title: row.source },
        muted(row.label), node('div', { className: 'a-probe-address-value' }, node('span', { dir: 'ltr' }, row.value.split(':').flatMap((part, index, parts) => index < parts.length - 1 ? [part + ':', node('wbr')] : [part])),
          button('', async event => {
            const control = event.currentTarget;
            if (control.disabled) return;
            control.disabled = true;
            try {
              await copyText(row.value);
              if (ctx.current()) ctx.announce(`${row.label} 已复制`);
            } catch {
              if (ctx.current()) ctx.announce('复制失败，请重试。', 'error');
            } finally { if (control.isConnected) control.disabled = false; }
          }, { glyph: 'copy', className: 'a-ip-copy', title: `复制 ${row.label}`, 'aria-label': `复制 ${agent.name} 的 ${row.label}：${row.value}` })))) : muted('暂无公网 IP'),
      manualConnectionAddressNote(agent.labels) && node('div', { className: 'a-probe-address-value' }, node('span', { dir: 'ltr' }, muted(manualConnectionAddressNote(agent.labels)))));
  }

  async function load() {
    const sequence = ++loadSequence;
    let received;
    let uninstallStates;
    try {
      [received, uninstallStates] = await Promise.all([
        ctx.api('/agents'),
        ctx.can('tasks.read') ? Promise.all(Object.entries(uninstallTasks).map(async ([key, id]) => {
          try { return [key, id, await ctx.api(`/tasks/${pathID(id)}`)]; }
          catch (error) { if (error.name === 'AbortError') throw error; return [key, id, null]; }
        })) : [],
      ]);
      received = list(received);
    }
    catch (error) {
      if (error.status === 403 && ctx.current() && sequence === loadSequence) {
        agents = []; selected.clear(); draw(true);
        dialogUpdates.forEach(update => update());
      }
      throw error;
    }
    if (!ctx.current() || sequence !== loadSequence) return;
    for (const [key, id, task] of uninstallStates) if (task && taskTerminal(task) && uninstallTasks[key] === id) delete uninstallTasks[key];
    const existing = new Map(agents.map(agent => [agent.id, agent]));
    agents = received.map(agent => { const current = existing.get(agent.id); if (!current) return agent; for (const name of Object.keys(current)) if (!(name in agent)) delete current[name]; return Object.assign(current, agent); });
    if (refreshState.textContent !== '正在实时更新') refreshState.textContent = '正在实时更新';
    for (const id of selected) if (!agents.some(agent => agent.id === id)) selected.delete(id);
    draw(true);
    dialogUpdates.forEach(update => update());
  }
  async function submit(agent, engine, action, extra = {}) {
    const task = await ctx.api('/tasks', { method: 'POST', body: { agent_id: agent.id, engine, action, ...extra } });
    if (!ctx.current()) return;
    ctx.announce('任务已提交');
    if (action === 'uninstall' && ctx.can('tasks.read') && !taskTerminal(task)) {
      uninstallTasks[coreKey(agent, engine)] = task.id;
      dialogUpdates.forEach(update => update());
    }
    if (ctx.can('tasks.read')) await taskDetail(ctx, task.id);
    return task;
  }
  function draw(live = false) {
    if (dragging.active()) return;
    const shown = shownAgents();
    for (const id of selected) if (!shown.some(agent => agent.id === id && selectable(agent))) selected.delete(id);
    cardToggle.setAttribute('aria-pressed',String(view==='cards'));listToggle.setAttribute('aria-pressed',String(view==='list'));
    if (!live) drawBatch();
    // Only retain the current projection, within this page/account lifetime.
    // Include permissions, selection and formatted time (including relative
    // age), so unchanged payloads never suppress a visible state transition.
    const signature = JSON.stringify([view, selecting, Boolean(agents.length), [...selected], shown,
      ctx.can('agents.read'), ctx.can('agent-config.read'), shown.map(agent => [canManage(agent), selectable(agent), date(agent.last_seen)])]);
    // Legacy responses without a resolved region still need their lookup's
    // normal retry/update path. Current API lists always include region_code.
    const regionsResolved = shown.every(agent => Object.hasOwn(agent, 'region_code') || geoRegionDetails(agent.labels?.region_code) || !ctx.can('agents.read'));
    if (regionsResolved && renderSignature === signature) return;
    if(!shown.length){results.replaceChildren(node('p',{className:'a-empty'},agents.length?'没有符合条件的节点':'暂无节点，添加 QAgent 后显示资源状态'));renderSignature = signature;return;}
    if(view==='cards') { liveTable = null; const fresh = node('div',{className:'a-probe-grid'},shown.map(agent=>
      node('article',{className:'a-card a-probe','data-agent-id':agent.id, 'data-selected': selecting ? String(selected.has(agent.id)) : null, tabindex: selecting && selectable(agent) ? 0 : null, role: selecting ? 'group' : null, 'aria-label': selecting ? `${agent.name}，${selected.has(agent.id) ? '已选择' : '未选择'}` : null},
        node('header',{className:'a-probe-head'},node('div',{},node('div',{className:'a-probe-name'},regionFlag(agent),node('h2',{},agent.name)),muted(`${agent.os} / ${agent.arch}`)),
          selecting ? node('div',{className:'a-probe-choice'},status(agent.status),choice(agent)) : status(agent.status)),
        node('div',{className:'a-probe-body'},addresses(agent),metrics(agent),network(agent)),
        node('div',{className:'a-probe-meta'},actions((agent.capabilities||[]).map(engine=>badge(engine)),badge(agent.can_manage===false?'本人共享实例':'主实例')),muted(`最近在线 · ${date(agent.last_seen)}`)),
        !selecting&&node('footer',{className:'a-probe-footer'},controls(agent)))));
      if (results.firstElementChild?.matches('.a-probe-grid')) patch(results.firstElementChild, fresh); else results.replaceChildren(fresh);
    }
    else{
      const compactMeter = (value, available, detail) => {
        const percent = Math.min(100,Math.max(0,Number(value)||0));
        return node('div',{className:'a-probe-cell',title:available?detail:'暂无数据'},
          node('strong',{},available?`${percent.toFixed(1)}%`:'—'),
          metricTrack(percent, available),
          detail&&muted(available?detail:'暂无数据'));
      };
      const columns = [...(selecting?['选择']:[]),'节点','状态','CPU','内存','磁盘','网络速率','操作'];
      const rows = shown.map(agent=>{
        const m=agent.metrics||{},online=agent.status==='online';
        return [...(selecting?[choice(agent)]:[]),
          node('div',{className:'a-probe-identity', 'data-agent-id':agent.id},node('div',{className:'a-probe-name'},regionFlag(agent),node('strong',{},agent.name)),addresses(agent),muted(`${agent.os} / ${agent.arch} · ${(agent.capabilities||[]).join(' / ')||'无内核'}`),
            muted(agent.can_manage===false?'本人共享实例':'主实例')),
          node('div',{className:'a-probe-state',title:`最近在线 · ${date(agent.last_seen)}`},status(agent.status)),
          compactMeter(m.cpu_percent,online&&m.cpu_available),
          compactMeter(percentage(m.memory_used_bytes,m.memory_total_bytes),online&&m.memory_available,`${bytes(m.memory_used_bytes)} / ${bytes(m.memory_total_bytes)}`),
          compactMeter(percentage(m.disk_used_bytes,m.disk_total_bytes),online&&m.disk_available,`${bytes(m.disk_used_bytes)} / ${bytes(m.disk_total_bytes)}`),
          node('div',{className:'a-probe-rates'},node('div',{},muted('接收'),node('span',{},online&&m.network_available?`${bytes(m.network_rx_bps)}/s`:'—')),
            node('div',{},muted('发送'),node('span',{},online&&m.network_available?`${bytes(m.network_tx_bps)}/s`:'—')),
            muted(`累计接收 ${m.network_available ? bytes(m.network_rx_bytes) : '—'}`), muted(`累计发送 ${m.network_available ? bytes(m.network_tx_bytes) : '—'}`)),
          node('div',{className:'a-probe-row-actions'},controls(agent,true))];
      });
      if (live && liveTable) liveTable.updateRows(rows);
      else {
        const widths = { '选择': 64, '节点': 360, '状态': 90, 'CPU': 110, '内存': 180, '磁盘': 180, '网络速率': 180, '操作': 190 };
        liveTable = table(columns, rows, {label:'节点监控', columnWidths: columns.map(column => widths[column]), rowKey: row => row.find(cell => cell?.dataset?.agentId)?.dataset.agentId});
        results.replaceChildren(node('div',{className:'a-card a-probe-list'},liveTable));
      }
    }
    results.querySelectorAll('input[type=checkbox]').forEach(input => {
      if (input.name.startsWith('select:')) {
        input.checked = selected.has(input.name.slice(7));
        input.nextElementSibling.textContent = input.checked ? '已选择' : '选择';
      }
    });
    renderSignature = signature;
  }

  function command(created) {
    const value = enrollmentInstallCommand(created);
    ctx.dialog('QAgent 安装命令', stack(code(value), button('复制命令', event => void ctx.mutate(event.currentTarget, async () => {
      await copyText(value); ctx.announce('安装命令已复制');
    }))));
  }
  async function enrollment() {
    const records = node('div', { className: 'a-traffic-list' }, muted('正在读取添加记录…'));
    const historyTitle = node('summary', {}, '添加记录');
    const history = node('details', { className: 'a-disclosure' }, historyTitle, node('div', { className: 'a-disclosure-body' }, records));
    let loaded = false, changed = false;
    async function reload() {
      const tokens = list(await ctx.api('/enrollment-tokens'));
      if (!ctx.current() || !box.isConnected) return;
      historyTitle.textContent = `添加记录 · ${tokens.length}`;
      if (!loaded) { history.open = tokens.length > 0; loaded = true; }
      records.replaceChildren(table(['节点', '添加时间', '状态', '操作'], tokens.map(token => [token.name, date(token.created_at), badge(token.revoked_at ? '已撤销' : token.used_count ? '已接入' : '等待接入', token.revoked_at ? '' : token.used_count ? 'ok' : 'warn'), actions(
        token.command_available && button('安装命令', event => void ctx.mutate(event.currentTarget, async () => {
          const created = await ctx.api(`/enrollment-tokens/${pathID(token.id)}/command`, { method: 'POST' });
          if (!ctx.current() || !box.open) return;
          changed = false; ctx.setDirty(false); box.close(); command(created);
        })),
        button('删除记录', async event => {
          const control = event.currentTarget;
          if (!await ctx.confirm('删除此添加记录？已接入的节点不会被卸载。', '删除记录') || !ctx.current()) return;
          await ctx.mutate(control, async () => { await ctx.api(`/enrollment-tokens/${pathID(token.id)}`, { method: 'DELETE' }); await reload(); });
        }, { tone: 'danger' }),
      )]), { label: '节点添加记录' }));
    }
    const form = ctx.form([
      field('节点名称', 'name', '', { required: true, maxlength: 100, autofocus: true }),
      field('对管理员隐藏此节点', 'admin_hidden', false, { type: 'checkbox' }),
    ], '生成安装命令', async (values, form) => {
      const created = await ctx.api('/enrollment-tokens', { method: 'POST', body: { name: values.name.trim(), admin_hidden: form.elements.admin_hidden.checked } });
      if (!ctx.current()) return;
      changed = false; ctx.setDirty(false); box.close(); command(created);
    });
    for (const type of ['input', 'change']) form.addEventListener(type, () => { changed = Boolean(form.elements.name.value || form.elements.admin_hidden.checked); ctx.setDirty(changed); });
    const box = ctx.dialog('添加节点', stack(form, history));
    box.onRequestClose = async () => {
      if (changed && !await ctx.confirm('放弃未保存的节点添加信息？', '放弃更改')) return;
      ctx.setDirty(false); box.close();
    };
    box.addEventListener('close', () => ctx.setDirty(false), { once: true });
    void reload().catch(error => {
      if (ctx.current() && box.open && error.name !== 'AbortError') records.replaceChildren(notice(error.message));
    });
  }
  async function directory() {
    const entries = list(await ctx.api('/agent-directory'));
    if (!ctx.current()) return;
    const content = node('div');
    const box = ctx.dialog('节点目录', content, { wide: true });
    function drawDirectory() {
      content.replaceChildren(table(['节点', '所有者', '状态', '内核', '操作'], entries.map(entry => [
        stack(entry.name, muted(entry.id)), entry.owner_username || '未分配账号', status(entry.status), (entry.capabilities || []).join(' · '),
        nodeDeleteControl(ctx, entry, { available: () => box.open && ctx.can('users.manage'), onDeleted() {
          entries.splice(entries.indexOf(entry), 1); agents = agents.filter(agent => agent.id !== entry.id); drawDirectory(); draw(true);
        }, refresh: load }),
      ]), { label: '节点目录' }));
    }
    drawDirectory(); return box;
  }
  async function settings(agent) {
    const base = `/agents/${pathID(agent.id)}`;
    const manage = canManage(agent);
    const settingsScope = () => JSON.stringify([agent.can_manage !== false, agent.owner_id || '', agent.shared_allocation?.owner_id || '',
      engineIDs.map(engine => agent.runtime?.[engine]?.instance?.id || '')]);
    const openedScope = settingsScope();
    const renderFacts = () => section('节点信息', node('div', { className: 'a-card-body' }, stack(
      actions(status(agent.status), badge(agent.can_manage === false ? '本人共享实例' : '主实例')),
      muted(agent.id), muted(`${agent.os} / ${agent.arch} · QAgent ${agent.version || '—'}`),
      addresses(agent),
      muted(`最近在线 · ${date(agent.last_seen)}`),
      manage && button('国家／地区', () => chooseRegion(agent)),
      agent.metrics && grid(
        muted(`CPU · ${agent.metrics.cpu_available ? Number(agent.metrics.cpu_percent).toFixed(1)+'%' : '—'}`),
        muted(`内存 · ${agent.metrics.memory_available ? bytes(agent.metrics.memory_used_bytes)+' / '+bytes(agent.metrics.memory_total_bytes) : '—'}`),
        muted(`磁盘 · ${agent.metrics.disk_available ? bytes(agent.metrics.disk_used_bytes)+' / '+bytes(agent.metrics.disk_total_bytes) : '—'}`),
        stack(muted(`网络 · ${agent.metrics.network_available ? '接收 '+bytes(agent.metrics.network_rx_bps)+'/s · 发送 '+bytes(agent.metrics.network_tx_bps)+'/s' : '—'}`), muted(`累计 · ${agent.metrics.network_available ? '接收 '+bytes(agent.metrics.network_rx_bytes)+' · 发送 '+bytes(agent.metrics.network_tx_bytes) : '—'}`))),
      agent.shared_allocation && stack(muted(`所有者 · ${agent.shared_allocation.owner_username || '—'}`),
        muted(`端口 · ${formatSharedPorts(agent.shared_allocation.ports) || '未分配'}`),
        muted(`流量 · ${bytes(agent.shared_allocation.used_bytes)} / ${agent.shared_allocation.limit_bytes ? bytes(agent.shared_allocation.limit_bytes) : '不限量'}`)),
    )));
    const facts = renderFacts();
    const sections = [facts], settingsUpdates = [];
    if(ctx.can('metrics.read'))sections.push(section('资源指标',node('div',{className:'a-card-body'},button('查看指标历史',event=>void ctx.mutate(event.currentTarget,()=>nodeMetrics(ctx,agent))))));
    if (manage) {
      sections.push(section('Agent 设置', node('div', { className: 'a-card-body' }, stack(
        ctx.form([field('节点名称', 'name', agent.name, { required: true, maxlength: 100 })], '保存名称', async values => {
          const name = values.name.trim();
          if (!name || [...name].length > 100 || /[\u0000-\u001f\u007f-\u009f]/u.test(name)) throw new Error('节点名称须为 1–100 个字符，且不能包含控制字符');
          if (!box.open || settingsScope() !== openedScope || !canManage(agent)) return;
          const saved = await ctx.api(`${base}/name`, { method: 'PUT', body: { name } });
          if (!ctx.current() || !box.open || settingsScope() !== openedScope || !canManage(agent)) return;
          agent.name = saved.name; box.querySelector('.a-dialog-head h2').textContent = `${agent.name} · 节点设置`; await load(); ctx.announce('名称已保存');
        }),
        agent.can_hide && ctx.form([field('对管理员隐藏', 'admin_hidden', agent.admin_hidden, { type: 'checkbox' })], '保存可见性', async (_, form) => {
          const saved = await ctx.api(`${base}/visibility`, { method: 'PUT', body: { admin_hidden: form.elements.admin_hidden.checked } });
          agent.admin_hidden = saved.admin_hidden; ctx.announce('可见性已保存'); await load();
        }),

      ))));
      sections.push(section('内核能力', node('div', { className: 'a-card-body a-stack' }, (agent.supported_capabilities || engineIDs).map(engine => {
        const transitionState = notice('');
        const form = ctx.form([field(engine, 'enabled', agent.capabilities.includes(engine), { type: 'checkbox' }), transitionState], '保存能力', async (_, element) => {
          if (!canManage(agent) || ['pending', 'running'].includes(agent.capability_transitions?.[engine]?.status)) return;
          const received = await ctx.api(`${base}/capabilities/${engine}`, { method: 'PUT', body: { enabled: element.elements.enabled.checked } });
          ctx.announce(received.task_id ? '启停任务已提交，成功后更新能力' : '内核能力已更新'); await load();
          if (received.task_id && ctx.can('tasks.read')) await taskDetail(ctx, received.task_id);
        });
        let savedEnabled = agent.capabilities.includes(engine);
        const update = () => {
          const input = form.elements.enabled, latest = agent.capabilities.includes(engine);
          const transition = agent.capability_transitions?.[engine];
          const waiting = ['pending', 'running'].includes(transition?.status);
          if (waiting || form.getAttribute('aria-busy') !== 'true' && (input.checked === savedEnabled || input.checked === latest)) {
            input.checked = latest; savedEnabled = latest;
          }
          input.disabled = !canManage(agent) || waiting;
          form.querySelector('[type=submit]').disabled = !canManage(agent) || waiting || form.getAttribute('aria-busy') === 'true';
          transitionState.textContent = waiting ? `${transition.enabled ? '启动' : '停止'}${transition.status === 'pending' ? '排队中' : '执行中'}`
            : ['failed', 'canceled'].includes(transition?.status) ? `上次启停失败或取消${transition.error ? `：${transition.error}` : ''}` : '';
        };
        settingsUpdates.push(update);
        form.querySelector('[type=submit]').addEventListener('mutationend', update);
        form.elements.enabled.dataset.engineCapability = engine;
        update();
        return form;
      }))));
    }
    if (agent.can_manage !== false) {
      const zone = nodeTimeZone(ctx, agent, () => canManage(agent));
      sections.push(zone.view); settingsUpdates.push(zone.update);
    }
    const runtimes = node('div', { className: 'a-stack' });
    sections.push(runtimes);
    const renderRuntimes = () => {
      if (runtimes.querySelector('[aria-busy=true]')) return;
      const fresh = node('div', { className: 'a-stack' });
      const engines = [...new Set([...(agent.capabilities || []), ...(agent.can_manage !== false ? Object.keys(agent.runtime || {}).filter(engine => agent.runtime[engine]?.installed) : [])])];
      for (const engine of engines) {
        const renderService = () => {
          const runtime = agent.runtime?.[engine] || {};
          const instance = sharedInstancePresentation(agent, engine);
          const enabled = agent.capabilities?.includes(engine);
          const busy = actionBusy.has(coreKey(agent, engine));
          const uninstallReason = () => !agents.includes(agent) || agent.can_manage === false || !ctx.can('host.manage', agent) || !ctx.can('tasks.execute') ||
            !agent.features?.includes('core-uninstall-v1') || agent.status !== 'online' || !agent.runtime?.[engine]?.installed ||
            Boolean(agent.runtime?.[engine]?.existing_config_unsupported_reason) || Boolean(uninstallTasks[coreKey(agent, engine)]);
          const reason = coreInstallBlockReason(agent, engine, ctx.can);
          const controls = actions(['status', 'start', 'stop', 'restart'].map(action => button(actionName(action), async event => {
            const control = event.currentTarget;
            const key = coreKey(agent, engine);
            if (actionBusy.has(key)) return;
            actionBusy.add(key); control.disabled = true;
            try {
              if (action !== 'status' && !await ctx.confirm(`对 ${agent.name} 的 ${engine} 执行“${actionName(action)}”？`, actionName(action)) || !ctx.current()) return;
              await ctx.mutate(null, async () => {
                await load();
                const latest = agent.runtime?.[engine] || {};
                if (!ctx.current() || !box.open || settingsScope() !== openedScope || !agents.includes(agent) || !agent.capabilities?.includes(engine) || guard(action, agent.status === 'online', latest.installed, latest.service_status, agent, engine)) return;
                await submit(agent, engine, action);
              });
            } finally { actionBusy.delete(key); if (ctx.current()) dialogUpdates.forEach(update => update()); }
          }, { disabled: !enabled || busy || guard(action, agent.status === 'online', runtime.installed, runtime.service_status, agent, engine) })));
          const install = button(runtime.installed ? '切换版本' : agent.can_manage === false ? '准备内核' : '安装内核', () => installCore(agent, engine), { disabled: Boolean(reason), title: reason || '安装或切换内核版本' });
          const uninstall = runtime.installed && button('卸载内核', async event => {
            const control = event.currentTarget;
            const key = coreKey(agent, engine);
            if (actionBusy.has(key) || uninstallReason()) return;
            actionBusy.add(key); control.disabled = true;
            try {
              if (!await ctx.confirm(`卸载 ${engine}？目标服务会停止，内核文件将删除，已保存配置保留。`, '卸载内核') || !ctx.current()) return;
              await ctx.mutate(null, async () => { await load(); if (ctx.current() && !uninstallReason()) await submit(agent, engine, 'uninstall'); });
            } finally { actionBusy.delete(key); if (ctx.current()) dialogUpdates.forEach(update => update()); }
          }, { tone: 'danger', disabled: busy || uninstallReason() });
          return section(engine, node('div', { className: 'a-card-body' }, stack(
            actions(instance ? badge(instance.label, instance.tone) : status(runtime.service_status === 'running' ? 'active' : runtime.service_status), !enabled && badge('能力已关闭', 'warn'), muted(runtime.version || (runtime.installed === true ? '版本未上报' : runtime.installed === false ? '尚未安装' : '安装状态未知'))),
            instance && stack(muted(instance.version), instance.reason && notice(instance.reason)),
            runtime.existing_config_unsupported_reason && notice(runtime.existing_config_unsupported_reason),
            controls, actions(install, agent.can_manage !== false && uninstall, enabled && ctx.can('agent-config.read') && link('编辑配置', `#live-config?agent=${pathID(agent.id)}&engine=${engine}`),
              runtime.existing_config_available && ctx.can('host.manage', agent) && link('导入系统配置', `#live-config?agent=${pathID(agent.id)}&engine=${engine}&source=import`)),
          )));
        };
        const view = renderService();
        view.dataset.refreshKey = engine; view.dataset.coreInstalled = typeof agent.runtime?.[engine]?.installed === 'boolean' ? agent.runtime[engine].installed ? '1' : '0' : 'unknown'; view.dataset.capabilityEnabled = agent.capabilities?.includes(engine) ? '1' : '0'; fresh.append(view);
      }
      patch(runtimes, fresh);
    };
    renderRuntimes();
    const box = ctx.dialog(`${agent.name} · 节点设置`, stack(sections, actions(
      ctx.can('host.manage',agent) && button('升级 Agent',async event=>{
        const control=event.currentTarget;
        if(!await ctx.confirm('升级此节点的 QAgent？替换后自动重连，期间节点会短暂离线。','升级 Agent')||!ctx.current())return;
        await ctx.mutate(control,()=>submit(agent,undefined,'upgrade-agent'));
      },{disabled:agent.status!=='online'||!agent.features?.includes('agent-self-upgrade-v1'),title:agent.features?.includes('agent-self-upgrade-v1')?'升级 QAgent':'当前 Agent 需重新执行安装命令'}),
      manage && agent.enrollment_command_available && button('查看安装命令', event => void ctx.mutate(event.currentTarget, async () => command(await ctx.api(`${base}/enrollment-command`, { method: 'POST' })))),
      manage && nodeDeleteControl(ctx, agent, { available: () => box.open && settingsScope() === openedScope && canManage(agent), onDeleted() {
        agents = agents.filter(value => value.id !== agent.id); box.close(); draw(true);
      }, refresh: load }),
    )), { wide: true });
    const update = () => {
      if (!box.open || !ctx.current()) { dialogUpdates.delete(update); return; }
      if (!agents.some(value => value.id === agent.id) || settingsScope() !== openedScope || manage && !canManage(agent)) { box.close(); dialogUpdates.delete(update); return; }
      patch(facts, renderFacts());
      settingsUpdates.forEach(refresh => refresh());
      renderRuntimes();
    };
    dialogUpdates.add(update);
    box.addEventListener('close', () => dialogUpdates.delete(update), { once: true });
    ctx.signal.addEventListener('abort', () => { dialogUpdates.delete(update); box.close(); }, { once: true });
  }
  function installCore(agent, engine) {
    const shared = agent.can_manage === false;
    const scope = target => JSON.stringify([target.can_manage !== false, target.owner_id || '', target.shared_allocation?.owner_id || '', target.runtime?.[engine]?.instance?.id || '']);
    const openedScope = scope(agent);
    const custom = field('指定版本', 'custom_version', '', { placeholder: '软件发行版本' });
    const source = field('开发版来源', 'core_source', 'official', { options: [['official', 'MetaCubeX 官方'], ['mirror', 'vernesong 镜像（第三方）']], hint: '使用第三方镜像需先升级 Agent。' });
    const feedback = notice('');
    const targetForSubmit = async values => {
      const received = list(await ctx.api('/agents'));
      if (!ctx.current() || !box.open) return;
      const latest = received.find(item => item.id === agent.id);
      const reason = !latest || scope(latest) !== openedScope ? '节点授权或实例已变化，请重新打开安装窗口。' : coreInstallBlockReason(latest, engine, ctx.can);
      if (reason) throw new Error(reason);
      if (shared && values.release_channel !== 'stable') throw new Error('共享实例仅可准备稳定版内核。');
      if (coreSourceForInstall(engine, values.release_channel, values.core_source) === 'mirror' && !latest.features?.includes('mihomo-development-source-v1')) throw new Error('使用第三方镜像需先升级 Agent。');
      return latest;
    };
    const form = ctx.form([
      field('发行通道', 'release_channel', 'stable', { options: shared ? [['stable', '稳定版']] : [['stable', '稳定版'], ...(engine === 'mihomo' ? [['development', '开发版']] : []), ['custom', '指定版本']] }), custom, source, feedback,
    ], shared ? '准备内核' : '提交安装', async values => {
      if (!await targetForSubmit(values)) return;
      if (values.release_channel === 'custom' && !values.custom_version.trim()) throw new Error('请输入软件发行版本');
      if (!await ctx.confirm(shared ? '准备缺少的稳定版内核？已有版本会复用。' : '安装完成后将重启对应内核服务，确定继续？', '提交安装') || !ctx.current()) return;
      if (!await targetForSubmit(values)) return;
      const task = await ctx.api('/tasks', { method: 'POST', body: { agent_id: agent.id, engine, action: 'install', core_version: values.release_channel === 'custom' ? values.custom_version.trim() : values.release_channel, core_source: coreSourceForInstall(engine, values.release_channel, values.core_source) } });
      feedback.textContent = ''; ctx.announce('安装任务已提交');
      if (ctx.can('tasks.read')) { await taskDetail(ctx, task.id); if (taskTerminal(task)) await load(); }
    });
    const sync = () => {
      ctx.visible(custom, form.elements.release_channel.value === 'custom'); form.elements.custom_version.required = form.elements.release_channel.value === 'custom';
      ctx.visible(source, engine === 'mihomo' && form.elements.release_channel.value === 'development');
      const canMirror = agent.features?.includes('mihomo-development-source-v1');
      form.elements.core_source.querySelector('[value=mirror]').disabled = !canMirror;
      ctx.visible(source.querySelector('.a-field-hint'), !canMirror);
    };
    form.elements.release_channel.addEventListener('change', sync); sync();
    const box = ctx.dialog(`${agent.name} · ${engine}`, form);
    const update = () => {
      if (!ctx.current() || !agents.includes(agent) || scope(agent) !== openedScope) box.close();
      else sync();
    };
    dialogUpdates.add(update);
    box.addEventListener('close', () => dialogUpdates.delete(update), { once: true });
  }
  function batch() { openNodeBatch(ctx, () => agents, selected); }

  const failedRefresh = error => { refreshState.textContent = error?.status === 403 ? '节点访问权限已变化' : '更新失败，保留上次数据'; };
  poll({ ...ctx, onRefreshError: failedRefresh }, load);
  ctx.onRefresh(async () => { try { await load(); } catch (error) { if (ctx.current()) failedRefresh(error); throw error; } });
  draw(); search.querySelector('input').addEventListener('input', draw);
  const deepID = location.hash.match(/^#(?:settings-node-|node-(?!settings(?:\?|$)))([^?]+)/)?.[1]
    || new URLSearchParams(location.hash.split('?')[1] || '').get('agent');
  if (deepID) queueMicrotask(() => {
    if (!ctx.current()) return;
    const agent = agents.find(agent => agent.id === deepID);
    if (agent) void settings(agent);
    else ctx.dialog('节点不可用', notice('节点不存在或当前账号没有访问权限。'));
  });
  return stack(node('div', { className: 'a-probe-toolbar' }, search, actions(
    cardToggle, listToggle,
    ctx.can('enrollment.manage') && button('添加节点', event => void ctx.mutate(event.currentTarget, enrollment), { tone: 'primary' }),
    ctx.can('users.manage') && button('节点目录', event => void ctx.mutate(event.currentTarget, directory)),
    batchToggle,
  )), refreshState, batchBar, results);
}
