import { nodeAddressSetting, publicAddressRows } from '../modules/agent-address-model.js';
import { node, field, muted, stack, pathID } from './ui.js';
import { patch } from './live-dom.js';

export function nodeAddress(ctx, agent, { available, form }) {
  let baseline = nodeAddressSetting(agent.labels), busy = false;
  const effective = node('div', { className: 'a-stack', role: 'status' });
  const values = () => ({ mode: form.elements.node_address_mode.value, address: form.elements.node_address.value.trim() });
  const same = (a, b) => a.mode === b.mode && a.address === b.address;
  const current = () => ctx.current() && form.isConnected && available();
  const fields = [
    field('地址模式', 'node_address_mode', baseline.mode, { options: [['auto', '自动获取'], ['manual', '手动设置']] }),
    field('节点默认连接地址', 'node_address', baseline.address, { maxlength: 253, placeholder: 'IPv4、IPv6 或域名' }),
    effective,
  ];
  async function save() {
    if (!current() || busy) return;
    const submitted = values();
    if (submitted.mode === 'manual' && !submitted.address) throw new Error('请输入节点默认连接地址');
    busy = true;
    try {
      const saved = await ctx.api(`/agents/${pathID(agent.id)}/client-address`, { method: 'PUT', body: { address: submitted.mode === 'manual' ? submitted.address : '' } });
      if (!current()) return;
      agent.labels = { ...agent.labels };
      for (const key of ['client_address', 'public_host', 'public_ip']) delete agent.labels[key];
      if (saved.address) agent.labels.client_address = saved.address;
      baseline = nodeAddressSetting(agent.labels);
      if (same(values(), submitted)) {
        form.elements.node_address_mode.value = baseline.mode;
        form.elements.node_address.value = baseline.address;
      }
      sync();
    } finally { busy = false; if (current()) sync(); }
  }
  form.dataset.nodeAddress = agent.id;
  const submit = form.querySelector('[type=submit]');
  function sync() {
    const manual = form.elements.node_address_mode.value === 'manual';
    form.elements.node_address.disabled = !manual;
    form.elements.node_address.required = manual;
    form.elements.node_address.setCustomValidity(manual && !form.elements.node_address.value.trim() ? '请输入节点默认连接地址' : '');
    const setting = nodeAddressSetting(agent.labels);
    patch(effective, node('div', { className: 'a-stack', role: 'status' }, muted(`当前模式 · ${setting.mode === 'manual' ? '手动设置' : '自动获取'}`),
      setting.address && stack(muted(`当前默认地址 · ${setting.source}`), node('div', { className: 'a-probe-address-value' }, node('span', { dir: 'ltr' }, setting.address))),
      ...publicAddressRows(agent.metrics, agent.labels, agent.features).map(row => muted(`${row.label} · ${row.value || '暂无公网地址'} · ${row.source}`))));
  }
  function update() {
    const latest = nodeAddressSetting(agent.labels);
    if (!busy && same(values(), baseline)) {
      form.elements.node_address_mode.value = latest.mode;
      form.elements.node_address.value = latest.address;
      baseline = latest;
    }
    sync();
  }
  form.addEventListener('input', sync);
  form.addEventListener('change', sync);
  submit.addEventListener('mutationend', sync);
  return { view: stack(node('h3', {}, '节点地址'), fields), save, update, dirty: () => !same(values(), baseline) };
}
