import { installAgentFixture } from '/assets/browser/agent-fixture.mjs';
const params = new URLSearchParams(location.search);
const kind = params.get('kind') || 'certificates';
Date.now = () => Date.parse('2026-10-18T05:00:00Z');
const scenario = installAgentFixture(kind === 'client' ? 'client-layout' : kind === 'substore' ? 'substore-layout' : kind === 'bbr' ? 'bbr-preview' : kind === 'connections' ? 'connections' : kind === 'batch' ? 'batch-layout' : kind === 'traffic' ? 'traffic-layout' : kind === 'retry' ? 'tasks-preview' : kind === 'users' ? 'users-layout' : kind === 'shared' ? 'shared-node' : kind === 'logs' ? 'logs' : kind === 'config' ? 'config-layout' : 'admin');
window.reviewScenario = scenario;
const json = (data, status = 200) => new Response(JSON.stringify(data), {status, headers:{'Content-Type':'application/json'}});
const cert = {id:'review-cert', name:'边缘节点证书', domains:['edge.example.test','api.example.test'], owned:true, version:2, source:'caddy', method:'http-01', ca:'letsencrypt', key_type:'ecdsa256', status:'issued', not_before:'2026-10-01T00:00:00Z', not_after:'2027-01-01T00:00:00Z', auto_renew:false, sync_status:'synced', last_synced:'2026-10-18T04:00:00Z', source_agent:'香港 · HK-01', environment:'system-trusted', fingerprint:'ab:'.repeat(31)+'cd', binding_count:1, pending_count:0};
const waiting = {...cert, id:'review-dns',name:'手动 DNS 证书',domains:['*.example.test'],source:'acme',method:'dns-manual',version:0,status:'waiting_dns',not_after:null};
const accounts = [{id:'acme-1',kind:'acme',name:'签发账号',ca:'letsencrypt-staging',email:'ops@example.test'},{id:'dns-1',kind:'dns',name:'Cloudflare DNS',provider:'cloudflare'}];
const httpNodes = [{id:'alpha',name:'香港 · HK-01',available:true,owned:true,domains:[]},{id:'bravo',name:'新加坡 · SG-02',available:true,owned:false,domains:['edge.example.test']},{id:'charlie',name:'东京 · JP-03',available:false,owned:false,domains:[],reason:'节点离线'}];
const fetchFixture = window.fetch;
window.reviewCalls = [];
window.fetch = async (input, options={}) => {
  const path = new URL(input instanceof Request ? input.url : input,location.href).pathname.replace(/^\/api\/v1/,'');
  if (path==='/traffic-endpoints/sync' && window.reviewTrafficFailure) return json({error:window.reviewTrafficFailure},500);
  if (path.startsWith('/certificate') || path.endsWith('/sharing')) {
    window.reviewCalls.push({path, method:options.method||'GET',body:options.body});
    if (window.reviewMutationGate && options.method && options.method !== 'GET') await window.reviewMutationGate;
    if (window.reviewFailure && options.method && options.method !== 'GET') return json({error:typeof window.reviewFailure==='string'?window.reviewFailure:'域名验证失败，请检查 DNS 记录。'},400);
    if (path==='/certificates') return json({items:window.reviewEmpty?[]:[cert,waiting],encryption_ready:!window.reviewLocked});
    if (path==='/certificate-accounts') return json(accounts);
    if (path==='/certificate-http-nodes') return json(httpNodes);
    if (path==='/certificate-http-grants') return json([{agent_id:'bravo',username:'alice',domain:'edge.example.test'}]);
    if (path==='/certificate-managed-sources') return json([{id:'source-1',certificate_id:cert.id,domain:'edge.example.test',agent_name:'香港 · HK-01',engine:'sing-box',status:'synced',last_synced:cert.last_synced},{id:'source-2',domain:'offline.example.test',agent_name:'东京 · JP-03',engine:'xray',status:'offline',error:'节点离线，等待重新连接。'}]);
    if (path.endsWith('/sharing')) return json({revision:1,shares:[{id:'share-1',username:'alice',user_id:'alice-id',engines:['mihomo','xray'],ports:[21000,21001],limit_bytes:10737418240,used_bytes:1073741824,enabled:true,status:'accepted',http_domains:['edge.example.test']}]});
    const item=path.includes('review-dns')?waiting:cert;
    return json({certificate:item,bindings:item===cert?[{agent_name:'新加坡 · SG-02',engine:'sing-box',config_name:'WebSocket TLS',username:'alice',owner_id:'alice-id',version:2,authorized:true,status:'synced'}]:[],grants:[{username:'alice',user_id:'alice-id'}],versions:[{version:2,not_after:cert.not_after,fingerprint:cert.fingerprint}],orders:item===waiting?[{id:'order-1',created_at:'2026-10-18T04:00:00Z',status:'waiting_dns',stage:'waiting_dns',attempt:1,challenges:[{domain:'*.example.test',status:'pending',type:'dns-01',name:'_acme-challenge.example.test',value:'fixture-dns-token-20261018'}]}]:[]});
  }
  return fetchFixture(input,options);
};
if (kind==='inbound') {
  const {createShellAppearance}=await import('/assets/modules/shell-appearance.js');
  createShellAppearance({data:{}}).applyTheme();
  const engine=params.get('engine')||'xray', protocol=params.get('protocol')||'vless';
  if(params.get('embedded')==='1') {
    const {createConfigFixture}=await import('/assets/browser/config-fixture.mjs');
    window.reviewConfig=await createConfigFixture(engine,{protocol});
  }else{
    const {fixture}=await import('/preset-fixture.mjs');
    const cases=await fetch('/assets/preset-plans.json').then(r=>r.json());
    await fixture(engine,false,cases.find(c=>c.engine===engine&&c.protocol.key===protocol));
  }
} else if (kind==='substore'||kind==='config-editor') {
  const {createShellAppearance}=await import('/assets/modules/shell-appearance.js');
  createShellAppearance({data:{}}).applyTheme();
  if(kind==='substore'){
    const {subStoreFixture}=await import('/substore-fixture.mjs');
    await subStoreFixture();
  }else{
    const {createConfigFixture}=await import('/assets/browser/config-fixture.mjs');
    window.reviewConfig=await createConfigFixture('mihomo');
  }
} else if (kind==='xhttp'||kind==='tls'||kind==='reality') {
  const {createShellAppearance}=await import('/assets/modules/shell-appearance.js');
  createShellAppearance({data:{}}).applyTheme();
  const {fixture}=await import('/preset-fixture.mjs');
  const cases=await fetch('/assets/preset-plans.json').then(r=>r.json());
  await fixture(kind==='tls'?'sing-box':'xray',false,cases.find(c=>kind==='tls'?c.engine==='sing-box'&&c.protocol.key==='vless-ws-tls':c.engine==='xray'&&c.protocol.key===(kind==='reality'?'vless':'vless-xhttp-tls')));
} else {
  location.hash=kind==='client'?'#client-access':kind==='substore'?'#substore-sync':kind==='bbr'?'#system-bbr':kind==='connections'?'#client-connections':kind==='batch'?'#node-settings':kind==='traffic'?'#traffic':kind==='retry'?'#tasks':kind==='sharing'?'#node-settings':kind==='settings'?'#settings-certificates':kind==='users'?'#users':kind==='shared'?'#settings-node-alpha':kind==='config'?'#live-config?agent=alpha&engine=mihomo':kind==='logs'?'#core-logs':'#certificates';
  await import('/assets/app.js');
}
if(document.documentElement.dataset.reviewVersion==='after'||kind==='inbound'){
  const {installSelectMenus}=await import('/assets/modules/select-menu.js');
  installSelectMenus();
}
document.documentElement.dataset.reviewReady='true';
