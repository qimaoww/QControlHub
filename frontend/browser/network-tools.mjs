import { networkToolsPage } from '../analytics/network-tools.js';
import { node } from '../analytics/ui.js';
import { assert, waitFor } from './assertions.mjs';

// Exercise the production form in the existing real-browser region matrix.
export async function testDiagnosticNodeFlags() {
  window.dispatchEvent(new Event('pagehide'));
  const controller = new AbortController();
  let active = true, refresh, releaseRegions;
  let regionGate = new Promise(resolve => { releaseRegions = resolve; });
  let agents = [{id:'host-a',region_code:'HK'}, {id:'host-b',region_code:'TW'}];
  let nodes = ['host-a','host-b'].map((agent_id,index) => ({agent_id,instance_id:`share-${index}`,name:`同名节点 ${'长名称'.repeat(12)}`,online:true,capabilities:{tools:{ping:{version:'ICMP'},nexttrace:{version:'NextTrace v1.7.6 (built-in)'}},providers:{'disable-geoip':''},addresses:[`192.0.2.${index+1}`]}}));
  const calls = [];
  const ctx = {signal:controller.signal,current:()=>active,state:{data:{}},can:permission=>['agents.read','tasks.execute'].includes(permission),onRefresh:run=>{refresh=run},api:async path=>{
    calls.push(path);
    if (path==='/agents') { await regionGate; return structuredClone(agents); }
    if (path==='/network-diagnostics/nodes') return structuredClone(nodes);
    if (path.startsWith('/network-diagnostics?')) return [];
    throw new Error(`unexpected diagnostic request: ${path}`);
  }};
  const host = node('main',{className:'a-main'},networkToolsPage(ctx));
  document.body.replaceChildren(host);
  const control = name => host.querySelector(`[name=${name}]`);
  const choose = (name,value) => {const input=control(name);input.value=value;input.dispatchEvent(new Event('change',{bubbles:true}));};
  const key = n => `${n.agent_id}/${n.instance_id}`;
  const code = i => control(`node${i}`).querySelector('selectedcontent [data-region-code]')?.dataset.regionCode;
  try {
    await waitFor(()=>control('node1').options.length===3,'地区慢请求阻塞诊断节点');
    choose('node1',key(nodes[0]));choose('node2',key(nodes[1]));
    const start = [...host.querySelectorAll('button')].find(button=>button.textContent==='开始测试');
    assert.ok(!start.disabled,'未加载地区阻塞可用诊断');
    const address = control('address1').value;
    releaseRegions();
    await waitFor(()=>code(1)==='HK' && code(2)==='TW','两端已选旗帜未更新');
    assert.ok(control('node2').querySelector('selectedcontent img[src$="/cn"]'),'未复用台湾旗帜规则');
    assert.equal(control('address1').value,address,'地区更新重置地址');
    assert.ok(!calls.some(path=>path.includes('/region')),'诊断身份触发错误地区请求');
    const option = control('node1').options[1];
    control('node1').focus();
    agents[0].region_code='US';regionGate=Promise.resolve();
    await refresh();
    await waitFor(()=>code(1)==='US','刷新未恢复地区');
    assert.equal(control('node1').options[1],option,'地区刷新重建选项');
    assert.equal(document.activeElement,control('node1'),'地区刷新丢失焦点');
    assert.equal(control('address1').value,address,'刷新重置参数');
    [...host.querySelectorAll('button')].find(button=>button.textContent==='交换两端').click();
    assert.equal(code(1),'TW','交换后节点1旗帜错配');assert.equal(code(2),'US','交换后节点2旗帜错配');
    choose('target_mode','ip');choose('target_mode','');
    assert.equal(code(2),'US','切换目标类型丢失旗帜');
    const image = control('node1').querySelector('option[value="host-b/share-1"] img');
    image.dispatchEvent(new Event('error'));
    assert.ok(control('node1').options[2].textContent.includes('TW'),'图片失败没有地区代码回退');
    agents[1].region_code='';await refresh();
    assert.equal(code(1),'','未知地区保留旧旗帜');
    assert.ok(control('node1').textContent.includes('同名节点'),'未知地区丢失节点名称');
    nodes=nodes.slice(0,1);await refresh();
    assert.equal(control('node1').value,'','授权撤销保留失效选择');
    assert.equal(control('node1').options.length,2,'授权撤销保留失效节点');
    regionGate=new Promise(resolve=>{releaseRegions=resolve});
    const pending=refresh();active=false;controller.abort();releaseRegions();await pending;
    assert.equal(control('node1').options.length,2,'迟到响应改变已销毁页面');
    assert.ok(document.documentElement.scrollWidth<=innerWidth+1,'诊断长节点名称造成页面溢出');
  } finally { active=false;controller.abort(); }
}
