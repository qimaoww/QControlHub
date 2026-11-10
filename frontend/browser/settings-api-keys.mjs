import { startAnalytics } from '../analytics/app.js';
import { assert, waitFor, delay } from './assertions.mjs';

export async function testSettingsAPIKeysRuntime() {
  const nativeFetch = globalThis.fetch;
  const clipboard = Object.getOwnPropertyDescriptor(navigator, 'clipboard');
  const session = { user_id: 'key-owner', username: 'key-owner', role: 'admin', csrf_token: 'key-fixture' };
  const key = { id: 'first', name: '只读排查', prefix: 'qch_preview', status: 'active', revision: 3,
    permissions: ['agents.read'], ip_allowlist: ['192.0.2.10', '2001:db8:1234::/48'], disabled: false,
    created_at: '2026-11-05T08:10:30Z', expires_at: '2027-01-01T12:34:56.123Z', last_used_at: '2026-11-06T09:20:30Z' };
  let keys = [key, ...['expired', 'disabled', 'revoked'].map((status, i) => ({ ...key, id: status, name: `历史密钥 ${i}`, status, disabled: status === 'disabled', revoked_at: status === 'revoked' ? key.created_at : null, ip_allowlist: [], expires_at: null }))];
  let readError = 503, writeError = false, gate, release, copied, reads = 0;
  const writes = [];
  const response = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
  globalThis.fetch = async (url, options = {}) => {
    const path = String(url).replace('/api/v1', ''), method = options.method || 'GET';
    if (path === '/auth/session') return response(session);
    if (path === '/settings/check-update') {
      assert.equal(method, 'POST');
      assert.equal(new Headers(options.headers).get('X-QControlHub-CSRF'), session.csrf_token);
      return response({ update_available: true, latest_version: 'v-next', release_url: 'https://github.com/qimaoww/QControlHub/releases' });
    }
    if (!path.startsWith('/settings/api-keys')) return nativeFetch(url, options);
    if (method === 'GET') {
      reads++; if (gate) await gate;
      return readError ? response({ error: 'key list unavailable' }, readError) : response({ items: keys, read_permissions: ['agents.read', 'tasks.read'] });
    }
    assert.equal(new Headers(options.headers).get('X-QControlHub-CSRF'), session.csrf_token, 'API Key mutation lacks CSRF');
    const body = JSON.parse(options.body || '{}'); writes.push({ path, method, body });
    if (gate) await gate;
    if (writeError) return response({ error: 'key version conflict' }, 409);
    if (method === 'POST') {
      const created = { ...body, id: 'created', prefix: 'qch_created', status: 'active', revision: 1, created_at: key.created_at, disabled: false };
      keys.push(created); return response({ key: created, secret: 'synthetic-one-time-key' }, 201);
    }
    const item = keys.find(item => item.id === path.split('/').at(-1));
    assert.equal(body.revision ?? item.revision, item.revision, 'API Key mutation lost CAS');
    if (method === 'DELETE') Object.assign(item, { status: 'revoked', revoked_at: key.created_at });
    else Object.assign(item, body, { revision: item.revision + 1, status: body.disabled ? 'disabled' : 'active' });
    return response(item);
  };
  const button = (name, root = document) => [...root.querySelectorAll('button')].find(control => control.textContent === name);
  const section = () => [...document.querySelectorAll('.a-main section')].find(item => item.querySelector('h2')?.textContent === 'API Key');
  const row = id => [...section().querySelectorAll('tbody tr')].find(item => item.cells[0].textContent.includes(keys.find(key => key.id === id).name));
  const dialog = () => [...document.querySelectorAll('dialog[open]')].at(-1);
  const form = () => dialog()?.querySelector('form');
  const input = (name, value) => { form().elements.namedItem(name).value = value; form().dispatchEvent(new Event('input', { bubbles: true })); };
  const settled = () => waitFor(() => !document.querySelector('#app[aria-busy=true]'), 'API Key mutation remained pending');
  async function mount(next = session) {
    window.dispatchEvent(new Event('pagehide'));
    document.querySelectorAll('dialog').forEach(box => box.close());
    document.querySelector('#app').replaceChildren(); history.replaceState(null, '', '#api');
    startAnalytics({ app: document.querySelector('#app'), session: next });
    await waitFor(() => next.permissions?.includes('settings.manage') || next.role === 'admin' ? section() : document.querySelector('.a-main'), 'API page structure missing');
  }
  try {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { writeText: async value => { copied = value; } } });
    await mount();
    assert.ok(section().textContent.includes('无法读取 API Key') && button('创建 Key', section()).disabled, 'initial key failure has no scoped recovery');
    readError = 0; button('重新读取', section()).click(); await waitFor(() => row('first'), 'API Key retry failed'); await settled();
    assert.equal(document.querySelector('#panel-settings-form'), null, 'API page duplicated panel settings');
    assert.ok(row('first').textContent.includes('2001:db8:1234::/48') && row('first').textContent.includes('192.0.2.10') && row('first').textContent.includes('只读'), 'API Key omits source/permission scope');
    assert.ok(row('first').cells[0].textContent.includes('2026') && row('expired').textContent.includes('已过期') && row('disabled').textContent.includes('已停用'), 'key dates/statuses lost');
    assert.equal(row('revoked').querySelector('button'), null, 'revoked key retains mutation controls');
    button('创建 Key', section()).click(); await waitFor(() => form(), 'API Key create form missing');
    input('name', '新建密钥'); input('ip_allowlist', '192.0.2.11\n2001:db8::/32');
    form().elements.namedItem('permission:tasks.read').checked = false;
    form().elements.namedItem('permission:agents.read').checked = true;
    writeError = true; form().requestSubmit(); await waitFor(() => form()?.textContent.includes('数据已发生变化'), 'key save failure not visible'); await settled();
    assert.equal(form().elements.name.value, '新建密钥', 'key failure discarded draft');
    writeError = false; gate = new Promise(resolve => { release = resolve; });
    const before = writes.length; form().requestSubmit(); form().requestSubmit();
    await waitFor(() => writes.length === before + 1, 'key create request missing');
    assert.ok(form().querySelector('fieldset').disabled, 'pending key edit remains writable');
    dialog().querySelector('[aria-label=关闭]').click(); assert.ok(form(), 'pending key create closed');
    release(); gate = null; await waitFor(() => dialog()?.textContent.includes('密钥仅显示一次'), 'one-time key result missing'); await settled();
    assert.equal(writes.length, before + 1, 'duplicate key create request');
    assert.equal(writes.at(-1).body.permissions.join(','), 'agents.read'); assert.equal(writes.at(-1).body.ip_allowlist.join(','), '192.0.2.11,2001:db8::/32');
    const secret = dialog().querySelector('pre'); button('复制 Key', dialog()).click(); await waitFor(() => copied, 'key copy failed'); await settled();
    assert.equal(copied, 'synthetic-one-time-key');
    dialog().querySelector('[aria-label=关闭]').click(); await waitFor(() => secret.textContent === '', 'closed key result retains secret');
    assert.ok(!document.body.textContent.includes(copied), 'key secret leaked into listing');
    button('编辑授权', row('first')).click(); await waitFor(() => form(), 'key edit missing');
    input('name', '已更新排查密钥'); form().requestSubmit(); await waitFor(() => !dialog(), 'key edit did not finish'); await settled();
    assert.equal(writes.at(-1).body.expires_at, '2027-01-01T12:34:56.123Z', 'unchanged expiry lost seconds');
    assert.equal(writes.at(-1).body.revision, 3, 'key edit lost revision');
    assert.equal(writes.at(-1).body.ip_allowlist.join(','), '192.0.2.10,2001:db8:1234::/48', 'key edit lost allowlist');
    readError = 503; button('停用', row('first')).click(); await waitFor(() => section().textContent.includes('无法读取 API Key'), 'key readback failure missing'); await settled();
    assert.ok(row('first') && section().textContent.includes('已更新排查密钥'), 'transient read failure cleared valid keys');
    assert.equal(writes.at(-1).body.disabled, true); assert.equal(writes.at(-1).body.expires_at, key.expires_at);
    readError = 0; button('重新读取', section()).click(); await waitFor(() => button('启用', row('first')), 'key state retry did not refresh'); await settled();
    const count = writes.length; button('撤销', row('first')).click(); await waitFor(() => dialog(), 'key revoke confirmation missing');
    dialog().dispatchEvent(new Event('cancel', { cancelable: true })); dialog().close(); await delay(20);
    assert.equal(writes.length, count, 'canceling revoke mutated key');
    button('撤销', row('first')).click(); await waitFor(() => dialog(), 'key revoke did not reopen'); button('撤销 API Key', dialog()).click();
    await waitFor(() => !row('first').querySelector('button'), 'revoked key actions remain'); await settled();
    assert.equal(writes.at(-1).method, 'DELETE');
    // A denied read clears protected cached metadata; transient errors retain it.
    readError = 503; button('停用', row('created')).click(); await waitFor(() => section().textContent.includes('无法读取 API Key'), 'key readback recovery missing'); await settled();
    readError = 403; button('重新读取', section()).click(); await waitFor(() => button('创建 Key', section()).disabled, 'denied key read did not disable creation'); await settled();
    assert.equal(section().querySelector('tbody'), null, 'denied key read retained metadata');
    readError = 0; const beforeUser = reads;
    await mount({ ...session, user_id: 'shared-user', role: 'user', permissions: ['settings.read'] });
    assert.equal(section(), undefined, 'ordinary/shared user received administrator keys'); assert.equal(reads, beforeUser, 'ordinary/shared user requested administrator keys');
    gate = new Promise(resolve => { release = resolve; }); const old = reads;
    const mounting = mount(); await waitFor(() => reads > old, 'old account key read did not start');
    await mount({ ...session, user_id: 'readonly', role: 'user', permissions: ['settings.read'] }); release(); gate = null; await mounting; await delay(30);
    assert.equal(section(), undefined, 'late administrator response entered readonly account');
    assert.ok(document.documentElement.scrollWidth <= innerWidth + 1, 'settings page overflows');
  } finally {
    release?.(); window.dispatchEvent(new Event('pagehide')); document.querySelectorAll('dialog').forEach(box => box.close());
    globalThis.fetch = nativeFetch;
    if (clipboard) Object.defineProperty(navigator, 'clipboard', clipboard); else delete navigator.clipboard;
  }
}
