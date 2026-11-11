import { assert, waitFor } from "./assertions.mjs";
import { startAnalytics } from "../analytics/app.js";
import { accountStorage } from "../modules/account-storage.js";

export async function testTrafficLayoutRuntime({ testAPI }) {
    await startTrafficTheme();
    await testTrafficScopeAndFormContracts(testAPI);
    const trafficReads = () => testAPI.calls.filter(call => call.path.startsWith('/traffic-policies') || call.path === '/traffic-endpoints').length;
    const revisit = async () => {
      location.hash = '#themes';
      await waitFor(() => document.querySelector('[data-theme-i-d]') || document.querySelector('.a-preview'), 'theme route did not load');
      location.hash = '#traffic';
      await waitFor(() => document.querySelector('.a-traffic-page [data-traffic-key]'), 'traffic snapshot return failed');
    };
    const firstReads = trafficReads();
    await revisit();
    assert.equal(trafficReads(), firstReads, 'returning within the account snapshot window repeated traffic reads');
    const clock = Date.now;
    try {
      const expired = clock() + 31000;
      Date.now = () => expired;
      await revisit();
      assert.equal(trafficReads(), firstReads + 2, 'expired traffic snapshot did not reload policies and configured ports');
    } finally { Date.now = clock; }
    [...document.querySelectorAll('.a-main button')].find(button => button.textContent === '列表').click();
    const cardNodes = () => [...document.querySelectorAll('.a-traffic-list tbody tr')].map(row => testAPI.trafficPolicies.find(policy => row.dataset.trafficKey === `policy:${policy.id}`)?.agent_id).join(',');
    const selectedNodes = () => [...document.querySelector('[name="agent"]').options].slice(1).map(option => option.value).join(',');
    assert.equal(cardNodes(), "alpha,delta,bravo,charlie", "traffic rows must follow saved node order");
    assert.equal(cardNodes(), selectedNodes(), "rows and node picker order disagree");
    assert.equal(accountStorage.getItem("qcontrolhub:traffic-card-order"), "[]", "rendering must not freeze a custom order");
    const button = (text, root = document) => [...root.querySelectorAll('button')].find(button => button.textContent === text);
    const active = () => [...document.querySelectorAll('dialog[open]')].at(-1);
    const close = async box => {
      box.querySelector('[aria-label="关闭"]').click();
      await waitFor(() => !box.isConnected, 'dialog did not close');
    };
    button('统计详情').click();
    let detailDialog = await waitFor(() => active(), 'statistics dialog missing');
    assert.ok(!detailDialog.querySelector('h2').textContent.includes(':8443'), 'standalone port retains colon');
    const diagnostic = detailDialog.querySelector('details');
    assert.ok(!diagnostic.open, 'diagnostics must start collapsed');
    diagnostic.querySelector('summary').click();
    assert.ok(diagnostic.open && diagnostic.textContent.includes('single-protocol'), 'original diagnostic inaccessible');
    assert.ok(detailDialog.textContent.includes('仅监听端口') && !detailDialog.textContent.includes('统计异常'), 'scope limitation rendered as error');
    const policy = testAPI.trafficPolicies[0];
    const original = structuredClone(policy);
    const diagnose = async (error, expected) => {
      policy.enforcement_error = error;
      await waitFor(() => detailDialog.textContent.includes(expected), `native diagnostic missing: ${expected}`);
    };
    await diagnose(`${policy.enforcement_error}; read failed`, '统计异常');
    await diagnose('dual accounting unavailable: outbound tags must be present and unique', '重复标签须修改配置，不能合并计数');
    await diagnose('dual accounting unavailable: outbounds[1].tag duplicates an earlier outbound; assign distinct tags and update the intended route targets', '使用不同标签并同步修改路由');
    await diagnose('<script>window.trafficInjected=true</script><img src=x onerror="window.trafficInjected=true">', '<script>');
    assert.equal(detailDialog.querySelectorAll('script,img').length, 0, 'Agent diagnostic became executable markup');
    assert.ok(!window.trafficInjected, 'Agent diagnostic executed');
    policy.enforcement_error = ''; policy.enforcement_available = false;
    await waitFor(() => detailDialog.textContent.includes('暂未提供详细诊断'), 'unavailable accounting lacks fallback diagnostic');
    Object.assign(policy, original);
    policy.enforcement_error = 'API connection refused';
    await waitFor(() => detailDialog.textContent.includes('API connection refused'), 'open status dialog did not refresh failure');
    assert.ok(detailDialog.open && diagnostic.open, 'refresh closed dialog or diagnostics');
    policy.enforcement_error = '';
    policy.accounting = {source:'core-api',client_received:1024,client_sent:2048,target_received:4096,target_sent:8192};
    await waitFor(() => detailDialog.textContent.includes('双链路统计') && !detailDialog.textContent.includes('API connection refused'), 'open dialog did not recover');
    const previous = detailDialog.textContent;
    policy.accounting.target_sent = 16384;
    await waitFor(() => detailDialog.textContent !== previous, 'open counters did not refresh');
    assert.ok(detailDialog.open, 'counter refresh closed dialog');
    assert.ok(detailDialog.textContent.includes('不等同于本月总量'), 'metering generation counters confused with monthly totals');
    for (const label of ['客户端至代理', '代理至客户端', '目标至代理', '代理至目标']) assert.ok(detailDialog.textContent.includes(label), `dual counter lost: ${label}`);
    policy.engine = 'mihomo'; policy.accounting.source = 'nft-dual';
    await waitFor(() => detailDialog.textContent.includes('双链路 · 范围受限'), 'Mihomo partial egress not identified');
    assert.ok(detailDialog.textContent.includes('非全局单播目标仅计入口，出口不补算或翻倍'), 'Mihomo scope limitation missing');
    delete policy.accounting; Object.assign(policy, original);
    await close(detailDialog);
    button('统计详情', document.querySelectorAll('.a-traffic-list tbody tr')[2]).click();
    detailDialog = await waitFor(() => active(), 'failure detail missing');
    assert.ok(detailDialog.textContent.includes('统计异常'), 'real accounting failure not rendered as error');
    await close(detailDialog);
    button('设置配额').click();
    const editDialog = await waitFor(() => active()?.querySelector('form') && active(), 'quota editor missing');
    await fitDialog(editDialog);
    const quota = editDialog.querySelector('[name="limit_gb"]');
    assert.ok(quota && !quota.required && Number(quota.value) === 0 && quota.checkValidity(), 'monitor-only quota must allow no limit');
    quota.value = ''; assert.ok(quota.checkValidity(), 'empty quota must be valid');
    quota.value = '0'; assert.ok(quota.checkValidity(), 'zero quota must be valid');
    await close(editDialog);
    if (new URLSearchParams(location.search).has('preview')) { document.documentElement.dataset.trafficPreview = 'ready'; await new Promise(() => {}); }
    testAPI.nodeOrder = {ids:['bravo','alpha','delta','charlie'],revision:testAPI.nodeOrder.revision + 1};
    await waitFor(() => cardNodes() === 'bravo,alpha,delta,charlie', 'polling did not apply updated node order');
    assert.equal(cardNodes(), selectedNodes(), 'polling row/picker order disagree');
    testAPI.trafficOrder = {ids:['charlie:8445','alpha:8443'],revision:testAPI.trafficOrder.revision + 1};
    await waitFor(() => cardNodes() === 'charlie,alpha,bravo,delta', 'custom port order must take priority');
    const picker = document.querySelector('[name="agent"]');
    picker.value = 'bravo'; picker.dispatchEvent(new Event('change',{bubbles:true}));
    assert.equal(cardNodes(),'bravo','node filtering did not keep selected node');
    picker.value = ''; picker.dispatchEvent(new Event('change',{bubbles:true}));
    assert.equal(cardNodes(),'charlie,alpha,bravo,delta','clearing filter lost custom order');
    const posts = () => testAPI.calls.filter(call => call.path === "/traffic-endpoints/sync" && call.method === "POST").length;
    const openSync = async () => {
      button('同步配置端口').click();
      return waitFor(() => document.querySelector('dialog[data-traffic-sync][open]'), "sync selection dialog did not open");
    };
    let dialog = await openSync();
    await waitFor(() => dialog.querySelectorAll('input[name^="port:"]').length === 3, "candidates did not load");
    assert.equal(posts(), 0, "preview must not write");
    assert.ok(dialog.querySelector('[type=submit]').disabled, "nothing selected by default");
    assert.ok(dialog.textContent.includes("已删除，可恢复") && dialog.textContent.includes("新增"), "candidate types missing");
    await close(dialog);
    assert.equal(posts(), 0, "cancel must not write");
    assert.ok(!button('同步配置端口').disabled, "cancel must unlock sync");
    testAPI.trafficPreviewFailure = true;
    dialog = await openSync();
    await waitFor(() => !button('重新扫描',dialog).disabled && [...dialog.querySelectorAll('.a-notice')].some(item => item.textContent), "preview failure must allow retry");
    assert.ok(dialog.textContent.includes("服务器处理失败"), "Chinese preview error hidden");
    testAPI.trafficPreviewFailure = false;
    button('重新扫描',dialog).click();
    await waitFor(() => dialog.querySelectorAll('input[name^="port:"]').length === 3, "retry did not load");
    dialog.querySelector('[name=select-all]').click();
    assert.equal(dialog.querySelectorAll('input[name^="port:"]:checked').length, 3, "select all failed");
    dialog.querySelector('input[name="port:2"]').click();
    assert.ok(dialog.querySelector('[name=select-all]').indeterminate, "partial selection indicator missing");
    const reads = testAPI.calls.filter(call => call.path === "/traffic-policies").length;
    await waitFor(() => testAPI.calls.filter(call => call.path === "/traffic-policies").length > reads, "background poll stopped");
    assert.ok(dialog.open && dialog.querySelectorAll('input[name^="port:"]:checked').length === 2, "polling reset selection");
    assert.ok(button('同步配置端口').disabled, "polling unlocked duplicate dialog");
    testAPI.trafficSyncFailure = true;
    dialog.querySelector('[type=submit]').click();
    await waitFor(() => posts() === 1 && !dialog.querySelector('[type=submit]').disabled
      && [...dialog.querySelectorAll('.a-notice')].some(item => item.textContent)
      && dialog.textContent.includes("服务器处理失败"), "Chinese mutation error hidden");
    assert.equal(dialog.querySelectorAll('input[name^="port:"]:checked').length, 2, "failure lost selection");
    testAPI.trafficSyncFailure = false;
    let finish;
    testAPI.trafficSyncGate = new Promise(resolve => { finish = resolve; });
    dialog.querySelector('[type=submit]').click();
    dialog.querySelector('[type=submit]').click();
    assert.equal(posts(), 2, "duplicate submission not blocked");
    dialog.querySelector('[aria-label=关闭]').click();
    assert.ok(dialog.open && dialog.isConnected, 'pending mutation must prevent close');
    assert.equal(JSON.stringify(testAPI.trafficSelection), JSON.stringify([{agent_id:"alpha",port:443},{agent_id:"bravo",port:9443}]), "must send only selected identities");
    assert.equal(testAPI.trafficSnapshot, "scan-version-1", "submit must carry the scanned configuration snapshot");
    finish();
    await waitFor(() => !document.querySelector('dialog[data-traffic-sync]') && !button('同步配置端口').disabled, "success did not close and unlock");
    assert.equal(testAPI.trafficCandidates.length, 1, "unselected candidate changed");
    testAPI.trafficSyncGate = null;
    dialog = await openSync();
    await waitFor(() => dialog.querySelectorAll('input[name^="port:"]').length === 1, "reopen did not reload remaining candidates");
    await close(dialog);
    testAPI.trafficCandidates = [];
    dialog = await openSync();
    await waitFor(() => dialog.textContent.includes("当前筛选范围内暂无可同步端口"), "empty state missing");
    assert.ok(dialog.querySelector('[type=submit]').disabled, "empty list allows write");
    await close(dialog);
    let finishPreview;
    testAPI.trafficPreviewGate = new Promise(resolve => { finishPreview = resolve; });
    await openSync();
    location.hash = "#node-settings";
    await waitFor(() => !document.querySelector('dialog[data-traffic-sync]'), "navigation left a modal behind");
    finishPreview();
    assert.equal(posts(), 2, "navigation must not write");
    testAPI.trafficPreviewGate = null;
    location.hash = "#traffic";
    await waitFor(() => !document.querySelector(".a-main [aria-busy]") && button('同步配置端口'), "traffic return failed");
    testAPI.trafficCandidates = [{agent_id:"alpha",name:"late selection",engine:"xray",port:11443,protocol:"both",kind:"new"}];
    dialog = await openSync();
    await waitFor(() => dialog.querySelector('input[name^="port:"]'), "late selection missing");
    dialog.querySelector('input[name^="port:"]').click();
    testAPI.trafficSyncGate = new Promise(resolve => { finish = resolve; });
    dialog.querySelector('[type=submit]').click();
    assert.equal(posts(), 3, "selected navigation test did not submit");
    location.hash = "#node-settings";
    await waitFor(() => location.hash === '#traffic', 'pending write navigation was not blocked');
    assert.ok(dialog.open, 'blocked navigation lost pending dialog');
    await startTrafficTheme('#node-settings');
    assert.ok(!document.querySelector('dialog[data-traffic-sync]'), 'ended account lifetime left pending dialog');
    finish();
    await waitFor(() => testAPI.trafficCandidates.length === 0, "late mutation did not finish");
    assert.ok(!button('同步配置端口'), "late mutation returned to traffic route");
    testAPI.trafficSyncGate = null;
    location.hash = "#traffic";
    await waitFor(() => button('同步配置端口'), "traffic return before expiry failed");
    testAPI.trafficCandidates = [
      {agent_id:"alpha",name:"stable protocol",engine:"sing-box",port:25103,previous_port:25101,protocol:"tcp",kind:"changed",deployment_status:"pending",blocked_reason:"已保存但尚未部署"},
      {agent_id:"alpha",name:"active protocol",engine:"sing-box",port:25101,protocol:"tcp",kind:"unchanged",deployment_status:"applied"},
    ];
    dialog = await openSync();
    await waitFor(() => dialog.querySelectorAll('input[name^="port:"]').length === 2, "versioned candidates missing");
    assert.ok(dialog.textContent.includes("旧 25101 · 新 25103") && dialog.textContent.includes("待部署"), "port/deployment change missing");
    assert.ok(dialog.querySelector('input[name="port:0"]').disabled, "undeployed port is selectable");
    dialog.querySelector('[name=select-all]').click();
    assert.equal(dialog.querySelectorAll('input[name^="port:"]:checked').length, 1, "select all included blocked port");
    testAPI.trafficSyncStale = true;
    dialog.querySelector('[type=submit]').click();
    await waitFor(() => dialog.textContent.includes("候选已失效"), "stale submission did not clear choices");
    assert.ok(dialog.querySelector('[type=submit]').disabled, "stale candidate remains submittable");
    testAPI.trafficSyncStale = false;
    button('重新扫描',dialog).click();
    await waitFor(() => dialog.querySelectorAll('input[name^="port:"]').length === 2, "rescan did not refresh choices");
    assert.equal(dialog.querySelectorAll('input[name^="port:"]:checked').length, 0, "rescan retained old selection");
    const bounds = dialog.getBoundingClientRect();
    assert.ok(bounds.left >= -1 && bounds.right <= innerWidth + 1 && dialog.scrollWidth <= dialog.clientWidth + 1, "sync dialog overflows viewport");
    await close(dialog);
    testAPI.trafficPreviewUnauthorized = true;
    button('同步配置端口').click();
    await waitFor(() => document.querySelector("#analytics-login-form") || document.querySelector(".a-login form"), "expired session did not reach login");
    assert.ok(!document.querySelector('dialog[data-traffic-sync][open]'), "expired session left a modal blocking login");
    await waitFor(() => !document.querySelector('dialog[data-traffic-sync]'), "expired session did not remove the closed modal");
    assert.ok(!document.querySelector('dialog[data-traffic-sync]'), "expired session left an orphan modal blocking login");
}

