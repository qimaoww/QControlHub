import { node, field, stack, actions, muted, list } from './ui.js';

export function proxyIngressLabel(share) {
  const modes = [...new Set(list(share.proxy_ingresses).map(g => g.mode))].filter(mode => ['nginx', 'caddy'].includes(mode));
  const grants = modes.map(mode => mode === 'nginx' ? 'Nginx' : 'Caddy').join(' / ');
  const status = { upgrade_required: '需升级 Agent', pending: '等待节点同步', failed: '节点清理失败', applied: grants ? '授权已同步' : '入口已移除', idle: grants ? '等待部署' : '无入口引用' }[share.proxy_ingress_status];
  return [grants || '未授权', status].filter(Boolean).join(' · ');
}

export function proxyIngressEditor(grants, supported = true) {
  const selected = new Set(list(grants).map(g => g.mode));
  const choices = actions(...[['nginx', 'Nginx'], ['caddy', 'Caddy']].map(([mode, label]) =>
    field(label, 'proxy_mode', selected.has(mode), { type: 'checkbox', value: mode, disabled: !supported })));
  const hint = muted('');
  const element = node('details', { className: 'a-disclosure' }, node('summary', {}, '共享反代'),
    node('div', { className: 'a-disclosure-body' }, stack(choices, hint)));
  const setSupported = available => {
    supported = Boolean(available);
    choices.querySelectorAll('input').forEach(control => { control.disabled = !supported && !selected.has(control.value); });
    hint.textContent = supported ? '收回授权会清理对应入口；重新获授权后需重新部署。证书需另行授权。' : '可收回已有授权；新增共享反代需先升级 Agent。';
  };
  const value = () => [...choices.querySelectorAll('input:checked')].map(input => ({ mode: input.value, scope: 'type' }));
  setSupported(supported);
  return { element, value, setSupported };
}
