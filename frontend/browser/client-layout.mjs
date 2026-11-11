import { clientAccessExport } from "../modules/client-access-model.js";
import { startAnalytics } from "../analytics/app.js";
import { assert, waitFor, delay } from "./assertions.mjs";

// Client exports shaped like a real fleet: several engines per node, long names,
// single-line YAML, an offline node and a node that still needs an address.
const ss = (host, port, name) => `ss://MjAyMi1ibGFrZTMtYWVzLTEyOC1nY206ZXhhbXBsZS1wYXNzd29yZA@${host}:${port}#${encodeURIComponent(name)}`;
const vless = (host, port, name) => `vless://7f9c2ba4-5e7c-4b1b-9d6a-0c3f1e2d4a5b@${host}:${port}?encryption=none&flow=xtls-rprx-vision&security=reality&sni=www.example.com&fp=chrome&pbk=Z84J2IelR9ch3k8VtlVhhs5ycBUlXA7wHBWcBrjqnAw&sid=6ba85179e30d4fc2&type=tcp#${encodeURIComponent(name)}`;
const field = (label, value, secret = false) => ({ label, value, secret });
const ssFields = (host, port) => [field("协议", "Shadowsocks 2022"), field("服务器", host), field("端口", String(port)), field("加密方法", "2022-blake3-aes-128-gcm"), field("密码", "ZXhhbXBsZS1wYXNzd29yZA==", true)];
const vlessFields = (host, port) => [field("协议", "VLESS"), field("服务器", host), field("端口", String(port)), field("UUID", "7f9c2ba4-5e7c-4b1b-9d6a-0c3f1e2d4a5b", true), field("传输", "tcp"), field("传输安全", "reality"), field("TLS ServerName", "www.example.com"), field("Reality Public Key", "Z84J2IelR9ch3k8VtlVhhs5ycBUlXA7wHBWcBrjqnAw"), field("Reality Short ID", "6ba85179e30d4fc2"), field("客户端指纹", "chrome")];
const profile = (tag, port, protocol, format, uri, fields, extra = {}) => {
  const value = (label) => fields.find((field) => field.label === label)?.value;
  const type = protocol.startsWith("Shadowsocks") ? "ss" : protocol.startsWith("VLESS") ? "vless" : protocol === "Hysteria 2" ? "hysteria2" : protocol === "TUIC v5" ? "tuic" : protocol.toLowerCase();
  const proxy = { name: extra.client_name || tag, type, server: value("服务器"), port };
  if (type === "ss") Object.assign(proxy, { cipher: value("加密方法"), password: value("密码") });
  if (type === "vless") Object.assign(proxy, { uuid: value("UUID"), tls: true, flow: "xtls-rprx-vision", servername: value("TLS ServerName"), "client-fingerprint": "chrome", "reality-opts": { "public-key": value("Reality Public Key"), "short-id": value("Reality Short ID") } });
  if (type === "mieru") Object.assign(proxy, { username: value("用户名"), password: value("用户密码"), transport: "TCP" });
  if (["trojan", "hysteria2", "anytls"].includes(type)) proxy.password = value("密码");
  if (type === "tuic") Object.assign(proxy, { uuid: "7f9c2ba4-5e7c-4b1b-9d6a-0c3f1e2d4a5b", password: "example-password" });
  const mihomo_yaml = `{${Object.entries(proxy).map(([key, value]) => `${key}: ${JSON.stringify(value)}`).join(", ")}}`;
  return { tag, port, protocol, address_mode: "auto", ...extra, mihomo_yaml, profile: { format, uri, fields } };
};
const agent = (id, name, region_code, status = "online") => ({
  id, name, region_code, status, os: "Debian", arch: "amd64", version: "1.2.3", can_manage: true,
  capabilities: ["mihomo", "xray", "sing-box", "ss-rust"], supported_capabilities: ["mihomo", "xray", "sing-box", "ss-rust"],
  features: ["agent-self-upgrade-v1"], labels: {}, metrics: {}, runtime: {},
  last_seen: "2026-09-20T00:00:00Z", enrolled_at: "2026-08-24T00:00:00Z",
});
const entry = (node, engine, address, profiles, extra = {}) => ({
  agent_id: node.id, agent_name: node.name, region_code: node.region_code, agent_status: node.status, engine, address, source: "公网 IPv4",
  address_mode: "auto", profiles, ...extra,
});

