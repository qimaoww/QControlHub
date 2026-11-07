import { startAnalytics } from '../analytics/app.js';
import { assert, waitFor } from './assertions.mjs';

export async function testLogSourceStates() {
  window.dispatchEvent(new Event('pagehide'));
  const nativeFetch = globalThis.fetch;
  const session = { role: 'admin', user_id: 'log-source-states', username: 'source-review' };
  let sources = [], records = [];
  let settings = { core_log_minimum_level: 'warning', core_log_retention_days: 7 };
  const source = (engine, patch = {}) => ({ agent_id: 'alpha', name: 'ALPHA', engine, instance_id: 'main', status: 'active', last_log_at: '2026-09-07T00:03:00Z', owner_id: 'self', username: '主实例', grant_revision: '1', ...patch });
  const row = (patch = {}) => ({ id: 1, agent_id: 'alpha', engine: 'ss-rust', instance_id: 'main', level: 'error', message: 'retained SS Rust connection error', logged_at: '2026-09-07T00:04:00Z', ...patch });
  const reply = value => new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
  globalThis.fetch = (input, options) => {
    const path = new URL(input instanceof Request ? input.url : input, location.href).pathname;
    if (path === '/api/v1/auth/session') return Promise.resolve(reply(session));
    if (path === '/api/v1/settings') return Promise.resolve(reply(settings));
    if (path === '/api/v1/core-log-sources') return Promise.resolve(reply(sources));
    if (path === '/api/v1/core-logs') return Promise.resolve(reply(records));
    return nativeFetch(input, options);
  };
  const button = (text, root = document) => [...root.querySelectorAll('button')].find(item => item.textContent === text);
  const main = () => document.querySelector('.a-main');
  const dialog = () => document.querySelector('dialog[data-log-sources][open]');
  const boot = async () => {
    window.dispatchEvent(new Event('pagehide'));
    document.querySelectorAll('dialog[open]').forEach(box => box.close());
    document.body.replaceChildren(Object.assign(document.createElement('div'), { id: 'log-source-state-test' }));
    history.replaceState(null, '', '#core-logs');
    startAnalytics({ app: document.querySelector('#log-source-state-test'), session });
    await waitFor(() => document.querySelector('[data-log-filters]') && !main().querySelector('[aria-busy=true]'), 'native source state page did not load');
    if (button('暂停更新', main())) button('暂停更新', main()).click();
  };
  const refresh = async () => {
    button('刷新', document.querySelector('[data-log-filters]')).click();
    await waitFor(() => !document.querySelector('[data-log-refresh-label][aria-busy=true]'), 'source state refresh did not settle');
  };
  const open = () => { document.querySelector('[data-log-source-status]').click(); assert.ok(dialog(), 'collection state dialog missing'); };
  const close = () => dialog().close();
  const filter = async (name, value) => {
    const input = document.querySelector(`[data-log-filters] [name=${name}]`);
    input.value = value; input.dispatchEvent(new Event('change', { bubbles: true }));
    await waitFor(() => !document.querySelector('[data-log-refresh-label][aria-busy=true]'), 'scope change did not settle');
  };
  try {
    await boot(); open();
    assert.equal(dialog().querySelector('.a-dialog-body > .a-stack > .a-actions').textContent, '来源 0', 'empty source totals leaked a duplicate zero'); close();
    sources = [source('xray')]; await boot();
    for (const days of [1, 3, 7, 14, 30]) {
      settings = { ...settings, core_log_retention_days: days }; await boot(); open();
      assert.ok(dialog().textContent.includes(`保存警告及以上 · 保留 ${days} 天`), 'actual retention policy not displayed');
      assert.equal((dialog().textContent.match(/保留 \d+ 天/g) || []).length, 1, 'retention policy repeated'); close();
    }
    for (const days of [null, 0, -1, 1.5, 'unknown']) {
      settings = { ...settings, core_log_retention_days: days }; await boot(); open();
      assert.ok(!/保留 (?:\d+|null|undefined|unknown|NaN) 天/.test(dialog().textContent), 'unknown retention was fabricated'); close();
    }
    session.role = 'user'; session.permissions = ['core-logs.read']; await boot(); open();
    assert.ok(dialog().textContent.includes('保存策略不可见') && !dialog().textContent.includes('保存警告及以上'), 'settings policy leaked after read permission was revoked'); close();
    session.role = 'admin'; await boot();
    for (const [status, title] of [['not-configured', '未配置'], ['waiting', '等待日志'], ['pending', '等待日志'], ['not-installed', '内核尚未安装'], ['offline', '节点离线'], ['unsupported', 'Agent 功能不足'], ['status-unavailable', '日志状态能力不可用'], ['failed', '日志采集失败']]) {
      sources = [source('ss-rust', { status, error: 'collector-failed' })]; await refresh();
      assert.ok(main().textContent.includes(title), `empty source ${status} has the wrong state`);
      records = [row()]; await refresh();
      assert.ok(main().textContent.includes(records[0].message), `source ${status} hid retained history`);
      open(); assert.ok(dialog().textContent.includes(title), `source ${status} missing from collection dialog`); close(); records = [];
    }
    for (const last_log_at of ['', 'not-a-date']) {
      sources = [source('ss-rust', { last_log_at })]; await refresh();
      assert.ok(main().textContent.includes('尚未收到日志') && !main().textContent.includes('采集正常'), 'undelivered source was called healthy');
      open(); assert.ok(dialog().textContent.includes('尚无日志') && !dialog().textContent.includes('Invalid Date'), 'missing source timestamp was fabricated'); close();
    }
    sources = [source('xray', { last_log_at: '', core_log_last_at: '2026-09-05T00:05:00Z', core_log_source: 'file' })]; await refresh(); open();
    assert.ok(dialog().textContent.includes('日志文件') && !dialog().textContent.includes('尚无日志'), 'legacy source transport or delivery time was lost'); close();
    sources = [source('xray'), source('sing-box', { status: 'failed', error: 'token=fixture-secret', instance_id: 'shared-alice', username: 'alice' }), source('ss-rust', { status: 'not-installed' })];
    records = [row({ engine: 'xray', message: 'xray healthy' })]; await refresh(); open();
    assert.ok(dialog().textContent.includes('alice') && dialog().textContent.includes('日志采集失败') && !dialog().textContent.includes('fixture-secret'), 'shared source failure lost ownership or redaction'); close();
    await filter('engine', 'xray'); open();
    assert.ok(!dialog().textContent.includes('日志采集失败') && !dialog().textContent.includes('内核尚未安装'), 'out-of-scope sibling contaminated selected engine'); close();
    await filter('engine', ''); await filter('instance_id', 'shared-alice'); open();
    assert.ok(dialog().textContent.includes('alice') && dialog().textContent.includes('日志采集失败') && !dialog().textContent.includes('采集正常'), 'shared instance received the main source state'); close();
    await filter('instance_id', '');
    for (const instance_id of ['main', 'shared-alice']) for (const status of ['waiting', 'pending', 'not-configured', 'active']) {
      sources = [source('xray', { status: 'not-installed' }), source('ss-rust', { status, last_log_at: '', instance_id, username: 'alice' })];
      records = [row({ instance_id })]; await refresh();
      assert.ok(main().textContent.includes('retained SS Rust connection error'), 'mixed sources lost retained instance history');
      await filter('engine', 'ss-rust'); open();
      assert.ok(!dialog().textContent.includes('内核尚未安装') && !dialog().textContent.includes('采集正常'), 'missing sibling or historical rows established false collection health'); close(); await filter('engine', '');
    }
    assert.ok(document.documentElement.scrollWidth <= innerWidth, 'source states overflow horizontally');
  } finally { window.dispatchEvent(new Event('pagehide')); globalThis.fetch = nativeFetch; }
}
