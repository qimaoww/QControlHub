import { clientAccessAddressChoices, clientAccessEntryForAddress, clientAccessExport, filterClientAccessEntries, loadClientDisplayFormat, saveClientDisplayFormat } from '../modules/client-access-model.js';
import { copyText, node, button, actions, stack, section, table, badge, field, muted, list, notice, link, pathID } from './ui.js';

export async function clientAccessPage(ctx) {
  let entries = list(await ctx.api('/client-access'));
  const agents = ctx.can('agents.read') ? list(await ctx.api('/agents')) : [];
  const search = field('搜索接入配置', 'search', '', { type: 'search', placeholder: '节点、协议、入站或地址' });
  const agentFilter = field('节点', 'agent', '', { options: [['', '全部节点'], ...[...new Map(entries.map(entry => [entry.agent_id, entry.agent_name || entry.agent_id]))]] });
  const engineFilter = field('内核', 'engine', '', { options: [['', '全部内核'], ...new Set(entries.map(entry => entry.engine))] });
  const format = field('导出格式', 'format', loadClientDisplayFormat(), { options: [['url', 'URL'], ['mihomo', 'Mihomo YAML']] });
  const result = node('div', { className: 'a-stack' });
  const addressModes = new Map();
  const urls = new Set(), timers = new Set();
  ctx.signal.addEventListener('abort', () => { urls.forEach(url => URL.revokeObjectURL(url)); timers.forEach(clearTimeout); urls.clear(); timers.clear(); }, { once: true });
  function download(value, name, type = 'text/plain;charset=utf-8') {
    const url = URL.createObjectURL(new Blob([value], { type })); urls.add(url);
    const anchor = node('a', { href: url, download: name }); document.body.append(anchor); anchor.click(); anchor.remove();
    const timer = setTimeout(() => { URL.revokeObjectURL(url); urls.delete(url); timers.delete(timer); }, 1000); timers.add(timer);
  }
  function copy(value, control) {
    return ctx.mutate(control, async () => { await copyText(String(value)); ctx.announce('已复制'); });
  }
  function secret(label, value, multiline = false) {
    const content = field(label, 'export', String(value ?? ''), multiline ? { multiline: 8, readonly: true, spellcheck: false } : { type: 'password', readonly: true, autocomplete: 'off' });
    const input = content.querySelector('input,textarea');
    let revealed = false;
    if (multiline) input.value = '••••••••';
    const toggle = button('显示', event => {
      const masked = !revealed; revealed = masked;
      if (multiline) input.value = revealed ? String(value ?? '') : '••••••••'; else input.type = revealed ? 'text' : 'password';
      event.currentTarget.textContent = masked ? '隐藏' : '显示'; event.currentTarget.setAttribute('aria-pressed', String(masked));
    }, { 'aria-pressed': 'false' });
    return stack(content, actions(toggle, button('复制', event => void copy(String(value ?? ''), event.currentTarget)),
      multiline && button('下载', () => download(String(value ?? ''), 'client-config.txt'))));
  }
  function details(entry, item) {
    const profile = item.profile || {};
    const exports = [];
    const primary = clientAccessExport(item, format.querySelector('select').value);
    if (primary.value) exports.push(secret(primary.format, primary.value, format.querySelector('select').value === 'mihomo'));
    if (primary.error) exports.push(notice(primary.error));
    const variants = profile.variants?.length ? profile.variants : profile.xray_json ? [{ name: 'Xray 客户端配置', profile }] : [];
    for (const variant of variants) {
      const value = variant.profile || {};
      if (!value.xray_json) continue;
      exports.push(section(variant.name || 'Xray JSON', node('div', { className: 'a-card-body' }, stack(
        value.uri_error && notice(value.uri_error), value.sing_box_error && notice(value.sing_box_error), value.mihomo_error && notice(value.mihomo_error),
        secret('完整 Xray JSON', value.xray_json, true), button('下载 JSON', () => download(value.xray_json, 'xray-client.json', 'application/json')),
      ))));
    }
    if (profile.uri && !primary.value && !profile.xray_json) exports.push(secret(profile.format || '原生配置', profile.uri, true));
    ctx.dialog(`${item.protocol || entry.engine} · 参数详情`, stack(
      actions(badge(entry.engine), muted(item.tag), muted(`端口 ${item.port}`)),
      ...list(profile.fields).map(part => part.secret ? secret(part.label, part.value) : field(part.label, 'parameter', part.value, { readonly: true })),
      ...exports,
    ), { wide: true });
  }
  function display(entry, item) {
    const selector = { engine: entry.engine, tag: item.tag, port: Number(item.port) };
    const originalAddress = item.address_overridden ? item.address || '' : '';
    const choices = clientAccessAddressChoices(entry);
    const form = ctx.form([
      muted(`${item.tag} · ${entry.engine} · 端口 ${item.port}`),
      field('客户端节点名称', 'name', item.client_name || '', { maxlength: 100, placeholder: '留空使用入站标签' }),
      field('客户端连接地址', 'address', originalAddress, { maxlength: 253, placeholder: '留空使用自动识别地址', hint: originalAddress ? null : `自动识别 · ${item.address || '未识别'}` }),
      choices.length && field('客户端地址协议栈', 'address_mode', item.address_mode || 'auto', { options: choices.map(choice => [choice.value, choice.label]), disabled: Boolean(item.address_overridden) }),
    ], '保存参数', async values => {
      const body = { profile: selector, name: values.name.trim() };
      if (values.address.trim() !== originalAddress) body.address = values.address.trim();
      if (values.address_mode && values.address_mode !== (item.address_mode || 'auto')) body.address_mode = values.address_mode;
      await ctx.api(`/agents/${pathID(entry.agent_id)}/client-address`, { method: 'PUT', body });
      if (!ctx.current()) return;
      ctx.setDirty(false); box.close(); await reload(); ctx.announce('客户端显示参数已保存');
    });
    form.addEventListener('input', () => ctx.setDirty(true));
    const box = ctx.dialog('修改显示参数', stack(form, item.address_overridden && button('恢复自动识别', async event => {
      const control = event.currentTarget;
      if (!await ctx.confirm('清除当前端口的手动连接地址？', '恢复自动识别') || !ctx.current()) return;
      await ctx.mutate(control, async () => {
        await ctx.api(`/agents/${pathID(entry.agent_id)}/client-address`, { method: 'PUT', body: { address: '', profile: selector } });
        ctx.setDirty(false); box.close(); await reload(); ctx.announce('已恢复自动识别');
      });
    })));
    box.onRequestClose = async () => {
      if (!await ctx.confirm('关闭显示参数？尚未保存的修改将丢弃。', '关闭') || !ctx.current()) return;
      ctx.setDirty(false); box.close();
    };
  }
  async function reload() {
    const received = list(await ctx.api('/client-access'));
    if (!ctx.current()) return;
    entries = received; draw();
  }
  function draw() {
    const shown = filterClientAccessEntries(entries, { query: search.querySelector('input').value, agent: agentFilter.querySelector('select').value, engine: engineFilter.querySelector('select').value });
    result.replaceChildren(...shown.map(entry => {
      // Bind the selection to the real instance identity, never to list order.
      const key = JSON.stringify([entry.agent_id, entry.engine, entry.instance_id || '']);
      const choices = clientAccessAddressChoices(entry);
      const selected = clientAccessEntryForAddress(entry, addressModes.get(key) || 'auto');
      const address = choices.length ? field('预览地址', 'address', addressModes.get(key) || 'auto', { options: choices.map(choice => [choice.value, choice.label]) }) : muted(selected.address || '暂无接入地址');
      address.querySelector('select')?.addEventListener('change', event => { addressModes.set(key, event.target.value); draw(); });
      const agent = agents.find(agent => agent.id === entry.agent_id);
      return section(entry.agent_name || entry.agent_id, stack(node('div', { className: 'a-card-body' }, address),
        entry.error && node('div', { className: 'a-card-body' }, notice(entry.error)),
        table(['入站', '协议', '客户端', '操作'], list(selected.profiles).map(item => {
          const exported = clientAccessExport(item, format.querySelector('select').value);
          return [stack(item.tag || '—', muted(`端口 ${item.port}`)), item.protocol || '—', item.client_name || entry.client_name || '—', actions(
            button('参数详情', () => details(entry, item)),
            button('复制', event => void copy(exported.value, event.currentTarget), { disabled: Boolean(exported.error || !exported.value) }),
            button('下载', () => download(exported.value, `client-${entry.engine}.${format.querySelector('select').value === 'mihomo' ? 'yaml' : 'txt'}`), { disabled: Boolean(exported.error || !exported.value) }),
            agent && ctx.can('agents.manage', agent) && button('修改显示参数', () => display(entry, item)),
          )];
        }), { label: '客户端接入' }),
        ctx.can('agent-config.read') && node('div', { className: 'a-card-body' }, link('服务端配置', `#live-config?agent=${pathID(entry.agent_id)}&engine=${pathID(entry.engine)}`)),
      ), badge(entry.engine));
    }));
    if (!shown.length) result.replaceChildren(stack(node('p', { className: 'a-empty' }, entries.length ? '没有符合条件的接入配置' : '部署服务端入站后生成客户端配置'), actions(button('清除筛选', () => {
      search.querySelector('input').value = ''; agentFilter.querySelector('select').value = ''; engineFilter.querySelector('select').value = ''; draw();
    }), link('前往节点设置', '#node-settings'))));
  }
  search.querySelector('input').addEventListener('input', draw);
  for (const control of [agentFilter, engineFilter, format]) control.querySelector('select').addEventListener('change', () => { saveClientDisplayFormat(format.querySelector('select').value); draw(); });
  draw();
  return stack(section(null, node('div', { className: 'a-card-body a-form-grid' }, search, agentFilter, engineFilter, format)), result);
}