export function clientLayoutFixture() {
  const agents = [
    agent("hk-01", "香港 · HK-01", "HK"),
    agent("lax-02", "洛杉矶 · LAX-02", "US"),
    agent("mad-03", "马德里 · MAD-03", "ES"),
    agent("sg-04", "新加坡 · SG-04", "SG"),
    agent("hk-05", "香港 · HK-05", "HK"),
    agent("sjc-06", "圣何塞 · SJC-06", "US"),
    agent("fra-07", "法兰克福 · Frankfurt-edge-with-a-very-long-node-name-07", "DE", "offline"),
    agent("jp-08", "东京 · JP-08", "JP"),
  ];
  const [hk, lax, mad, sg, hk5, sjc, fra, jp] = agents;
  const laxProfiles = (host) => [
    profile("ss-rust-1", 44667, "Shadowsocks 2022", "Shadowsocks SIP002 URI", ss(host, 44667, "LAX-02-v4"), ssFields(host, 44667), { client_name: "LAX-02-v4", address: host }),
    profile("ss-rust-2", 21026, "Shadowsocks 2022", "Shadowsocks SIP002 URI", ss("[2001:db8::21]", 21026, "LAX-02-v6"), ssFields("2001:db8::21", 21026), { client_name: "LAX-02-v6", address: "[2001:db8::21]", address_mode: "ipv6" }),
  ];
  const entries = [
    entry(hk, "ss-rust", "203.0.113.17", [profile("ss-rust-1", 52717, "Shadowsocks 2022", "Shadowsocks SIP002 URI", ss("203.0.113.17", 52717, "ss-rust-1"), ssFields("203.0.113.17", 52717))]),
    entry(lax, "ss-rust", "198.51.100.41", laxProfiles("198.51.100.41"), {
      address_options: [
        { family: "ipv4", address: "198.51.100.41", source: "公网 IPv4", profiles: laxProfiles("198.51.100.41") },
        { family: "ipv6", address: "2001:db8::21", source: "公网 IPv6", profiles: laxProfiles("[2001:db8::21]") },
      ],
    }),
    entry(mad, "ss-rust", "192.0.2.19", [profile("ss2022-b1ca1314", 31164, "Shadowsocks 2022", "Shadowsocks SIP002 URI", ss("192.0.2.19", 31164, "ss2022-b1ca1314"), ssFields("192.0.2.19", 31164))]),
    entry(sg, "sing-box", "203.0.113.84", [profile("VLESS-REALITY-443.json", 443, "VLESS-Vision-uTLS-REALITY", "VLESS URI", vless("203.0.113.84", 443, "VLESS-REALITY-443.json"), vlessFields("203.0.113.84", 443))]),
    entry(sg, "ss-rust", "203.0.113.84", [profile("ss-rust-1", 38092, "Shadowsocks 2022", "Shadowsocks SIP002 URI", ss("203.0.113.84", 38092, "ss-rust-1"), ssFields("203.0.113.84", 38092))]),
    entry(hk5, "xray", "198.51.100.197", [profile("HK-05-VLESS", 443, "VLESS-ENC-TCP-Vision-uTLS-REALITY", "VLESS URI", vless("198.51.100.197", 443, "HK-05-VLESS"), vlessFields("198.51.100.197", 443), { client_name: "香港 HK-05 · VLESS Reality 主线路" })]),
    entry(sjc, "mihomo", "192.0.2.30", [
      profile("vless-eeeafb5f", 443, "VLESS-Vision-uTLS-REALITY", "VLESS URI", vless("192.0.2.30", 443, "vless-eeeafb5f"), vlessFields("192.0.2.30", 443)),
      profile("mieru-in", 27015, "Mieru", "Mihomo Mieru YAML", "- name: mieru-in\n  type: mieru\n  server: 192.0.2.30\n  port: 27015\n  transport: TCP\n  username: client\n  password: example-password\n  multiplexing: MULTIPLEXING_LOW", [field("协议", "Mieru"), field("服务器", "192.0.2.30"), field("端口", "27015"), field("用户名", "client"), field("用户密码", "example-password", true), field("传输", "TCP")]),
    ]),
    entry(fra, "sing-box", "198.51.100.7", [
      profile("hy2-in", 8443, "Hysteria 2", "Hysteria 2 URI", "hysteria2://example-password@198.51.100.7:8443?sni=www.example.com#hy2-in", [field("协议", "Hysteria 2"), field("服务器", "198.51.100.7"), field("端口", "8443"), field("密码", "example-password", true)]),
      profile("tuic-in", 8444, "TUIC v5", "TUIC v5 URI", "tuic://7f9c2ba4-5e7c-4b1b-9d6a-0c3f1e2d4a5b:example-password@198.51.100.7:8444?congestion_control=bbr&alpn=h3#tuic-in", [field("协议", "TUIC v5"), field("服务器", "198.51.100.7"), field("端口", "8444")]),
      profile("anytls-in", 8445, "AnyTLS", "AnyTLS URI", "anytls://example-password@198.51.100.7:8445?sni=www.example.com#anytls-in", [field("协议", "AnyTLS"), field("服务器", "198.51.100.7"), field("端口", "8445")]),
    ]),
    entry(fra, "xray", "198.51.100.7", [profile("trojan-in", 9443, "Trojan", "Trojan URI", "trojan://example-password@198.51.100.7:9443?security=tls&sni=www.example.com#trojan-in", [field("协议", "Trojan"), field("服务器", "198.51.100.7"), field("端口", "9443"), field("密码", "example-password", true)])]),
    entry(jp, "sing-box", "", [], { address_required: true, source: "" }),
  ];
  return { agents, entries };
}

