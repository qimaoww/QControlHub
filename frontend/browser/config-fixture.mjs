import { startAnalytics } from "../analytics/app.js";
import { generatedPlanFields } from "../modules/inbound-options-model.js";

export const assert = (value, message) => { if (!value) throw new Error(message); };
export const pause = () => new Promise(resolve => setTimeout(resolve, 30));
export const waitFor = async (condition, message) => {
  const end = Date.now() + 4000;
  while (!condition()) {
    if (Date.now() > end) throw new Error(message);
    await new Promise(resolve => setTimeout(resolve, 10));
  }
};
const esc = value => String(value ?? "").replace(/[&<>"']/g, char => (
  {"&":"&amp;", "<":"&lt;", ">":"&gt;", '"':"&quot;", "'":"&#39;"}[char]));
let catalog;

export async function createConfigFixture(engine, options = {}) {
  catalog ||= await (await fetch("/assets/preset-plans.json")).json();
  const entries = catalog.filter(entry => entry.engine === engine);
  const preset = entries.find(entry => entry.protocol.key === options.protocol) || entries[0];
  const basePlan = {...structuredClone(preset.saved_plan || preset.plan), ...options.plan};
  let controller = new AbortController();
  const agent = {id:"node", name:"香港 · HK-01", os:"linux", arch:"amd64", status:options.offline ? "offline" : "online",
    can_manage:options.shared !== true, ...(options.shared ? {shared_allocation:{id:"share",enabled:true,status:"accepted"}} : {}), capabilities:options.multi ? ["mihomo", engine] : [engine], features:["managed-config-read-v1", "independent-egress-v1", "shared-core-instances-v1", "shared-core-lifecycle-v1",
      ...(options.legacy ? [] : ["preset-auto-install-v1"])],
    runtime:{...(options.multi ? {mihomo:{installed:false}} : {}), [engine]:{installed:!options.missing, service_status:options.serviceStatus || "active", version:"test-development", existing_config_available:Boolean(options.import)}}};
  const test = {writes:[], calls:[], readRequests:[], notices:[], confirmations:[], confirm:false, serial:0, fail:false, gate:null, taskStatus:"pending",deploymentState:options.deploymentState};
  let inbounds = options.missing || options.emptyInbounds ? [] : ["first", "second"].map((tag, index) => ({...basePlan, tag, port:21001+index}));
  const primaryField = engine === "ss-rust" ? "timeout" : engine === "mihomo" ? "log-level" : "log";
  const secondaryField = engine === "xray" ? "stats" : engine === "sing-box" ? "experimental" : "mode";
  const extraField = engine === "xray" ? "policy" : engine === "ss-rust" ? "ipv6_first" : "ntp";
  const fields = [
    {key:primaryField, label:engine === "ss-rust" ? "TCP 超时" : "日志", scope:"global"},
    {key:"dns", label:"DNS", scope:"global"},
    {key:extraField, label:engine === "ss-rust" ? "IPv6 优先" : engine === "xray" ? "本地策略" : "NTP", scope:"global"},
    {key:secondaryField, label:engine === "xray" ? "统计" : engine === "sing-box" ? "实验功能" : "运行模式", scope:engine === "ss-rust" ? "override" : "global"},
    ...["inbounds", "endpoints", "outbounds", "listeners", "servers", "shadowsocks", "server_port"].map(key=>({key, label:key, scope:"inbound"})),
  ].map(field=>({...field, kind:"object", docs:"https://example.invalid/config"}));
  const values = new Map(options.noCommon ? [] : [
    [primaryField, engine === "ss-rust" ? "300" : engine === "mihomo" ? '"info"' : '{"level":"info"}'],
    [secondaryField, engine === "ss-rust" ? '"tcp_and_udp"' : engine === "mihomo" ? '"rule"' : "{}"],
  ]);
  if (options.allCommon) for (const field of fields.filter(field=>field.scope !== "inbound"))
    if (!values.has(field.key)) values.set(field.key, "{}");
  const root = () => ({
    ...Object.fromEntries([...values].map(([key, value])=>[key, JSON.parse(value)])),
    ...(engine === "mihomo"
    ? {listeners:inbounds.map(item => ({name:item.tag, port:item.port, type:"socks"})), rules:["MATCH,DIRECT"]}
    : engine === "ss-rust" ? {servers:inbounds.map(item => ({remarks:item.tag, server_port:item.port, method:item.method, password:item.credential}))}
    : {inbounds:inbounds.filter(item => engine !== "sing-box" || item.protocol !== "wireguard").map(item => ({tag:item.tag, port:item.port, listen_port:item.port, protocol:item.protocol === "wireguard" ? "wireguard" : "shadowsocks", type:"shadowsocks"})),
      ...(engine === "sing-box" && inbounds.some(item => item.protocol === "wireguard") ? {
        endpoints:inbounds.filter(item => item.protocol === "wireguard").map(item => ({
          type:"wireguard", tag:item.tag, listen_port:item.port, system:false, mtu:item.wireguard_mtu,
          address:[item.wireguard_server_address], private_key:item.wireguard_server_private_key,
          peers:[{public_key:item.wireguard_client_public_key, pre_shared_key:item.wireguard_preshared_key, allowed_ips:[item.wireguard_client_address]}],
        })),
      } : {}),
      outbounds:[{tag:"direct", protocol:"freedom", type:"direct"},
        ...inbounds.map(item=>({tag:`qch-trf-${item.port}-abcdef012345`, protocol:"freedom", type:"direct"}))]})});
  const nativeFragments = new Map();
  const content = () => {
    const doc = root();
    for (const section of ['inbounds','endpoints','listeners','servers']) if (doc[section])
      doc[section] = doc[section].map(item=>nativeFragments.get(item.tag || item.name || item.remarks || item.id) || item);
    return JSON.stringify(doc, null, 2);
  };
  const savedDate = version => `2026-11-02T04:05:${String(version).padStart(2,"0")}Z`;
  let saved = options.missing && !options.savedMissing ? null : {id:"cfg", agent_id:agent.id, engine, name:"配置", version:1, content:options.content || content(),updated_at:savedDate(1)};
  let agentContent = saved ? options.drift ? saved.content + "\n# node-only" : saved.content : "";
  const revisions = new Map(saved ? [[saved.version, structuredClone(saved)]] : []);
  if (options.history && saved) {
    saved = {...saved, version:2, updated_at:savedDate(2)};
    revisions.set(2, structuredClone(saved));
  }
  const state = {route:"live-config", navigationEpoch:1, routeSignal:controller.signal,
    session:{role:options.shared ? "user" : "admin"}, data:{liveAgent:agent.id, liveEngine:options.multi ? "" : engine, liveConfigSource:options.import ? "import" : "",
      liveSources:saved ? {[`node|${engine}${options.import ? "|import" : ""}`]:{content:agentContent, agentContent}} : {}}};
  const tasks = new Map();
  let disabled = [];
  const workspace = () => ({agent:structuredClone(agent), config:saved && {...saved, version:saved.version+(test.stale?1:0)},
    deployment:{state: test.deploymentState || ([...tasks.values()].some(task=>task.action==='deploy'&&task.config_version===saved?.version) ?
      test.taskStatus==='succeeded'?'applied':test.taskStatus==='failed'?'failed':test.taskStatus==='canceled'?'canceled':'deploying' : 'pending'),
      service_status:agent.status==='online'?agent.runtime[engine].service_status:'unknown',
      task_id:[...tasks.values()].reverse().find(task=>task.action==='deploy'&&task.config_version===saved?.version)?.id,
      task_status:[...tasks.values()].some(task=>task.action==='deploy'&&task.config_version===saved?.version) ? test.taskStatus : undefined},
    engine, inbounds:structuredClone(options.native ? [] : inbounds),
    management:{disabled:structuredClone(disabled)},
    managed_objects:inbounds.map(item=>({section:engine === "mihomo" ? "listeners" : engine === "ss-rust" ? "servers" : "inbounds",tag:item.tag,port:item.port,inbound:true,enabled:!disabled.some(ref=>ref.tag === item.tag)})),
    inbound_targets:inbounds.map(item=>({tag:item.tag, port:item.port, kind:options.native ? "socks" : item.protocol})),
    protocols:entries.map(entry => entry.protocol), reality_presets:["www.amazon.com"],
    catalog:{name:engine, format:engine === "mihomo" ? "YAML" : "JSON", fields, topic_groups:[]},
    present_fields:Object.fromEntries(fields.map(field=>[field.key, Object.hasOwn(root(), field.key)]))});
  const api = async (path, request = {}) => {
    test.calls.push({path, method:request.method || "GET"});
    const body = request.body && JSON.parse(request.body);
    if (path === "/auth/session") return session;
    if (path === "/settings") return {};
    if (path === "/agent-access") return {revision:1,shares:[]};
    if (path === "/certificates") {
      if (test.certificateGate) await test.certificateGate;
      return {items:test.certificates || options.certificates || []};
    }
    if (path === "/agents") return [options.cachedMissing ? {...structuredClone(agent), runtime:{[engine]:{installed:false}}} : structuredClone(agent)];
    if (path === "/client-access") return options.clientEntries || [];
    if (path.startsWith("/client-access?")) {
      if (test.peerGate) await test.peerGate;
      const outgoing = engine === "xray" ? {tag:"peer", protocol:"vless", settings:{vnext:[{address:"203.0.113.10", port:2443,
        users:[{id:"123e4567-e89b-42d3-a456-426614174000", encryption:"none"}]}]}, streamSettings:{network:"tcp", security:"tls", tlsSettings:{serverName:"peer.example"}}} :
        {tag:"peer", type:"vless", server:"203.0.113.10", server_port:2443, uuid:"123e4567-e89b-42d3-a456-426614174000", tls:{enabled:true, server_name:"peer.example"}};
      return test.peerEntries || [{agent_id:agent.id, agent_name:"当前节点", profiles:[{tag:"self", outbound:outgoing}]},
        {agent_id:"peer-node", agent_name:"出口 · JP", agent_status:"online", engine:"xray",
          profiles:[{tag:"remote-inbound", protocol:"VLESS", port:2443, address:"203.0.113.10", outbound:outgoing}]},
        {agent_id:"incompatible", agent_name:"不兼容节点", engine:"mihomo",
          profiles:[{tag:"snell", protocol:"Snell", port:2444, outbound_error:"当前内核不支持此 Snell 入站"}]}];
    }
    if (path.includes('/workspace/snapshots/')) {
      if (test.workspaceGate) await test.workspaceGate;
      const result=workspace(), doc=JSON.parse(agentContent), entries=doc.inbounds || doc.listeners || doc.servers || [];
      const port = tag => {const item=entries.find(item=>(item.tag || item.name || item.id || item.remarks)===tag);return item?.port || item?.listen_port || item?.server_port;};
      result.config={...result.config,content:agentContent};
      for (const list of [result.inbounds,result.inbound_targets,result.managed_objects]) for (const item of list) if (port(item.tag)) item.port=port(item.tag);
      result.deployment={...result.deployment,state:'pending',task_id:undefined,task_status:undefined};
      return result;
    }
    if (path.includes('/server-inbounds/') && (!request.method || request.method==='GET')) {
      const tag=decodeURIComponent(path.split('/server-inbounds/')[1].split('?')[0]);
      const doc=JSON.parse(path.includes('?snapshot=') ? agentContent : saved.content);
      const entry=(doc.inbounds || doc.listeners || doc.servers || []).find(item=>(item.tag || item.name || item.id || item.remarks)===tag);
      return {version:saved.version,fragment:JSON.stringify(entry,null,2)};
    }
    if (path.endsWith("/workspace")) {
      if (test.workspaceGate) await test.workspaceGate;
      if (test.failSwitch && path.includes("/mihomo/")) throw new Error("fixture switch failed");
      if (test.failRefresh && test.writes.length) throw Object.assign(new Error("fixture refresh unavailable"), {status:503});
      return workspace();
    }
    if (path === `/agents/node/configs/${engine}` && (!request.method || request.method === 'GET')) return saved;
    if (path.endsWith("/management") && request.method === "PUT") {
      if (test.gate) await test.gate;
      if (test.fail) throw Object.assign(new Error("fixture version conflict"), {status:409});
      assert(body.version === saved.version, "state lost optimistic version");
      const source = saved.content;
      disabled = structuredClone(body.disabled);
      test.writes.push({path, ...body});
      saved = {...saved, version:saved.version+1, updated_at:savedDate(saved.version+1)};
      assert(saved.content === source, "state rewrote full source");
      const task = {id:`task-${saved.version}`, action:body.intent, config_version:saved.version, status:test.taskStatus};
      tasks.set(task.id, task);
      return {config:saved, task};
    }
    if (path.endsWith("/plans")) {
      if (test.planGate) await test.planGate;
      const entry = entries.find(item => item.protocol.key === body.protocol);
      return {...structuredClone(entry.plan), ...body.input,
        ...Object.fromEntries(generatedPlanFields.filter(key=>Object.hasOwn(entry.plan,key)).map(key=>[key,entry.plan[key]])),
        tag:`new-${++test.serial}`, port:22000+test.serial};
    }
    if (path.endsWith("/restore")) {
      if (test.restoreGate) await test.restoreGate;
      if (test.restoreError) throw Object.assign(new Error("fixture restore rejected"), {status:test.restoreError});
      assert(body.expected_version === saved.version, "restore lost optimistic version");
      test.restores.push(body);
      saved = {...revisions.get(Number(path.split("/")[4])), version:saved.version+1, updated_at:savedDate(saved.version+1)};
      revisions.set(saved.version, structuredClone(saved));
      return saved;
    }
    if (path.includes("/revisions/")) {
      if (test.revisionGate) await test.revisionGate;
      return revisions.get(Number(path.split("/")[4])) || {...saved, content:"{}"};
    }
    if (path.includes("/revisions")) {
      if (test.historyGate) await test.historyGate;
      return [...revisions.values()].reverse();
    }
    if (path === '/access-controls' && request.method === 'PUT') {
      if (test.accessGate) await test.accessGate;
      if (test.accessError) throw Object.assign(new Error('测试访问限制保存失败'), {status:test.accessError});
      assert(body.agent_id === agent.id && body.engine === engine && body.expected_version === saved.version, 'access policy lost target or CAS');
      const target=inbounds.find(item=>item.tag===body.tag&&item.port===body.port);
      assert(target,'access policy selected another inbound');
      test.accessWrites ||= [];test.accessWrites.push(body);
      Object.assign(target,{block_mainland_destination:body.block_mainland_destination,block_mainland_source:body.block_mainland_source});
      saved={...saved,version:saved.version+1,updated_at:savedDate(saved.version+1)};
      const task={id:`access-${saved.version}`,agent_id:agent.id,engine,shared_instance:Boolean(options.shared),config_id:saved.id,config_version:saved.version,action:body.intent,status:test.taskStatus};
      tasks.set(task.id,task);return {config:saved,task};
    }
    if (path === "/access-controls") {
      if (test.accessReadGate) await test.accessReadGate;
      return inbounds.map(item=>({agent_id:agent.id,engine,config_id:saved.id,config_version:saved.version,tag:item.tag,port:item.port,kind:item.protocol,block_mainland_destination:Boolean(item.block_mainland_destination),block_mainland_source:Boolean(item.block_mainland_source)}));
    }
    if (request.method === "PUT" && path === `/agents/node/configs/${engine}`) {
      if(test.gate)await test.gate;
      if(test.fail)throw Object.assign(new Error('fixture version conflict'),{status:409});
      assert(body.version===(saved?.version||0),'source save lost CAS');
      saved={...saved,id:'cfg',agent_id:agent.id,engine,name:body.name,description:body.description,content:body.content,version:(saved?.version||0)+1,updated_at:savedDate((saved?.version||0)+1)};
      revisions.set(saved.version,structuredClone(saved));test.writes.push({path,...body});return saved;
    }
    if (path === "/deployments") return [{agent_id:agent.id, engine, config_id:"cfg", config_version:1}];
    if (request.method === "POST" && path === "/tasks") {
      if (body.action === 'import-existing') {
        test.imports ||= [];test.imports.push(body);
        const task={id:`import-${++test.serial}`,agent_id:agent.id,engine,config_id:saved.id,config_version:saved.version,action:body.action,status:test.taskStatus};
        tasks.set(task.id,task);return task;
      }
      assert(body.action === "read-managed-config" || body.action === "read-config", "deploy preflight created a non-read task");
      test.readRequests.push(body);
      const task = {id:`read-${++test.serial}`, action:body.action, engine, status:"succeeded", snapshot:agentContent,
        reused:Boolean(body.prefer_cached), finished_at:new Date().toISOString()};
      tasks.set(task.id, task);
      return task;
    }
    if (request.method === "POST" && path.endsWith("/source")) {
      if (test.gate) await test.gate;
      if (test.sourceFailure) throw Object.assign(new Error("测试源码保存失败"), test.sourceFailure);
      assert(body.version === saved.version, "outbound save lost version check");
      test.writes.push({path, ...body});
      saved = {...saved, content:body.content, version:saved.version+1,updated_at:savedDate(saved.version+1)};
      const task = {id:request.headers?.['X-Config-Task-ID'] || `task-${saved.version}`, agent_id:agent.id, engine, shared_instance:Boolean(options.shared), config_id:saved.id,
        action:body.intent, config_version:saved.version, status:test.taskStatus};
      tasks.set(task.id, task);
      if (test.supersedeSourceResponse) saved={...saved,version:saved.version+1,content:saved.content+'\n '};
      if (test.loseSourceResponse) throw TypeError('测试保存响应中断');
      return {config:saved, task};
    }
    if (request.method === "POST" && (path.endsWith("/server-inbounds") || path.includes("/fields/"))) {
      if (test.gate) await test.gate;
      if (test.portConflict) throw Object.assign(new Error("测试端口已被占用"), {status:409, code:"port_conflict", field:test.portConflict});
      if (test.mutationFailure) throw Object.assign(new Error("测试保存响应中断"), test.mutationFailure);
      if (test.fail) throw Object.assign(new Error("fixture version conflict"), {status:409});
      assert(body.expected_version === (saved?.version || 0), "mutation request lost optimistic version");
      test.writes.push({path, ...body});
      if (path.endsWith("/server-inbounds")) {
        if (body.fragment) {
          const entry=JSON.parse(body.fragment), previous=inbounds.find(item=>item.tag===body.original_tag);
          nativeFragments.set(body.original_tag,entry);
          body.input={...previous,port:entry.port || entry.listen_port || entry.server_port};
        }
        if (body.operation === "delete") assert(Object.keys(body.input).sort().join(",") === "port,tag", "delete leaked selection metadata into the strict input schema");
        if (body.operation !== "add") inbounds = inbounds.filter(item => item.tag !== body.original_tag);
        if (body.operation !== "delete") inbounds.push(body.input);
      } else {
        const key = decodeURIComponent(path.split("/fields/")[1].split("?")[0]);
        const present = values.has(key);
        assert(present === (body.mutation !== "add"), "field request does not match saved presence");
        if (body.mutation === "delete") values.delete(key);
        else {
          JSON.parse(body.fragment); // JSON fragments are also valid YAML.
          values.set(key, body.fragment);
        }
      }
      saved = {id:"cfg", agent_id:agent.id, engine, name:"配置", version:(saved?.version || 0)+1, content:content(),updated_at:savedDate((saved?.version || 0)+1)};
      const task = {id:request.headers?.['X-Config-Task-ID'] || `task-${saved.version}`, agent_id:agent.id, engine, shared_instance:Boolean(options.shared), config_id:saved.id,
        action:body.intent, config_version:saved.version, status:test.taskStatus,
        install_if_missing:Boolean(body.install_if_missing && !agent.runtime[engine].installed)};
      tasks.set(task.id, task);
      return {config:saved, task};
    }
    if (path.includes("/fields/")) {
      if (test.fieldGate) await test.fieldGate;
      if (test.fieldFailure) throw Object.assign(new Error("测试配置项读取失败"), {status:503});
      const key = decodeURIComponent(path.split("/fields/")[1].split("?")[0]);
      return {version:saved.version+(test.staleField?1:0), present:values.has(key), fragment:values.get(key) || ""};
    }
    if (path.startsWith("/tasks/")) {
      if (test.taskReadGate) await test.taskReadGate;
      if (test.taskReadFailure) throw Object.assign(new Error('测试任务详情读取失败'), {status:test.taskReadFailure});
      if (test.receiptUnavailable) throw Object.assign(new Error('测试任务暂不可读'),{status:503});
      const task = tasks.get(path.split("/")[2].split("?")[0]);
      if (path.endsWith("/config-snapshot")) {
        test.snapshotReads = (test.snapshotReads || 0) + 1;
        if (test.snapshotGate) await test.snapshotGate;
        if (test.missingSnapshots > 0) { test.missingSnapshots--; throw Object.assign(new Error("测试配置快照已失效"), {status:404}); }
        return {content:task?.snapshot || ""};
      }
      if (task?.action === "deploy" && test.taskStatus === "succeeded") agentContent = saved.content;
      return {...task, status:test.taskStatus, error:test.taskError, output:test.taskOutput, ...test.taskOverride};
    }
    throw new Error(`Unexpected fixture API: ${path}`);
  };
  const nativeFetch = globalThis.fetch;
  const permissions = ['overview.read','agents.read','agent-config.read','agent-config.write','configs.read','configs.write','configs.restore','tasks.read','tasks.execute','client-access.read','templates.read','settings.read'];
  const session = {role:options.shared||options.readonly||options.noFleet||options.noTasks||options.noPeerRead?'user':'admin',username:options.shared?'shared-user':'owner',csrf_token:'fixture',
    permissions:permissions.filter(permission=>(!options.readonly || ['agents.read','agent-config.read','configs.read','tasks.read'].includes(permission))&&!(options.noFleet&&permission==='agents.read')&&!(options.noTasks&&permission==='tasks.execute')&&!(options.noPeerRead&&permission==='client-access.read'))};
  globalThis.fetch = async (url, request={}) => {
    if (!String(url).startsWith('/api/v1')) return nativeFetch(url,request);
    try {
      const result = await api(String(url).slice('/api/v1'.length),request);
      return new Response(JSON.stringify(result),{status:200,headers:{'Content-Type':'application/json'}});
    } catch(error) {
      if (error.name==='AbortError') throw error;
      if (!error.status || error.status===200) throw error;
      return new Response(JSON.stringify({error:error.message,code:error.code,field:error.field}),{status:error.status,headers:{'Content-Type':'application/json'}});
    }
  };
  const styles = document.createElement('link');styles.rel='stylesheet';styles.href='/assets/analytics/theme.css';document.head.append(styles);
  await new Promise((resolve,reject)=>{styles.onload=resolve;styles.onerror=()=>reject(new Error('analytics styles failed'));});
  history.replaceState(null,'',`#live-config?agent=node&engine=${engine}${options.import?'&source=import':''}`);
  document.body.replaceChildren(Object.assign(document.createElement('div'),{id:'analytics-config-test'}));
  const appRoot = document.querySelector('#analytics-config-test');
  startAnalytics({app:appRoot,session});
  const topDialog = () => [...document.querySelectorAll('dialog[open]')].at(-1);
  const observer = new MutationObserver(()=>{
    const box=topDialog();
    if (box?.querySelector('h2')?.textContent==='任务详情') test.sawTaskDetails=true;
    if(!box||box.querySelector('form')||box.querySelector('h2')?.textContent==='任务详情'||box.dataset.answered)return;
    const cancel=[...box.querySelectorAll('button')].find(control=>control.textContent==='取消');
    if(!cancel)return;
    box.dataset.answered='1';
    test.confirmations.push({title:box.querySelector('h2')?.textContent,message:box.textContent});
    const choices=[...box.querySelectorAll('.a-dialog-body button')];
    queueMicrotask(()=>{if(box.open)(test.confirm?choices.at(-1):cancel).click();});
  });
  observer.observe(document.body,{childList:true,subtree:true});
  await waitFor(()=>document.querySelector('.a-main form [name=content]')&&!appRoot.querySelector('[aria-busy=true]'),'analytics live workspace did not load');
  let selected='first';
  const row = tag => [...appRoot.querySelectorAll('.a-port-list tbody tr')].find(row=>row.textContent.includes(tag));
  const clickButton = (label, scope=appRoot) => {const control=[...scope.querySelectorAll('button')].find(control=>control.textContent===label);control?.click();return control;};
  const click = kind => {
    if(kind==='history')return clickButton('保存历史');
    if(kind==='advanced')return clickButton('通用配置项');
    if(kind==='add')return clickButton('添加入站');
    const item=row(selected);if(!item)return null;
    return clickButton({modify:'修改',delete:'删除',toggle:item.textContent.includes('停用')?'停用':'启用',access:'访问限制',outbound:'出站'}[kind]||kind,item);
  };
  const settle = async (count=test.writes.length+1) => {
    await waitFor(()=>test.writes.length===count&&!appRoot.hasAttribute('aria-busy')&&!appRoot.querySelector('[aria-busy=true]'),`configuration mutation did not settle (writes=${test.writes.length}/${count}; notices=${[...document.querySelectorAll('.a-notice')].filter(item=>item.checkVisibility()).map(item=>item.textContent.slice(0,160)).join(';')})`);
    if(topDialog()?.querySelector('h2')?.textContent==='任务详情')topDialog().close();
    await pause();
  };
  const submit = intent => {const form=topDialog()?.querySelector('form');assert(form,'expected actual analytics form');if(form.elements.intent)form.elements.intent.value=intent||'deploy';form.requestSubmit();};
  const reenter = async () => {const before=test.calls.filter(call=>call.path.endsWith('/workspace')).length;clickButton('刷新',appRoot);await waitFor(()=>test.calls.filter(call=>call.path.endsWith('/workspace')).length>before&&!appRoot.querySelector('[aria-busy=true]'),'workspace refresh failed');};
  const dispose = () => {window.dispatchEvent(new Event('pagehide'));controller.abort();observer.disconnect();document.querySelectorAll('dialog.a-dialog').forEach(box=>box.close());styles.remove();globalThis.fetch=nativeFetch;state.data={};};
  Object.assign(test,{state,agent,root:appRoot,row,topDialog,clickButton,click,select:tag=>{selected=tag;},selectCommon:()=>{},common:()=>click('advanced'),reenter,dispose,settle,submit,
    setAgentContent:value=>{agentContent=value;},primaryField,secondaryField,extraField,saved:()=>saved,revisions,restores:[]});
  return test;
}
