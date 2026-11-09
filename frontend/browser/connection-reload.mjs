// Deterministic network fault/delay fixture for actual CDP Page.reload checks.
// The production app, query builder, cache, forms and table are used unchanged.
export async function bootConnectionReloadFixture() {
  const key = 'connection-reload-fixture';
  const store = sessionStorage;
  let config = JSON.parse(store.getItem(key) || 'null') || { user: 'owner', role: 'admin', login: 1, scope: 1, offset: 0, reads: 0, bytes: 0, delay: 800, empty: false, failure: 0, scopeFailure: 0, order: ['alpha', 'bravo'] };
  const save = () => store.setItem(key, JSON.stringify(config));
  save();
  const nativeNow = Date.now;
  Date.now = () => nativeNow() + config.offset;
  const nativeFetch = window.fetch;
  const identity = () => config.user ? { role: config.role, user_id: config.user, username: config.user, permissions: ['core-logs.read'], csrf_token: `fixture-${config.user}-${config.login}` } : null;
  const scope = () => `${config.user}:${config.login}:${config.scope}`;
  const records = Array.from({ length: 6 }, (_, index) => ({ id: index + 1, agent_id: index < 3 ? 'alpha' : 'bravo', agent_name: index < 3 ? 'Alpha · 东京测试节点' : 'Bravo · 长名称边界测试节点-abcdefghijklmnopqrstuvwxyz0123456789', engine: index % 2 ? 'mihomo' : 'xray', client_ip: `8.8.8.${index + 1}`, first_seen: '2026-11-01T00:00:00Z', last_seen: '2026-11-08T00:00:00Z', endpoints: [{ local_port: 443 }, { local_port: 8443 }], location: { country_code: index < 3 ? 'US' : index < 5 ? 'CN' : 'TW', ...(index >= 3 && index < 5 ? { province: '广东' } : {}) } }));
  window.connectionFixture = {
    config, document: crypto.randomUUID(),
    configure(values) { Object.assign(config, values); save(); },
    counts: () => ({ reads: config.reads, checks: config.checks || 0, bytes: config.bytes }),
  };
  window.fetch = async (input, options) => {
    const url = new URL(input instanceof Request ? input.url : input, location.href), path = url.pathname;
    if (!path.startsWith('/api/v1/') || path.startsWith('/api/v1/region-flags/')) return nativeFetch(input, options);
    let value = {}, status = 200;
    if (path.endsWith('/auth/session')) { value = identity() || { error: '请登录' }; status = config.user ? 200 : 401; }
    else if (path.endsWith('/auth/logout')) { config.user = null; save(); }
    else if (path.endsWith('/auth/login')) { const body = JSON.parse(options.body); config.user = body.username; config.login++; save(); value = identity(); }
    else if (path.endsWith('/preferences/node-order')) value = { ids: config.order };
    else if (path.endsWith('/preferences/theme')) value = { theme_id: 'analytics' };
    else if (path.endsWith('/client-connections')) {
      if (url.searchParams.get('scope_only') === 'true') { status = config.scopeFailure || 200; value = status === 200 ? { scope: scope() } : { error: '访问范围暂不可用' }; }
      else {
        const params = url.searchParams, failure = config.failure;

        const available = config.empty ? [] : records.filter(row => (config.user !== 'shared' || row.agent_id === 'bravo') && (!params.get('agent_id') || params.get('agent_id') === row.agent_id) && (!params.get('engine') || params.get('engine') === row.engine) && (!params.get('client_ip') || params.get('client_ip') === row.client_ip));
        const selected = available.filter(row => !params.get('country_code') || params.get('country_code') === row.location.country_code);
        const snapshot = { country_codes: [...new Set(available.map(row => row.location.country_code))].sort(), records: params.has('cursor') ? selected.slice(3) : selected.slice(0, 3), ips: selected.length, flows: selected.length * 10001, sources: [...new Set(selected.map(row => row.agent_id))].map(agent_id => ({ agent_id, agent_name: records.find(row => row.agent_id === agent_id).agent_name, status: 'ok', updated_at: records[0].last_seen })), timeline: [], next_cursor: !params.has('cursor') && selected.length > 3 ? 'page-2' : '', cache_scope: scope() };
        const token = `fixture:${config.empty}:${config.scope}`;
        snapshot.sync_token = token;
        if (params.get('sync_token') === token) {
          config.checks = (config.checks || 0) + 1;
          snapshot.delta = true;
          snapshot.record_keys = snapshot.records.map(row => JSON.stringify([row.agent_id,row.engine,row.client_ip]));
          snapshot.records = [];
        } else config.reads++;
        save();
        await new Promise(resolve => setTimeout(resolve, config.delay)); // Deliberately ignore abort: the app must reject late results.
        status = failure || 200; value = status === 200 ? snapshot : { error: status === 403 ? '连接查看权限已撤销' : '连接查询暂不可用' };
        config.bytes += JSON.stringify(value).length; save();
      }
    }
    return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
  };
  if (config.storage === 'disabled') Object.defineProperty(window, 'sessionStorage', { configurable: true, get() { throw new Error('disabled storage'); } });
  if (config.storage === 'quota') {
    const setItem = Storage.prototype.setItem;
    Storage.prototype.setItem = function(name, value) { if (name.startsWith('qcontrolhub:connections:')) throw new Error('quota exceeded'); return setItem.call(this, name, value); };
  }
  const session = identity();
  const { startAnalytics } = await import('../analytics/app.js');
  startAnalytics({ app: document.querySelector('#app'), session });
}
