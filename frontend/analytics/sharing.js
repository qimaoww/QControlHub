import { proxyIngressEditor, proxyIngressLabel } from "./proxy-ingresses.js";
import { preparedDialog, readRegion } from "./loading.js";
import { parseSharedPorts, formatSharedPorts, selectedSharedEngines, sharedLimitBytes, sharedLimitGiB, agentShareStatus } from '../modules/user-model.js';
import { node, button, actions, stack, grid, section, field, muted, notice, pathID, list } from './ui.js';
import { bytes } from './operations.js';

export function editAgentSharing(ctx, agent) {
  if (agent.can_manage === false || !ctx.can('agents.manage', agent)) return;
  return preparedDialog(ctx, `${agent.name} · 共享 Agent`, ['用户名', '可用内核', '端口', '总额度 · GiB', 'HTTP-01 允许域名'], local => editAgentSharingLoaded(local, agent), { submit: '保存共享设置', closeWhilePending: true });
}
async function editAgentSharingLoaded(ctx, agent) {
  if (agent.can_manage === false || !ctx.can('agents.manage', agent)) return;
  const data = ctx.state.data, current = () => ctx.current() && ctx.state.data === data;
  const path = `/agents/${pathID(agent.id)}/sharing`;
  let saved = await ctx.api(path);
  if (!current()) return;
  const drafts = data.agentSharingDrafts ||= new Map();
  const draft = drafts.get(agent.id);
  const rows = node('div', { className: 'a-stack' });
  const supported = ['shared-traffic-v1', 'shared-engines-v1', 'independent-egress-v1'].every(feature => agent.features?.includes(feature));
  let baseline, revision = draft?.revision ?? saved.revision, changed = false, saving = false, reading = false, available = true, edits = 0;
  const values = () => [...rows.children].map(row => ({
    username: row.querySelector('[name=username]').value,
    enabled: row.querySelector('[name=enabled]').checked,
    engines: [...row.querySelectorAll('[name=engines]:checked')].map(input => input.value),
    ports_text: row.querySelector('[name=ports]').value,
    limit_gib: row.querySelector('[name=limit_gib]').value,
    http_domains_text: row.querySelector('[name=http_domains]').value,
    proxy_ingresses: row.ingressEditor.value(),
    reinvite: row.dataset.reinvite === 'true',
  }));
  function markChanged(event) {
    if (!current()) return;
    if (event) { edits++; form.querySelector(':scope > [role=alert]').textContent = ''; }
    const snapshot = values();
    changed = JSON.stringify(snapshot) !== baseline;
    if (changed) drafts.set(agent.id, { revision, rows: snapshot, baseline });
    else drafts.delete(agent.id);
    ctx.setDirty(changed);
    submit.disabled = !available || !supported || !changed || saving || reading;
  }
  const reload = button('重新读取', async () => {
    if (!current() || reading || saving) return;
    reading = true; reload.disabled = true; markChanged();
    try {
      if (changed && !await ctx.confirm('重新读取将丢弃此节点未保存的共享设置。', '重新读取') || !current()) return;
      await reloadRead.load();
    } finally {
      reading = false;
      if (current()) { reload.disabled = false; markChanged(); }
    }
  });
  const reloadRead = readRegion(ctx, '共享设置', async signal => {
    const previous = edits;
    const received = await ctx.api(path, { signal });
    return { received, previous };
  }, ({ received, previous }) => {
    if (previous !== edits) throw Object.assign(new Error('共享设置已修改，当前草稿已保留。'), { readOnly: true });
    available = true; addUser.disabled = false; adopt(received);
  }, { retry: false, showRefresh: true, clear() {
    available = false; rows.replaceChildren(); baseline = JSON.stringify(values());
    drafts.delete(agent.id); addUser.disabled = true; markChanged();
  } });
  const addUser = button('添加用户', () => { if (!available) return; edits++; add({ enabled: true }); markChanged(); });
  const form = ctx.form([
    rows, reloadRead.element, actions(addUser, reload),
  ], '保存共享设置', async () => {
    if (!current() || saving || reading || !available || !supported || !changed) return;
    const shares = values().map((row, index) => ({
      username: row.username.trim(), enabled: row.enabled,
      ports: parseSharedPorts(row.ports_text), limit_bytes: sharedLimitBytes(row.limit_gib),
      engines: selectedSharedEngines(rows.children[index]),
      http_domains: row.http_domains_text.split(/[\n,，]+/).map(value => value.trim()).filter(Boolean),
      proxy_ingresses: row.proxy_ingresses,
      reinvite: row.reinvite,
    }));
    saving = true;
    try {
      const received = await ctx.api(path, { method: 'PUT', body: { revision, shares } });
      if (!current()) return;
      adopt(received); ctx.announce('共享设置已保存');
    } finally { saving = false; }
  });
  function add(share) {
    const supportedEngines = agent.supported_capabilities || agent.capabilities || [];
    const choices = [...new Set([...supportedEngines, ...list(share.engines)])];
    const engines = node('div', { className: 'a-actions' }, choices.map(engine =>
      field(supportedEngines.includes(engine) ? engine : `${engine} · 不可用`, 'engines', list(share.engines).includes(engine), { type: 'checkbox', value: engine })));
    const shareState = muted(agentShareStatus(share));
    const ingress = proxyIngressEditor(share.proxy_ingresses, agent.features?.includes('shared-proxy-ingress-v2'));
    const row = section(share.username || '新用户', node('div', { className: 'a-card-body a-stack' },
      grid(field('用户名', 'username', share.username || '', { required: true, maxlength: 64, readonly: Boolean(share.user_id) }),
        field('总额度 · GiB', 'limit_gib', share.limit_gib ?? sharedLimitGiB(share.limit_bytes), { type: 'number', min: 0, max: 8388607, step: 'any', required: true })),
      field('内核端口', 'ports', share.ports_text ?? formatSharedPorts(share.ports), { placeholder: '21000-21100, 22000', hint: '留空未分配，0 表示不限端口' }), engines,
      field('HTTP-01 允许域名', 'http_domains', share.http_domains_text ?? list(share.http_domains).join('\n'), { multiline: 2 }),
      ingress.element,
      actions(field('启用共享', 'enabled', share.enabled !== false, { type: 'checkbox' }),
        share.status === 'rejected' && button('重新邀请', event => {
          row.dataset.reinvite = 'true'; event.currentTarget.disabled = true;
          row.querySelector('[name=enabled]').checked = true; shareState.textContent = '待发送'; edits++; markChanged();
        }, { disabled: Boolean(share.reinvite) }),
        !share.user_id && button('移除', () => { row.remove(); edits++; markChanged(); })),
      muted(`共享反代 · ${proxyIngressLabel(share)}`),
      actions(shareState, share.user_id && muted(`已用 ${bytes(share.used_bytes)}`)),
    ));
    row.ingressEditor = ingress;
    row.dataset.reinvite = String(Boolean(share.reinvite));
    rows.append(row);
  }
  function adopt(received) {
    const unchanged = rows.children.length === received.shares.length && !changed && JSON.stringify(saved.shares) === JSON.stringify(received.shares);
    saved = received; revision = saved.revision;
    if (!unchanged) { rows.replaceChildren(); saved.shares.forEach(add); }
    baseline = JSON.stringify(values()); changed = false;
    drafts.delete(agent.id); ctx.setDirty(false); form.querySelector(':scope > [role=alert]').textContent = ''; markChanged();
  }
  (draft ? draft.rows.map(row => ({ ...saved.shares.find(share => share.username === row.username), ...row })) : saved.shares).forEach(add);
  baseline = draft?.baseline ?? JSON.stringify(values());
  const submit = form.querySelector('button[type=submit]');
  form.addEventListener('input', markChanged);
  form.addEventListener('change', markChanged);
  submit.addEventListener('mutationend', () => markChanged());
  markChanged();
  const box = ctx.dialog(`${agent.name} · 共享 Agent`, stack(!supported && notice('请先升级 Agent，再启用共享'), form), { wide: true });
  box.onRequestClose = () => {
    if (saving) { ctx.announce('操作正在提交，请等待结果。', 'warn'); return; }
    markChanged(); ctx.setDirty(false); box.close();
  };
  box.addEventListener('close', () => { if (current()) ctx.setDirty(false); }, { once: true });
}
