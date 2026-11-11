import assert from 'node:assert/strict';
import { setTimeout as delay } from 'node:timers/promises';

export async function runUIPreferenceSessions({ createPage, login, api, evaluate, wait, call, reload, fixture, record, screenshot, mode }) {
  const [a, device, b] = await Promise.all([createPage(), createPage(), createPage()]);
  const [alice, otherLogin, bob] = await Promise.all([login(a, 'alice'), login(device, 'alice'), login(b, 'bob')]);
  const read = (page, session) => api(page, session, 'GET', '/preferences/ui');
  const saved = async (page, session, scope, fields) => {
    for (let attempt = 0; attempt < 100; attempt++) {
      const current = (await read(page, session)).values[scope] || {};
      if (Object.entries(fields).every(([key, value]) => current[key] === value)) return;
      await delay(50);
    }
    assert.fail(`preferences did not save: ${scope} ${JSON.stringify(fields)}`);
  };
  const route = async (page, hash, selector) => {
    await evaluate(page, `location.hash=${JSON.stringify('#'+hash)}`);
    const titles = { traffic:'流量', tasks:'任务', 'core-logs':'日志', 'client-connections':'客户端连接', 'live-config':'实时配置', api:'API', 'node-settings':'节点', dashboard:'仪表盘', users:'用户管理', certificates:'证书', notifications:'通知', 'client-access':'客户端接入', 'substore-sync':'订阅同步', 'ip-quality':'IP 质量', 'system-bbr':'TCP 调优' };
    const title = hash.startsWith('system-bbr-agent-') ? 'TCP 调优' : titles[hash.split('?')[0]];
    await wait(page, `document.title.startsWith(${JSON.stringify(title)}+' ·') && !!document.querySelector(${JSON.stringify(selector)}) && !!document.querySelector('.a-main > .a-stack:not([aria-busy])') && !document.querySelector('#app[aria-busy]')`);
  };
  const choose = (page, selector, value) => evaluate(page, `(() => {const input=document.querySelector(${JSON.stringify(selector)});input.type==='checkbox'?input.checked=${JSON.stringify(value)}:input.value=${JSON.stringify(value)};input.dispatchEvent(new Event(input.tagName==='SELECT'||input.type==='checkbox'?'change':'input',{bubbles:true}));})()`);
  await route(a, 'traffic', '[name=search]');
  await choose(a, '[name=search]', 'independent account');
  await choose(a, '[name=scope]', 'all');
  await saved(a, alice, 'traffic', {search:'independent account',scope:'all'});
  await route(device, 'traffic', '[name=search]');
  await wait(device, "document.querySelector('[name=search]').value==='independent account' && document.querySelector('[name=scope]').value==='all'");
  await choose(device, '[name=search]', 'second device');
  await choose(a, '[name=engine]', 'xray');
  await saved(a, alice, 'traffic', {search:'second device',engine:'xray'});
  await evaluate(a, "[...document.querySelectorAll('.a-main button')].find(x=>x.textContent==='重置').click()");
  await saved(a, alice, 'traffic', {search:'',scope:'current',agent:'',engine:'',status:''});
  await reload(device);
  await wait(device, "document.querySelector('[name=search]')?.value==='' && document.querySelector('[name=engine]')?.value===''");
  record(`${mode}: independent browser contexts restore, concurrent fields merge, reset saves empty/default values`);

  await route(a, 'tasks', '[name=action]');
  await choose(a, '[name=status]', 'failed'); await choose(a, '[name=action]', 'deploy'); await choose(a, '[name=limit]', '50');
  await saved(a, alice, 'tasks', {status:'failed',action:'deploy',limit:'50'});
  await reload(a);
  await wait(a, "document.querySelector('[name=status]')?.value==='failed' && document.querySelector('[name=action]')?.value==='deploy' && document.querySelector('[name=limit]')?.value==='50'");
  await route(a, 'core-logs', '[data-log-filters]');
  await choose(a, '[name=q]', 'account log filter'); await choose(a, '[name=level]', 'error');
  await evaluate(a, "[...document.querySelectorAll('.a-main button')].find(x=>x.textContent==='暂停更新').click()");
  await saved(a, alice, 'logs', {q:'account log filter',level:'error',automatic:false});
  await route(device, 'core-logs', '[data-log-filters]');
  await wait(device, "document.querySelector('[name=q]').value==='account log filter' && document.querySelector('[name=level]').value==='error' && document.querySelector('[data-log-refresh-label]').textContent.includes('暂停')");
  await route(a, 'client-connections', '[name=client_ip]');
  await choose(a, '[name=client_ip]', '8.8.8.8'); await choose(a, '[name=include_non_public]', true);
  await saved(a, alice, 'connections', {client_ip:'8.8.8.8',include_non_public:true});
  await reload(a);
  await wait(a, "document.querySelector('[name=client_ip]')?.value==='8.8.8.8' && document.querySelector('[name=include_non_public]')?.checked");
  await evaluate(a, "[...document.querySelectorAll('.a-main button')].find(x=>x.textContent==='清除筛选').click()");
  await saved(a, alice, 'connections', {client_ip:'',include_non_public:false});
  record(`${mode}: task and log combinations, automatic=false, connection IP and cleared switches restore through real APIs`);

  // Seed an older saved date: relative choices must resolve at entry time,
  // while an absolute date must retain its original query interval.
  for (const timeMode of ['today', 'month', 'absolute']) {
    const preference = await read(a, alice);
    await api(a, alice, 'PATCH', '/preferences/ui', {revision:preference.revision,values:{connections:{date:'2026-01-02',period:'day',time_mode:timeMode}}});
    await reload(a);
    await wait(a, `(() => {
      const mode=${JSON.stringify(timeMode)}, now=new Date();
      const date=mode==='absolute'?'2026-01-02':new Date(now.getTime()-now.getTimezoneOffset()*60000).toISOString().slice(0,10);
      const since=new Date(date+'T00:00:00');if(mode==='month')since.setDate(1);
      const until=new Date(since);if(mode==='month')until.setMonth(until.getMonth()+1);else until.setDate(until.getDate()+1);
      const requests=performance.getEntriesByType('resource').map(entry=>new URL(entry.name)).filter(url=>url.pathname==='/api/v1/client-connections'&&url.searchParams.get('locations')==='cached');
      const query=requests.at(-1)?.searchParams;
      return document.querySelector('[name=date]')?.value===date&&document.querySelector('[name=period]')?.value===(mode==='month'?'month':'day')&&query?.get('since')===since.toISOString()&&query?.get('until')===until.toISOString();
    })()`);
  }
  record(`${mode}: today/month recompute an old saved date; absolute dates retain their interval; actual history query parameters match the restored controls`);

  await route(a, `live-config?agent=${fixture.shared}&engine=mihomo`, 'textarea[name=content]');
  await saved(a, alice, 'live-config', {agent:fixture.shared});
  await route(device, 'live-config', 'textarea[name=content]');
  await wait(device, `document.querySelector('.a-target-form [name=agent]').value===${JSON.stringify(fixture.shared)}`);
  assert.deepEqual(Object.keys((await read(a, alice)).values['live-config']), ['agent']);
  await choose(a, 'textarea[name=content]', 'unsaved preference regression draft');
  await choose(a, '.a-target-form [name=agent]', fixture.owned);
  await wait(a, "!!document.querySelector('dialog[open]')");
  await evaluate(a, "[...document.querySelectorAll('dialog[open] button')].find(x=>x.textContent==='取消').click()");
  await wait(a, `!document.querySelector('dialog[open]') && new URLSearchParams(location.hash.split('?')[1]).get('agent')===${JSON.stringify(fixture.shared)}`);
  assert.equal((await read(a, alice)).values['live-config'].agent, fixture.shared);
  assert.equal(await evaluate(a, "document.querySelector('textarea[name=content]').value"), 'unsaved preference regression draft');
  await choose(a, '.a-target-form [name=agent]', fixture.owned);
  await wait(a, "!!document.querySelector('dialog[open]')");
  await evaluate(a, "[...document.querySelectorAll('dialog[open] button')].find(x=>x.textContent==='离开页面').click()");
  await saved(a, alice, 'live-config', {agent:fixture.owned});
  await route(a, `live-config?agent=${fixture.shared}&engine=mihomo`, 'textarea[name=content]');
  await wait(a, `document.querySelector('.a-target-form [name=agent]').value===${JSON.stringify(fixture.shared)}`);
  await route(b, `live-config?agent=${fixture.shared}&engine=mihomo`, 'textarea[name=content]');
  assert.notEqual(await evaluate(a, "document.querySelector('textarea[name=content]').value"), await evaluate(b, "document.querySelector('textarea[name=content]').value"));
  assert.equal((await read(b, bob)).values.logs, undefined);
  record(`${mode}: node-only restoration, explicit links, canceled dirty navigation and different users on one shared node preserve real config ownership`);

  const readerPage = await createPage();
  const reader = await login(readerPage, 'workspace-reader');
  await api(readerPage, reader, 'GET', '/agents', undefined, 403);
  await route(readerPage, `live-config?agent=${fixture.shared}&engine=mihomo`, 'textarea[name=content]');
  await saved(readerPage, reader, 'live-config', {agent:fixture.shared});
  await route(readerPage, 'api', '.a-main');
  await route(readerPage, 'live-config', 'textarea[name=content]');
  await wait(readerPage, `document.querySelector('.a-target-form [name=agent]').value===${JSON.stringify(fixture.shared)}`);
  record(`${mode}: a workspace-only shared user restores a node while agents.read remains forbidden`);

  for (const item of [
    {route:'dashboard',scope:'dashboard',fields:{traffic_month:'2026-01'}},
    {route:'certificates',scope:'certificates',fields:{search:'remembered certificate'}},
    {route:'client-access',scope:'client-access',fields:{search:'remembered access',format:'mihomo'}},
    {route:'substore-sync',scope:'substore',fields:{search:'remembered subscription',agent:''}},
    {route:'notifications',scope:'notifications',fields:{template_event:'task.failed'}},
    {route:'ip-quality',scope:'ip-quality',fields:{date:''}},
    {route:'api?tab=docs',scope:'api',fields:{'api-filter':'preferences','api-group':'设置与用户'}},
  ]) {
    const first = `[name="${Object.keys(item.fields)[0]}"]`;
    await route(a, item.route, first);
    for (const [name,value] of Object.entries(item.fields)) await choose(a, `[name="${name}"]`, value);
    await saved(a, alice, item.scope, item.fields);
    await reload(a);
    await wait(a, Object.entries(item.fields).map(([name,value])=>`document.querySelector(${JSON.stringify(`[name="${name}"]`)})?.value===${JSON.stringify(value)}`).join(' && '));
  }
  record(`${mode}: dashboard month, certificates, client export format, subscription filters, template event, IP history and API search/category restored after real reloads`);

  await route(a, 'traffic', '[name=search]');
  await evaluate(a, "window.__preferenceFetch=window.fetch;window.fetch=(...args)=>String(args[0]).includes('/preferences/ui')&&args[1]?.method==='PATCH'?Promise.resolve(new Response(JSON.stringify({error:'isolated preference failure'}),{status:503,headers:{'Content-Type':'application/json'}})):window.__preferenceFetch(...args)");
  await choose(a, '[name=search]', 'recoverable failure');
  await wait(a, "document.body.innerText.includes('界面偏好未确认保存')");
  assert.equal(await evaluate(a, "document.querySelector('[name=search]').value"), 'recoverable failure');
  await screenshot(a, 'save-failure');
  await evaluate(a, "window.fetch=window.__preferenceFetch;document.querySelector('.a-topbar button:last-child')?.click()");
  // An ordinary new operation also retries a retained unsaved field.
  await choose(a, '[name=search]', 'recovered');
  await saved(a, alice, 'traffic', {search:'recovered'});
  record(`${mode}: failed persistence reports truthfully, retains the current control and recovers on the next operation`);
  if (!mode.endsWith('mobile')) {
    await evaluate(a, "document.querySelector('[aria-label=切换导航]').click()");
    await saved(a, alice, 'shell', {collapsed:true});
    await reload(a); await wait(a, "!!document.querySelector('.a-layout.a-collapsed')");
    await evaluate(a, "document.querySelector('[aria-label=切换导航]').click()");
    await saved(a, alice, 'shell', {collapsed:false});
  }

  // Delay the account read without delaying the visible page structure.
  await evaluate(a, "window.__preferenceFetch=window.fetch;window.fetch=async(...args)=>{const response=await window.__preferenceFetch(...args);if(String(args[0]).includes('/preferences/ui')&&(!args[1]?.method||args[1].method==='GET'))await new Promise(resolve=>setTimeout(resolve,700));return response;}");
  await route(a, 'node-settings', '[name=search]');
  await choose(a, '[name=search]', 'new input before restoration');
  await saved(a, alice, 'nodes', {search:'new input before restoration'});
  assert.equal(await evaluate(a, "document.querySelector('[name=search]').value"), 'new input before restoration');
  await evaluate(a, 'window.fetch=window.__preferenceFetch');
  await choose(a, '[name=search]', '');
  await wait(a, "!!document.querySelector('.a-region-trigger')");
  await evaluate(a, "document.querySelector('.a-region-trigger').click()");
  await wait(a, "!!document.querySelector('dialog[open] [name=region_search]')");
  await choose(a, '[name=region_search]', 'United');
  await saved(a, alice, 'node-region', {region_search:'United'});
  await evaluate(a, "document.querySelector('dialog[open] [aria-label=关闭]').click();document.querySelector('.a-region-trigger').click()");
  await wait(a, "document.querySelector('dialog[open] [name=region_search]')?.value==='United'");
  await evaluate(a, "document.querySelector('dialog[open] [aria-label=关闭]').click()");
  await route(a, 'traffic', '[name=search]');
  const invalidPreference = await read(a, alice);
  await api(a, alice, 'PATCH', '/preferences/ui', {revision:invalidPreference.revision,values:{traffic:{agent:'agt_0000000000000000'}}});
  await route(a, 'tasks', '[name=action]');
  await route(a, 'traffic', '[name=search]');
  await saved(a, alice, 'traffic', {agent:''});
  assert.equal(await evaluate(a, "document.querySelector('[name=agent]').value"), '', 'invalid late preference was not normalized against the successful node directory');

  const manager = await createPage(), admin = await login(manager, 'preferences-admin');
  await route(manager, 'users', '[name=query]');
  await choose(manager, '[name=query]', 'alice');
  await saved(manager, admin, 'users', {query:'alice'});
  await reload(manager); await wait(manager, "document.querySelector('[name=query]')?.value==='alice'");
  await route(a, `system-bbr-agent-${fixture.owned}`, '[aria-label="TCP 调优"]');
  await saved(a, alice, 'tcp', {agent:fixture.owned});
  await route(a, 'system-bbr', '[aria-label="TCP 调优"]');
  await wait(a, `document.querySelectorAll('[aria-label="TCP 调优"] tbody tr').length===1`);
  await route(a, 'traffic', '[name=search]');
  const sharing = await api(manager, admin, 'GET', `/agents/${fixture.shared}/sharing`);
  await api(manager, admin, 'PUT', `/agents/${fixture.shared}/sharing`, {revision:sharing.revision,shares:sharing.shares.map(share=>({username:share.username,engines:share.engines,ports:share.ports,limit_bytes:share.limit_bytes,enabled:share.user_id!==alice.user_id}))});
  await route(a, 'live-config', 'textarea[name=content]');
  await wait(a, `document.querySelector('.a-target-form [name=agent]').value===${JSON.stringify(fixture.owned)}`);
  await saved(a, alice, 'live-config', {agent:fixture.owned});
  await api(b, bob, 'GET', `/agents/${fixture.shared}/configs/mihomo/workspace`);
  await route(a, 'traffic', '[name=search]');
  record(`${mode}: late reads preserve new input; revoked shared nodes fall back to an accessible node and other users retain their own workspace`);

  const sizes = mode.endsWith('mobile') ? [[390,844],[844,390],[768,1024]] : [[1920,1080],[3840,2160],[1280,900],[960,800]];
  for (const [width,height] of sizes) {
    await call('Emulation.setDeviceMetricsOverride', {width,height,deviceScaleFactor:1,mobile:mode.endsWith('mobile')}, a);
    assert.ok(await evaluate(a, 'document.documentElement.scrollWidth<=innerWidth+1'), `${width}: no horizontal page overflow`);
    await screenshot(a, `filters-${width}x${height}`);
  }
  record(`${mode}: filter layout verified at ${sizes.map(size=>size.join('x')).join(', ')} CSS pixels, DPR 1, simulated Chromium`);
}
