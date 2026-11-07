import { assert, delay, waitFor } from './assertions.mjs';

export async function testNodeAddressRuntime({ testAPI, mode }) {
  const agent = testAPI.agents.find(agent => agent.id === 'alpha');
  agent.labels = { client_address: 'old.example', public_host: 'legacy.example', public_ip: '9.9.9.9', client_name: '保留名称', client_address_mode: 'ipv6' };
  agent.metrics = { public_ipv4: '93.184.216.34', public_ipv6: '2606:4700:4700::1111' };
  location.hash = '#settings-node-alpha';
  const session = await (await fetch('/api/v1/auth/session')).json();
  (await import('../analytics/app.js')).startAnalytics({ app: document.querySelector('#app'), session });
  const form = await waitFor(() => document.querySelector('[data-node-address=alpha]'), '节点地址未加载');
  const dialog = form.closest('dialog'), input = form.elements.node_address, picker = form.elements.node_address_mode;
  const change = (control, value) => { control.value = value; control.dispatchEvent(new Event('input', { bubbles: true })); control.dispatchEvent(new Event('change', { bubbles: true })); };
  const settle = async () => { await waitFor(() => !document.querySelector('#app[aria-busy=true]'), '地址保存未结束'); await delay(30); };
  const refresh = async () => { document.dispatchEvent(new Event('visibilitychange')); await delay(160); };
  const save = async () => {
    form.requestSubmit();
    const confirm = await waitFor(() => [...document.querySelectorAll('dialog[open]')].find(box => box !== dialog), '缺少地址影响确认');
    confirm.querySelector('.a-dialog-body .a-actions button:last-child').click();
    await settle();
  };
  assert.equal(picker.value, 'manual'); assert.equal(input.value, 'old.example');
  change(input, 'draft.example'); input.focus(); input.setSelectionRange(2, 6);
  await delay(400); dialog.scrollTop = 100; const scroll = dialog.scrollTop;
  await refresh();
  assert.equal(input.value, 'draft.example'); assert.equal(document.activeElement, input); assert.equal(input.selectionStart, 2); assert.equal(dialog.scrollTop, scroll);
  testAPI.profileSaveFailure = true;
  await save(); assert.equal(input.value, 'draft.example'); assert.equal(agent.labels.client_address, 'old.example');
  assert.match(form.querySelector(':scope > .a-notice').textContent, /服务暂不可用/);
  testAPI.profileSaveFailure = false;
  await save(); assert.equal(agent.labels.client_address, 'draft.example'); assert.equal(agent.labels.public_host, undefined); assert.equal(agent.labels.public_ip, undefined);
  assert.equal(agent.labels.client_name, '保留名称'); assert.equal(agent.labels.client_address_mode, 'ipv6');
  change(picker, 'auto'); assert.ok(input.disabled);
  await save(); assert.equal(agent.labels.client_address, undefined); assert.equal(picker.value, 'auto');
  assert.match(form.textContent, /93\.184\.216\.34/); assert.match(form.textContent, /自动获取/);
  change(picker, 'manual'); change(input, '');
  const before = testAPI.profileSaves.length;
  form.requestSubmit(); await delay(30); assert.equal(testAPI.profileSaves.length, before);
  change(input, 'a'.repeat(60) + '.example');
  agent.status = 'offline'; agent.metrics = {}; agent.features = [];
  await refresh(); assert.match(form.textContent, /暂无公网地址/); assert.match(form.textContent, /公网探测未启用/);
  assert.equal(input.value, 'a'.repeat(60) + '.example');
  assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, '地址设置导致页面溢出');
  assert.ok(form.scrollWidth <= form.clientWidth + 1, '地址表单溢出');
  // A confirmed write with a failed readback has a separate outcome.
  const originalFetch = window.fetch;
  window.fetch = async (url, options) => {
    const response = await originalFetch(url, options);
    if (String(url).endsWith('/client-address') && options?.method === 'PUT') testAPI.agentsFailure = true;
    return response;
  };
  await save(); assert.equal(agent.labels.client_address, input.value); assert.match(document.querySelector('.a-toast-region').textContent, /地址已保存，节点刷新失败/);
  testAPI.agentsFailure = false; window.fetch = originalFetch;
  change(picker, 'auto'); await save();
  assert.equal(agent.labels.client_address, undefined);
  await refresh(); assert.equal(picker.value, 'auto');
  // Revoked node authority closes the draft, so no stale target can submit.
  agent.can_manage = false; await refresh(); await waitFor(() => !dialog.open, '权限失效后仍保留地址表单');
  if (new URLSearchParams(location.search).has('preview')) {
    agent.can_manage = true; await refresh();
    const control = [...document.querySelectorAll('article[data-agent-id=alpha] button')].find(button => button.textContent === '查看设置');
    control.click(); await waitFor(() => document.querySelector('[data-node-address=alpha]'), '预览设置未打开');
  }
}
