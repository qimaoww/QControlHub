import { startAnalytics } from '../analytics/app.js';
import { accountStorage, setStorageAccount } from '../modules/account-storage.js';
import { assert, waitFor, delay } from './assertions.mjs';

export async function testConnectionReadLifecycle() {
  window.dispatchEvent(new Event('pagehide'));
  const nativeFetch = globalThis.fetch, nativeNow = Date.now;
  let clockOffset = 0; Date.now = () => nativeNow() + clockOffset;
  let session = { role: 'admin', user_id: 'native-connection-cache' };
  const base = { agent_id: 'alpha', agent_name: '<script>Alpha</script>', engine: 'xray', client_ip: '8.8.8.8', first_seen: '2026-09-21T08:00:00Z', last_seen: '2026-09-21T09:00:00Z', endpoints: [{ local_port: 8443 }, { local_port: 443 }, { local_port: 8443 }], location: { country_code: 'CN', province: '广东' } };
  let records = [{ ...base, id: 1 }, { ...base, id: 2, agent_id: 'bravo', agent_name: 'Bravo', engine: 'sing-box', client_ip: '1.1.1.1' }];
  let failure = 0, historyGate, locationGate, geography = 'complete';
  const calls = [];
  const reply = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
  globalThis.fetch = async (input, options = {}) => {
    const url = new URL(input instanceof Request ? input.url : input, location.href), path = url.pathname;
    if (path === '/api/v1/auth/session') return reply(session);
    if (path === '/api/v1/settings') return reply({});
    if (path === '/api/v1/preferences/node-order') return reply({ ids: ['bravo', 'alpha'], revision: 1 });
    if (path !== '/api/v1/client-connections') return nativeFetch(input, options);
    if (url.searchParams.get('scope_only') === 'true') return reply({ scope: '' });
    const params = url.searchParams, locationOnly = params.get('locations') === 'only';
    calls.push({ params, signal: options.signal, locationOnly });
    const snapshot = structuredClone(records).filter(row => (!params.get('agent_id') || row.agent_id === params.get('agent_id')) && (!params.get('engine') || row.engine === params.get('engine')) && (!params.get('client_ip') || row.client_ip === params.get('client_ip')));
    const gate = locationOnly ? locationGate : historyGate;
    if (gate) await gate.promise; // Ignore abort deliberately; view ownership must reject late responses.
    if (!locationOnly && failure) return reply({ error: failure === 403 ? '连接查看权限已撤销' : '连接查询暂不可用' }, failure);
    return reply({ records: snapshot.map(row => ({ ...row, location: locationOnly ? geography === 'unknown' ? {} : { country_code: 'CN', province: '江苏' } : geography === 'unknown' ? {} : geography === 'country' ? { country_code: 'CN' } : row.location })), ips: snapshot.length, flows: 10, sources: [...new Set(snapshot.map(row => row.agent_id))].map(agent_id => ({ agent_id, agent_name: agent_id, status: 'ok', updated_at: base.last_seen })), next_cursor: params.has('cursor') ? '' : 'page-2', page_cursor: params.get('cursor') || 'page-1' });
  };
  const main = () => document.querySelector('.a-main');
  const form = () => main()?.querySelector('form');
  const rows = () => [...document.querySelectorAll('[aria-label="客户端来源 IP 明细"] td[data-column="客户端来源 IP"] code')].map(code => code.closest('tr'));
  const historyReads = () => calls.filter(call => !call.locationOnly).length;
  const locationReads = () => calls.filter(call => call.locationOnly).length;
  const button = text => [...main().querySelectorAll('button')].find(button => button.textContent === text);
  const ready = () => main()?.textContent.includes('已读取');
  const boot = async () => {
    window.dispatchEvent(new Event('pagehide'));
    setStorageAccount(session); accountStorage.setItem('qcontrolhub:node-card-order', '[]');
    document.body.replaceChildren(Object.assign(document.createElement('div'), { id: 'native-connections-test' }));
    history.replaceState(null, '', '#client-connections');
    startAnalytics({ app: document.querySelector('#native-connections-test'), session });
    await waitFor(() => form() && ready(), 'native connection lifecycle did not load');
  };
  const change = (name, value) => { form().elements[name].value = value; form().elements[name].dispatchEvent(new Event('change', { bubbles: true })); };
  const changedRead = async (name, value) => { const before = historyReads(); change(name, value); await waitFor(() => historyReads() > before && ready(), 'new connection scope did not settle'); };
  const refresh = () => button('刷新').click();
  try {
    await boot();
    assert.equal(rows().length, 2);
    assert.ok(rows()[0].textContent.includes('<script>Alpha</script>') && !main().querySelector('script'), 'connection name was parsed as markup');
    assert.equal(rows()[0].querySelector('[data-column="入站端口"]').textContent, '443 · 8443', 'native port set did not deduplicate and sort');
    assert.equal(calls.find(call => !call.locationOnly).params.getAll('node_order').join(','), 'bravo,alpha', 'history did not use authenticated node order');
    assert.equal(form().elements.agent_id.options[1].value, 'bravo', 'node choices ignored saved order');
    historyGate = deferred(); const initialGate = historyGate;
    change('agent_id', 'alpha');
    assert.equal(rows().length, 1, 'narrow preview exposed another node');
    assert.ok(main().textContent.includes('结果待更新') && !main().textContent.includes('观测连接 10'), 'partial preview claimed complete totals');
    initialGate.resolve(); historyGate = null; await waitFor(ready, 'narrow result did not finish');
    const cached = historyReads(); change('agent_id', ''); change('agent_id', 'alpha');
    assert.equal(historyReads(), cached, 'fresh node scopes did not reuse cache');
    button('下一页').click(); await waitFor(() => ready() && main().textContent.includes('第 2 页'), 'connection next page missing');
    const paged = historyReads(); button('上一页').click(); button('下一页').click();
    assert.equal(historyReads(), paged, 'previous/next page did not retain independent cache');
    button('上一页').click();
    clockOffset = 31000;
    historyGate = deferred(); const staleGate = historyGate, beforeStale = historyReads(); change('agent_id', '');
    await waitFor(() => historyReads() > beforeStale, 'expired connection scope did not revalidate');
    assert.equal(rows().length, 2, 'expired connection snapshot disappeared while refreshing');
    assert.ok([...form().elements].filter(control => control.name).every(control => !control.disabled), 'slow history disabled filters');
    staleGate.resolve(); historyGate = null; await waitFor(ready, 'stale scope refresh did not finish'); clockOffset = 0;
    for (const [name, value] of [['engine', 'xray'], ['engine', 'sing-box'], ['engine', ''], ['client_ip', '8.8.8.8']]) {
      change(name, value); await waitFor(ready, 'engine/IP query did not settle');
    }
    const scopedReads = historyReads();
    change('client_ip', ''); change('engine', 'xray'); change('engine', 'sing-box'); change('engine', ''); change('client_ip', '8.8.8.8');
    assert.equal(historyReads(), scopedReads, 'engine/IP scope caches were combined or lost');
    assert.ok(rows().every(row => row.textContent.includes('8.8.8.8') && !row.textContent.includes('1.1.1.1')), 'IP cache contains another client');
    failure = 503; refresh(); await waitFor(() => main().textContent.includes('更新失败，保留上次结果'), 'connection failure did not preserve history');
    assert.equal(rows().length, 1, 'connection failure cleared valid rows');
    failure = 403; refresh(); await waitFor(() => main().textContent.includes('连接查看权限已撤销'), 'connection denial did not render');
    assert.equal(rows().length, 0, 'connection denial retained protected history');
    assert.equal(form().elements.agent_id.options.length, 1, 'denied node choices remained selectable');
    failure = 0; const afterDenial = historyReads(); refresh(); await waitFor(() => historyReads() > afterDenial && ready(), 'denied cache was reused after recovery');
    geography = 'unknown'; await boot(); await waitFor(() => locationReads() > 0, 'unknown geography did not start enrichment'); await delay(20);
    const geoCount = locationReads(); change('agent_id', 'alpha'); await waitFor(ready, 'unknown geography narrower scope failed');
    change('agent_id', ''); await waitFor(ready, 'unknown geography cached scope failed');
    assert.equal(locationReads(), geoCount + 1, 'unknown geography was retried on every fresh cache hit');
    geography = 'country'; locationGate = deferred(); await boot();
    const heldLocation = locationGate; change('engine', 'sing-box'); await waitFor(ready, 'new scope blocked on old geography');
    heldLocation.resolve(); locationGate = null; await waitFor(() => rows()[0]?.textContent.includes('中国 · 江苏'), 'China province enrichment did not paint');
    assert.ok(rows().every(row => row.textContent.includes('sing-box') && !row.textContent.includes('Alpha')), 'late geography restored an older query');
    historyGate = deferred(); const oldGate = historyGate, beforeOld = historyReads(); change('engine', 'xray');
    await waitFor(() => historyReads() > beforeOld, 'old-account request did not start');
    const oldRequest = calls.filter(call => !call.locationOnly).at(-1);
    session = { role: 'admin', user_id: 'another-connection-account' }; records = []; historyGate = null; await boot();
    oldGate.resolve(); await delay(30);
    assert.ok(oldRequest.signal.aborted && rows().length === 0, 'late previous-account history populated current account');
    session = { role: 'admin', user_id: 'restored-connection-target' };
    records = [{ ...base, id: 1 }, { ...base, id: 2, agent_id: 'bravo', agent_name: 'Bravo' }];
    geography = 'complete'; await boot();
    await changedRead('agent_id', 'alpha');
    location.hash = '#themes';
    await waitFor(() => document.title.startsWith('主题') && !form(), 'connection route did not leave');
    clockOffset = 31000; historyGate = deferred();
    const restoredGate = historyGate, beforeRestore = historyReads();
    location.hash = '#client-connections';
    await waitFor(() => form() && historyReads() > beforeRestore, 'restored connection query did not start');
    assert.equal(form().elements.agent_id.value, 'alpha', 'pending node choices lost the restored target');
    change('period', 'month');
    await waitFor(() => historyReads() > beforeRestore + 1, 'pending restored query could not change date range');
    assert.equal(calls.filter(call => !call.locationOnly).at(-1).params.get('agent_id'), 'alpha', 'editing another filter widened the restored target');
    restoredGate.resolve(); historyGate = null; await waitFor(ready, 'restored target did not settle');
    clockOffset = 0; change('period', 'day'); await waitFor(ready, 'restored day cache did not settle');
    change('agent_id', ''); await waitFor(() => ready() && rows().length === 2, 'restored all-node cache did not settle');
  } finally { window.dispatchEvent(new Event('pagehide')); globalThis.fetch = nativeFetch; Date.now = nativeNow; }
}
function deferred() { let resolve; const promise = new Promise(done => { resolve = done; }); return { promise, resolve }; }
