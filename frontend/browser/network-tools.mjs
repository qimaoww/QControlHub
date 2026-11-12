import { networkToolsPage } from '../analytics/network-tools.js';
import { node } from '../analytics/ui.js';
import { assert, waitFor } from './assertions.mjs';

// Exercise the production form in the existing real-browser region matrix.
export async function testDiagnosticNodeFlags(preview = false) {
  window.dispatchEvent(new Event('pagehide'));
  const controller = new AbortController();
  let active = true, refresh, releaseRegions;
  let regionGate = new Promise(resolve => { releaseRegions = resolve; });
  let agents = [{id:'host-a',region_code:'HK'}, {id:'host-b',region_code:'TW'}];
  let nodes = ['host-a','host-b'].map((agent_id,index) => ({agent_id,instance_id:`share-${index}`,name:`同名节点 ${'长名称'.repeat(12)}`,online:true,capabilities:{tools:{ping:{version:'ICMP'},nexttrace:{version:'NextTrace v1.7.6 (built-in)'}},providers:{'disable-geoip':''},addresses:[`192.0.2.${index+1}`]}}));
  const records = [0,1].map(index => ({id:`record-${index}`,created_at:`2026-11-12T12:0${1-index}:00Z`,state:'succeeded',results:{},request:{parameters:{tool:'ping',protocol:'icmp',count:3,interval_ms:1000,timeout_ms:1000,size:56},bidirectional:index===0,node1:{agent_id:'host-a',instance_id:'share-0',name:'历史来源节点',address:'2001:db8::1'},node2:index===0?{agent_id:'host-b',instance_id:'share-1',name:'历史目标节点',address:'2001:db8::2'}:{name:'192.0.2.99',address:'192.0.2.99'}}}));
  for (const record of records) for (const id of [record.id,...(record.request.bidirectional?[record.id+'-reverse']:[])]) record.results[id]={id,state:'succeeded',output:{sent:3,received:3,loss_percent:0,avg_ms:0.2,min_ms:0.1,max_ms:0.3,stdev_ms:0.1,reached:true}};
  const calls = [];
  const ctx = {signal:controller.signal,current:()=>active,state:{data:{}},can:permission=>['agents.read','tasks.execute'].includes(permission),onRefresh:run=>{refresh=run},api:async path=>{
    calls.push(path);
    if (path==='/agents') { await regionGate; return structuredClone(agents); }
    if (path==='/network-diagnostics/nodes') return structuredClone(nodes);
    if (path.startsWith('/network-diagnostics?')) return structuredClone(records);
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
    await waitFor(()=>control('measurement').options.length===2,'测量记录未载入');
    const latest = control('measurement').options[0].textContent;
    for (const text of ['历史来源节点','2001:db8::1','历史目标节点','2001:db8::2','↔','ICMP']) assert.ok(latest.includes(text),`记录缺少 ${text}`);
    assert.equal(control('measurement').options[0].querySelectorAll('.a-probe-name').length,2,'记录未分行');
    for (const root of [control('measurement').options[0],control('measurement').querySelector('selectedcontent')]) {
      assert.ok(root.querySelector('[data-region-code="HK"]') && root.querySelector('[data-region-code="TW"]'),'记录两端旗帜缺失');
    }
    assert.ok(!latest.includes('最新') && !latest.includes('共享实例'),'记录保留冗余标签');
    const path = control('measurement').querySelector('selectedcontent [data-record-endpoints]');
    const [source, direction, target] = [...path.children].map(child=>child.getBoundingClientRect());
    assert.ok(source.right<direction.left && direction.right<target.left,'方向箭头未独立置于两端之间');
    assert.ok(Math.abs(direction.top+direction.height/2-(source.top+source.height/2))<2,'方向箭头未垂直居中');
    choose('measurement','record-1');
    assert.ok(host.querySelector('[data-refresh-key="record-1"]'),'选择记录与结果不一致');
    assert.equal(control('measurement').options[1].textContent.split('192.0.2.99').length,2,'自定义目标地址重复');
    assert.ok(!calls.some(path=>path.includes('/region')),'诊断身份触发错误地区请求');
    const option = control('node1').options[1];
    control('node1').focus();
    agents[0].region_code='US';regionGate=Promise.resolve();
    await refresh();
    await waitFor(()=>code(1)==='US','刷新未恢复地区');
    assert.equal(control('node1').options[1],option,'地区刷新重建选项');
    assert.equal(document.activeElement,control('node1'),'地区刷新丢失焦点');
    assert.equal(control('address1').value,address,'刷新重置参数');
    assert.equal(control('measurement').value,'record-1','刷新重置历史记录选择');
    assert.ok(control('measurement').querySelector('selectedcontent [data-region-code="US"]'),'地区刷新未更新已选记录旗帜');
    choose('measurement','record-0');
    if (preview) return;
    records[0].request.node1.name += '超长节点名称'.repeat(12);
    records[0].request.node1.address = '2001:db8:1234:5678:abcd:ef01:2345:6789';
    await refresh();
    const selectedRecord = control('measurement').querySelector('selectedcontent');
    assert.ok(selectedRecord.textContent.includes(records[0].request.node1.address),'记录截断 IPv6 数据');
    for (const column of selectedRecord.querySelectorAll('.a-tools-endpoints > div')) assert.ok(column.scrollWidth<=column.clientWidth+1,'长记录信息超出分组');
    selectedRecord.querySelector('img').dispatchEvent(new Event('error'));
    assert.ok(selectedRecord.textContent.includes('US') && selectedRecord.textContent.includes('历史来源节点'),'记录旗帜失败未回退');
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