async function testTrafficScopeAndFormContracts(testAPI) {
  const originalPolicies = testAPI.trafficPolicies, originalEndpoints = testAPI.trafficEndpoints, originalAgents = testAPI.agents;
  testAPI.agents = originalAgents.map(agent => agent.id === 'bravo' ? { ...agent, capabilities: ['xray'] } : agent);
  const button = text => [...document.querySelectorAll('.a-main button')].find(control => control.textContent === text);
  const keys = () => [...document.querySelectorAll('.a-main [data-traffic-key]')].map(item => item.dataset.trafficKey);
  const foreign = { ...originalPolicies[0], id: 'foreign-policy', agent_id: 'bravo', port: 24443, is_current_user: false, owner_id: 'another-user', owner_name: '其他用户测试' };
  testAPI.trafficPolicies = [...originalPolicies, foreign];
  await startTrafficTheme();
  assert.ok(!keys().includes('policy:foreign-policy'), 'default traffic scope exposes another user');
  const scope = document.querySelector('[name=scope]');
  scope.value = 'all'; scope.dispatchEvent(new Event('change', { bubbles: true }));
  assert.ok(keys().includes('policy:foreign-policy'), 'authorized all scope loses a visible foreign port');
  assert.ok(document.querySelector('[data-traffic-key="policy:foreign-policy"]').textContent.includes('其他用户测试'), 'foreign port ownership missing');
  scope.value = 'current'; scope.dispatchEvent(new Event('change', { bubbles: true }));
  const endpoint = { agent_id: 'alpha', engine: 'mihomo', port: 25443, protocol: 'udp', name: '新发现监听', is_current_user: true };
  testAPI.trafficEndpoints = [endpoint];
  await waitFor(() => keys().some(key => key.includes(':25443:')), 'polling did not discover a newly deployed endpoint');
  testAPI.trafficEndpoints = [];
  await waitFor(() => !keys().some(key => key.includes(':25443:')), 'polling retained a removed configured endpoint');
  button('添加配额').click();
  let box = document.querySelector('dialog[open]'), form = box.querySelector('form');
  const agent = form.elements.agent_id;
  const first = agent.value;
  const other = [...agent.options].find(option => option.value !== first).value;
  agent.value = other; agent.dispatchEvent(new Event('change', { bubbles: true }));
  assert.equal([...form.elements.engine.options].map(option => option.value).join(','), testAPI.agents.find(value => value.id === other).capabilities.join(','), 'changing quota node kept stale engines');
  box.close();
  await waitFor(() => !box.isConnected, 'quota dialog did not dispose');
  button('添加配额').click();
  box = document.querySelector('dialog[open]'); form = box.querySelector('form');
  assert.equal(form.elements.agent_id.value, first, 'new quota inherited the previous dialog node');
  assert.equal([...form.elements.engine.options].map(option => option.value).join(','), testAPI.agents.find(value => value.id === first).capabilities.join(','), 'new quota inherited the previous node engines');
  box.close(); await waitFor(() => !box.isConnected, 'new quota dialog did not dispose');
  assert.ok(!testAPI.calls.some(call => call.method !== 'GET'), 'rendering or cancelling quota wrote data');
  button('管理').click();
  button('批量操作').click();
  box = document.querySelector('dialog[open]');
  assert.ok(box.matches(':modal'), 'batch selection must block background filters and dragging');
  assert.ok(!box.querySelector('[name="foreign-policy"]'), 'current-user batch includes another owner');
  box.querySelector(`input[name="${originalPolicies[0].id}"]`).click();
  box.close(); await waitFor(() => !box.isConnected, 'batch selection did not dispose');
  scope.value = 'all'; scope.dispatchEvent(new Event('change', { bubbles: true }));
  button('批量操作').click();
  box = document.querySelector('dialog[open]');
  assert.ok(box.querySelector('[name="foreign-policy"]'), 'authorized all-scope batch omitted a visible target');
  assert.equal(box.querySelectorAll('input[type=checkbox]:checked').length, 0, 'batch selection survived a scope change');
  assert.ok([...box.querySelectorAll('button')].find(control => control.textContent === '重试未完成项').hidden, 'new scope retained a previous retry');
  box.close(); await waitFor(() => !box.isConnected, 'all-scope batch did not dispose');
  const nodeReads = testAPI.calls.filter(call => call.path === '/agents' || /\/agents\/.+\/region/.test(call.path)).length;
  testAPI.agents[0].region_code = 'HK';
  await startTrafficTheme('#traffic', { role: 'user', user_id: 'traffic-reader', permissions: ['traffic.read'], csrf_token: 'browser-test-csrf' });
  assert.equal(testAPI.calls.filter(call => call.path === '/agents' || /\/agents\/.+\/region/.test(call.path)).length,nodeReads,'traffic-only view read general node metadata');
  assert.ok(document.querySelector('.a-traffic-page [data-region-code=HK]'),'traffic-only view lost minimal region metadata');
  assert.ok(!button('添加配额') && !button('批量操作') && !button('同步配置端口'), 'readonly traffic exposes management actions');
  assert.ok(!button('设置配额') && !button('编辑配额'), 'readonly port exposes a quota editor');
  button('统计详情').click();
  box = document.querySelector('dialog[open]');
  assert.ok(box && ![...box.querySelectorAll('button')].some(control => ['清零', '取消配额', '删除'].includes(control.textContent)), 'readonly statistics exposes mutation actions');
  box.close(); await waitFor(() => !box.isConnected, 'readonly statistics did not dispose');
  testAPI.trafficPolicies = originalPolicies; testAPI.trafficEndpoints = originalEndpoints; testAPI.agents = originalAgents;
  await startTrafficTheme();
}

