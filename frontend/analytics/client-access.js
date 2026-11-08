import { groupClientAccessEntries, clientAccessAddressChoices, clientAccessExport, clientAccessBatchExport, clientAccessRecordKey, filterClientAccessEntries, normalizeClientAccessFilters, loadClientDisplayFormat, saveClientDisplayFormat } from '../modules/client-access-model.js';
import { createNodeOrderSync, orderNodesBySavedOrder } from '../modules/node-order.js';
import { accountStorage } from '../modules/account-storage.js';
import { nodeRegionFlag } from './node-region.js';
import { patch } from './live-dom.js';
import { filterbar, copyText, node, button, actions, stack, section, table, field, muted, list, notice, link, pathID } from './ui.js';

export async function clientAccessPage(ctx) {
  const ordering = createNodeOrderSync((path, options = {}) => ctx.api(path, { ...options, body: options.body ? JSON.parse(options.body) : undefined }), accountStorage, ctx.current);
  let [entries, agents] = await Promise.all([ctx.api('/client-access'), ctx.can('agents.read') ? ctx.api('/agents') : [], ordering.load()]);
  entries = list(entries); agents = orderNodesBySavedOrder(list(agents), ordering.ids());
  const search = field('搜索接入配置', 'search', '', { type: 'search', placeholder: '节点、协议、入站或地址' });
  const agentOptions = () => [['', '全部节点'], ...groupClientAccessEntries(entries, agents).map(group => [group.agent_id, group.entries[0].agent_name || group.agent_id])];
  const agentFilter = field('节点', 'agent', '', { options: agentOptions() });
  const format = field('导出格式', 'format', loadClientDisplayFormat(), { options: [['url', 'URL'], ['mihomo', 'Mihomo YAML']] });
  const result = node('div', { className: 'a-stack' });
  const scopes = new Map();
  let readSequence = 0;
  function updateOptions(control, options) {
    const picker = control.querySelector('select'), selected = picker.value;
    if (JSON.stringify([...picker.options].map(option => [option.value, option.textContent])) !== JSON.stringify(options))
      picker.replaceChildren(picker.querySelector('button'), ...options.map(([value, label]) => node('option', { value }, label)));
    picker.value = options.some(([value]) => value === selected) ? selected : '';
  }
  const urls = new Set(), timers = new Set();
  ctx.signal.addEventListener('abort', () => { urls.forEach(url => URL.revokeObjectURL(url)); timers.forEach(clearTimeout); urls.clear(); timers.clear(); }, { once: true });
  function download(value, name, type = 'text/plain;charset=utf-8') {
    const url = URL.createObjectURL(new Blob([value], { type })); urls.add(url);
    const anchor = node('a', { href: url, download: name }); document.body.append(anchor); anchor.click(); anchor.remove();
    const timer = setTimeout(() => { URL.revokeObjectURL(url); urls.delete(url); timers.delete(timer); }, 1000); timers.add(timer);
  }
  const selectedFormat = () => format.querySelector('select').value;
  const current = () => ctx.current() && !ctx.signal.aborted;
  async function authorizedEntries() {
    const received = list(await ctx.api('/client-access'));
    if (!current()) throw new DOMException('页面已切换', 'AbortError');
    return received;
  }
  function matching(received, entry, item) {
    const resource = received.find(candidate => clientAccessRecordKey(candidate) === clientAccessRecordKey(entry));
    const profile = resource?.profiles?.find(candidate => clientAccessRecordKey(resource, candidate) === clientAccessRecordKey(entry, item));
    return resource?.config_version === entry.config_version && JSON.stringify(profile) === JSON.stringify(item) ? profile : null;
  }
  async function validate(entry, item) {
    if (!matching(await authorizedEntries(), entry, item)) throw new Error('接入配置已变更，请刷新后重试');
  }
  function track(box, entry, item) {
    scopes.set(box, { entry, item });
    box.addEventListener('close', () => scopes.delete(box), { once: true });
    return box;
  }
  function copy(value, control, check, error = "") {
    return ctx.mutate(control, async () => {
      await check();
      await copyText(String(value), { current });
      if (current()) ctx.announce(error ? `已复制可用方案；未输出：${error}` : '已复制', error ? 'error' : undefined);
    });
  }
  function copyBatch(agentID, control) {
    return ctx.mutate(control, async () => {
      const requestedFormat = selectedFormat();
      const received = await authorizedEntries();
      const groups = groupClientAccessEntries(received, agents).filter(group => !agentID || group.agent_id === agentID);
      const exported = clientAccessBatchExport(groups, requestedFormat);
      if (!exported.value) throw new Error(exported.errors.join('；') || '暂无可复制的接入配置');
      await copyText(exported.value, { current });
      if (!current()) return;
      ctx.announce(exported.errors.length ? `已复制 ${exported.outputs.length} 个方案；未输出：${exported.errors.join('；')}` : `已复制 ${exported.outputs.length} 个方案`, exported.errors.length ? 'error' : undefined);
    });
  }
  function downloadExport(entry, item, control) {
    return ctx.mutate(control, async () => {
      const requestedFormat = selectedFormat();
      await validate(entry, item);
      const exported = clientAccessExport(item, requestedFormat);
      for (const [index, output] of exported.outputs.entries()) download(output.value, `client-${entry.engine}-${item.port}-${index + 1}.${output.extension}`, output.mime);
      if (exported.error) throw new Error(`未输出：${exported.error}`);
    });
  }
  function secret(label, value, multiline = false, check = async () => {}, output) {
    const content = field(label, 'export', String(value ?? ''), multiline ? { multiline: 8, readonly: true, spellcheck: false } : { type: 'password', readonly: true, autocomplete: 'off' });
    const input = content.querySelector('input,textarea');
    let revealed = false;
    if (multiline) input.value = '••••••••';
    const toggle = button('显示', event => {
      const masked = !revealed; revealed = masked;
      if (multiline) input.value = revealed ? String(value ?? '') : '••••••••'; else input.type = revealed ? 'text' : 'password';
      event.currentTarget.textContent = masked ? '隐藏' : '显示'; event.currentTarget.setAttribute('aria-pressed', String(masked));
    }, { 'aria-pressed': 'false' });
    return stack(content, actions(toggle, button('复制', event => void copy(String(value ?? ''), event.currentTarget, check)),
      output && button('下载', event => void ctx.mutate(event.currentTarget, async () => { await check(); download(value, `client-config.${output.extension}`, output.mime); }))));
  }
  function details(entry, item) {
    const profile = item.profile || {};
    const exported = clientAccessExport(item, selectedFormat());
    const check = () => validate(entry, item);
    track(ctx.dialog(`${item.protocol || entry.engine} · 参数详情`, stack(
      actions(muted(item.tag), muted(`端口 ${item.port}`)),
      ...list(profile.fields).map(part => part.secret ? secret(part.label, part.value, false, check) : field(part.label, 'parameter', part.value, { readonly: true })),
      ...exported.outputs.map(output => secret([output.name, output.format].filter(Boolean).join(' · '), output.value, !output.isURL, check, output)),
      exported.error && notice(exported.error),
    ), { wide: true }), entry, item);
  }
  function display(entry, item) {
    let changed = false;
    const selector = { engine: entry.engine, config_id: entry.config_id, config_version: entry.config_version, tag: item.tag, port: Number(item.port) };
    const originalAddress = item.address_overridden ? item.address || '' : '';
    const choices = clientAccessAddressChoices(entry);
    const form = ctx.form([
      muted(`${item.tag} · ${entry.engine} · 端口 ${item.port}`),
      field('客户端节点名称', 'name', item.client_name || '', { maxlength: 100, placeholder: '留空使用入站标签' }),
      field('客户端连接地址', 'address', originalAddress, { maxlength: 253, placeholder: '留空使用自动识别地址', hint: originalAddress ? null : `自动识别 · ${item.address || '未识别'}` }),
      Boolean(choices.length) && field('客户端地址协议栈', 'address_mode', item.address_mode || 'auto', { options: choices.map(choice => [choice.value, choice.label]), disabled: Boolean(item.address_overridden) }),
    ], '保存参数', async values => {
      const body = { profile: selector, name: values.name.trim() };
      if (values.address.trim() !== originalAddress) body.address = values.address.trim();
      if (values.address_mode && values.address_mode !== (item.address_mode || 'auto')) body.address_mode = values.address_mode;
      await validate(entry, item);
      await ctx.api(`/agents/${pathID(entry.agent_id)}/client-address`, { method: 'PUT', body });
      if (!ctx.current()) return;
      ctx.setDirty(false); box.close(); await reload(); ctx.announce('客户端显示参数已保存');
    });
    for (const event of ['input', 'change']) form.addEventListener(event, () => { changed = true; ctx.setDirty(true); });
    const box = ctx.dialog('修改显示参数', stack(form, item.address_overridden && button('恢复自动识别', async event => {
      const control = event.currentTarget;
      if (!await ctx.confirm('清除当前端口的手动连接地址？', '恢复自动识别') || !ctx.current()) return;
      await ctx.mutate(control, async () => {
        await validate(entry, item);
        await ctx.api(`/agents/${pathID(entry.agent_id)}/client-address`, { method: 'PUT', body: { address: '', profile: selector } });
        ctx.setDirty(false); box.close(); await reload(); ctx.announce('已恢复自动识别');
      });
    })));
    track(box, entry, item);
    box.onRequestClose = async () => {
      if (changed && !await ctx.confirm('关闭显示参数？尚未保存的修改将丢弃。', '关闭') || !ctx.current()) return;
      ctx.setDirty(false); box.close();
    };
  }
  async function reload() {
    const sequence = ++readSequence;
    const [received, nodes] = await Promise.all([ctx.api('/client-access'), ctx.can('agents.read') ? ctx.api('/agents') : [], ordering.load()]);
    if (!ctx.current() || sequence !== readSequence) return;
    entries = list(received); agents = orderNodesBySavedOrder(list(nodes), ordering.ids());
    updateOptions(agentFilter, agentOptions());
    for (const [box, scope] of scopes) {
      if (matching(entries, scope.entry, scope.item) || scope.invalid) continue;
      if (!entries.some(entry => clientAccessRecordKey(entry) === clientAccessRecordKey(scope.entry))) {
        ctx.setDirty(false); box.close();
      } else {
        // Keep an editor's draft visible, but never apply it to a changed
        // deployment. The original controls retain their focus and values.
        scope.invalid = true;
        box.querySelectorAll('.a-dialog-body button').forEach(control => { control.disabled = true; });
        box.querySelectorAll('[name=export]').forEach(input => { if (input.tagName === 'TEXTAREA') input.value = '••••••••'; else input.type = 'password'; });
        box.querySelector('.a-dialog-body').append(notice('接入配置已变更，请关闭后重新打开'));
      }
    }
    draw();
  }
  function draw() {
    // Export-only accounts group the authorized response without /agents.
    const available = groupClientAccessEntries(entries, agents).map(group => ({ id: group.agent_id }));
    const filters = normalizeClientAccessFilters(entries, available, {
      query: search.querySelector('input').value, agent: agentFilter.querySelector('select').value,
    });
    agentFilter.querySelector('select').value = filters.agent;
    const groups = groupClientAccessEntries(filterClientAccessEntries(entries, filters), agents);
    const cards = groups.map(group => {
      const first = group.entries[0];
      const agent = agents.find(agent => agent.id === group.agent_id);
      const rows = group.entries.flatMap(entry => list(entry.profiles).map(item => {
        const exported = clientAccessExport(item, selectedFormat());
        const row = [stack(item.tag || '—', muted(`端口 ${item.port}`)), stack(item.protocol || '—',
          exported.format && muted(exported.format), exported.error && muted(exported.error)),
        item.client_name || entry.client_name || item.tag || '—', actions(
          button('参数详情', () => details(entry, item)),
          button('复制', event => void copy(exported.value, event.currentTarget, () => validate(entry, item), exported.error), { disabled: !exported.value }),
          button('下载', event => void downloadExport(entry, item, event.currentTarget), { disabled: !exported.value }),
          ctx.can('agents.manage') && (entry.config_id || agent && ctx.can('agents.manage', agent)) && button('修改显示参数', () => display(entry, item)),
        )];
        row.key = clientAccessRecordKey(entry, item);
        return row;
      }));
      const records = table(['入站', '协议', '客户端', '操作'], rows, { label: '客户端接入', rowKey: row => row.key });
      records.querySelector('.a-table-scroll').classList.add('a-traffic-list');
      const card = section(node('span', { className: 'a-probe-name' }, nodeRegionFlag(ctx, agent), first.agent_name || group.agent_id), stack(
        ...group.entries.filter(entry => !entry.profiles?.length).map(entry => node('div', { className: 'a-card-body' }, notice(entry.error || (entry.address_required ? '暂无接入地址' : '暂无可用接入配置')))),
        Boolean(rows.length) && records,
      ), actions(button('复制全部协议', event => void copyBatch(group.agent_id, event.currentTarget))));
      card.dataset.clientAgent = group.agent_id; card.dataset.refreshKey = group.agent_id;
      return card;
    });
    const content = groups.length ? cards : [stack(
      node('p', { className: 'a-empty' }, entries.length ? '没有符合条件的接入配置' : '部署服务端入站后生成客户端配置'),
      !entries.length && ctx.can('agents.read') && actions(link('前往节点设置', '#node-settings')))];
    patch(result, node('div', { className: 'a-stack' }, content));
  }
  search.querySelector('input').addEventListener('input', draw);
  for (const control of [agentFilter, format]) control.querySelector('select').addEventListener('change', () => { saveClientDisplayFormat(format.querySelector('select').value); draw(); });
  ctx.onRefresh(reload);
  draw();
  return stack(filterbar(node('div', { className: 'a-filter-fields a-traffic-filters' }, search, agentFilter, format), button('复制全部节点', event => void copyBatch('', event.currentTarget))), result);
}
