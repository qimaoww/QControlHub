import { startAnalytics } from '../analytics/app.js';
import { setStorageAccount, accountStorage } from '../modules/account-storage.js';
import { assert, waitFor, delay } from './assertions.mjs';

export async function testLogReadOwnership() {
  window.dispatchEvent(new Event('pagehide'));
  const nativeFetch = globalThis.fetch;
  let session = { role: 'admin', user_id: 'native-log-ownership', username: 'log-owner' };
  const source = agent => ({ agent_id: agent, name: agent.toUpperCase(), engine: 'xray', instance_id: 'main', owner_id: session.user_id, username: session.username, status: 'active', grant_revision: '1' });
  let sources = ['alpha', 'beta', 'gamma'].map(source), directory = deferred(), windows = new Map(), denied = false;
  const calls = [], failedWindows = new Set();
  const entries = (agent, count) => Array.from({ length: count }, (_, index) => ({ id: index + 1, agent_id: agent, engine: 'xray', instance_id: 'main', level: 'info', message: `${agent} authorized log ${index}`, logged_at: '2026-09-07T00:00:00Z' }));
  const reply = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
  globalThis.fetch = async (input, options = {}) => {
    const url = new URL(input instanceof Request ? input.url : input, location.href), path = url.pathname;
    if (path === '/api/v1/auth/session') return reply(session);
    if (path === '/api/v1/settings') return reply({});
    if (path === '/api/v1/preferences/node-order') return reply({ ids: ['gamma', 'beta', 'alpha'], revision: 1 });
    if (path === '/api/v1/core-log-sources') {
      const item = { kind: 'directory', signal: options.signal }; calls.push(item);
      const snapshot = structuredClone(sources), gate = directory;
      if (gate) await gate.promise;
      // Deliberately ignore abort to prove view ownership rejects late results.
      return denied ? reply({ error: '日志权限已撤销' }, 403) : reply(snapshot);
    }
    if (path === '/api/v1/core-logs') {
      const agent = url.searchParams.get('agent_id') || sources[0]?.agent_id || '', limit = Number(url.searchParams.get('limit'));
      calls.push({ kind: 'window', agent, instance: url.searchParams.get('instance_id'), engine: url.searchParams.get('engine'), limit, signal: options.signal });
      const gate = windows.get(`${agent}:${limit}`);
      const result = gate ? await gate.promise : agent ? entries(agent, limit).map(record => ({ ...record, instance_id: url.searchParams.get('instance_id') || 'main' })) : [];
      return failedWindows.has(`${agent}:${limit}`) ? reply({ error: '所选日志窗口读取失败' }, 503) : reply(result);
    }
    return nativeFetch(input, options);
  };
  const main = () => document.querySelector('.a-main');
  const rows = () => document.querySelectorAll('[data-log-entry]');
  const ready = () => !main()?.querySelector('[aria-busy=true]') && !document.querySelector('[data-log-refresh-label][aria-busy=true]');
  const boot = (hash = '#core-logs') => {
    window.dispatchEvent(new Event('pagehide'));
    setStorageAccount(session);
    accountStorage.setItem('qcontrolhub:core-log-preferences', JSON.stringify({ agent_id: 'alpha', limit: 2000, auto_refresh: false }));
    document.body.replaceChildren(Object.assign(document.createElement('div'), { id: 'native-log-read-test' }));
    history.replaceState(null, '', hash);
    startAnalytics({ app: document.querySelector('#native-log-read-test'), session });
  };
  const refresh = () => [...document.querySelectorAll('[data-log-filters] button')].find(button => button.textContent === '刷新').click();
  const choose = agent => {
    const control = document.querySelector('[data-log-filters] [name=agent_id]');
    control.value = agent; control.dispatchEvent(new Event('change', { bubbles: true }));
  };
  try {
    windows.set('alpha:200', deferred()); windows.set('alpha:2000', deferred());
    boot(); await waitFor(() => calls.length === 1, 'authorization directory did not gate initial read');
    assert.equal(rows().length, 0, 'unvalidated cached logs appeared');
    assert.equal(calls.filter(call => call.kind === 'window').length, 0, 'logs were requested before authorization');
    directory.resolve(); directory = null;
    await waitFor(() => calls.filter(call => call.kind === 'window').length === 2, 'preview and full windows did not start concurrently');
    assert.equal(document.querySelector('[name=agent_id]').options[1].value, 'gamma', 'log node choices ignored the server order');
    windows.get('alpha:200').resolve(entries('alpha', 200));
    await waitFor(() => rows().length === 200 && main().textContent.includes('正在补齐'), 'preview did not display before the complete window');
    windows.get('alpha:2000').resolve(entries('alpha', 2000));
    await waitFor(() => ready() && main().textContent.includes('2000 条日志'), 'complete authorized window did not replace preview'); windows.clear();
    windows.set('gamma:200', deferred()); windows.set('gamma:2000', deferred()); failedWindows.add('gamma:2000');
    choose('gamma'); await waitFor(() => calls.filter(call => call.kind === 'window' && call.agent === 'gamma').length === 2, 'partial window did not start both reads');
    windows.get('gamma:200').resolve(entries('gamma', 200));
    await waitFor(() => rows().length === 200 && main().textContent.includes('正在补齐'), 'partial preview was not displayed');
    windows.get('gamma:2000').resolve([]);
    await waitFor(() => ready() && main().textContent.includes('补齐失败，当前仅显示部分日志'), 'failed full read mislabeled the partial preview');
    assert.ok([...rows()].every(row => row.textContent.includes('gamma authorized log')), 'failed completion discarded useful preview');
    windows.clear(); failedWindows.clear(); refresh();
    await waitFor(() => ready() && main().textContent.includes('2000 条日志') && !main().textContent.includes('补齐失败'), 'partial preview did not recover to complete window');
    choose('alpha'); await waitFor(() => ready() && rows().length === 200 && document.querySelector('[name=agent_id]').value === 'alpha', 'return from partial-read recovery failed');
    // Leaving and returning may reuse an account cache only after fresh grants.
    accountStorage.setItem('qcontrolhub:core-log-preferences', JSON.stringify({ limit: 100, auto_refresh: true }));
    location.hash = '#themes'; await waitFor(() => document.querySelector('.a-preview'), 'route departure failed');
    directory = deferred(); const beforeReturn = calls.length; location.hash = '#core-logs';
    await waitFor(() => calls.length > beforeReturn, 'return did not verify authorization');
    assert.equal(rows().length, 0, 'cached logs bypassed authorization on return');
    directory.resolve(); directory = null; await waitFor(() => ready() && rows().length === 200, 'authorized cached scope did not recover');
    assert.equal(document.querySelector('[name=limit]').value, '2000', 'stored preferences replaced the current session window');
    assert.equal(document.querySelector('[name=agent_id]').value, 'alpha', 'stored preferences replaced the current node');
    assert.ok([...document.querySelectorAll('[data-log-filters] button')].some(button => button.textContent === '自动更新'), 'stored preferences resumed a paused session');
    const search = document.querySelector('[data-log-filters] [name=q]');
    search.value = 'alpha'; search.dispatchEvent(new Event('input', { bubbles: true }));
    await waitFor(() => JSON.parse(accountStorage.getItem('qcontrolhub:core-log-preferences')).q === 'alpha', 'keyword edits were not remembered');
    const engine = document.querySelector('[data-log-filters] [name=engine]');
    engine.value = 'xray'; engine.dispatchEvent(new Event('change', { bubbles: true }));
    assert.equal(JSON.parse(accountStorage.getItem('qcontrolhub:core-log-preferences')).engine, 'xray', 'engine edits were not remembered');
    [...document.querySelectorAll('[data-log-filters] button')].find(button => button.textContent === '清除筛选').click();
    const cleared = JSON.parse(accountStorage.getItem('qcontrolhub:core-log-preferences'));
    assert.ok(cleared.agent_id === 'alpha' && cleared.limit === 2000 && cleared.auto_refresh === false && !cleared.engine && !cleared.q, 'clearing local filters lost the session scope/window/pause');
    location.hash = '#themes'; await waitFor(() => document.querySelector('.a-preview'), 'grant-change route departure failed');
    sources = sources.map(source => ({ ...source, grant_revision: '2' }));
    windows.set('alpha:200', deferred()); windows.set('alpha:2000', deferred());
    const beforeGrantReturn = calls.filter(call => call.kind === 'window').length;
    location.hash = '#core-logs';
    await waitFor(() => calls.filter(call => call.kind === 'window').length > beforeGrantReturn, 'grant-change reentry did not start fresh window');
    assert.equal(rows().length, 0, 'return reused a cached window from an older grant revision');
    await waitFor(() => calls.filter(call => call.kind === 'window').length === beforeGrantReturn + 2, 'grant-change reentry skipped fresh preview');
    windows.get('alpha:200').resolve(entries('alpha', 200)); windows.get('alpha:2000').resolve(entries('alpha', 2000));
    await waitFor(() => ready() && rows().length === 200, 'new grant window did not recover'); windows.clear();
    // A newer target cancels the old read, even if its transport answers late.
    directory = deferred(); const oldGate = directory, beforeOld = calls.length;
    choose('beta'); await waitFor(() => calls.length > beforeOld, 'first scope read did not start');
    const oldRead = calls.at(-1); directory = deferred(); const nextGate = directory;
    choose('alpha'); await waitFor(() => calls.at(-1) !== oldRead, 'replacement scope read did not start');
    assert.ok(oldRead.signal.aborted, 'new scope did not cancel previous authorization');
    nextGate.resolve(); directory = null; await waitFor(() => ready() && rows().length === 200, 'newest scope did not load');
    oldGate.resolve(); await delay(30);
    assert.ok([...rows()].every(row => row.textContent.includes('alpha authorized log')), 'late old-scope result polluted current rows');
    // Any grant revision/membership change drops old authorized windows.
    sources = [source('beta')]; const beforeRevocation = calls.length; refresh();
    await waitFor(() => calls.length > beforeRevocation && ready(), 'grant refresh did not finish');
    assert.ok([...rows()].every(row => row.textContent.includes('beta authorized log')), 'revoked node remained visible from cache');
    assert.equal(document.querySelector('[name=agent_id]').value, '', 'revoked target remained selected');
    denied = true; refresh(); await waitFor(() => ready() && main().textContent.includes('日志权限已撤销'), 'permission denial did not render');
    assert.equal(rows().length, 0, 'permission denial retained protected log data');
    assert.equal([...document.querySelector('[name=agent_id]').options].length, 1, 'denied sources remained selectable');
    denied = false; sources = [source('alpha')]; directory = deferred(); const lateGate = directory; const beforeLate = calls.length; refresh();
    await waitFor(() => calls.length > beforeLate, 'late account directory did not begin');
    session = { ...session, user_id: 'another-log-account' }; sources = []; directory = null; boot();
    await waitFor(() => ready() && main().textContent.includes('没有获授权的日志实例'), 'new account did not load its own empty scope');
    lateGate.resolve(); await delay(30);
    assert.equal(rows().length, 0, 'previous account authorization populated the new account');
    assert.equal([...document.querySelector('[name=agent_id]').options].length, 1, 'previous account nodes leaked after late read');
    const linkedAgent = 'agt_0000000000000001', linkedInstance = 'shr_0000000000000001';
    sources = [{ ...source(linkedAgent), instance_id: linkedInstance, username: '共享测试用户' }];
    const beforeLink = calls.length;
    boot(`#core-logs?agent=${linkedAgent}&instance=${linkedInstance}&engine=xray`);
    await waitFor(() => ready() && rows().length === 200, 'shared-instance log link did not load');
    assert.equal(document.querySelector('[name=agent_id]').value, linkedAgent, 'shared link ignored node target');
    assert.equal(document.querySelector('[name=instance_id]').value, linkedInstance, 'shared link ignored instance target');
    assert.equal(document.querySelector('[name=engine]').value, 'xray', 'shared link ignored engine target');
    assert.ok(calls.slice(beforeLink).filter(call => call.kind === 'window').every(call => call.agent === linkedAgent && call.instance === linkedInstance), 'shared link requested another resource scope');
    sources = []; const beforeDeniedLink = calls.length;
    boot(`#core-logs?agent=${linkedAgent}&instance=${linkedInstance}&engine=xray`);
    await waitFor(() => ready() && main().textContent.includes('没有获授权的日志实例'), 'revoked shared link did not clear scope');
    assert.equal(rows().length, 0, 'revoked shared link retained history');
    assert.ok(calls.slice(beforeDeniedLink).filter(call => call.kind === 'window').every(call => call.agent !== linkedAgent && call.instance !== linkedInstance), 'revoked shared link bypassed source authorization');
    session = { role: 'user', user_id: 'logs-only', permissions: ['core-logs.read'] };
    sources = [source('alpha')]; boot();
    await waitFor(() => ready() && rows().length === 200, 'log-only user lost authorized history without agents.read');
    assert.ok([...rows()].every(row => row.textContent.includes('alpha authorized log')), 'log-only user history was replaced with unverified node data');
    denied = true; boot();
    await waitFor(() => ready() && main().textContent.includes('日志权限已撤销'), 'initial source denial did not show a distinct failure');
    assert.equal(rows().length, 0, 'initial permission denial retained history');
  } finally { window.dispatchEvent(new Event('pagehide')); globalThis.fetch = nativeFetch; }
}
function deferred() { let resolve; const promise = new Promise(accept => { resolve = accept; }); return { promise, resolve }; }