async function startTrafficTheme(route = '#traffic', sessionOverride) {
  window.dispatchEvent(new Event('pagehide'));
  document.querySelectorAll('link[rel=stylesheet]').forEach(link=>link.remove());
  const style=Object.assign(document.createElement('link'),{rel:'stylesheet',href:'/assets/analytics/theme.css'});
  document.head.append(style);
  await new Promise((resolve,reject)=>{style.onload=resolve;style.onerror=reject;});
  document.body.replaceChildren(Object.assign(document.createElement('div'),{id:'app'}));
  history.replaceState(null,'',route);
  const session=await(await fetch('/api/v1/auth/session')).json();
  startAnalytics({app:document.querySelector('#app'),session:sessionOverride || session});
  await waitFor(()=>document.querySelector(route === '#traffic' ? '.a-traffic-page [data-traffic-key]' : '.a-probe'),'native theme did not load');
}
async function fitDialog(dialog) {
  await Promise.all(dialog.getAnimations({subtree:true}).filter(animation=>animation.effect?.getTiming().iterations!==Infinity).map(animation=>animation.finished.catch(()=>{})));
  assert.ok(dialog.scrollWidth<=dialog.clientWidth+1,'quota dialog overflows horizontally');
  const bounds=dialog.getBoundingClientRect();
  assert.ok(bounds.top>=-1&&bounds.bottom<=innerHeight+1,'quota dialog outside viewport');
  for(const control of dialog.querySelectorAll('input,select,button')) {
    control.scrollIntoView({block:'nearest'});
    const box=control.getBoundingClientRect();
    assert.ok(box.left>=bounds.left-1&&box.right<=bounds.right+1&&box.top>=0&&box.bottom<=innerHeight+1,'quota control unreachable');
  }
}
export async function testDenseTrafficLayoutRuntime({ testAPI }) {
  await startTrafficTheme();
  const key = index => `policy:${testAPI.trafficPolicies[index].id}`;
  const item = index => document.querySelector(`[data-traffic-key="${key(index)}"]`);
  const quota = (index, expected) => {
    const meter = item(index).querySelector('[role=meter]');
    assert.ok(meter, 'quota usage meter missing');
    assert.equal(meter.getAttribute('aria-valuenow'), String(Math.min(100, expected)), 'quota fill does not match the API value');
    assert.ok(meter.getAttribute('aria-valuetext').includes(expected < 0.1 && expected > 0 ? '不足0.1%' : `${expected}%`), 'quota percentage missing or capped in text');
    const bounds = meter.getBoundingClientRect();
    assert.ok(bounds.width > 0 && bounds.right <= item(index).getBoundingClientRect().right, 'quota meter escaped its record');
  };
  const inspectQuotas = () => {
    for (const [index, expected] of [[0, 88.8], [1, 20.8], [2, 157.7], [5, 0], [6, 75]]) quota(index, expected);
    assert.ok(item(2).querySelector('[role=meter] .bad'), 'over-quota state lost warning color');
    assert.ok(!item(4).querySelector('[role=meter]') && item(4).textContent.includes('未设置配额'), 'monitor-only port fabricates a quota');
    assert.ok(item(6).textContent.includes('共享配额') && item(6).textContent.includes('共享已用75%'), 'shared pool usage confused with port usage');
  };
  inspectQuotas();
  assert.equal(document.querySelector('.a-traffic-page .a-filter-fields .a-field select')?.name,'agent','traffic node filter must be first');
  [...document.querySelectorAll('.a-main button')].find(button => button.textContent === '列表').click();
  inspectQuotas();
  const rows=()=>document.querySelectorAll('.a-traffic-list tbody tr');
  assert.equal(rows().length,30,'dense traffic must page rather than create every row');
  assert.ok(document.querySelector('.a-main').textContent.includes('第 1 / 3 页'),'dense count missing');
  const all=[];
  const inspect=()=>{for(const row of rows()){
    const port=row.querySelector('[data-column="端口 / 协议"] strong');
    assert.match(port.textContent,/^\d+$/,'standalone port includes a colon');
    assert.ok(row.querySelector('[data-column="端口 / 协议"]').textContent.includes('TCP'),'protocol lost');
    assert.ok(![...row.querySelectorAll('button')].some(control=>['删除','清零','排序'].includes(control.textContent)),'low frequency actions crowded the list');
    assert.ok(row.querySelector('.a-badge'),'status lost theme badge');
    all.push(row.dataset.trafficKey);
  }};
  inspect();
  [...rows()[0].querySelectorAll('button')].find(control=>control.textContent==='统计详情').click();
  const detail=document.querySelector('dialog[open]');
  assert.ok([...detail.querySelectorAll('button')].find(control=>control.textContent==='删除')?.classList.contains('danger'),'delete lost danger styling in details');
  detail.querySelector('[aria-label=关闭]').click();
  [...document.querySelectorAll('.a-main button')].find(control=>control.textContent==='下一页').click();
  assert.equal(rows().length,30,'dense final page lost records');inspect();
  assert.equal(new Set(all).size,60,'dense pagination repeats or drops records');
  [...document.querySelectorAll('.a-main button')].find(control=>control.textContent==='下一页' && control.checkVisibility()).click();
  assert.equal(rows().length,1,'61st traffic record missing');
  assert.ok(!all.includes(rows()[0].dataset.trafficKey),'traffic final page repeats records');
  [...document.querySelectorAll('.a-main button')].find(control=>control.textContent==='上一页' && control.checkVisibility()).click();
  const records = [...rows()].map(row => ({ key: row.dataset.trafficKey, total: row.querySelector('[data-column="累计流量"] strong').textContent, receive: row.querySelector('[data-column="累计接收"] .a-stack').firstChild.textContent, send: row.querySelector('[data-column="累计发送"] .a-stack').firstChild.textContent }));
  const view = name => [...document.querySelectorAll('.a-main button')].find(button => button.textContent === name);
  view('卡片').click();
  const cards = [...document.querySelectorAll('article[data-traffic-key]')];
  assert.equal(cards.length,61,'card view must display all records');
  assert.ok(!document.querySelector('.a-main [name=page_size]').checkVisibility(),'cards expose list page size');
  records.forEach((record,index) => {
    const card = cards.find(card => card.dataset.trafficKey === record.key);
    assert.ok(card,'view switch lost a port target');
    assert.equal(card.querySelector('.a-stat > .a-actions').textContent.replace(/\s/g,''), records[index].total.replace(/\s/g,''),'card total differs from list');
    const values = [...card.querySelectorAll('.a-probe-network b')].map(value=>value.textContent);
    assert.equal(values[0],records[index].receive,'card missing cumulative receive');
    assert.equal(values[1],records[index].send,'card missing cumulative send');
    assert.ok(card.textContent.includes('累计流量') && card.textContent.includes('累计接收') && card.textContent.includes('累计发送'),'card cumulative labels missing');
  });
  view('列表').click();
  assert.equal(rows().length,30,'switching views reset pagination');
  assert.ok(document.querySelector('.a-main').textContent.includes('第 2 / 3 页'),'view switch lost page position');

  assert.ok(document.documentElement.scrollWidth<=innerWidth,'dense page overflows horizontally');
  [...document.querySelectorAll('.a-main button')].find(button => button.textContent === '上一页').click();
  const first = item(0), meter = first.querySelector('[role=meter]'), control = first.querySelector('button');
  control.focus();
  const original = structuredClone(testAPI.trafficPolicies[0]);
  testAPI.trafficPolicies[0].used_bytes = 50 * 1024 ** 3;
  await waitFor(() => first.querySelector('[role=meter]')?.getAttribute('aria-valuenow') === '50', 'quota bar did not refresh');
  assert.equal(first, item(0), 'quota refresh replaced the record');
  assert.equal(meter, first.querySelector('[role=meter]'), 'quota refresh replaced the meter');
  assert.equal(document.activeElement, control, 'quota refresh stole keyboard focus');
  testAPI.trafficPolicies[0].used_bytes = 2 ** 64 - 1;
  await waitFor(() => first.querySelector('[role=meter]')?.getAttribute('aria-valuetext').includes('亿%'), 'large quota usage did not refresh');
  assert.equal(first.querySelector('[role=meter]').getAttribute('aria-valuenow'), '100', 'over-quota fill exceeded its track');
  assert.ok(first.scrollWidth <= first.clientWidth && document.documentElement.scrollWidth <= innerWidth, 'large usage overflowed the record');
  testAPI.trafficPolicies[0].used_bytes = undefined;
  await waitFor(() => !first.querySelector('[role=meter]') && first.textContent.includes('用量待同步'), 'unknown usage fabricated a percentage');
  assert.equal(first.querySelector('[data-column="累计流量"] strong').textContent, '—', 'unknown usage became zero');
  Object.assign(testAPI.trafficPolicies[0], original);
  await waitFor(() => first.querySelector('[role=meter]')?.getAttribute('aria-valuenow') === '88.8', 'quota fixture did not restore');
  await testTrafficPageSizes(testAPI);
  if(new URLSearchParams(location.search).has('preview')) { document.documentElement.dataset.trafficPreview = 'ready'; await new Promise(()=>{}); }
}

