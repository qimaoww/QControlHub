import { installConfigPages } from "../modules/configs.js";

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
  const basePlan = structuredClone(entries[0].plan);
  let controller = new AbortController();
  const agent = {id:"node", name:"香港 · HK-01", os:"linux", arch:"amd64", status:options.offline ? "offline" : "online",
    can_manage:options.shared !== true, capabilities:options.multi ? ["mihomo", engine] : [engine], features:["managed-config-read-v1", "independent-egress-v1",
      ...(options.legacy ? [] : ["preset-auto-install-v1"])],
    runtime:{...(options.multi ? {mihomo:{installed:false}} : {}), [engine]:{installed:!options.missing, version:"test-development", existing_config_available:Boolean(options.import)}}};
  const test = {writes:[], calls:[], readRequests:[], notices:[], confirmations:[], confirm:false, serial:0, fail:false, gate:null, taskStatus:"pending"};
  let inbounds = options.missing || options.emptyInbounds ? [] : ["first", "second"].map((tag, index) => ({...basePlan, tag, port:21001+index}));
  const primaryField = engine === "ss-rust" ? "timeout" : engine === "mihomo" ? "log-level" : "log";
  const secondaryField = engine === "xray" ? "stats" : engine === "sing-box" ? "experimental" : "mode";
  const extraField = engine === "xray" ? "policy" : engine === "ss-rust" ? "ipv6_first" : "ntp";
  const fields = [
    {key:primaryField, label:engine === "ss-rust" ? "TCP 超时" : "日志", scope:"global"},
    {key:"dns", label:"DNS", scope:"global"},
    {key:extraField, label:engine === "ss-rust" ? "IPv6 优先" : engine === "xray" ? "本地策略" : "NTP", scope:"global"},
    {key:secondaryField, label:engine === "xray" ? "统计" : engine === "sing-box" ? "实验功能" : "运行模式", scope:engine === "ss-rust" ? "override" : "global"},
    ...["inbounds", "outbounds", "listeners", "servers", "shadowsocks", "server_port"].map(key=>({key, label:key, scope:"inbound"})),
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
    : {inbounds:inbounds.map(item => ({tag:item.tag, port:item.port, listen_port:item.port, protocol:"shadowsocks", type:"shadowsocks"})),
      outbounds:[{tag:"direct", protocol:"freedom", type:"direct"},
        ...inbounds.map(item=>({tag:`qch-trf-${item.port}-abcdef012345`, protocol:"freedom", type:"direct"}))]})});
  const content = () => JSON.stringify(root(), null, 2);
  let saved = options.missing && !options.savedMissing ? null : {id:"cfg", agent_id:agent.id, engine, name:"配置", version:1, content:content()};
  let agentContent = saved ? options.drift ? saved.content + "\n# node-only" : saved.content : "";
  const state = {route:"live-config", navigationEpoch:1, routeSignal:controller.signal,
    session:{role:options.shared ? "user" : "admin"}, data:{liveAgent:agent.id, liveEngine:options.multi ? "" : engine, liveConfigSource:options.import ? "import" : "",
      liveSources:saved ? {[`node|${engine}${options.import ? "|import" : ""}`]:{content:agentContent, agentContent}} : {}}};
  const tasks = new Map();
  const workspace = () => ({agent:structuredClone(agent), config:saved && {...saved, version:saved.version+(test.stale?1:0)},
    engine, inbounds:structuredClone(options.native ? [] : inbounds),
    inbound_targets:inbounds.map(item=>({tag:item.tag, port:item.port, kind:options.native ? "socks" : item.protocol})),
    protocols:entries.map(entry => entry.protocol), reality_presets:["www.amazon.com"],
    catalog:{name:engine, format:engine === "mihomo" ? "YAML" : "JSON", fields, topic_groups:[]},
    present_fields:Object.fromEntries(fields.map(field=>[field.key, Object.hasOwn(root(), field.key)]))});
  const api = async (path, request = {}) => {
    test.calls.push({path, method:request.method || "GET"});
    const body = request.body && JSON.parse(request.body);
    if (path === "/agents") return [structuredClone(agent)];
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
    if (path.endsWith("/workspace")) {
      if (test.workspaceGate) await test.workspaceGate;
      if (test.failSwitch && path.includes("/mihomo/")) throw new Error("fixture switch failed");
      if (test.failRefresh && test.writes.length) throw Object.assign(new Error("fixture refresh unavailable"), {status:503});
      return workspace();
    }
    if (path.endsWith("/plans")) {
      const entry = entries.find(item => item.protocol.key === body.protocol);
      return {...structuredClone(entry.plan), tag:`new-${++test.serial}`, port:22000+test.serial};
    }
    if (path.includes("/revisions/")) return {...saved, content:"{}"};
    if (path.includes("/revisions")) return [{...saved, updated_at:new Date().toISOString()}];
    if (path === "/deployments") return [{agent_id:agent.id, engine, config_id:"cfg", config_version:1}];
    if (request.method === "POST" && path === "/tasks") {
      assert(body.action === "read-managed-config" || body.action === "read-config", "deploy preflight created a non-read task");
      test.readRequests.push(body);
      const task = {id:`read-${++test.serial}`, action:body.action, engine, status:"succeeded", snapshot:agentContent};
      tasks.set(task.id, task);
      return task;
    }
    if (request.method === "POST" && path.endsWith("/source")) {
      if (test.gate) await test.gate;
      if (test.sourceFailure) throw Object.assign(new Error("fixture source save failed"), test.sourceFailure);
      assert(body.version === saved.version, "outbound save lost version check");
      test.writes.push({path, ...body});
      saved = {...saved, content:body.content, version:saved.version+1};
      const task = {id:`task-${saved.version}`, action:body.intent, config_version:saved.version, status:"pending"};
      tasks.set(task.id, task);
      return {config:saved, task};
    }
    if (request.method === "POST" && (path.endsWith("/server-inbounds") || path.includes("/fields/"))) {
      if (test.gate) await test.gate;
      if (test.fail) throw Object.assign(new Error("fixture version conflict"), {status:409});
      assert(body.expected_version === (saved?.version || 0), "mutation request lost optimistic version");
      test.writes.push({path, ...body});
      if (path.endsWith("/server-inbounds")) {
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
      saved = {id:"cfg", agent_id:agent.id, engine, name:"配置", version:(saved?.version || 0)+1, content:content()};
      const task = {id:`task-${saved.version}`, action:body.intent, config_version:saved.version, status:test.taskStatus,
        install_if_missing:Boolean(body.install_if_missing && !agent.runtime[engine].installed)};
      tasks.set(task.id, task);
      return {config:saved, task};
    }
    if (path.includes("/fields/")) {
      if (test.fieldGate) await test.fieldGate;
      const key = decodeURIComponent(path.split("/fields/")[1].split("?")[0]);
      return {version:saved.version+(test.staleField?1:0), present:values.has(key), fragment:values.get(key) || ""};
    }
    if (path.startsWith("/tasks/")) {
      const task = tasks.get(path.split("/")[2].split("?")[0]);
      if (path.endsWith("/config-snapshot")) return {content:task?.snapshot || ""};
      if (task?.action === "deploy" && test.taskStatus === "succeeded") agentContent = saved.content;
      return {...task, status:test.taskStatus};
    }
    throw new Error(`Unexpected fixture API: ${path}`);
  };
  const pages = installConfigPages({state, api, optionalAPI:api, engines:[engine], esc, engineName:value=>value,
    conciseVersion:(_engine, version)=>version, date:String, ago:()=> "刚刚", bytes:String,
    can:permission=>!options.readonly && !(options.noFleet && permission === "agents.read") && !(options.noTasks && permission === "tasks.execute") &&
      !(options.noPeerRead && permission === "client-access.read"),
    confirmAction:async(message, title)=>{test.confirmations.push({message, title}); return test.confirm;}, notify:message=>test.notices.push(message),
    renderConfigDiff:()=>'<pre class="config-diff">fixture diff</pre>', bindCodeEditors:()=>{},
    submitTask:async()=>{throw new Error("inbounds must use atomic mutation, not a second task request");},
    shell:markup=>{
      document.body.className = "app-body page-live-config";
      document.body.innerHTML = `<main class="workspace-main" style="max-width:1280px;margin:auto">${markup}</main>`;
    },
  });
  await pages.liveConfig();
  const click = kind => document.querySelector(`[data-inbound-action="${kind}"]`).click();
  const select = tag => {
    const editor = document.querySelector("[data-code-editor]");
    if (editor.configFileController) {
      const inbound = inbounds.find(item => item.tag === tag);
      editor.configFileController.selectInbound(tag, inbound.port);
    } else [...document.querySelectorAll("[data-access-inbound]")].find(button => button.dataset.accessInbound === tag).click();
  };
  const selectCommon = () => document.querySelector('[data-config-file="0"], [data-config-common]').click();
  const common = kind => document.querySelector(`[data-common-action="${kind}"]`).click();
  const reenter = async () => {
    controller.abort();
    controller = new AbortController();
    state.routeSignal = controller.signal;
    state.navigationEpoch++;
    await pages.liveConfig();
  };
  const dispose = () => { controller.abort(); state.data = {}; };
  Object.assign(test, {state, agent, pages, click, common, select, selectCommon, reenter, dispose,
    setAgentContent:value=>{agentContent=value;},
    primaryField, secondaryField, extraField, saved:()=>saved});
  return test;
}