export async function startNativeClients() {
  window.dispatchEvent(new Event('pagehide'));
  document.querySelectorAll('link[rel=stylesheet]').forEach(link=>link.remove());
  const style=Object.assign(document.createElement('link'),{rel:'stylesheet',href:'/assets/analytics/theme.css'});
  document.head.append(style);
  await new Promise((resolve,reject)=>{style.onload=resolve;style.onerror=reject;});
  document.body.replaceChildren(Object.assign(document.createElement('div'),{id:'app'}));
  history.replaceState(null,'','#client-access');
  const session=await(await fetch('/api/v1/auth/session')).json();
  startAnalytics({app:document.querySelector('#app'),session});
  await waitFor(()=>document.querySelector('[data-client-agent]'),'native clients did not load');
}
export async function testClientLayoutRuntime({ testAPI }) {
  const catalog=await(await fetch('/assets/preset-plans.json')).json();
  // Use actual builder output, including five non-equivalent XHTTP schemes.
  for(const [engine,key] of [['mihomo','snell'],['xray','vless-xhttp-five']]) {
    const fixture=catalog.find(item=>item.engine===engine&&item.protocol.key===key);
    assert.ok(fixture?.profile, `missing real builder fixture ${engine}/${key}`);
    testAPI.clientAccessEntries.push({agent_id:'sg-04',agent_name:'新加坡 · SG-04',engine,config_id:`fixture-${key}`,profiles:[{
      tag:fixture.plan.tag,port:fixture.plan.port,protocol:fixture.protocol.name,profile:fixture.profile,mihomo_yaml:fixture.mihomo_yaml,
    }]});
  }
  for(const fixture of catalog.filter(item=>item.profile)) {
    const exported=clientAccessExport({profile:fixture.profile,mihomo_yaml:fixture.mihomo_yaml},'url');
    assert.equal(exported.errors.length,0,`${fixture.engine}/${fixture.protocol.key}: real native output dropped`);
    const expected=fixture.profile.variants?.length||1;assert.equal(exported.outputs.length,expected,'real scheme count changed');
    for(const output of exported.outputs) {
      if(output.extension==='json')assert.ok(JSON.parse(output.value).outbounds.length,'native JSON malformed');
      if(!output.isURL)assert.ok(!output.value.startsWith('vless://'),'native format mislabeled');
    }
  }
  await startNativeClients();
  const button=(text,root=document)=>[...root.querySelectorAll('button')].find(control=>control.textContent===text);
  const dialog=()=>[...document.querySelectorAll('dialog[open]')].at(-1);
  const close=async()=>{button('',dialog())?.click(); await waitFor(()=>!dialog(),'client dialog did not close');};
  const rows=()=>[...document.querySelectorAll('[data-client-agent] tbody tr')];
  const nodeCount=()=>new Set([...document.querySelectorAll('[data-client-agent]')].map(card=>card.dataset.clientAgent)).size;
  const select=(name,value)=>{const control=document.querySelector(`.a-main select[name="${name}"]`);control.value=value;control.dispatchEvent(new Event('change',{bubbles:true}));};
  const search=value=>{const control=document.querySelector('.a-main input[name=search]');control.value=value;control.dispatchEvent(new Event('input',{bubbles:true}));};
  assert.equal(nodeCount(),8,'client layout lost a node');
  const nodePicker = document.querySelector('.a-main [name=agent]');
  assert.equal(nodePicker.querySelectorAll('option .a-probe-flag').length, 8, 'client node filter missing flags');
  assert.equal(nodePicker.querySelector('option[value=hk-01] [data-region-code]').dataset.regionCode, 'HK');
  const filterFields = [...document.querySelector('.a-main input[name=search]').closest('.a-filterbar').querySelectorAll('.a-field')];
  assert.equal(filterFields.at(-1).querySelector('input')?.name, 'search', 'client search must be last');
  assert.ok(document.documentElement.scrollWidth<=innerWidth,'client page overflows viewport');
  if(matchMedia('(max-width:96rem)').matches) for(const row of rows()) {
    for(const cell of [...row.cells].slice(0,-1)) assert.equal(getComputedStyle(cell).display,'grid','responsive labels must stack above content in every row');
  }
  assert.ok(document.querySelector('input[name=search]').getBoundingClientRect().width>=140,'search input unusable');
  select('agent','sjc-06');
  assert.equal(rows().length,2,'engine filter must show every Mihomo profile');
  assert.ok(rows().some(row=>row.textContent.includes('Mihomo Mieru YAML')),'native-only protocol mislabeled as URL');
  select('format','mihomo');
  for(const row of rows())assert.ok(!button('复制',row).disabled,'supported YAML cannot be copied');
  button('参数详情',rows()[1]).click();
  const yamlField=[...dialog().querySelectorAll('.a-field')].find(field=>field.querySelector('label')?.textContent==='Mihomo YAML');
  const yaml=yamlField.querySelector('textarea');
  assert.equal(yaml.value,'••••••••','YAML must start masked');
  button('显示',yamlField.parentElement).click();
  assert.notEqual(yaml.value,'••••••••','YAML must be revealable');
  assert.ok(yaml.value.startsWith('{')&&!/[\r\n]/.test(yaml.value),'YAML must retain single-line Sub-Store map');
  button('隐藏',yamlField.parentElement).click();assert.equal(yaml.value,'••••••••','YAML must be maskable again');
  await close();select('format','url');
  button('参数详情',rows()[0]).click();
  const urlField=[...dialog().querySelectorAll('input[name=export]')].find(input=>input.value.startsWith('vless://'));
  assert.ok(urlField,'switching format did not restore URL');
  await testNativeClientClipboard(dialog(),urlField,button('复制',urlField.closest('.a-field').parentElement));
  await close();select('agent','');search('LAX-02-v6');
  assert.equal(rows().length,1,'search must match custom client name');
  assert.ok(rows()[0].cells[0].textContent.includes('端口 21026'),'port metadata lost');
  button('参数详情',rows()[0]).click();assert.ok(dialog().textContent.includes('服务器'),'parameter details inaccessible');await close();
  button('修改显示参数',rows()[0]).click();assert.ok(dialog().querySelector('form'),'display settings inaccessible');await close();
  button('重置').click();assert.equal(nodeCount(),8,'clearing search lost nodes');
  search('no-such-client');assert.equal(rows().length,0,'unmatched search has results');
  button('重置').click();assert.equal(nodeCount(),8,'empty reset lost nodes');
  button('刷新').click();await waitFor(()=>!button('刷新').disabled,'client refresh did not finish');
  select('format','mihomo');
  const missing=testAPI.clientAccessEntries[0].profiles[0],savedYAML=missing.mihomo_yaml;
  missing.mihomo_yaml='';missing.mihomo_error='此协议不支持 Mihomo YAML';
  button('刷新').click();await waitFor(()=>rows().some(row=>row.cells[1].textContent.includes('URL')),'fallback format must be visible');
  assert.equal(document.querySelector('select[name=format]').value,'mihomo','refresh lost export format');
  const unsupported=rows().find(row=>row.cells[1].textContent.includes('URL'));
  assert.ok(!button('复制',unsupported).disabled&&!button('下载',unsupported).disabled,'available fallback was disabled');
  button('参数详情',unsupported).click();
  assert.ok([...dialog().querySelectorAll('input[name=export]')].some(input=>input.value.startsWith('ss://')),'fallback details lost available URL');
  await close();missing.mihomo_yaml=savedYAML;delete missing.mihomo_error;select('format','url');
  assert.ok(document.documentElement.scrollWidth<=innerWidth,'filtered page overflows');
  for(const list of document.querySelectorAll('.a-traffic-list .a-table-scroll'))assert.ok(list.scrollWidth<=list.clientWidth+1,'client list needs sideways scroll');
  await testClientBatchClipboard(testAPI,button,dialog,rows,search,select);
  if(new URLSearchParams(location.search).has('preview')){document.documentElement.dataset.clientPreview='ready';await new Promise(()=>{});}
  const refresh=async()=>{button('刷新').click();await waitFor(()=>!button('刷新').disabled,'客户端局部刷新未结束');await delay(25);};
  assert.equal(document.querySelector('[data-client-agent] select[name=address]'),null,'preview address remains');
  assert.equal(document.querySelector('.a-main select[name=engine]'),null,'engine classification remains');
  assert.equal([...document.querySelectorAll('.a-main a')].some(link=>link.textContent==='服务端配置'),false,'server shortcut remains');
  assert.equal(document.querySelectorAll('[data-client-agent=sg-04]').length,1,'engines still split a node');
  assert.equal(document.querySelector('[data-client-agent=sg-04]').querySelectorAll('tbody tr').length,4,'merged node lost a protocol');
  const picker=document.querySelector('select[name=agent]');picker.focus();await refresh();assert.equal(document.activeElement,picker,'refresh lost filter focus');
  const resource=testAPI.clientAccessEntries[0],profile=resource.profiles[0];
  resource.profiles=Array.from({length:26},(_,index)=>({...structuredClone(profile),tag:`阅读中的入站-${index}`,port:22000+index}));
  await refresh();
  const card=document.querySelector(`[data-client-agent="${resource.agent_id}"]`),table=card.querySelector('table'),next=button('下一页',card);
  assert.ok(next,'长客户端列表缺少分页');next.click();
  await waitFor(()=>card.textContent.includes('第 2 / 3 页'),'客户端分页未切换');
  const row=table.querySelector('tbody tr'),control=button('参数详情',row);control.focus();
  const range=document.createRange();range.selectNodeContents(row.cells[0].querySelector('.a-stack').firstChild);getSelection().removeAllRanges();getSelection().addRange(range);const selection=getSelection().toString();
  await waitFor(()=>!document.getAnimations().some(a=>a.id.startsWith('analytics-')),'分页动效未结束');
  await new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve)));const top=scrollY;
  await refresh();
  assert.equal(card.querySelector('table'),table,'刷新替换客户端列表');assert.equal(table.querySelector('tbody tr'),row,'刷新替换当前分页记录');
  assert.ok(card.textContent.includes('第 2 / 3 页'),'刷新重置分页');assert.equal(document.activeElement,control,'刷新丢失行按钮焦点');assert.equal(getSelection().toString(),selection,'刷新丢失选中文本');assert.equal(scrollY,top,'刷新改变客户端阅读位置');
  button('下一页',card).click();assert.ok(card.textContent.includes('第 3 / 3 页'),'刷新后分页处理器失效');
  const previousFetch=window.fetch;
  window.fetch=async(input,options)=>String(input).endsWith('/client-access')?new Response(JSON.stringify({error:'客户端读取失败'}),{status:503,headers:{'Content-Type':'application/json'}}):previousFetch(input,options);
  try{await refresh();assert.ok(card.isConnected&&card.textContent.includes('第 3 / 3 页'),'读取失败清空有效客户端列表');}
  finally{window.fetch=previousFetch;}
  await refresh();assert.ok(card.isConnected&&card.textContent.includes('第 3 / 3 页'),'恢复读取重置页码');
}

