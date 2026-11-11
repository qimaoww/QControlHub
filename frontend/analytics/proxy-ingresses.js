import { node, field, grid, stack, actions, button, muted, list } from './ui.js';

export function proxyIngressLabel(share) {
  const grants = list(share.proxy_ingresses).map(g => `${g.mode === 'nginx' ? 'Nginx' : 'Caddy'} · ${g.port}`).join(' / ');
  const status = { pending: '等待节点同步', failed: '节点清理失败', applied: grants ? '授权已同步' : '入口已移除', idle: grants ? '等待部署' : '无入口引用' }[share.proxy_ingress_status];
  return [grants || '未授权', status].filter(Boolean).join(' · ');
}

export function proxyIngressEditor(grants, supported = true) {
  const rows = stack();
  function add(value = {}) {
    const row = grid(field('反代类型', 'proxy_mode', value.mode || 'nginx', { options: [['nginx', 'Nginx'], ['caddy', 'Caddy']], disabled: !supported }),
      field('公共端口', 'proxy_port', value.port || 443, { type: 'number', min: 1, max: 65535, required: true, disabled: !supported }));
    row.append(actions(button('移除入口', () => { row.remove(); rows.dispatchEvent(new Event('input', { bubbles: true })); })));
    rows.append(row);
  }
  list(grants).forEach(add);
  const addButton = button('添加入口', () => {
    if (rows.children.length >= 32) return;
    add(); rows.dispatchEvent(new Event('input', { bubbles: true }));
  }, { disabled: !supported });
  const element = node('details', { className: 'a-disclosure' }, node('summary', {}, '托管入口'),
    node('div', { className: 'a-disclosure-body' }, stack(rows, actions(addButton),
      muted(supported ? '收回入口会停止使用它的实例，重新获授权后需重新部署。证书需另行授权。' : '请先升级 Agent 以授权共用入口。'))));
  const setSupported = available => {
    supported = Boolean(available);
    addButton.disabled = !supported;
    rows.querySelectorAll('input,select').forEach(control => { control.disabled = !supported; });
    element.querySelector('.a-muted').textContent = supported ? '收回入口会停止使用它的实例，重新获授权后需重新部署。证书需另行授权。' : '请先升级 Agent 以授权共用入口。';
  };
  const value = () => [...rows.children].map(row => ({ mode: row.querySelector('[name=proxy_mode]').value, port: Number(row.querySelector('[name=proxy_port]').value) }));
  return { element, value, setSupported };
}
