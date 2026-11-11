import assert from 'node:assert/strict';
import { setTimeout as delay } from 'node:timers/promises';

// Gate real reads before dispatch, then release them to the isolated API. This
// checks the pending state without fixture data or a production timing delay.
export async function runStructureLoadingBrowser({ base, mode, fixture, createPage, login, call, evaluate, wait, screenshot, record }) {
  const page = await createPage();
  await login(page, 'analytics-admin');
  if (mode === 'desktop') await call('Emulation.setDeviceMetricsOverride', {width:1920, height:1080, deviceScaleFactor:1, mobile:false}, page);
  const source = `(() => {
    const fetch = window.fetch.bind(window);
    window.__held = [];
    let open = false;
    window.__releaseReads = (match = '') => { if (!match) open = true; window.__held.forEach(read => { if (read.path.includes(match)) read.release(); }); };
    window.fetch = (input, options = {}) => {
      const path = String(input), method = String(options.method || 'GET').toUpperCase();
      if (open || method !== 'GET' || !path.includes('/api/v1/') || path.includes('/auth/')) return fetch(input, options);
      return new Promise((resolve, reject) => {
        let done = false;
        const read = {path, release() {
          if (done) return; done = true;
          const failure = window.__failRead;
          if (failure && path.includes(failure.path)) resolve(new Response(JSON.stringify({error:'隔离环境读取失败'}), {status:failure.status, headers:{'Content-Type':'application/json'}}));
          else resolve(fetch(input, options));
        }};
        options.signal?.addEventListener('abort', () => { done = true; reject(options.signal.reason || new DOMException('Canceled', 'AbortError')); }, {once:true});
        window.__held.push(read);
      });
    };
  })()`;
  const script = await call('Page.addScriptToEvaluateOnNewDocument', { source }, page);
  const routes = [
    ['dashboard', '仪表盘'], ['node-settings', '节点'], ['tasks', '任务'], ['my-quota', '共享与额度'],
    ['archive-config', '配置档案'], ['themes', '主题'], ['certificates', '证书'], ['ip-quality', 'IP 质量'],
    ['users', '用户管理'], ['notifications', '通知'], ['traffic', '流量'], ['settings', '面板设置'],
    ['substore-sync', '订阅同步'], ['live-config', '实时配置'], ['system-bbr', 'TCP 调优'],
    ['core-logs', '日志'], ['client-connections', '客户端连接'], ['client-access', '客户端接入'],
  ];
  for (const [route, title] of routes) {
    await call('Page.navigate', { url: `${base}/?structure=${route}#${route}` }, page);
    await wait(page, `document.querySelector('.a-top h1')?.textContent === ${JSON.stringify(title)} && window.__held?.length > 0 && !!document.querySelector('.a-main .a-card,.a-main label,.a-main table')`);
    const structure = await evaluate(page, `(() => {
      const main = document.querySelector('.a-main');
      return { text: main.innerText, labels: main.querySelectorAll('label').length, headers: main.querySelectorAll('th').length,
        cards: main.querySelectorAll('.a-card').length, width: document.documentElement.scrollWidth, viewport: innerWidth };
    })()`);
    assert.ok(structure.labels || structure.headers || structure.cards, `${route}: real structure while reads are held`);
    assert.ok(structure.width <= structure.viewport + 1, `${route}: pending structure fits viewport`);
    assert.notEqual(structure.text.trim(), '正在加载…');
    if (['node-settings', 'live-config', 'settings', 'notifications'].includes(route)) await screenshot(page, `structure-${route}-pending`);
    // A cold page cannot submit blank server settings as loaded defaults.
    if (route === 'settings') assert.ok(await evaluate(page, `document.querySelector('.a-main button[type=submit]')?.disabled`));
    await evaluate(page, 'window.__releaseReads()');
    await wait(page, `!document.querySelector('[aria-busy=true]') && !document.querySelector('.a-main')?.innerText.includes('正在加载…')`);
    await screenshot(page, `structure-${route}-ready`);
    assert.ok(await evaluate(page, `![...document.querySelectorAll('.a-main [role=alert]')].some(el => el.textContent.trim())`), `${route}: real reads finish without errors`);
    record(`${mode}: ${route}: cold structure visible with real reads pending; API data completed in place`);
  }
  for (const [aliases,title] of [
    [['summary','panel-host','traffic-usage','fleet','activity'],'仪表盘'],
    [['enrollment','settings-node'],'节点'],
    [['settings-api-keys'],'API'],
    [['settings-engines','settings-basic','settings-runtime','settings-data','settings-cnip','settings-certificates','settings-deployment','settings-unknown'],'面板设置'],
    [['settings-notify'],'通知'], [['preset-node','agents','agent-config','access-control','access-control-all'],'实时配置'],
    [['new-config','templates','archive'],'配置档案'], [['traffic-new','traffic-all'],'流量'],
  ]) for (const alias of aliases) {
    await evaluate(page, `location.hash=${JSON.stringify('#'+alias)}`);
    await wait(page, `document.querySelector('.a-top h1')?.textContent===${JSON.stringify(title)} && !!document.querySelector('.a-main .a-card') && !document.querySelector('[aria-busy=true]')`);
    assert.ok(await evaluate(page, `![...document.querySelectorAll('.a-main [role=alert]')].some(el=>el.textContent.trim())`), `${alias}: legacy entry completes`);
  }
  await evaluate(page, `location.hash='#tasks'`);
  await wait(page, `document.querySelector('.a-top h1')?.textContent==='任务' && !document.querySelector('[aria-busy=true]')`);
  await evaluate(page, `location.hash='#themes'`);
  await wait(page, `document.querySelector('.a-top h1')?.textContent==='主题' && !document.querySelector('[aria-busy=true]')`);
  await evaluate(page, 'history.back()');
  await wait(page, `document.querySelector('.a-top h1')?.textContent==='任务' && !!document.querySelector('.a-main th')`);
  await evaluate(page, 'history.forward()');
  await wait(page, `document.querySelector('.a-top h1')?.textContent==='主题' && !!document.querySelector('.a-main .a-preview')`);
  record(`${mode}: legacy aliases, settings fallback, backward and forward navigation complete with their real page structure`);
  await call('Page.navigate', {url:`${base}/?structure=independent#node-settings`}, page);
  await wait(page, `window.__held?.some(read=>read.path.endsWith('/agents')) && !!document.querySelector('.a-main input[name=search]')`);
  await evaluate(page, `window.__releaseReads('/agents')`);
  await wait(page, `!!document.querySelector('.a-main [data-agent-id]')`);
  assert.ok(await evaluate(page, `!!document.querySelector('[aria-busy=true]')`), 'node data fills while independent preferences/settings still wait');
  await evaluate(page, `location.hash='#themes'`);
  await wait(page, `document.querySelector('.a-top h1')?.textContent==='主题' && !!document.querySelector('.a-main .a-preview')`);
  await evaluate(page, 'window.__releaseReads()');
  await wait(page, `!document.querySelector('[aria-busy=true]')`);
  assert.equal(await evaluate(page, `document.querySelectorAll('.a-main [data-agent-id]').length`), 0, 'late node results cannot populate another route');
  record(`${mode}: independent data beats held preferences/settings; navigation remains available and stale reads cannot repaint the next route`);
  await call('Page.navigate', {url:`${base}/?structure=initial-failure#node-settings`}, page);
  await wait(page, `window.__held?.some(read=>read.path.endsWith('/agents')) && !!document.querySelector('.a-main th')`);
  await evaluate(page, `window.__failRead={path:'/agents',status:503};window.__releaseReads()`);
  await wait(page, `!!document.querySelector('.a-main [role=alert]')?.textContent`);
  assert.ok(await evaluate(page, `!document.querySelector('.a-main').innerText.includes('保留上次数据') && !document.querySelector('.a-main').innerText.includes('暂无节点')`), 'failed initial read cannot claim retained or empty business data');
  await screenshot(page, 'structure-initial-failure');
  await evaluate(page, `window.__failRead=null;[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='重新读取').click()`);
  await wait(page, `!!document.querySelector('.a-main [data-agent-id]') && !document.querySelector('[aria-busy=true]')`);
  record(`${mode}: initial read failure stays distinct from a successful empty list and failed refresh; retry recovers`);
  await call('Page.removeScriptToEvaluateOnNewDocument', { identifier: script.identifier }, page);
  const navigate = async route => {
    await call('Page.navigate', {url:`${base}/?dialog=${encodeURIComponent(route)}#${route}`}, page);
    await wait(page, `!!document.querySelector('.a-main .a-card') && !document.querySelector('[aria-busy=true]')`);
  };
  const click = async label => evaluate(page, `[...document.querySelectorAll('.a-main button')].find(button => button.textContent === ${JSON.stringify(label)}).click()`);
  for (const [route, trigger, title] of [
    ['certificates', '账号与认证授权', '证书账号与认证授权'],
    ['users', '共享资源', '共享资源'],
    ['live-config', '添加入站', '添加入站'],
    ['live-config?engine=xray', '展开源码', '展开源码'],
  ]) {
    await navigate(route);
    await evaluate(page, source);
    await click(trigger);
    await wait(page, `!!document.querySelector('dialog[open] [aria-label=关闭]') && window.__held.length > 0`);
    assert.ok(await evaluate(page, `!!document.querySelector('dialog[open] label,dialog[open] th')`), `${title}: fields or headers precede reads`);
    await screenshot(page, `structure-dialog-${trigger}-pending`);
    await evaluate(page, 'window.__releaseReads()');
    await wait(page, `!document.querySelector('dialog[open] [aria-busy=true]')`);
    await screenshot(page, `structure-dialog-${trigger}-ready`);
    await evaluate(page, `document.querySelector('dialog[open] [aria-label=关闭]').click()`);
    await wait(page, `!document.querySelector('dialog[open]')`);
    record(`${mode}: ${title}: real dialog fields/headers, delayed data, completion and close`);
  }
  await navigate('node-settings');
  await evaluate(page, source);
  await evaluate(page, `[...document.querySelectorAll('.a-top button')].find(button=>button.textContent==='刷新').click()`);
  await wait(page, `window.__held.some(read => read.path.endsWith('/agents'))`);
  await evaluate(page, `window.__failRead = {path:'/agents',status:503};window.__releaseReads()`);
  await wait(page, `!!document.querySelector('.a-main [role=alert]')?.textContent`);
  assert.ok(await evaluate(page, `!!document.querySelector('.a-main [data-agent-id]')`), 'failed refresh retains authorized rows');
  await screenshot(page, 'structure-refresh-failure');
  await evaluate(page, `window.__failRead=null;[...document.querySelectorAll('.a-main button')].find(button=>button.textContent==='重新读取').click()`);
  await wait(page, `!document.querySelector('[aria-busy=true]') && ![...document.querySelectorAll('.a-main [role=alert]')].some(el=>el.textContent.trim())`);
  record(`${mode}: refresh failure retains data and recovers without rebuilding page`);
  await call('Page.navigate', { url: `${base}/#node-settings` }, page);
  await wait(page, `!!document.querySelector('.a-main [data-agent-id]') && !document.querySelector('[aria-busy=true]')`);
  await evaluate(page, source);
  await evaluate(page, `[...document.querySelectorAll('button')].find(button => button.textContent === '节点目录').click()`);
  await wait(page, `!!document.querySelector('dialog[open] th') && window.__held.length > 0`);
  await screenshot(page, 'structure-directory-pending');
  await evaluate(page, `document.querySelector('dialog[open] [aria-label=关闭]').click()`);
  await wait(page, `!document.querySelector('dialog[open]')`);
  await evaluate(page, 'window.__releaseReads()');
  assert.equal(await evaluate(page, `document.querySelectorAll('dialog[open]').length`), 0, 'closed directory is not resurrected');
  // Cached history must still render filters while its scope is reverified.
  await call('Page.navigate', { url: `${base}/#client-connections` }, page);
  await wait(page, `!!document.querySelector('.a-main input[name=client_ip]') && !document.querySelector('[aria-busy=true]')`);
  record(`${mode}: directory closes during a held request; connection filters survive authenticated cache restoration`);

  // Delay only rule targets and fail one delivery poll. The configuration and
  // table still use the running page; no notification is actually dispatched.
  const notificationScript = await call('Page.addScriptToEvaluateOnNewDocument', {source: `(() => {
    const fetch = window.fetch.bind(window);
    window.__notificationReads = 0;
    window.fetch = async (input, options = {}) => {
      const path = String(input);
      if ((options.method || 'GET') !== 'GET') return fetch(input, options);
      if (path.endsWith('/api/v1/agents')) await new Promise(resolve => { window.__releaseNotificationAgents = resolve; });
      if (path.endsWith('/settings/notifications/deliveries')) {
        const attempt = ++window.__notificationReads;
        return new Response(JSON.stringify(attempt === 2 ? {error:'隔离环境投递记录读取失败'} : [{
          id:'structure-delivery', channel_id:'structure-channel', created_at:new Date().toISOString(), updated_at:new Date().toISOString(),
          event:{type:'agent.offline'}, status:attempt < 3 ? 'pending' : 'accepted', attempts:attempt < 3 ? 0 : 1
        }]), {status:attempt === 2 ? 503 : 200, headers:{'Content-Type':'application/json'}});
      }
      const response = await fetch(input, options);
      if (!path.endsWith('/settings/notifications') || !response.ok) return response;
      const value = await response.json();
      value.configuration.rules.push({id:'structure-rule',name:'延迟目标规则',event:'agent.offline',enabled:false,channels:[],agent_id:${JSON.stringify(fixture.analyticsAgent)}});
      return new Response(JSON.stringify(value), {headers:{'Content-Type':'application/json'}});
    };
  })()`}, page);
  await call('Page.navigate', {url:`${base}/?structure=notification-recovery#notifications`}, page);
  const ruleRow = `[...document.querySelectorAll('.a-main tr')].find(row=>row.textContent.includes('延迟目标规则'))`;
  await wait(page, `${ruleRow}?.querySelector('button')?.disabled === false && !!window.__releaseNotificationAgents`);
  await evaluate(page, `${ruleRow}.querySelector('button').click()`);
  await wait(page, `!!document.querySelector('dialog[open] select[name=agent_id]')`);
  assert.ok(await evaluate(page, `document.querySelector('dialog[open] select[name=agent_id]').matches(':disabled')`), 'dependent target controls wait inside the immediately available rule dialog');
  await evaluate(page, `(()=>{const event=document.querySelector('dialog[open] select[name=event]');event.value='panel.update';event.dispatchEvent(new Event('change',{bubbles:true}));})()`);
  assert.ok(await evaluate(page, `!document.querySelector('dialog[open] input[name=interval_seconds]').matches(':disabled')`), 'panel event is independent of the pending node list');
  await evaluate(page, `document.querySelector('dialog[open] [aria-label="关闭"]').click()`);
  await wait(page, `document.querySelectorAll('dialog[open]').length===2`);
  await evaluate(page, `[...document.querySelectorAll('dialog[open] button')].find(b=>b.textContent==='关闭编辑').click()`);
  await wait(page, `!document.querySelector('dialog[open]')`);
  await evaluate(page, `window.__notificationTitle=document.querySelector('input[name=title]');window.__notificationTitle.value='选项晚到时保留的草稿';window.__notificationTitle.dispatchEvent(new Event('input',{bubbles:true}));window.__releaseNotificationAgents()`);
  await wait(page, `!${ruleRow}?.textContent.includes('正在读取节点')`);
  assert.ok(await evaluate(page, `!${ruleRow}.textContent.includes('节点已不可用') && window.__notificationTitle===document.querySelector('input[name=title]') && window.__notificationTitle.value==='选项晚到时保留的草稿'`), 'late targets update existing rule controls and labels without replacing the template draft');
  await wait(page, `window.__notificationReads===2 && document.querySelector('.a-main').textContent.includes('隔离环境投递记录读取失败')`);
  assert.ok(await evaluate(page, `!!document.querySelector('[data-delivery-id="structure-delivery"]')`), 'failed poll preserves the pending record');
  await screenshot(page, 'structure-notification-poll-failure');
  for (let attempt = 0; attempt < 400; attempt++) {
    if (await evaluate(page, `window.__notificationReads>=3 && document.querySelector('[data-delivery-id="structure-delivery"]')?.closest('tr').textContent.includes('平台已接受')`)) break;
    await delay(50);
  }
  await delay(3500);
  assert.ok(await evaluate(page, `window.__notificationReads===3 && document.querySelector('[data-delivery-id="structure-delivery"]')?.closest('tr').textContent.includes('平台已接受') && ![...document.querySelectorAll('.a-main [role=alert]')].some(el=>el.textContent.trim())`), 'pending delivery polling recovers automatically after a transient failure and stops at the terminal result');
  await screenshot(page, 'structure-notification-poll-recovered');
  await call('Page.removeScriptToEvaluateOnNewDocument', {identifier:notificationScript.identifier}, page);
  record(`${mode}: late notification targets enable rule editing without replacing drafts; pending delivery polling retains rows through 503 and automatically reaches its terminal result`);
  const { targetInfo } = await call('Target.getTargetInfo', {}, page);
  await call('Target.closeTarget', { targetId: targetInfo.targetId });
}
