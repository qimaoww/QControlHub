import { managedCoreBlockReason } from "../modules/managed-core-state.js";
import { createNodeOrderSync, orderNodesBySavedOrder } from "../modules/node-order.js";
import { accountStorage } from "../modules/account-storage.js";
import { nodeTimeZone } from "./node-time-zone.js";
import { nodeAddress } from './node-address.js';
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
import { copyText, node, button, dragHandle, actions, stack, grid, section, table, field, code, badge, muted, notice, link, pathID, list } from './ui.js';
import { date, bytes, status, actionName, taskDetail } from './operations.js';

const engineIDs = ['mihomo', 'xray', 'sing-box', 'ss-rust'];
const taskTerminal = task => ['succeeded', 'failed', 'canceled'].includes(task.status);
const addressRows = agent => publicAddressRows(agent.metrics || {}, agent.labels || {}, agent.features || []);
export async function nodesPage(ctx) {
  const ordering = createNodeOrderSync((path, options = {}) => ctx.api(path, { ...options, body: options.body ? JSON.parse(options.body) : undefined }), accountStorage, ctx.current);
  const [receivedAgents, savedView] = await Promise.all([ctx.api('/agents'), ctx.api('/preferences/node-view'), ordering.load()]);
  let agents = list(receivedAgents), preference = savedView;
  let liveTable, loadSequence = 0, renderSignature;
  const dialogUpdates = new Set();
  const actionBusy = new Set();
  const uninstallTasks = ctx.state.data.coreUninstallTasks ||= {};
  const coreKey = (agent, engine) => `${agent.id}:${engine}`;
  const refreshState = muted('正在实时更新');
  refreshState.setAttribute('role', 'status');

  const canManage = agent => ctx.can('agents.manage', agent) && agent.can_manage !== false;
  const guard = createServiceActionGuard(ctx.can);
  const search = field('搜索节点', 'search', '', { type: 'search', placeholder: '名称、节点 ID 或地址' });
  const results = node('div');
  const dragging = bindNodeOrdering(ctx, results, {
    enabled: () => true,
    order: () => shownAgents().map(agent => agent.id), save: ids => ordering.save(ids), redraw: () => draw(true),
  });
  let view = preference.view === 'list' ? 'list' : 'cards';
  let batchBox;
  const cardToggle = button('卡片',event=>void ctx.mutate(event.currentTarget,()=>setView('cards')));
  const listToggle = button('列表',event=>void ctx.mutate(event.currentTarget,()=>setView('list')));
  const batchToggle = ctx.can('tasks.execute') && button('批量操作', batch);
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
  function dragControl(agent) { return dragHandle(agent.id, agent.name); }
  function controls(agent,compact=false){return actions(ctx.can('agent-config.read')&&link(compact?'配置':'实时配置',`#live-config?agent=${pathID(agent.id)}`),
    button(compact?'设置':'查看设置',event=>void ctx.mutate(event.currentTarget,()=>settings(agent)),{title:'查看设置'}),
    dragControl(agent));}
  function traffic(agent, key, rate = false) {
    const metrics = agent.metrics || {}, value = metrics[key];
    return metrics.network_available && (!rate || agent.status === 'online') && value != null && Number.isFinite(Number(value))
      ? `${bytes(value)}${rate ? '/s' : ''}` : '—';
  }
  function network(agent){return node('div',{className:'a-probe-network'},
    stack(muted('接收'),node('strong',{},traffic(agent,'network_rx_bps',true)),muted(`累计 ${traffic(agent,'network_rx_bytes')}`)),
    stack(muted('发送'),node('strong',{},traffic(agent,'network_tx_bps',true)),muted(`累计 ${traffic(agent,'network_tx_bytes')}`)));}
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
      catch (error) { if (ctx.current() && error.name !== 'AbortError') ctx.announce(`国家／地区已保存，节点刷新失败。\n${error.message}`, 'error'); }
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
        agents = []; draw(true);
        dialogUpdates.forEach(update => update());
      }
      throw error;
    }
    if (!ctx.current() || sequence !== loadSequence) return;
    for (const [key, id, task] of uninstallStates) if (task && taskTerminal(task) && uninstallTasks[key] === id) delete uninstallTasks[key];
    const existing = new Map(agents.map(agent => [agent.id, agent]));
    agents = received.map(agent => { const current = existing.get(agent.id); if (!current) return agent; for (const name of Object.keys(current)) if (!(name in agent)) delete current[name]; return Object.assign(current, agent); });
    if (refreshState.textContent !== '正在实时更新') refreshState.textContent = '正在实时更新';
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
    cardToggle.setAttribute('aria-pressed',String(view==='cards'));listToggle.setAttribute('aria-pressed',String(view==='list'));
    // Only retain the current projection, within this page/account lifetime.
    // Include permissions and formatted time (including relative
    // age), so unchanged payloads never suppress a visible state transition.
    const signature = JSON.stringify([view, Boolean(agents.length), shown,
      ctx.can('agents.read'), ctx.can('agent-config.read'), shown.map(agent => [canManage(agent), date(agent.last_seen)])]);
    // Legacy responses without a resolved region still need their lookup's
    // normal retry/update path. Current API lists always include region_code.
    const regionsResolved = shown.every(agent => Object.hasOwn(agent, 'region_code') || geoRegionDetails(agent.labels?.region_code) || !ctx.can('agents.read'));
    if (regionsResolved && renderSignature === signature) return;
    if(!shown.length){results.replaceChildren(node('p',{className:'a-empty'},agents.length?'没有符合条件的节点':'暂无节点，添加 QAgent 后显示资源状态'));renderSignature = signature;return;}
    if(view==='cards') { liveTable = null; const fresh = node('div',{className:'a-probe-grid'},shown.map(agent=>
      node('article',{className:'a-card a-probe','data-agent-id':agent.id},
        node('header',{className:'a-probe-head'},node('div',{},node('div',{className:'a-probe-name'},regionFlag(agent),node('h2',{},agent.name)),muted(`${agent.os} / ${agent.arch}`)),
          status(agent.status)),
        node('div',{className:'a-probe-body'},addresses(agent),metrics(agent),network(agent)),
        node('div',{className:'a-probe-meta'},actions((agent.capabilities||[]).map(engine=>badge(engine)),badge(agent.can_manage===false?'本人共享实例':'主实例')),muted(`最近在线 · ${date(agent.last_seen)}`)),
        node('footer',{className:'a-probe-footer'},controls(agent)))));
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
      const columns = ['节点','状态','CPU','内存','磁盘','网络收发','操作'];
      const rows = shown.map(agent=>{
        const m=agent.metrics||{},online=agent.status==='online';
        return [
          node('div',{className:'a-probe-identity', 'data-agent-id':agent.id},node('div',{className:'a-probe-name'},regionFlag(agent),node('strong',{},agent.name)),addresses(agent),muted(`${agent.os} / ${agent.arch} · ${(agent.capabilities||[]).join(' / ')||'无内核'}`),
            muted(agent.can_manage===false?'本人共享实例':'主实例')),
          node('div',{className:'a-probe-state',title:`最近在线 · ${date(agent.last_seen)}`},status(agent.status)),
          compactMeter(m.cpu_percent,online&&m.cpu_available),
          compactMeter(percentage(m.memory_used_bytes,m.memory_total_bytes),online&&m.memory_available,`${bytes(m.memory_used_bytes)} / ${bytes(m.memory_total_bytes)}`),
          compactMeter(percentage(m.disk_used_bytes,m.disk_total_bytes),online&&m.disk_available,`${bytes(m.disk_used_bytes)} / ${bytes(m.disk_total_bytes)}`),
          node('div',{className:'a-probe-rates'},
            node('div',{},muted('实时接收'),node('span',{},traffic(agent,'network_rx_bps',true))),
            node('div',{},muted('实时发送'),node('span',{},traffic(agent,'network_tx_bps',true))),
            node('div',{},muted('累计接收'),muted(traffic(agent,'network_rx_bytes'))),
            node('div',{},muted('累计发送'),muted(traffic(agent,'network_tx_bytes')))),
          node('div',{className:'a-probe-row-actions'},controls(agent,true))];
      });
      if (live && liveTable) liveTable.updateRows(rows);
      else {
        const widths = { '节点': 360, '状态': 90, 'CPU': 110, '内存': 180, '磁盘': 180, '网络收发': 230, '操作': 190 };
        liveTable = table(columns, rows, {label:'节点监控', columnWidths: columns.map(column => widths[column]), rowKey: row => row.find(cell => cell?.dataset?.agentId)?.dataset.agentId});
        results.replaceChildren(node('div',{className:'a-card a-probe-list'},liveTable));
      }
    }
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
    const renderAllocation = () => agent.shared_allocation && section('共享范围', node('div', { className: 'a-card-body' }, stack(
      node('p', {}, muted(`所有者 · ${agent.shared_allocation.owner_username || '—'}`)),
      node('p', {}, muted(`端口 · ${formatSharedPorts(agent.shared_allocation.ports) || '未分配'}`)),
      muted(`流量 · ${bytes(agent.shared_allocation.used_bytes)} / ${agent.shared_allocation.limit_bytes ? bytes(agent.shared_allocation.limit_bytes) : '不限量'}`),
    )));
    const allocation = renderAllocation();
    const sections = [], settingsUpdates = [], capabilities = new Map();
    let savingCapabilities = false, savingNode = false, address, zone;
    let savedName = agent.name, savedVisibility = Boolean(agent.admin_hidden);
    const available = () => ctx.current() && box.open && settingsScope() === openedScope && agents.includes(agent) && canManage(agent);
    const nodeForm = agent.can_manage !== false ? ctx.form([], '保存并应用', async () => {
      if (!available() || savingNode) return;
      const name = nodeForm.elements.name?.value.trim();
      if (name != null && (!name || [...name].length > 100 || /[\u0000-\u001f\u007f-\u009f]/u.test(name))) throw new Error('节点名称须为 1–100 个字符，且不能包含控制字符');
      const addressChanged = address?.dirty();
      const nameChanged = name != null && name !== savedName;
      const visibility = nodeForm.elements.admin_hidden?.checked;
      const visibilityChanged = visibility != null && visibility !== savedVisibility;
      const zoneChanged = zone?.dirty();
      if (addressChanged && !await ctx.confirm('更改节点默认地址将影响继承它的客户端配置，独立端口覆盖保持不变。', nodeForm.elements.node_address_mode.value === 'auto' ? '恢复自动获取' : '保存并应用') || !available()) return;
      savingNode = true;
      let savedCount = 0;
      try {
        await load();
        if (!available()) return;
        if (addressChanged) { await address.save(); savedCount++; }
        if (!available()) return;
        if (nameChanged) {
          const saved = await ctx.api(`${base}/name`, { method: 'PUT', body: { name } });
          if (!available()) return;
          savedName = agent.name = saved.name;
          if (nodeForm.elements.name.value.trim() === name) nodeForm.elements.name.value = saved.name;
          updateTitle(); savedCount++;
        }
        if (visibilityChanged) {
          const saved = await ctx.api(`${base}/visibility`, { method: 'PUT', body: { admin_hidden: visibility } });
          if (!available()) return;
          savedVisibility = agent.admin_hidden = Boolean(saved.admin_hidden); savedCount++;
        }
        if (!available()) return;
        if (zone && (zoneChanged || !addressChanged && !nameChanged && !visibilityChanged)) { await zone.save(); savedCount++; }
        if (!available()) return;
        ctx.announce('节点设置已保存');
        try { await load(); }
        catch (error) { if (available() && error.name !== 'AbortError') ctx.announce(`节点设置已保存，刷新失败。\n${error.message}`, 'error'); }
      } catch (error) {
        if (savedCount && available()) throw new Error(`部分设置已保存，其余更改尚未保存：${error.message}`);
        throw error;
      } finally { savingNode = false; if (available()) settingsUpdates.forEach(update => update()); }
    }) : null;
    if (nodeForm) nodeForm.dataset.nodeSettingsForm = agent.id;
    if (manage) {
      address = nodeAddress(ctx, agent, { available, form: nodeForm });
      settingsUpdates.push(address.update);
      const nameField = field('节点名称', 'name', agent.name, { required: true, maxlength: 100 });
      const visibilityField = agent.can_hide && field('对管理员隐藏', 'admin_hidden', agent.admin_hidden, { type: 'checkbox' });
      sections.push(section('节点资料', node('div', { className: 'a-card-body' }, stack(
        nameField, button('国家／地区', () => chooseRegion(agent)), address.view, visibilityField,
      ))));
      settingsUpdates.push(() => {
        const name = nameField.querySelector('input');
        if (!savingNode && name.value === savedName) { name.value = agent.name; savedName = agent.name; }
        const visibility = visibilityField?.querySelector('input');
        if (visibility && !savingNode && visibility.checked === savedVisibility) { visibility.checked = Boolean(agent.admin_hidden); savedVisibility = Boolean(agent.admin_hidden); }
      });
      for (const engine of agent.supported_capabilities || engineIDs) {
        const inputField = field('启用内核能力', `capability:${engine}`, agent.capabilities.includes(engine), { type: 'checkbox' });
        const input = inputField.querySelector('input');
        input.dataset.engineCapability = engine;
        const transitionState = notice('');
        const task = button('查看启停任务', event => void ctx.mutate(event.currentTarget, () => taskDetail(ctx, capability.taskID)));
        const capability = { input, view: stack(inputField, transitionState, actions(task)), saved: input.checked, failure: '', taskID: '' };
        const update = () => {
          const latest = agent.capabilities.includes(engine), transition = agent.capability_transitions?.[engine];
          const waiting = ['pending', 'running'].includes(transition?.status);
          if (!savingCapabilities && !capability.failure && (waiting || input.checked === capability.saved || input.checked === latest)) {
            input.checked = latest; capability.saved = latest;
          }
          input.disabled = !canManage(agent) || waiting || savingCapabilities;
          capability.taskID = transition?.task_id || capability.taskID;
          task.hidden = !ctx.can('tasks.read') || !capability.taskID;
          transitionState.textContent = capability.failure || (waiting ? `${transition.enabled ? '启动' : '停止'}${transition.status === 'pending' ? '排队中' : '执行中'}`
            : ['failed', 'canceled'].includes(transition?.status) ? `上次启停失败或取消${transition.error ? `：${transition.error}` : ''}` : '');
        };
        input.addEventListener('change', () => { capability.failure = ''; update(); updateCapabilitySave(); });
        settingsUpdates.push(update); capabilities.set(engine, capability); update();
      }
    }
    if (agent.can_manage !== false) {
      zone = nodeTimeZone(ctx, agent, () => canManage(agent), nodeForm);
      sections.push(zone.view); settingsUpdates.push(zone.update);
    }
    const runtimes = node('div', { className: 'a-grid' });
    const coreViews = new Map();
    const renderRuntimes = () => {
      const engines = [...new Set([...capabilities.keys(), ...(agent.capabilities || []), ...(agent.can_manage !== false ? Object.keys(agent.runtime || {}).filter(engine => agent.runtime[engine]?.installed) : [])])];
      for (const [engine, view] of coreViews) if (!engines.includes(engine)) { view.card.remove(); coreViews.delete(engine); }
      for (const engine of engines) {
        const renderService = () => {
          const runtime = agent.runtime?.[engine] || {};
          const instance = sharedInstancePresentation(agent, engine);
          const enabled = agent.capabilities?.includes(engine);
          const busy = actionBusy.has(coreKey(agent, engine));
          const uninstallReason = () => !agents.includes(agent) || agent.can_manage === false || !ctx.can('host.manage', agent) || !ctx.can('tasks.execute') ||
            !agent.features?.includes('core-uninstall-v1') || agent.status !== 'online' || !agent.runtime?.[engine]?.installed ||
            Boolean(managedCoreBlockReason(agent, engine)) || Boolean(uninstallTasks[coreKey(agent, engine)]);
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
          return stack(
            actions(instance ? badge(instance.label, instance.tone) : status(runtime.service_status === 'running' ? 'active' : runtime.service_status), !enabled && badge('能力已关闭', 'warn')),
            node('p', {}, muted(runtime.version || (runtime.installed === true ? '版本未上报' : runtime.installed === false ? '尚未安装' : '安装状态未知'))),
            instance && stack(muted(instance.version), instance.reason && notice(instance.reason)),
            runtime.existing_config_unsupported_reason && notice(runtime.existing_config_unsupported_reason),
            controls, actions(install, enabled && ctx.can('agent-config.read') && link('编辑配置', `#live-config?agent=${pathID(agent.id)}&engine=${engine}`),
              runtime.existing_config_available && ctx.can('host.manage', agent) && link('导入系统配置', `#live-config?agent=${pathID(agent.id)}&engine=${engine}&source=import`)),
            agent.can_manage !== false && uninstall && actions(uninstall),
          );
        };
        let view = coreViews.get(engine);
        if (!view) {
          const service = renderService();
          const card = section(engine, node('div', { className: 'a-card-body a-stack' }, capabilities.get(engine)?.view, service));
          card.dataset.refreshKey = engine;
          view = { card, service }; coreViews.set(engine, view); runtimes.append(card);
        } else patch(view.service, renderService());
        view.card.dataset.coreInstalled = typeof agent.runtime?.[engine]?.installed === 'boolean' ? agent.runtime[engine].installed ? '1' : '0' : 'unknown';
        view.card.dataset.capabilityEnabled = agent.capabilities?.includes(engine) ? '1' : '0';
      }
      if (!engines.length && !runtimes.firstChild) runtimes.append(muted('暂无可用内核'));
      else if (engines.length && runtimes.firstChild?.nodeName === 'SPAN') runtimes.firstChild.remove();
      runtimes.className = engines.length > 1 ? 'a-grid' : 'a-stack';
    };
    const changedCapabilities = () => [...capabilities].filter(([, capability]) => !capability.input.disabled && capability.input.checked !== agent.capabilities.includes(capability.input.dataset.engineCapability));
    const capabilityForm = manage && ctx.form([runtimes], '保存内核能力', async () => {
      if (!box.open || settingsScope() !== openedScope || !canManage(agent)) return;
      const changes = changedCapabilities().map(([engine, capability]) => ({ engine, capability, enabled: capability.input.checked }));
      if (!changes.length) return;
      savingCapabilities = true; settingsUpdates.forEach(update => update());
      let failures = 0;
      try {
        for (const { engine, capability, enabled } of changes) {
          if (!ctx.current() || !box.open || settingsScope() !== openedScope || !canManage(agent)) return;
          try {
            const received = await ctx.api(`${base}/capabilities/${engine}`, { method: 'PUT', body: { enabled } });
            if (!ctx.current() || !box.open || settingsScope() !== openedScope || !canManage(agent)) return;
            capability.failure = ''; capability.taskID = received.task_id || '';
            capability.saved = received.enabled; capability.input.checked = received.enabled;
          } catch (error) {
            if (error.name === 'AbortError') throw error;
            capability.failure = error.message; failures++;
          }
        }
        await load();
        if (ctx.current() && box.open) ctx.announce(failures ? '部分内核能力保存失败' : '内核能力已提交', failures ? 'error' : undefined);
      } finally {
        savingCapabilities = false;
        if (ctx.current() && box.open) { settingsUpdates.forEach(update => update()); updateCapabilitySave(); }
      }
    });
    function updateCapabilitySave() {
      if (capabilityForm) capabilityForm.querySelector('[type=submit]').disabled = savingCapabilities || !canManage(agent) || !changedCapabilities().length || capabilityForm.getAttribute('aria-busy') === 'true';
    }
    if (capabilityForm) {
      capabilityForm.querySelector('[type=submit]').addEventListener('mutationend', updateCapabilitySave);
      settingsUpdates.push(updateCapabilitySave); updateCapabilitySave();
    }
    const corePanel = stack(capabilityForm || runtimes);
    corePanel.dataset.settingsPanel = 'core'; corePanel.setAttribute('role', 'region'); corePanel.setAttribute('aria-label', '内核设置'); corePanel.hidden = true;
    renderRuntimes();
    const renderNodeOperations = () => actions(
      ctx.can('metrics.read') && button('查看指标历史',event=>void ctx.mutate(event.currentTarget,()=>nodeMetrics(ctx,agent))),
      ctx.can('host.manage',agent) && button('升级 Agent',async event=>{
        const control=event.currentTarget;
        if(!await ctx.confirm('升级此节点的 QAgent？替换后自动重连，期间节点会短暂离线。','升级 Agent')||!ctx.current())return;
        await ctx.mutate(control,()=>submit(agent,undefined,'upgrade-agent'));
      },{disabled:agent.status!=='online'||!agent.features?.includes('agent-self-upgrade-v1'),title:agent.features?.includes('agent-self-upgrade-v1')?'升级 QAgent':'当前 Agent 需重新执行安装命令'}),
      manage && agent.enrollment_command_available && button('查看安装命令', event => void ctx.mutate(event.currentTarget, async () => command(await ctx.api(`${base}/enrollment-command`, { method: 'POST' })))),
      manage && nodeDeleteControl(ctx, agent, { available: () => box.open && settingsScope() === openedScope && canManage(agent), onDeleted() {
        agents = agents.filter(value => value.id !== agent.id); box.close(); draw(true);
      }, refresh: load }),
    );
    const nodeOperations = renderNodeOperations();
    const operations = nodeOperations.childElementCount > 0 && section('节点操作', node('div', { className: 'a-card-body' }, nodeOperations));
    const details = sections.length > 1 ? grid(sections) : stack(sections);
    if (nodeForm) {
      nodeForm.querySelector('fieldset').append(details);
      nodeForm.querySelector('[type=submit]').disabled = !manage;
    }
    const nodePanel = stack(nodeForm || details, allocation, operations);
    nodePanel.dataset.settingsPanel = 'node'; nodePanel.setAttribute('role', 'region'); nodePanel.setAttribute('aria-label', '节点设置');
    let activePanel = 'node';
    const scrollPositions = { node: 0, core: 0 };
    const updateTitle = () => { box.querySelector('.a-dialog-head h2').textContent = `${agent.name} · ${activePanel === 'node' ? '节点设置' : '内核设置'}`; };
    const selectPanel = value => {
      scrollPositions[activePanel] = box.scrollTop; activePanel = value;
      nodePanel.hidden = value !== 'node'; corePanel.hidden = value !== 'core';
      nodeTab.setAttribute('aria-pressed', String(value === 'node')); coreTab.setAttribute('aria-pressed', String(value === 'core'));
      updateTitle(); box.scrollTop = scrollPositions[value];
    };
    const nodeTab = button('节点设置', () => selectPanel('node'), { 'aria-pressed': 'true' });
    const coreTab = button('内核设置', () => selectPanel('core'), { 'aria-pressed': 'false' });
    const navigation = actions(nodeTab, coreTab);
    navigation.dataset.settingsNavigation = ''; navigation.setAttribute('role', 'group'); navigation.setAttribute('aria-label', '设置分区');
    const box = ctx.dialog(`${agent.name} · 节点设置`, stack(navigation, nodePanel, corePanel), { wide: true });
    const nodeDirty = () => address?.dirty() || zone?.dirty() || nodeForm?.elements.name && nodeForm.elements.name.value !== savedName || nodeForm?.elements.admin_hidden && nodeForm.elements.admin_hidden.checked !== savedVisibility;
    box.onRequestClose = async () => {
      if (nodeDirty() && !await ctx.confirm('存在未保存的设置更改，确定关闭？', '放弃更改')) return;
      if (ctx.current()) box.close();
    };
    const update = () => {
      if (!box.open || !ctx.current()) { dialogUpdates.delete(update); return; }
      if (!agents.some(value => value.id === agent.id) || settingsScope() !== openedScope || manage && !canManage(agent)) { box.close(); dialogUpdates.delete(update); return; }
      if (allocation) patch(allocation, renderAllocation());
      patch(nodeOperations, renderNodeOperations());
      settingsUpdates.forEach(refresh => refresh());
      renderRuntimes();
    };
    settingsUpdates.forEach(refresh => refresh());
    if (nodeForm) for (const event of ['input', 'change']) nodeForm.addEventListener(event, () => ctx.setDirty(Boolean(nodeDirty())));
    nodeForm?.querySelector('[type=submit]').addEventListener('mutationend', () => { if (ctx.current() && box.open) ctx.setDirty(Boolean(nodeDirty())); });
    box.addEventListener('close', () => { if (ctx.current()) ctx.setDirty(false); }, { once: true });
    dialogUpdates.add(update);
    box.addEventListener('close', () => dialogUpdates.delete(update), { once: true });
    const dispose = () => { dialogUpdates.delete(update); box.close(); };
    ctx.signal.addEventListener('abort', dispose, { once: true });
    box.addEventListener('close', () => ctx.signal.removeEventListener('abort', dispose), { once: true });
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
  function batch() {
    if (batchBox?.isConnected) { batchBox.focus(); return; }
    batchBox = openNodeBatch(ctx, (value = agents) => orderNodesBySavedOrder(value, ordering.ids()));
  }

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
  )), refreshState, results);
}