async function testNativeClientClipboard(box,input,copy){
  const clipboard=Object.getOwnPropertyDescriptor(navigator,'clipboard');
  const exec=Object.getOwnPropertyDescriptor(document,'execCommand');
  let copied='',calls=0,complete;
  const idle=()=>!document.querySelector('#app[aria-busy=true]');
  try{
    Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:value=>{calls++;copied=value;return new Promise(resolve=>{complete=resolve;});}}});
    copy.click();copy.click();await waitFor(()=>calls===1,'现代剪贴板复制未提交或重复提交');
    assert.ok(copy.disabled,'复制提交中没有锁定');assert.equal(copied,input.value);assert.equal(input.type,'password');
    complete();await waitFor(idle,'复制未结束');
    for(const mode of ['missing','rejected','text-selection','denied']){
      Object.defineProperty(navigator,'clipboard',{configurable:true,value:mode==='missing'?undefined:{writeText:async()=>{throw new Error('fixture clipboard denied');}}});
      const value=input.value;
      input.type=mode==='text-selection'?'text':'password';input.focus();
      if(input.type==='text')input.setSelectionRange(1,4,'forward');
      let invoked=false;
      Object.defineProperty(document,'execCommand',{configurable:true,value:command=>{
        const selected=document.activeElement;
        invoked=true;assert.equal(command,'copy');assert.equal(selected.tagName,'TEXTAREA');
        assert.ok(box.contains(selected),'复制助手位于模态之外的不可交互区域');
        assert.equal(selected.value,value);assert.equal(selected.selectionStart,0);assert.equal(selected.selectionEnd,value.length);
        return mode!=='denied';
      }});
      copy.click();await waitFor(()=>invoked&&idle(),'回退复制未结束');
      assert.equal(box.querySelector('.a-clipboard'),null,'复制助手没有移除');
      assert.equal(document.activeElement,input,'复制后没有恢复焦点');assert.equal(input.value,value,'复制改写原值');
      assert.equal(input.type,mode==='text-selection'?'text':'password','复制暴露隐藏内容');
      if(mode==='text-selection'){assert.equal(input.selectionStart,1);assert.equal(input.selectionEnd,4);assert.equal(input.selectionDirection,'forward');}
      if(mode==='denied')assert.ok([...document.querySelectorAll('.a-toast.error')].some(toast=>toast.textContent.includes('浏览器拒绝了剪贴板操作')),'复制被拒绝没有显示真实错误');
    }
    input.type='password';
  }finally{
    if(clipboard)Object.defineProperty(navigator,'clipboard',clipboard);else delete navigator.clipboard;
    if(exec)Object.defineProperty(document,'execCommand',exec);else delete document.execCommand;
  }
}

