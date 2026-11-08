import { node, field, button, notice, muted, actions, list } from './ui.js';

const prefix = '/etc/qcontrolhub/panel-certificates/';
export function bindAnalyticsCertificateSelector(ctx, { form, protocol, plan = {} }) {
  const group = ['vless-xhttp-five','vless-xhttp-tls-reality','vless-xhttp-reality-tls'].includes(protocol?.key);
  if (!form || !group && (protocol?.uses_reality || !(protocol?.supports_tls || protocol?.requires_tls)) || protocol?.uses_wireguard) return;
  const input = name => form.elements?.namedItem(name) || form.querySelector(`[name="${name}"]`);
  const certificatePath = input('certificate_path');
  if (!certificatePath || form.querySelector('[data-analytics-certificate-selector]')) return;
  let select = input('panel_certificate_id');
  let wrapper = select?.closest('.a-field,.a-check');
  if (!select) {
    wrapper = field('面板证书', 'panel_certificate_id', '', { options: [['', '节点已有证书 / 原有 TLS 方式']] });
    certificatePath.closest('.a-field,.a-check').before(wrapper);
    select = wrapper.querySelector('select');
  }
  wrapper.dataset.analyticsCertificateSelector = '';
  const summary = notice('');
  const retry = button('重试', () => void refresh());
  const feedback = node('div', { className: 'a-stack' }, summary, actions(retry));
  wrapper.after(feedback);
  const sniField = !input('tls_server_name') && field('TLS SNI / 证书域名', 'tls_server_name', plan.tls_server_name || '', { placeholder: '例如 edge.example.com' });
  if (sniField) feedback.append(sniField);
  const initial = plan.panel_certificate_id || certificatePath.value.match(/^\/etc\/qcontrolhub\/panel-certificates\/([^/]+)\//)?.[1] || '';
  const localPaths = new Map(['certificate_path', 'private_key_path'].map(name => [name, input(name)?.value.startsWith(prefix) ? '' : input(name)?.value || '']));
  let managed = Boolean(initial), items = [], loading = true, failed = false, signature = '', sequence = 0, timer;
  const live = () => ctx.current() && form.isConnected && wrapper.isConnected;
  const availability = cert => !cert ? '证书已删除或授权已撤销' : !cert.not_after || !cert.version ? '尚未签发' : !Number.isFinite(Date.parse(cert.not_after)) ? '有效期无效' : Date.parse(cert.not_before) > Date.now() ? '尚未生效' : Date.parse(cert.not_after) <= Date.now() ? '已过期' : '';
  const matches = (domain, name) => domain === name || domain.startsWith('*.') && name.endsWith(domain.slice(1)) && name.split('.').length === domain.split('.').length;
  const description = cert => {
    const reason = availability(cert);
    const renewal = ({queued:'等待更新',running:'更新中',retrying:'更新重试中',validating:'更新验证中',waiting_dns:'等待 DNS 验证',failed:'更新失败',canceled:'更新已取消',canceling:'取消更新中'})[cert.status];
    const sync = ['caddy','node'].includes(cert.source) && ({pending:'等待来源更新',synced:'来源已同步',blocked:'来源更新受阻',paused:'来源同步已暂停',detached:'来源已解除',unverified:'来源待核实'})[cert.sync_status];
    const bindings = cert.binding_statuses || {};
    const targetLabels = {pending:'等待同步',running:'同步中',synced:'已同步',failed:'同步失败',blocked:'同步受阻',disabled:'未启用同步'};
    return [list(cert.domains).filter(domain => domain.toLowerCase() !== cert.name?.trim().toLowerCase()).join('、'), ({node:'节点证书',caddy:'Caddy',acme:'ACME',import:'导入','self-signed':'自签'})[cert.source] || cert.source,
      reason || (Date.parse(cert.not_after) - Date.now() < 30 * 86400000 ? '临近到期' : '有效'),
      cert.not_after && Number.isFinite(Date.parse(cert.not_after)) && `${new Date(cert.not_after).toLocaleDateString('zh-CN')} 到期`, sync,
      renewal && `${renewal}${reason ? '' : '，现有证书有效'}`, bindings.synced && (bindings.pending || bindings.running || bindings.failed || bindings.blocked) && '部分完成',
      ...Object.entries(bindings).filter(([,count]) => count).map(([key,count]) => `${targetLabels[key] || key} ${count}`),
    ].filter(Boolean).join(' · ');
  };
  function mode() {
    const external = protocol.supports_managed_tls && ['caddy','nginx','external'].includes(input('tls_mode')?.value);
    wrapper.hidden = feedback.hidden = external;
    select.disabled = external;
  }
  function update(event, refreshOnly = false) {
    const id = select.value, active = Boolean(id), cert = items.find(item => item.id === id);
    const sni = input('tls_server_name');
    if (event && active && cert) {
      const names = list(cert.domains).filter(name => !name.startsWith('*.'));
      if (sni && !sni.value && (group || names.length === 1)) sni.value = names[0] || '';
      const reality = input('reality_server_name');
      if (group && reality && !reality.value) reality.value = names.find(name => name !== sni?.value) || '';
    }
    const name = sni?.value.trim().toLowerCase();
    const realityName = input('reality_server_name')?.value.trim().toLowerCase();
    const reason = active && !loading && !failed ? availability(cert) || (!name ? '请填写 TLS SNI' : '') || (!list(cert.domains).some(domain => matches(domain.toLowerCase(), name)) ? '证书不覆盖当前 TLS SNI' : '') || (group && (!realityName || realityName === name || !list(cert.domains).some(domain => matches(domain.toLowerCase(), realityName))) ? '请选择证书覆盖的不同 Reality 域名' : '') : '';
    select.setCustomValidity(reason || (active && loading ? '正在加载证书，请稍候' : '') || (active && failed ? '证书列表加载失败，请重试' : ''));
    const text = loading ? '正在加载证书…' : failed ? '证书列表加载失败，已保留选择' : reason || (cert ? description(cert) : items.length ? '使用节点原有证书配置' : '暂无可选证书，可使用节点原有配置');
    if (summary.textContent !== text) summary.textContent = text;
    select.title = cert ? `${cert.name || list(cert.domains).join('、')} · ${description(cert)}` : text;
    retry.hidden = !failed;
    if (refreshOnly) return;
    const tlsMode = input('tls_mode');
    if (active && tlsMode && event) { tlsMode.value = 'native'; tlsMode.dispatchEvent(new Event('change', { bubbles: true })); }
    const tls = input('tls_enabled');
    if (active && tls && !tls.checked && !group) { tls.checked = true; tls.dispatchEvent(new Event('change', { bubbles: true })); }
    for (const [name,file] of [['certificate_path','cert.pem'],['private_key_path','key.pem']]) {
      const control = input(name); if (!control) continue;
      if (active) { if (!managed) localPaths.set(name, control.value); control.value = `${prefix}${id}/${file}`; }
      else if (managed) control.value = localPaths.get(name) || '';
      control.readOnly = active;
      control.closest('.a-field,.a-check').hidden = active;
    }
    managed = active;
    mode();
  }
  if (initial && ![...select.options].some(option => option.value === initial)) select.add(new Option('正在加载所选证书…', initial));
  select.value = initial;
  select.addEventListener('change', event => update(event));
  input('tls_mode')?.addEventListener('change', mode);
  for (const name of ['tls_server_name','reality_server_name']) input(name)?.addEventListener('input', () => update(undefined, true));
  function options() {
    const next = JSON.stringify(items.map(cert => [cert.id, cert.owned, cert.name, description(cert)]));
    if (next === signature || document.activeElement === select || select.contains(document.activeElement) || select.matches(':open')) return;
    signature = next;
    const value = select.value;
    const picker = select.querySelector(':scope > button');
    select.replaceChildren(...(picker ? [picker] : []), new Option('节点已有证书 / 原有 TLS 方式', ''));
    for (const owned of [true,false]) {
      const group = node('optgroup', { label: owned ? '我的证书' : '授权给我的证书' });
      for (const cert of items.filter(item => item.owned === owned)) {
        const option = new Option(cert.name?.trim() || list(cert.domains).join('、'), cert.id);
        option.disabled = Boolean(availability(cert));
        option.title = description(cert); group.append(option);
      }
      if (group.childElementCount) select.append(group);
    }
    if (value && !items.some(cert => cert.id === value)) { const missing = new Option('所选证书不可用',value); missing.disabled = true; select.add(missing); }
    select.value = value;
  }
  async function refresh() {
    clearTimeout(timer);
    if (!live()) return;
    const request = ++sequence;
    retry.disabled = true;
    try {
      const resource = await ctx.api('/certificates');
      if (!live() || request !== sequence) return;
      items = list(resource.items); loading = false; failed = false;
      options();
    } catch (error) { if (!live() || request !== sequence || error.name === 'AbortError') return; loading = false; failed = true; }
    finally {
      if (live() && request === sequence) { retry.disabled = false; update(undefined, true); timer = setTimeout(refresh, 15000); }
    }
  }
  select.addEventListener('blur', () => { if (live()) options(); });
  ctx.signal.addEventListener('abort', () => { sequence++; clearTimeout(timer); }, { once: true });
  form.closest('dialog')?.addEventListener('close', () => { sequence++; clearTimeout(timer); }, { once: true });
  update();
  // The caller opens its dialog in the same turn; delayed responses still
  // require the original route, account and connected form.
  queueMicrotask(() => { if (live()) void refresh(); });
  return { refresh, validate: () => update(undefined, true) };
}
