import { nodeMetrics } from './node-metrics.js';
import { coreInstallBlockReason } from '../modules/core-install-state.js';
import { coreSourceForInstall } from '../modules/core-install-source.js';
import { createServiceActionGuard } from '../modules/agent-service-state.js';
import { enrollmentInstallCommand } from '../modules/enrollment-command.js';
import { parseSharedPorts, formatSharedPorts, sharedLimitBytes, sharedLimitGiB } from '../modules/user-model.js';
import { copyText, node, button, actions, stack, grid, section, table, field, code, badge, muted, notice, link, pathID, list } from './ui.js';
import { date, bytes, status, actionName, taskDetail } from './operations.js';

const engineIDs = ['mihomo', 'xray', 'sing-box', 'ss-rust'];
const taskTerminal = task => ['succeeded', 'failed', 'canceled'].includes(task.status);

export async function nodesPage(ctx) {
  const [receivedAgents, savedView] = await Promise.all([ctx.api('/agents'), ctx.api('/preferences/node-view')]);
  let agents = list(receivedAgents), preference = savedView;
  const canManage = agent => ctx.can('agents.manage', agent) && agent.can_manage !== false;
  const guard = createServiceActionGuard(ctx.can);
  const search = field('搜索节点', 'search', '', { type: 'search', placeholder: '名称、节点 ID 或地址' });
  const results = node('div');
  const selected = new Set();
  let view = preference.view === 'list' ? 'list' : 'cards', selecting = false;
  const batchBar = node('div', {className:'a-probe-batch'});
  const cardToggle = button('卡片',event=>void ctx.mutate(event.currentTarget,()=>setView('cards')));
  const listToggle = button('列表',event=>void ctx.mutate(event.currentTarget,()=>setView('list')));
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
  function choice(agent){return field(`选择 ${agent.name}`,`select:${agent.id}`,selected.has(agent.id),{type:'checkbox',onchange(event){event.target.checked?selected.add(agent.id):selected.delete(agent.id);drawBatch();}});}
  function drawBatch(){
    batchBar.hidden=!selecting;
    batchBar.replaceChildren(muted(`已选 ${selected.size} 个节点`),button('执行批量操作',batch,{disabled:!selected.size}),button('取消选择',()=>{selecting=false;selected.clear();draw();}));
  }
  const percentage=(used,total)=>total>0?Math.min(100,Math.max(0,Number(used)/Number(total)*100)):0;
  function meter(label,value,available,detail){
    const percent=Math.min(100,Math.max(0,Number(value)||0));
    return node('div',{className:'a-probe-meter'},node('div',{className:'a-probe-meter-label'},muted(label),node('strong',{},available?`${percent.toFixed(1)}%`:'—')),
      node('div',{className:'a-probe-track','aria-hidden':'true'},node('span',{style:`width:${available?percent:0}%`,className:percent>90?'bad':percent>75?'warn':''})),detail&&muted(available?detail:'暂无数据'));
  }
  function metrics(agent){const m=agent.metrics||{},online=agent.status==='online';return node('div',{className:'a-probe-meters'},
    meter('CPU',m.cpu_percent,online&&m.cpu_available),
    meter('内存',percentage(m.memory_used_bytes,m.memory_total_bytes),online&&m.memory_available,`${bytes(m.memory_used_bytes)} / ${bytes(m.memory_total_bytes)}`),
    meter('磁盘',percentage(m.disk_used_bytes,m.disk_total_bytes),online&&m.disk_available,`${bytes(m.disk_used_bytes)} / ${bytes(m.disk_total_bytes)}`));}
  function controls(agent){return actions(ctx.can('agent-config.read')&&link('实时配置',`#live-config?agent=${pathID(agent.id)}`),
    button('查看设置',event=>void ctx.mutate(event.currentTarget,()=>settings(agent))),
    canManage(agent)&&button('共享 Agent',event=>void ctx.mutate(event.currentTarget,()=>sharing(agent))));}
  function network(agent){const m=agent.metrics||{},available=agent.status==='online'&&m.network_available;return node('div',{className:'a-probe-network'},
    stack(muted('接收'),node('strong',{},available?`${bytes(m.network_rx_bps)}/s`:'—')),
    stack(muted('发送'),node('strong',{},available?`${bytes(m.network_tx_bps)}/s`:'—')));}

  async function load() {
    const received = list(await ctx.api('/agents'));
    if (!ctx.current()) return;
    agents = received;
    for (const id of selected) if (!agents.some(agent => agent.id === id)) selected.delete(id);
    draw();
  }
  async function submit(agent, engine, action, extra = {}) {
    const task = await ctx.api('/tasks', { method: 'POST', body: { agent_id: agent.id, engine, action, ...extra } });
    if (!ctx.current()) return;
    ctx.announce('任务已提交');
    if (ctx.can('tasks.read')) await taskDetail(ctx, task.id);
    return task;
  }
  function draw() {
    const query = search.querySelector('input').value.trim().toLowerCase();
    const shown = agents.filter(agent => [agent.name, agent.id, agent.metrics?.public_ipv4, agent.metrics?.public_ipv6].some(value => String(value || '').toLowerCase().includes(query)));
    cardToggle.setAttribute('aria-pressed',String(view==='cards'));listToggle.setAttribute('aria-pressed',String(view==='list'));
    drawBatch();
    if(!shown.length){results.replaceChildren(node('p',{className:'a-empty'},agents.length?'没有符合条件的节点':'暂无节点，添加 QAgent 后显示资源状态'));return;}
    if(view==='cards')results.replaceChildren(node('div',{className:'a-probe-grid'},shown.map(agent=>
      node('article',{className:'a-card a-probe','data-agent-id':agent.id},
        node('header',{className:'a-probe-head'},node('div',{},node('h2',{},agent.name),muted(`${agent.os} / ${agent.arch}`)),status(agent.status)),
        selecting&&node('div',{className:'a-probe-choice'},choice(agent)),
        node('div',{className:'a-probe-body'},metrics(agent),network(agent)),
        node('div',{className:'a-probe-meta'},actions((agent.capabilities||[]).map(engine=>badge(engine)),badge(agent.can_manage===false?'本人共享实例':'主实例')),muted(`最近在线 · ${date(agent.last_seen)}`)),
        node('footer',{className:'a-probe-footer'},controls(agent))))));
    else{
      const listTable=table([...(selecting?['选择']:[]),'节点','状态','资源使用','网络速率','最近在线','操作'],shown.map(agent=>[
        ...(selecting?[choice(agent)]:[]),stack(node('strong',{},agent.name),muted(`${agent.os} / ${agent.arch}`),actions((agent.capabilities||[]).map(engine=>badge(engine)))),
        status(agent.status),metrics(agent),network(agent),date(agent.last_seen),controls(agent)]),{label:'节点监控'});
      listTable.querySelectorAll('tbody tr').forEach((row,index)=>row.dataset.agentId=shown[index].id);
      results.replaceChildren(node('div',{className:'a-card a-probe-list'},listTable));
    }
  }

  function command(created) {
    const value = enrollmentInstallCommand(created);
    ctx.dialog('QAgent 安装命令', stack(code(value), button('复制命令', event => void ctx.mutate(event.currentTarget, async () => {
      await copyText(value); ctx.announce('安装命令已复制');
    }))));
  }
  async function enrollment() {
    const records = node('div');
    async function reload() {
      const tokens = list(await ctx.api('/enrollment-tokens'));
      if (!ctx.current() || !box.isConnected) return;
      records.replaceChildren(table(['节点', '添加时间', '状态', '操作'], tokens.map(token => [token.name, date(token.created_at), token.revoked_at ? '已撤销' : token.used_count ? '已接入' : '等待接入', actions(
        token.command_available && button('安装命令', event => void ctx.mutate(event.currentTarget, async () => command(await ctx.api(`/enrollment-tokens/${pathID(token.id)}/command`, { method: 'POST' })))),
        button('删除记录', async event => {
          const control = event.currentTarget;
          if (!await ctx.confirm('删除此添加记录？已接入的节点不会被卸载。', '删除记录') || !ctx.current()) return;
          await ctx.mutate(control, async () => { await ctx.api(`/enrollment-tokens/${pathID(token.id)}`, { method: 'DELETE' }); await reload(); });
        }, { tone: 'danger' }),
      )]), { label: '节点添加记录' }));
    }
    const form = ctx.form([
      field('节点名称', 'name', '', { required: true, maxlength: 100 }),
      field('对管理员隐藏此节点', 'admin_hidden', false, { type: 'checkbox' }),
    ], '生成安装命令', async (values, form) => {
      const created = await ctx.api('/enrollment-tokens', { method: 'POST', body: { name: values.name.trim(), admin_hidden: form.elements.admin_hidden.checked } });
      if (!ctx.current()) return;
      command(created); await reload();
    });
    const box = ctx.dialog('添加节点', stack(form, section('添加记录', records)), { wide: true });
    await reload();
  }
  async function directory() {
    const entries = list(await ctx.api('/agent-directory'));
    if (!ctx.current()) return;
    const content = node('div');
    const box = ctx.dialog('节点目录', content, { wide: true });
    function drawDirectory() {
      content.replaceChildren(table(['节点', '所有者', '状态', '内核', '操作'], entries.map(entry => [
        stack(entry.name, muted(entry.id)), entry.owner_username || '未分配账号', status(entry.status), (entry.capabilities || []).join(' · '),
        button('删除节点', async event => {
          const control = event.currentTarget;
          if (!await ctx.confirm(`删除“${entry.name}”？关联数据将被清理，节点上的 QAgent 不会被卸载。`, '删除节点') || !ctx.current()) return;
          await ctx.mutate(control, async () => { await ctx.api(`/agents/${pathID(entry.id)}`, { method: 'DELETE' }); entries.splice(entries.indexOf(entry), 1); drawDirectory(); await load(); });
        }, { tone: 'danger' }),
      ]), { label: '节点目录' }));
    }
    drawDirectory(); return box;
  }
  async function settings(agent) {
    const base = `/agents/${pathID(agent.id)}`;
    const manage = canManage(agent);
    const facts = section('节点信息', node('div', { className: 'a-card-body' }, stack(
      actions(status(agent.status), badge(agent.can_manage === false ? '本人共享实例' : '主实例')),
      muted(agent.id), muted(`${agent.os} / ${agent.arch} · QAgent ${agent.version || '—'}`),
      muted(`最近在线 · ${date(agent.last_seen)}`),
      agent.metrics && grid(
        muted(`CPU · ${agent.metrics.cpu_available ? Number(agent.metrics.cpu_percent).toFixed(1)+'%' : '—'}`),
        muted(`内存 · ${agent.metrics.memory_available ? bytes(agent.metrics.memory_used_bytes)+' / '+bytes(agent.metrics.memory_total_bytes) : '—'}`),
        muted(`磁盘 · ${agent.metrics.disk_available ? bytes(agent.metrics.disk_used_bytes)+' / '+bytes(agent.metrics.disk_total_bytes) : '—'}`),
        muted(`网络 · ${agent.metrics.network_available ? '接收 '+bytes(agent.metrics.network_rx_bps)+'/s · 发送 '+bytes(agent.metrics.network_tx_bps)+'/s' : '—'}`)),
      agent.shared_allocation && stack(muted(`所有者 · ${agent.shared_allocation.owner_username || '—'}`),
        muted(`端口 · ${formatSharedPorts(agent.shared_allocation.ports) || '未分配'}`),
        muted(`流量 · ${bytes(agent.shared_allocation.used_bytes)} / ${agent.shared_allocation.limit_bytes ? bytes(agent.shared_allocation.limit_bytes) : '不限量'}`)),
    )));
    const sections = [facts];
    if(ctx.can('metrics.read'))sections.push(section('资源指标',node('div',{className:'a-card-body'},button('查看指标历史',event=>void ctx.mutate(event.currentTarget,()=>nodeMetrics(ctx,agent))))));
    if (manage) {
      sections.push(section('Agent 设置', node('div', { className: 'a-card-body' }, stack(
        ctx.form([field('节点名称', 'name', agent.name, { required: true, maxlength: 100 })], '保存名称', async values => {
          const saved = await ctx.api(`${base}/name`, { method: 'PUT', body: { name: values.name.trim() } });
          if (!ctx.current()) return;
          agent.name = saved.name; box.querySelector('.a-dialog-head h2').textContent = `${agent.name} · 节点设置`; await load(); ctx.announce('名称已保存');
        }),
        agent.can_hide && ctx.form([field('对管理员隐藏', 'admin_hidden', agent.admin_hidden, { type: 'checkbox' })], '保存可见性', async (_, form) => {
          const saved = await ctx.api(`${base}/visibility`, { method: 'PUT', body: { admin_hidden: form.elements.admin_hidden.checked } });
          agent.admin_hidden = saved.admin_hidden; ctx.announce('可见性已保存'); await load();
        }),
        ctx.form([field('Komari UUID', 'uuid', agent.labels?.komari_uuid || '', { maxlength: 100 })], '保存 Komari 关联', async values => {
          await ctx.api(`${base}/komari`, { method: 'PUT', body: values }); ctx.announce('Komari 关联已保存'); await load();
        }),
      ))));
      let zone = agent.time_zone || {};
      const zoneStatus = notice(zone.error || (zone.time_zone ? `当前时区 · ${zone.time_zone}` : '尚未设置'));
      const zoneForm = ctx.form([
        grid(field('时区模式', 'mode', zone.mode || 'auto', { options: [['auto', '自动检测'], ['custom', '手动设置']] }),
          field('IANA 时区', 'time_zone', zone.time_zone || '', { placeholder: 'Asia/Shanghai', maxlength: 100 })), zoneStatus,
      ], '应用时区', async (values, form) => {
        if (values.mode === 'custom' && !values.time_zone.trim()) throw new Error('请输入 IANA 时区');
        zone = await ctx.api(`${base}/time-zone`, { method: 'PUT', body: { mode: values.mode, time_zone: values.mode === 'custom' ? values.time_zone.trim() : '', revision: zone.revision || 0, retry: values.mode === 'auto' } });
        form.elements.time_zone.value = zone.time_zone || '';
        zoneStatus.textContent = zone.error || (zone.status === 'pending' ? '已保存，等待节点应用' : zone.status === 'unsupported' ? '当前 Agent 不支持时区设置' : `已保存 · ${zone.time_zone || '自动检测'}`);
      });
      zoneForm.elements.mode.addEventListener('change', () => { zoneForm.elements.time_zone.disabled = zoneForm.elements.mode.value !== 'custom'; });
      zoneForm.elements.time_zone.disabled = zoneForm.elements.mode.value !== 'custom';
      sections.push(section('时区', node('div', { className: 'a-card-body' }, zoneForm)));
      sections.push(section('内核能力', node('div', { className: 'a-card-body a-stack' }, (agent.supported_capabilities || engineIDs).map(engine => {
        const form = ctx.form([field(engine, 'enabled', agent.capabilities.includes(engine), { type: 'checkbox' })], '保存能力', async (_, element) => {
          const received = await ctx.api(`${base}/capabilities/${engine}`, { method: 'PUT', body: { enabled: element.elements.enabled.checked } });
          ctx.announce(received.task_id ? '启停任务已提交，成功后更新能力' : '内核能力已更新'); await load();
          if (received.task_id && ctx.can('tasks.read')) await taskDetail(ctx, received.task_id);
        });
        return form;
      }))));
    }
    for (const engine of agent.capabilities || []) {
      const runtime = agent.runtime?.[engine] || {};
      const reason = coreInstallBlockReason(agent, engine, ctx.can);
      const controls = actions(['status', 'start', 'stop', 'restart'].map(action => button(actionName(action), async event => {
        const control = event.currentTarget;
        if (action !== 'status' && !await ctx.confirm(`对 ${agent.name} 的 ${engine} 执行“${actionName(action)}”？`, actionName(action)) || !ctx.current()) return;
        await ctx.mutate(control, () => submit(agent, engine, action));
      }, { disabled: guard(action, agent.status === 'online', runtime.installed, runtime.service_status, agent, engine) })));
      const install = button(runtime.installed ? '切换版本' : agent.can_manage === false ? '准备内核' : '安装内核', () => installCore(agent, engine), { disabled: Boolean(reason), title: reason || '安装或切换内核版本' });
      const uninstall = manage && ctx.can('host.manage', agent) && ctx.can('tasks.execute') && agent.features?.includes('core-uninstall-v1') && button('卸载内核', async event => {
        const control = event.currentTarget;
        if (!await ctx.confirm(`卸载 ${engine}？目标服务会停止，当前连接将断开，已保存配置保留。`, '卸载内核') || !ctx.current()) return;
        await ctx.mutate(control, () => submit(agent, engine, 'uninstall'));
      }, { tone: 'danger', disabled: agent.status !== 'online' || !runtime.installed || Boolean(runtime.existing_config_unsupported_reason) });
      sections.push(section(engine, node('div', { className: 'a-card-body' }, stack(
        actions(status(runtime.service_status), muted(runtime.version || '尚未安装')),
        runtime.instance && stack(muted(runtime.instance.id), runtime.instance.reason && notice(runtime.instance.reason), runtime.instance.restriction && notice(runtime.instance.restriction)),
        runtime.existing_config_unsupported_reason && notice(runtime.existing_config_unsupported_reason),
        controls, actions(install, uninstall, ctx.can('agent-config.read') && link('编辑配置', `#live-config?agent=${pathID(agent.id)}&engine=${engine}`),
          runtime.existing_config_available && ctx.can('host.manage', agent) && link('导入系统配置', `#live-config?agent=${pathID(agent.id)}&engine=${engine}&source=import`)),
      ))));
    }
    const box = ctx.dialog(`${agent.name} · 节点设置`, stack(sections, actions(
      ctx.can('host.manage',agent) && button('升级 Agent',async event=>{
        const control=event.currentTarget;
        if(!await ctx.confirm('升级此节点的 QAgent？替换后自动重连，期间节点会短暂离线。','升级 Agent')||!ctx.current())return;
        await ctx.mutate(control,()=>submit(agent,undefined,'upgrade-agent'));
      },{disabled:agent.status!=='online'||!agent.features?.includes('agent-self-upgrade-v1'),title:agent.features?.includes('agent-self-upgrade-v1')?'升级 QAgent':'当前 Agent 需重新执行安装命令'}),
      manage && agent.enrollment_command_available && button('查看安装命令', event => void ctx.mutate(event.currentTarget, async () => command(await ctx.api(`${base}/enrollment-command`, { method: 'POST' })))),
      manage && button('共享 Agent', event => void ctx.mutate(event.currentTarget, () => sharing(agent))),
      manage && button('删除节点', async event => {
        const control = event.currentTarget;
        if (!await ctx.confirm(`删除“${agent.name}”？关联数据将被清理，节点上的 QAgent 不会被卸载。`, '删除节点') || !ctx.current()) return;
        await ctx.mutate(control, async () => { await ctx.api(base, { method: 'DELETE' }); box.close(); await load(); ctx.announce('节点已删除'); });
      }, { tone: 'danger' }),
    )), { wide: true });
  }
  function installCore(agent, engine) {
    const shared = agent.can_manage === false;
    const custom = field('指定版本', 'custom_version', '', { placeholder: '软件发行版本' });
    const source = field('开发版来源', 'core_source', 'official', { options: [['official', 'MetaCubeX 官方'], ['mirror', 'vernesong 镜像']] });
    const feedback = notice('');
    const form = ctx.form([
      field('发行通道', 'release_channel', 'stable', { options: shared ? [['stable', '稳定版']] : [['stable', '稳定版'], ...(engine === 'mihomo' ? [['development', '开发版']] : []), ['custom', '指定版本']] }), custom, source, feedback,
    ], shared ? '准备内核' : '提交安装', async values => {
      const received = list(await ctx.api('/agents'));
      if (!ctx.current()) return;
      const latest = received.find(item => item.id === agent.id);
      const reason = !latest ? '节点已不可用' : coreInstallBlockReason(latest, engine, ctx.can);
      if (reason) throw new Error(reason);
      if (values.release_channel === 'custom' && !values.custom_version.trim()) throw new Error('请输入软件发行版本');
      if (!await ctx.confirm(shared ? '准备缺少的稳定版内核？已有版本会复用。' : '安装完成后将重启对应内核服务，确定继续？', '提交安装') || !ctx.current()) return;
      const task = await ctx.api('/tasks', { method: 'POST', body: { agent_id: agent.id, engine, action: 'install', core_version: values.release_channel === 'custom' ? values.custom_version.trim() : values.release_channel, core_source: coreSourceForInstall(engine, values.release_channel, values.core_source) } });
      feedback.textContent = ''; ctx.announce('安装任务已提交');
      if (ctx.can('tasks.read')) { await taskDetail(ctx, task.id); if (taskTerminal(task)) await load(); }
    });
    const sync = () => { ctx.visible(custom, form.elements.release_channel.value === 'custom'); form.elements.custom_version.required = form.elements.release_channel.value === 'custom'; ctx.visible(source, engine === 'mihomo' && form.elements.release_channel.value === 'development'); };
    form.elements.release_channel.addEventListener('change', sync); sync();
    ctx.dialog(`${agent.name} · ${engine}`, form);
  }
  async function sharing(agent) {
    let saved = await ctx.api(`/agents/${pathID(agent.id)}/sharing`);
    if (!ctx.current()) return;
    const rows = node('div', { className: 'a-stack' });
    const feedback = notice('');
    const form = ctx.form([rows, button('添加用户', () => add({ enabled: true })), feedback], '保存共享设置', async () => {
      const shares = [...rows.children].map(row => ({
        username: row.querySelector('[name=username]').value.trim(), enabled: row.querySelector('[name=enabled]').checked,
        ports: parseSharedPorts(row.querySelector('[name=ports]').value), limit_bytes: sharedLimitBytes(row.querySelector('[name=limit_gib]').value),
        engines: [...row.querySelectorAll('[name=engines]:checked')].map(input => input.value),
        http_domains: row.querySelector('[name=http_domains]').value.split(/[\n,，]+/).map(value => value.trim()).filter(Boolean),
        reinvite: row.dataset.reinvite === 'true',
      }));
      const received = await ctx.api(`/agents/${pathID(agent.id)}/sharing`, { method: 'PUT', body: { revision: saved.revision, shares } });
      if (!ctx.current()) return;
      saved = received; rows.replaceChildren(); saved.shares.forEach(add); ctx.setDirty(false); feedback.textContent = ''; ctx.announce('共享设置已保存');
    });
    function add(share) {
      const engines = node('div', { className: 'a-actions' }, (agent.supported_capabilities || agent.capabilities).map(engine => field(engine, 'engines', list(share.engines).includes(engine), { type: 'checkbox', value: engine })));
      const row = section(share.username || '新用户', node('div', { className: 'a-card-body a-stack' },
        grid(field('用户名', 'username', share.username || '', { required: true, maxlength: 64, readonly: Boolean(share.user_id) }),
          field('总额度 · GiB', 'limit_gib', sharedLimitGiB(share.limit_bytes), { type: 'number', min: 0, max: 8388607, step: 'any', required: true })),
        field('端口', 'ports', formatSharedPorts(share.ports), { placeholder: '21000-21100, 22000', hint: '留空未分配，0 表示不限端口' }), engines,
        field('HTTP-01 允许域名', 'http_domains', list(share.http_domains).join('\n'), { multiline: 2 }),
        actions(field('启用共享', 'enabled', share.enabled !== false, { type: 'checkbox' }),
          share.status === 'rejected' && button('重新邀请', event => { row.dataset.reinvite = 'true'; event.currentTarget.disabled = true; row.querySelector('[name=enabled]').checked = true; ctx.setDirty(true); }),
          !share.user_id && button('移除', () => { row.remove(); ctx.setDirty(true); })),
        share.user_id && muted(`已用 ${bytes(share.used_bytes)} · ${share.status === 'accepted' ? '已接受' : share.status === 'rejected' ? '已拒绝' : '等待接受'}`),
      ));
      rows.append(row);
    }
    saved.shares.forEach(add);
    form.addEventListener('input', () => ctx.setDirty(true));
    const supported = ['shared-traffic-v1', 'shared-engines-v1', 'independent-egress-v1'].every(feature => agent.features?.includes(feature));
    if (!supported) form.querySelector('button[type=submit]').disabled = true;
    const box = ctx.dialog(`${agent.name} · 共享 Agent`, stack(!supported && notice('请先升级 Agent，再启用共享'), form), { wide: true });
    box.onRequestClose = async () => {
      if (box.querySelector('form[aria-busy=true]')) return;
      if (!await ctx.confirm('关闭共享设置？尚未保存的修改将丢弃。', '关闭共享设置') || !ctx.current()) return;
      ctx.setDirty(false); box.close();
    };
  }
  function batch() {
    const chosen = agents.filter(agent => selected.has(agent.id));
    if (!chosen.length) { ctx.announce('请先选择节点', 'error'); return; }
    const output = node('div');
    const form = ctx.form([
      muted(`已选择 ${chosen.length} 个节点`),
      grid(field('内核', 'engine', 'mihomo', { options: engineIDs }), field('操作', 'action', 'status', { options: [['status', '查询状态'], ['start', '启动'], ['restart', '重启'], ['stop', '停止']] })), output,
    ], '执行批量操作', async values => {
      if (!await ctx.confirm(`对选中节点执行“${actionName(values.action)}”？不符合条件的节点会跳过。`, '执行批量操作') || !ctx.current()) return;
      const rows = [];
      for (const agent of chosen) {
        if (!ctx.current()) break;
        const runtime = agent.runtime?.[values.engine] || {};
        if (guard(values.action, agent.status === 'online', runtime.installed, runtime.service_status, agent, values.engine)) { rows.push([agent.name, '已跳过，当前状态或权限不适用']); continue; }
        try { const task = await ctx.api('/tasks', { method: 'POST', body: { agent_id: agent.id, engine: values.engine, action: values.action } }); rows.push([agent.name, actions('已提交', ctx.can('tasks.read') && button('任务详情', () => void taskDetail(ctx, task.id)))]); }
        catch (error) { if (error.name === 'AbortError') throw error; rows.push([agent.name, error.message]); }
        output.replaceChildren(table(['节点', '结果'], rows, { label: '批量操作结果' }));
      }
    });
    ctx.dialog('批量操作', form, { wide: true });
  }
  draw(); search.querySelector('input').addEventListener('input', draw);
  const deepID = location.hash.match(/^#(?:settings-node|node)-([^?]+)/)?.[1];
  if (deepID) { const agent = agents.find(agent => agent.id === deepID); if (agent) queueMicrotask(() => { if (ctx.current()) void settings(agent); }); }
  return stack(node('div', { className: 'a-probe-toolbar' }, search, actions(
    cardToggle, listToggle,
    ctx.can('enrollment.manage') && button('添加节点', event => void ctx.mutate(event.currentTarget, enrollment), { tone: 'primary' }),
    ctx.can('users.manage') && button('节点目录', event => void ctx.mutate(event.currentTarget, directory)),
    ctx.can('tasks.execute') && button('批量操作',()=>{selecting=true;draw();}),
  )), batchBar, results);
}