async function testTrafficPageSizes(testAPI) {
  const original = testAPI.trafficPolicies;
  const button = text => [...document.querySelectorAll('button')].find(control => control.textContent === text && control.checkVisibility());
  const picker = () => document.querySelector('.a-main [name=page_size]');
  const records = () => [...document.querySelectorAll('[data-traffic-key]')];
  const refresh = async count => {
    button('刷新').click();
    await waitFor(() => !button('刷新').disabled && records().length === count, 'traffic page size did not settle');
  };
  await waitFor(() => !picker().disabled, 'traffic preference did not load');
  for (const count of [0,1,29,30,31]) {
    testAPI.trafficPolicies = original.slice(0,count);
    await refresh(Math.min(30,count));
    assert.ok(picker().checkVisibility(), 'single or empty traffic page hides page size');
    if (count===31) { button('下一页').click(); assert.equal(records().length,1); }
  }
  testAPI.trafficPolicies=original.slice(0,29); await refresh(29);
  const choose = async size => {
    picker().value=String(size); picker().dispatchEvent(new Event('change',{bubbles:true}));
    await waitFor(() => !picker().disabled, 'traffic preference save did not settle');
  };
  testAPI.trafficPolicies=original; await refresh(30);
  for (const size of [50,100,200]) {
    await choose(size); assert.equal(records().length,Math.min(size,61));
    assert.equal(testAPI.pageSizes.traffic.page_size,size);
  }
  await startTrafficTheme();
  await waitFor(() => picker().value==='200' && !picker().disabled, 'traffic preference did not survive reentry');
  assert.equal(records().length,61);
  await choose(30);
  const before = records()[0];
  assert.ok(before.querySelector('.a-probe-flag'), 'traffic node flag missing');
}
