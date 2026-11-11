import { startAnalytics } from '../analytics/app.js';
import { setStorageAccount, accountStorage } from '../modules/account-storage.js';
import { assert, waitFor, delay } from './assertions.mjs';

export async function testLogReadOwnership() {
  window.dispatchEvent(new Event('pagehide'));
  const nativeFetch = globalThis.fetch;
  let session = { role: 'admin', user_id: 'native-log-ownership', username: 'log-owner' };
  const source = agent => ({ agent_id: agent, name: agent.toUpperCase(), engine: 'xray', instance_id: 'main', owner_id: session.user_id, username: session.username, status: 'active', grant_revision: '1' });
  let sources = ['alpha', 'beta', 'gamma'].map(source), directory = deferred(), order = deferred(), windows = new Map(), denied = false;
  let sourceFailure = 0, orderFailure = 0, windowFailure = 503;
  const calls = [], failedWindows = new Set();
  let accountPreferences = {values:{}, revision:0};
  const entries = (agent, count) => Array.from({ length: count }, (_, index) => ({ id: index + 1, agent_id: agent, engine: 'xray', instance_id: 'main', level: 'info', message: `${agent} authorized log ${index}`, logged_at: '2026-09-07T00:00:00Z' }));
  const reply = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
  globalThis.fetch = async (input, options = {}) => {
    const url = new URL(input instanceof Request ? input.url : input, location.href), path = url.pathname;
    if (path === '/api/v1/auth/session') return reply(session);
    if (path === '/api/v1/settings') return reply({});
    if (path === '/api/v1/preferences/ui') {
      if (options.method === 'PATCH') {
        const patch=JSON.parse(options.body);
        if(patch.revision!==accountPreferences.revision)return reply({error:'conflict'},409);
        for(const [page,fields] of Object.entries(patch.values))Object.assign(accountPreferences.values[page] ||= {},fields);
        accountPreferences.revision++;
      }
      return reply(accountPreferences);
    }
    if (path === '/api/v1/preferences/node-order') { if (order) await order.promise; return reply({ ids: ['gamma', 'beta', 'alpha'], revision: 1 }, orderFailure || 200); }
    if (path === '/api/v1/core-log-sources') {
      const item = { kind: 'directory', signal: options.signal }; calls.push(item);
      const snapshot = structuredClone(sources), gate = directory;
      if (gate) await gate.promise;
      // Deliberately ignore abort to prove view ownership rejects late results.
      return denied ? reply({ error: '日志权限已撤销' }, 403) : sourceFailure ? reply({}, sourceFailure) : reply(snapshot);
    }
    if (path === '/api/v1/core-logs') {
      const agent = url.searchParams.get('agent_id') || sources[0]?.agent_id || '', limit = Number(url.searchParams.get('limit'));
      calls.push({ kind: 'window', agent, instance: url.searchParams.get('instance_id'), engine: url.searchParams.get('engine'), limit, signal: options.signal });
      const gate = windows.get(`${agent}:${limit}`);
      const result = gate ? await gate.promise : agent ? entries(agent, limit).map(record => ({ ...record, instance_id: url.searchParams.get('instance_id') || 'main' })) : [];
      return failedWindows.has(`${agent}:${limit}`) ? reply({}, windowFailure) : reply(result);
    }
    return nativeFetch(input, options);
  };
  const main = () => document.querySelector('.a-main');
  const rows = () => document.querySelectorAll('[data-log-entry]');
  const ready = () => !main()?.querySelector('[aria-busy=true]') && !document.querySelector('[data-log-refresh-label][aria-busy=true]');
  const boot = (hash = '#core-logs', preferences = {}) => {
    window.dispatchEvent(new Event('pagehide'));
    setStorageAccount(session);
    accountPreferences = {values:{logs:{agent_id:'alpha',automatic:false,...preferences,limit:String(preferences.limit || 2000)}}, revision:1};
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
    boot(); await waitFor(() => calls.filter(call => call.kind === 'directory').length === 1 && calls.filter(call => call.kind === 'window').length === 2, 'independently authorized reads did not start with the directory');
    assert.equal(rows().length, 0, 'unvalidated cached logs appeared');
    assert.equal(calls.filter(call => call.kind === 'window').length, 2, 'source metadata blocked server-authorized windows');
    windows.get('alpha:200').resolve(entries('alpha', 200));
    await waitFor(() => rows().length === 200 && main().textContent.includes('正在补齐'), 'slow source metadata blocked independently authorized preview');
    directory.resolve(); directory = null;
    await waitFor(() => calls.filter(call => call.kind === 'window').length === 2, 'preview and full windows did not start concurrently');
    order.resolve(); order = null;
    await waitFor(() => document.querySelector('[name=agent_id]').options[1]?.value === 'gamma', 'log node choices ignored the delayed server order');
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
    windows.set('alpha:2000', deferred());
    directory = deferred(); const beforeReturn = calls.length; location.hash = '#core-logs';
    await waitFor(() => calls.length > beforeReturn, 'return did not verify authorization');
    assert.equal(rows().length, 0, 'cached logs bypassed authorization on return');
    directory.resolve(); directory = null; await waitFor(() => rows().length === 200, 'authorized cached scope did not recover');
    windows.get('alpha:2000').resolve(entries('alpha', 2000)); windows.clear(); await waitFor(ready, 'cached refresh did not settle');
    assert.equal(document.querySelector('[name=limit]').value, '2000', 'stored preferences replaced the current session window');
    assert.equal(document.querySelector('[name=agent_id]').value, 'alpha', 'stored preferences replaced the current node');
    assert.ok([...document.querySelectorAll('[data-log-filters] button')].some(button => button.textContent === '自动更新'), 'stored preferences resumed a paused session');
    const search = document.querySelector('[data-log-filters] [name=q]');
    search.value = 'alpha'; search.dispatchEvent(new Event('input', { bubbles: true }));
    await waitFor(() => accountPreferences.values.logs.q === 'alpha', 'keyword edits were not remembered');
    const engine = document.querySelector('[data-log-filters] [name=engine]');
    engine.value = 'xray'; engine.dispatchEvent(new Event('change', { bubbles: true }));
    await waitFor(() => accountPreferences.values.logs.engine === 'xray', 'engine edits were not remembered');
    [...document.querySelectorAll('[data-log-filters] button')].find(button => button.textContent === '清除筛选').click();
    await waitFor(() => accountPreferences.values.logs.q === '' && accountPreferences.values.logs.engine === '', 'cleared filters were not saved');
    const cleared = accountPreferences.values.logs;
    assert.ok(cleared.agent_id === 'alpha' && cleared.limit === '2000' && cleared.automatic === false && !cleared.engine && !cleared.q, 'clearing local filters lost the session scope/window/pause');
    location.hash = '#themes'; await waitFor(() => document.querySelector('.a-preview'), 'grant-change route departure failed');
    sources = sources.map(source => ({ ...source, grant_revision: '2' }));
    windows.set('alpha:200', deferred()); windows.set('alpha:2000', deferred());
    const beforeGrantReturn = calls.filter(call => call.kind === 'window').length;
    location.hash = '#core-logs';
    await waitFor(() => calls.filter(call => call.kind === 'window').length > beforeGrantReturn, 'grant-change reentry did not start fresh window');
    assert.equal(rows().length, 0, 'return reused a cached window from an older grant revision');
    await waitFor(() => calls.filter(call => call.kind === 'window').length === beforeGrantReturn + 2, 'changed grants did not start a fresh preview');
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
    assert.ok(calls.slice(beforeDeniedLink).some(call => call.kind === 'window' && call.instance !== linkedInstance), 'revoked target was not replaced by the verified scope');
    session = { role: 'user', user_id: 'logs-only', permissions: ['core-logs.read'] };
    sources = [source('alpha')]; boot();
    await waitFor(() => ready() && rows().length === 200, 'log-only user lost authorized history without agents.read');
    assert.ok([...rows()].every(row => row.textContent.includes('alpha authorized log')), 'log-only user history was replaced with unverified node data');
    denied = true; boot();
    await waitFor(() => ready() && main().textContent.includes('日志权限已撤销'), 'initial source denial did not show a distinct failure');
    assert.equal(rows().length, 0, 'initial permission denial retained history');

    // A terminal full failure cancels both the hung preview and directory.
    // Neither late response may turn the failed round back into loading.
    denied = false; session = { ...session, user_id: 'full-first-failure' };
    sources = [source('alpha')]; directory = deferred(); windows.set('alpha:200', deferred());
    const failedDirectory = directory, failedPreview = windows.get('alpha:200');
    windowFailure = 504; failedWindows.add('alpha:1000');
    const beforeFullFailure = calls.length;
    boot('#core-logs', { agent_id: '', limit: 1000 });
    await waitFor(() => ready() && main().textContent.includes('读取超时，请重试。'), 'full failure waited for a hung sibling');
    assert.ok(calls.slice(beforeFullFailure).every(call => call.signal.aborted), 'terminal failure retained a pending sibling');
    failedDirectory.resolve(); failedPreview.resolve([]); await delay(30);
    assert.ok(ready() && main().textContent.includes('日志读取失败'), 'late empty preview masked a full failure');
    directory = null; windows.clear(); failedWindows.clear(); refresh();
    await waitFor(() => ready() && main().textContent.includes('1000 条日志'), 'full-first timeout did not release manual retry');

    // A successful network window owns its authorization. Slow metadata must
    // release loading/manual refresh, while a late denial still clears data.
    session = { ...session, user_id: 'window-before-directory' };
    sources = [source('alpha')]; directory = deferred();
    const slowDirectory = directory;
    boot('#core-logs', { agent_id: '', limit: 1000 });
    await waitFor(() => ready() && main().textContent.includes('1000 条日志'), 'successful window waited for source metadata');
    const beforeMetadataRetry = calls.length; refresh();
    await waitFor(() => calls.length > beforeMetadataRetry && ready(), 'metadata retained pending ownership after window success');
    denied = true; slowDirectory.resolve(); directory = null;
    await waitFor(() => ready() && main().textContent.includes('日志权限已撤销'), 'late directory denial did not invalidate completed data');
    assert.equal(rows().length, 0);
    denied = false;

    // A late metadata failure must finish its status without discarding the
    // already authorized complete window or preventing a manual recovery.
    session = { ...session, user_id: 'late-directory-failure' };
    sources = [source('alpha')]; directory = deferred();
    const failedMetadata = directory;
    boot('#core-logs', { agent_id: '', limit: 1000 });
    await waitFor(() => ready() && main().textContent.includes('1000 条日志'), 'window waited for late failing metadata');
    sourceFailure = 503; directory = null; failedMetadata.resolve();
    await waitFor(() => main().textContent.includes('来源状态读取失败'), 'late metadata failure was not reported');
    assert.equal(document.querySelector('[data-log-refresh-label]').textContent, '自动更新已暂停', 'failed metadata retained a loading status');
    assert.equal(rows().length, 200, 'late metadata failure discarded authorized history');
    sourceFailure = 0; refresh();
    await waitFor(() => ready() && !main().textContent.includes('来源状态读取失败') && main().textContent.includes('1000 条日志'), 'late metadata failure prevented recovery');

    // A late grant result can normalize a completed target to a fresh scope.
    session = { ...session, user_id: 'late-target-revocation' };
    sources = [source('beta')]; directory = deferred();
    const revokedDirectory = directory;
    boot('#core-logs', { agent_id: 'alpha', limit: 1000 });
    await waitFor(() => ready() && rows().length === 200, 'late-revocation window did not finish independently');
    directory = null; revokedDirectory.resolve();
    await waitFor(() => ready() && [...rows()].every(row => row.textContent.includes('beta authorized log')) && rows().length === 200, 'late target normalization did not start a replacement read');
    assert.equal(document.querySelector('[name=agent_id]').value, '');

    // Reproduce entry -> 504 -> retry -> preview -> full recovery. Retries
    // must keep the 200/engine first paint even after scope was initialized.
    denied = false; windowFailure = 504; orderFailure = 504;
    session = { ...session, user_id: 'timeout-recovery' }; sources = [source('alpha')];
    failedWindows.add('alpha:200'); failedWindows.add('alpha:1000');
    boot('#core-logs', { agent_id: '', limit: 1000 });
    await waitFor(() => ready() && main().textContent.includes('读取超时，请重试。'), 'initial window timeout did not finish');
    assert.equal(rows().length, 0);
    assert.equal(document.querySelector('[aria-label=内核日志]').nextElementSibling.textContent, '日志读取失败', 'healthy collection masked the query failure');
    assert.ok(!main().textContent.includes('避免重复提交'), 'GET timeout used mutation advice');
    failedWindows.clear(); windows.set('alpha:1000', deferred());
    const beforeRetry = calls.length; refresh(); refresh(); refresh();
    await waitFor(() => rows().length === 200 && main().textContent.includes('正在补齐'), 'retry lost the fast preview after an initial timeout');
    const retry = calls.slice(beforeRetry);
    assert.equal(retry.filter(call => call.kind === 'directory').length, 1, 'consecutive refreshes restarted authorization');
    assert.equal(retry.filter(call => call.kind === 'window').length, 2, 'consecutive refreshes stacked log reads');
    assert.ok(retry.filter(call => call.kind === 'window' && call.limit === 1000).every(call => !call.signal.aborted), 'consecutive refreshes canceled useful pending reads');
    assert.ok(!main().textContent.includes('读取超时'), 'successful preview retained an obsolete read error');
    windows.get('alpha:1000').resolve(entries('alpha', 1000));
    await waitFor(() => ready() && main().textContent.includes('1000 条日志'), 'timeout retry did not finish the selected window');
    assert.equal(document.querySelector('[data-log-refresh-label]').textContent, '自动更新已暂停');
    windows.clear(); sourceFailure = 504; refresh();
    await waitFor(() => ready() && main().textContent.includes('来源状态读取失败'), 'source failure was not reported separately from readable history');
    assert.equal(rows().length, 200);
    sourceFailure = 0; refresh();
    await waitFor(() => ready() && !main().textContent.includes('读取超时'), 'source retry failed to clear its old error');
    session = { ...session, user_id: 'source-timeout' }; sourceFailure = 504;
    boot('#core-logs', { agent_id: '', limit: 1000 });
    await waitFor(() => ready() && main().textContent.includes('来源状态读取失败'), 'initial source timeout did not settle');
    assert.equal(rows().length, 200, 'source failure blocked independently authorized historical logs');
    sourceFailure = 0; refresh();
    await waitFor(() => ready() && rows().length === 200, 'initial source failure did not recover');
    location.hash = '#themes'; await waitFor(() => document.querySelector('.a-preview'), 'timeout route departure failed');
    location.hash = '#core-logs'; await waitFor(() => ready() && rows().length === 200, 'timeout recovery did not survive page reentry');
    const unmatched = document.querySelector('[data-log-filters] [name=q]');
    unmatched.value = 'no-authorized-log-matches'; unmatched.dispatchEvent(new Event('input', { bubbles: true }));
    await waitFor(() => rows().length === 0, 'unmatched keyword retained rows');
    assert.equal(document.querySelector('[aria-label=内核日志]').nextElementSibling.textContent, '没有匹配日志', 'loaded history was mislabeled as awaiting first delivery');
    assert.ok(main().textContent.includes('已加载 1000 条'));

    // Reuse only the same authorized resource when changing the window size.
    session = { ...session, user_id: 'cache-windows' };
    const shared = 'shr_0000000000000002';
    sources = [source('alpha'), { ...source('alpha'), instance_id: shared, owner_id: 'shared-owner', username: '共享用户' }];
    boot('#core-logs', { instance_id: 'main', limit: 100 });
    await waitFor(() => ready() && rows().length === 100, 'small initial cache window did not load');
    const setLimit = value => {
      const control = document.querySelector('[data-log-filters] [name=limit]');
      control.value = String(value); control.dispatchEvent(new Event('change', { bubbles: true }));
    };
    directory = deferred(); windows.set('alpha:2000', deferred());
    const beforeExpand = calls.length;
    setLimit(2000);
    await waitFor(() => calls.length > beforeExpand, 'window expansion did not check grants');
    assert.equal(rows().length, 0, 'related cache bypassed fresh authorization');
    directory.resolve(); directory = null;
    await waitFor(() => rows().length === 100 && main().textContent.includes('正在补齐'), 'larger window discarded the smaller authorized cache');
    assert.equal(calls.slice(beforeExpand).filter(call => call.kind === 'window').length, 1, 'cached expansion started an unnecessary preview request');
    failedWindows.add('alpha:2000'); windows.get('alpha:2000').resolve([]);
    await waitFor(() => ready() && main().textContent.includes('补齐失败，当前仅显示部分日志'), 'failed expansion claimed a complete cache window');
    assert.equal(rows().length, 100);
    failedWindows.clear(); windows.clear();

    // A failed staged read retains its preview across route departure.
    windows.set('alpha:2000', deferred());
    windows.set('alpha:200', deferred());
    const instance = document.querySelector('[data-log-filters] [name=instance_id]');
    instance.value = shared; instance.dispatchEvent(new Event('change', { bubbles: true }));
    const beforeShared = calls.length;
    await waitFor(() => calls.slice(beforeShared).filter(call => call.kind === 'window').length === 2, 'shared scope did not start fresh stages');
    assert.equal(rows().length, 0, 'main-instance cache leaked into the shared instance');
    assert.ok(calls.slice(beforeShared).filter(call => call.kind === 'window').every(call => call.instance === shared));
    windows.get('alpha:200').resolve(entries('alpha', 200).map(row => ({ ...row, instance_id: shared })));
    await waitFor(() => rows().length === 200 && main().textContent.includes('正在补齐'), 'shared preview did not display');
    failedWindows.add('alpha:2000'); windows.get('alpha:2000').resolve([]);
    await waitFor(() => ready() && main().textContent.includes('补齐失败'), 'shared completion failure did not settle');
    failedWindows.clear(); windows.set('alpha:2000', deferred());
    location.hash = '#themes'; await waitFor(() => document.querySelector('.a-preview'), 'partial-cache departure failed');
    const beforeCachedReturn = calls.length; location.hash = '#core-logs';
    await waitFor(() => rows().length === 200 && main().textContent.includes('正在补齐'), 'authorized partial cache was lost on route return');
    assert.equal(calls.slice(beforeCachedReturn).filter(call => call.kind === 'window').length, 1, 'retained preview was fetched again before completion');
    windows.get('alpha:2000').resolve(entries('alpha', 2000).map(row => ({ ...row, instance_id: shared })));
    await waitFor(() => ready() && main().textContent.includes('2000 条日志'), 'partial cache did not complete'); windows.clear();

    windows.set('alpha:1000', deferred()); const beforeShrink = calls.length; setLimit(1000);
    await waitFor(() => main().textContent.includes('1000 条日志') && main().textContent.includes('已显示缓存'), 'smaller window did not render its cached slice before the network');
    assert.equal(calls.slice(beforeShrink).filter(call => call.kind === 'window').length, 1, 'cached shrink started a redundant preview');
    const corrected = entries('alpha', 999).map(row => ({ ...row, instance_id: shared }));
    corrected[0].message = 'cache correction after retention';
    windows.get('alpha:1000').resolve(corrected);
    await waitFor(() => ready() && main().textContent.includes('999 条日志') && main().textContent.includes('cache correction after retention'), 'revalidation kept corrected or deleted cached rows');
  } finally { window.dispatchEvent(new Event('pagehide')); globalThis.fetch = nativeFetch; }
}
function deferred() { let resolve; const promise = new Promise(accept => { resolve = accept; }); return { promise, resolve }; }