async function testClientBatchClipboard(testAPI,button,dialog,rows,search,select) {
  const descriptor=Object.getOwnPropertyDescriptor(navigator,'clipboard');
  const original=testAPI.clientAccessEntries;
  let copied='',writes=0;
  Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async value=>{copied=value;writes++;}}});
  const idle=()=>!document.querySelector('#app[aria-busy]');
  const refresh=async()=>{button('刷新').click();await waitFor(()=>!button('刷新').disabled,'batch refresh stalled');};
  try {
    const row=structuredClone(original[0].profiles[0]);
    const json='{"outbounds":[{"protocol":"vless","settings":{"vnext":[{"address":"native.example.test","port":443,"users":[{"id":"fixture"}]}]},"streamSettings":{"network":"xhttp","xhttpSettings":{"extra":{"downloadSettings":{"address":"download.example.test","port":443}}}}}]}';
    testAPI.clientAccessEntries=[
      {...original[0],config_id:'url-config',profiles:Array.from({length:26},(_,i)=>({...structuredClone(row),tag:`bulk-${i}`,port:23000+i,profile:{...row.profile,uri:`ss://fixture@edge.test:${23000+i}`}}))},
      {...original[0],engine:'xray',config_id:'native-config',instance_id:'shared-two',profiles:[{...row,tag:'bulk-0',port:23000,profile:{uri:json,format:'Xray client JSON',uri_error:'no lossless URL',variants:[{name:'direct',profile:row.profile},{name:'split',profile:{xray_json:json,uri_error:'no URL'}}]}}]},
      {...original[0],agent_id:'same-name-other-id',config_id:'other',profiles:[row]},
    ];
    await refresh();
    assert.equal(document.querySelectorAll('[data-client-agent]').length,2,'same names merged distinct node IDs');
    const card=document.querySelector(`[data-client-agent="${original[0].agent_id}"]`);
    const keys=[...card.querySelectorAll('tbody tr')].map(row=>row.dataset.refreshKey);
    button('下一页',card).click();button('下一页',card).click();
    const nativeRow=[...card.querySelectorAll('tbody tr')].find(row=>row.textContent.includes('Xray JSON'));
    assert.ok(nativeRow,'duplicate tag and port overwrote native record');
    assert.ok(!keys.includes(nativeRow.dataset.refreshKey),'row identity omitted config/instance');
    button('参数详情',nativeRow).click();
    const textarea=dialog().querySelector('textarea[name=export]');
    assert.equal(textarea.value,'••••••••','native JSON starts exposed');
    button('复制',textarea.closest('.a-field').parentElement).click();await waitFor(()=>idle()&&copied===json,'masked native detail did not copy full JSON');
    assert.equal(textarea.value,'••••••••','copy exposed native JSON');
    const keep=dialog();button('刷新').click();await waitFor(()=>!button('刷新').disabled,'dialog refresh failed');assert.ok(keep.isConnected,'unchanged refresh closed details');
    dialog().querySelector('[aria-label=关闭]').click();
    button('复制',nativeRow).click();await waitFor(()=>idle()&&copied.includes(json),'single-row native copy failed');
    const blobs=[],downloads=[];const create=URL.createObjectURL,click=HTMLAnchorElement.prototype.click;
    URL.createObjectURL=blob=>{blobs.push(blob);return create(blob);};HTMLAnchorElement.prototype.click=function(){downloads.push(this.download);};
    try{button('下载',nativeRow).click();await waitFor(()=>idle()&&downloads.length===2,'multi-scheme download omitted a scheme');assert.ok(downloads[1].endsWith('.json'),'native extension wrong');assert.equal(blobs[1].type,'application/json');assert.equal(await blobs[1].text(),json);}finally{URL.createObjectURL=create;HTMLAnchorElement.prototype.click=click;}
    search('bulk-25');assert.equal(rows().length,1,'batch search fixture failed');
    let before=writes;button('复制全部协议',card).click();await waitFor(()=>idle()&&writes===before+1,'node batch did not write once');
    assert.ok(copied.includes('edge.test:23000')&&copied.includes('edge.test:23025')&&copied.includes(json),'node batch truncated by search or pagination');
    assert.ok(!copied.includes('[same-name-other-id]'),'node batch included another node');
    before=writes;button('复制全部节点').click();await waitFor(()=>idle()&&writes===before+1,'all batch did not write once');
    assert.ok(copied.includes('[same-name-other-id]'),'all batch omitted filtered node');
    // Revocation between opening a dialog and copying must not use old secrets.
    search('');button('参数详情',rows()[0]).click();
    const stale=dialog(),copy=button('复制',stale);testAPI.clientAccessEntries=[];before=writes;copy.click();await waitFor(idle,'revoked copy stayed busy');
    assert.equal(writes,before,'revoked export reached clipboard');
    stale.querySelector('[aria-label=关闭]').click();
    await refresh();assert.ok(document.querySelector('.a-empty'),'empty state missing');
    testAPI.clientAccessEntries=[{...original[0],profiles:[{...row,error:'credential missing'}]}];await refresh();
    assert.ok(rows()[0].textContent.includes('credential missing')&&button('复制',rows()[0]).disabled,'real generation failure hidden');
  } finally {
    testAPI.clientAccessEntries=original;search('');select('agent','');await refresh();
    if(descriptor)Object.defineProperty(navigator,'clipboard',descriptor);else delete navigator.clipboard;
  }
}
