import { installConfigPages } from "./modules/configs.js";

const assert = (value, message) => { if (!value) throw new Error(message); };
const pause = () => new Promise(resolve => setTimeout(resolve, 30));
const waitFor = async (condition, message) => {
  const end = Date.now() + 4000;
  while (!condition()) {
    if (Date.now() > end) throw new Error(message);
    await new Promise(resolve => setTimeout(resolve, 10));
  }
};
const esc = value => String(value ?? "").replace(/[&<>"']/g, char => (
  {"&":"&amp;", "<":"&lt;", ">":"&gt;", '"':"&quot;", "'":"&#39;"}[char]));
let catalog;

async function fixture(engine, options = {}) {
  catalog ||= await (await fetch("/assets/preset-plans.json")).json();
  const entries = catalog.filter(entry => entry.engine === engine);
  const basePlan = structuredClone(entries[0].plan);
  let controller = new AbortController();
  const agent = {id:"node", name:"香港 · HK-01", os:"linux", arch:"amd64", status:options.offline ? "offline" : "online",
    can_manage:options.shared !== true, capabilities:options.multi ? ["mihomo", engine] : [engine], features:["managed-config-read-v1", "independent-egress-v1",
      ...(options.legacy ? [] : ["preset-auto-install-v1"])],
    runtime:{...(options.multi ? {mihomo:{installed:false}} : {}), [engine]:{installed:!options.missing, version:"test-development", existing_config_available:Boolean(options.import)}}};
  const test = {writes:[], calls:[], notices:[], confirmations:[], confirm:false, serial:0, fail:false, gate:null, taskStatus:"pending"};
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
  const state = {route:"live-config", navigationEpoch:1, routeSignal:controller.signal,
    session:{role:options.shared ? "user" : "admin"}, data:{liveAgent:agent.id, liveEngine:options.multi ? "" : engine, liveConfigSource:options.import ? "import" : "",
      liveSources:saved ? {[`node|${engine}${options.import ? "|import" : ""}`]:{content:options.drift ? saved.content + "\n# node-only" : saved.content}} : {}}};
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
    primaryField, secondaryField, extraField, saved:()=>saved});
  return test;
}

export async function testConfigInboundsRuntime(preview = false) {
  if (preview) {
    const params = new URLSearchParams(location.search);
    window.inboundFixture = await fixture(params.get("engine") || "xray", {missing:params.has("missing"), multi:params.has("multi")});
    if (params.has("common")) window.inboundFixture.common(params.get("common") || "modify");
    else if (params.has("outbound")) {
      window.inboundFixture.select("first");
      document.querySelector(`[data-outbound-action="${params.get("outbound") || "add"}"]`).click();
    }
    else if (params.has("modal")) window.inboundFixture.click("add");
    return;
  }
  for (const engine of ["xray", "sing-box", "mihomo", "ss-rust"]) {
    const test = await fixture(engine);
    const action = kind => document.querySelector(`[data-inbound-action="${kind}"]`);
    assert(!document.querySelector('[data-live-intent="migrate-files"]'), "configuration page still renders the removed bundle action");
    assert(document.querySelector(".config-inbound-menu").previousElementSibling?.matches("[data-config-access-open]"), "inbound menu must be beside restrictions");
    assert(action("modify").disabled && action("delete").disabled, "public config allows inbound mutation");
    test.select("second");
    assert(!action("modify").disabled && !action("delete").disabled, "selected inbound actions disabled");
    test.click("modify");
    await waitFor(()=>document.querySelector("#server-plan-form"), "modify editor did not load");
    let form = document.querySelector("#server-plan-form");
    assert(form.elements.tag.value === "second" && form.elements.port.value === "21002", "modify opened the wrong inbound");
    assert(form.elements.operation.value === "modify" && form.elements.operation.type === "hidden", "operation can escape its selected target");
    assert(!document.querySelector("dialog .inbound-browser, dialog .config-source-studio, dialog [data-engine-select]"), "modal duplicated the page layout");
    if (innerWidth <= 600) {
      form.querySelector('[data-builder-step="identity"]').click();
      const section = form.querySelector("#identity").getBoundingClientRect();
      const tabs = form.querySelector(".builder-index").getBoundingClientRect();
      assert(tabs.bottom <= section.top + 1 && section.width > form.clientWidth * .85, "mobile tabs squeeze the authentication form into a narrow column");
      form.querySelector('[data-builder-step="listen"]').click();
    }
    form.elements.tag.value = "renamed";
    form.elements.port.value = "23002";
    document.querySelector("[data-inbound-close]").click(); await pause();
    assert(document.querySelector("dialog[open]") && form.elements.tag.value === "renamed", "cancel close lost dirty inputs");
    form.querySelector("[data-plan-intent=validate]").click();
    await waitFor(()=>!document.querySelector("dialog[open]"), "modify save did not close modal");
    assert(test.writes.length === 1 && test.writes[0].original_tag === "second" && test.writes[0].expected_version === 1 &&
      test.writes[0].input.tag === "renamed" && !test.writes[0].install_if_missing, "modify identity/version/installation regression");
    assert(document.querySelector("#live-config-form"), "save left the configuration page");
    test.click("add");
    await waitFor(()=>document.querySelector("#server-plan-form"), "add editor did not load");
    form = document.querySelector("#server-plan-form");
    assert(form.elements.operation.value === "add" && form.elements.tag.value !== "renamed", "add reused saved identity");
    let release;
    test.gate = new Promise(resolve=>{release=resolve;});
    const button = form.querySelector("[data-plan-intent=validate]");
    button.click(); button.click(); await pause();
    assert(button.disabled && form.getAttribute("aria-busy") === "true", "saving did not lock controls");
    document.querySelector("[data-inbound-close]").click(); await pause();
    assert(document.querySelector("dialog[open]"), "closed modal during an uncertain write");
    release();
    await waitFor(()=>!document.querySelector("dialog[open]"), "add did not finish");
    assert(test.writes.length === 2 && test.writes[1].operation === "add" && test.writes[1].original_tag === "", "duplicate add or copied original tag");
    test.gate = null;
    test.click("delete");
    await waitFor(()=>document.querySelector("[data-delete-intent]"), "delete confirmation absent");
    assert(document.querySelector("dialog").textContent.includes(test.writes[1].input.tag), "delete confirmation names wrong inbound");
    document.querySelector("[data-inbound-close]").click(); await pause();
    assert(test.writes.length === 2, "canceling deletion wrote data");
    test.click("delete");
    await waitFor(()=>document.querySelector("[data-delete-intent=validate]"), "delete reopened incorrectly");
    document.querySelector("[data-delete-intent=validate]").click();
    await waitFor(()=>!document.querySelector("dialog[open]"), "delete did not return to editor");
    assert(test.writes.length === 3 && test.writes[2].operation === "delete" && test.writes[2].original_tag === test.writes[1].input.tag, "wrong deletion target");
    assert(action("modify").disabled && action("delete").disabled, "deletion retained removed selection");
    if (["xray", "sing-box"].includes(engine)) {
      test.select("first");
      document.querySelector("[data-config-preview]").click();
      assert(action("modify").disabled && action("delete").disabled, "merged preview allows mutation");
      document.querySelector("[data-config-preview]").click();
    }
    const input = document.querySelector("[data-code-input]");
    const original = input.value;
    input.value += "\n "; input.dispatchEvent(new Event("input"));
    test.click("add"); await pause();
    assert(!document.querySelector("dialog[open]") && test.notices.some(message=>message.includes("未保存修改")), "dirty source was overwritten");
    input.value = original; input.dispatchEvent(new Event("input"));
    test.click("advanced");
    await waitFor(()=>document.querySelector("#field-form"), "advanced field editor missing");
    assert(!document.querySelector("dialog #server-plan-form"), "advanced fields unnecessarily mounted preset form");
    document.querySelector("#field-form textarea").value = '{"level":"debug"}';
    document.querySelector("#field-form [data-field-intent=validate]").click();
    await waitFor(()=>!document.querySelector("dialog[open]"), "field save did not refresh source page");
    test.click("history");
    await waitFor(()=>document.querySelector("#revisions [data-revision-body] nav"), "history not accessible");
    assert(!document.querySelector("dialog #server-plan-form"), "history generated an unused credential form");
    document.querySelector("[data-inbound-close]").click(); await pause();
    test.click("diff");
    await waitFor(()=>document.querySelector("dialog .config-diff"), "deployment diff unavailable");
    document.querySelector("[data-inbound-close]").click(); await pause();
    test.click("add");
    await waitFor(()=>document.querySelector("#server-plan-form"), "conflict form missing");
    form = document.querySelector("#server-plan-form");
    form.elements.tag.value = "keep-my-draft";
    test.fail = true;
    form.querySelector("[data-plan-intent=validate]").click();
    await waitFor(()=>document.querySelector("dialog [data-preset-status]")?.textContent.includes("草稿已保留"), "conflict did not preserve draft");
    assert(form.elements.tag.value === "keep-my-draft" && form.querySelector("[data-plan-intent=validate]").disabled, "conflict allowed stale resubmission");
    test.dispose();
    assert(!document.querySelector("dialog"), "route abort left an orphan modal");
  }
  for (const options of [{missing:true}, {missing:true, legacy:true}, {missing:true, shared:true}, {missing:true, offline:true}]) {
    const test = await fixture("xray", options);
    test.click("add");
    await waitFor(()=>document.querySelector("#server-plan-form"), "missing-core editor not available");
    const submit = document.querySelector("[data-plan-intent=validate]");
    const allowed = !options.legacy && !options.shared && !options.offline;
    assert(submit.disabled !== allowed, "auto-install permission/capability/offline gate incorrect");
    if (allowed) {
      submit.click();
      await waitFor(()=>test.writes.length === 1 && !document.querySelector("dialog"), "auto-install submission failed");
      assert(test.writes[0].install_if_missing && test.writes[0].intent === "validate", "missing core did not opt into stable installation");
      assert(!test.calls.some(call=>call.path === "/tasks"), "installation requires an open frontend for a second task");
      assert(document.body.textContent.includes("稳定版安装"), "installation progress missing");
      const runtimeReads = test.calls.filter(call=>call.path === "/agents").length;
      test.agent.runtime.xray.installed = true;
      test.agent.runtime.xray.version = "stable-installed";
      test.taskStatus = "succeeded";
      await waitFor(()=>test.calls.filter(call=>call.path === "/agents").length > runtimeReads &&
        !document.querySelector("[data-live-intent=validate]")?.disabled &&
        document.body.textContent.includes("stable-installed"), "terminal auto-install task did not refresh installed runtime");
    }
    test.dispose();
  }
  for (const options of [{readonly:true}]) {
    const test = await fixture("xray", options);
    test.click("add"); await pause();
    assert(!document.querySelector("dialog"), "read-only or diverged snapshot permitted mutation");
    test.dispose();
  }
  const switcher = await fixture("xray", {multi:true});
  assert(switcher.state.data.liveEngine === "xray", "default selected an uninstalled core");
  assert(document.querySelector('[data-live-engine="xray"] small').textContent === "已安装", "installed status not visible");
  assert(document.querySelector('[data-live-engine="mihomo"] small').textContent === "未安装", "missing status not visible");
  let releaseSwitch;
  switcher.workspaceGate = new Promise(resolve=>{releaseSwitch=resolve;});
  switcher.failSwitch = true;
  const missingTab = document.querySelector('[data-live-engine="mihomo"]');
  missingTab.click();
  await waitFor(()=>document.querySelector(".live-engine-loading"), "switch did not acknowledge input");
  assert(document.querySelector("#live-config-form").inert, "old source remains editable while switching engines");
  missingTab.click();
  const switchReads = switcher.calls.filter(call=>call.path.includes("/mihomo/")).length;
  assert(switchReads === 1, "repeated click duplicated switch request");
  releaseSwitch();
  await waitFor(()=>switcher.notices.some(message=>message.includes("切换内核失败")), "failed switch lost feedback");
  assert(switcher.state.data.liveEngine === "xray" && !missingTab.disabled, "failed switch did not restore usable previous editor");
  assert(!document.querySelector("#live-config-form").inert, "failed switch left the source inert");
  switcher.workspaceGate = new Promise(resolve=>{releaseSwitch=resolve;});
  missingTab.click();
  await waitFor(()=>document.querySelector(".live-engine-loading"), "second switch not started");
  document.querySelector('[data-live-engine="xray"]').click();
  await waitFor(()=>switcher.state.data.liveEngine === "xray", "latest selection was blocked by previous request");
  releaseSwitch();
  await waitFor(()=>!document.querySelector(".live-engine-loading"), "latest switch did not finish");
  assert(document.querySelector('[data-live-engine="xray"]').classList.contains("active"), "old response replaced latest selection");
  switcher.dispose();
  const drift = await fixture("xray", {drift:true});
  drift.click("add");
  await waitFor(()=>document.querySelector("#server-plan-form"), "node snapshot drift blocked saved config editing");
  assert(drift.writes.length === 0, "opening drifted config unexpectedly saved or deployed");
  drift.dispose();
  await testConfigOutboundsRuntime();
  const background = await fixture("xray");
  background.confirm = true;
  background.click("add");
  await waitFor(()=>document.querySelector("#server-plan-form"), "background fixture did not open");
  document.querySelector("[data-plan-intent=deploy]").click();
  await waitFor(()=>background.writes.length === 1 && !document.querySelector("dialog"), "background deployment not submitted");
  background.select("first"); background.click("delete");
  await waitFor(()=>document.querySelector("[data-delete-intent]"), "delete fixture did not open");
  const workspaceReads = background.calls.filter(call=>call.path.endsWith("/workspace")).length;
  background.taskStatus = "succeeded";
  await new Promise(resolve=>setTimeout(resolve, 1800));
  assert(background.calls.filter(call=>call.path.endsWith("/workspace")).length === workspaceReads,
    "background deployment invalidated the open delete dialog");
  background.dispose();
  const stale = await fixture("xray");
  let release;
  stale.workspaceGate = new Promise(resolve=>{release=resolve;});
  stale.click("add");
  stale.dispose();
  release(); await pause();
  assert(!document.querySelector("dialog") && !stale.writes.length, "late response escaped its session");
  const narrow = await fixture("xray", {noFleet:true});
  assert(document.querySelector("#live-config-form") && !narrow.calls.some(call=>call.path === "/agents"), "config-only deep link requires fleet permission");
  narrow.dispose();
  const native = await fixture("mihomo", {native:true});
  native.select("first");
  assert(document.querySelector('[data-inbound-action="modify"]').disabled &&
    !document.querySelector('[data-inbound-action="delete"]').disabled &&
    !document.querySelector("[data-config-access-open]").disabled, "native non-preset listener lost restriction/delete selection");
  native.click("delete");
  await waitFor(()=>document.querySelector("[data-delete-intent=validate]"), "native deletion did not open");
  document.querySelector("[data-delete-intent=validate]").click();
  await waitFor(()=>native.writes.length === 1 && !document.querySelector("dialog"), "native deletion failed");
  assert(native.writes[0].original_tag === "first", "native deletion targeted another inbound");
  native.dispose();
  const protocolDraft = await fixture("xray");
  protocolDraft.click("add");
  await waitFor(()=>document.querySelector("#server-plan-form"), "protocol draft form missing");
  let draftForm = document.querySelector("#server-plan-form");
  const selector = document.querySelector("[data-preset-protocol]");
  const originalProtocol = selector.value, otherProtocol = selector.options[1].value;
  draftForm.elements.tag.value = "protocol-specific-draft";
  draftForm.elements.tag.dispatchEvent(new Event("input", {bubbles:true}));
  selector.value = otherProtocol; selector.dispatchEvent(new Event("change", {bubbles:true}));
  await waitFor(()=>document.querySelector("#server-plan-form") !== draftForm, "protocol selection did not update the embedded form");
  draftForm = document.querySelector("#server-plan-form");
  const restoredSelector = document.querySelector("[data-preset-protocol]");
  restoredSelector.value = originalProtocol; restoredSelector.dispatchEvent(new Event("change", {bubbles:true}));
  await waitFor(()=>document.querySelector("#server-plan-form") !== draftForm, "protocol return did not load");
  assert(document.querySelector("#server-plan-form").elements.tag.value === "protocol-specific-draft", "protocol switch lost its local draft");
  protocolDraft.dispose();
  const refreshFailure = await fixture("xray");
  refreshFailure.click("add");
  await waitFor(()=>document.querySelector("#server-plan-form"), "refresh-failure form missing");
  refreshFailure.failRefresh = true;
  document.querySelector("[data-plan-intent=validate]").click();
  await waitFor(()=>!document.querySelector("dialog") &&
    document.querySelector("[data-preset-status]")?.textContent.includes("已保存且任务已提交，页面刷新失败"), "committed save was silently lost after parent refresh failed");
  assert(refreshFailure.writes.length === 1 && refreshFailure.notices.some(message=>message.includes("已保存且任务已提交")), "refresh failure misreported the durable mutation");
  refreshFailure.failRefresh = false;
  document.querySelector("[data-preset-status] button").click();
  await waitFor(()=>document.querySelector('.editor-toolbar-state b')?.textContent === "v2", "refresh recovery did not bind the saved revision");
  assert(refreshFailure.writes.length === 1, "reload repeated the inbound mutation");
  refreshFailure.dispose();
  await testCommonConfigRuntime();
}

async function testConfigOutboundsRuntime() {
  const action = kind => document.querySelector(`[data-outbound-action="${kind}"]`);
  const input = () => document.querySelector('textarea[aria-label="出站配置 JSON"]');
  const save = () => document.querySelector('dialog [data-intent="validate"]').click();
  const open = async kind => {
    action(kind).click();
    await waitFor(input, `${kind} outbound editor missing`);
  };
  const changeInput = value => {
    const mode = document.querySelector("[data-outbound-mode]");
    if (mode && mode.value !== "json") { mode.value = "json"; mode.dispatchEvent(new Event("change")); }
    input().value = value;
    input().dispatchEvent(new Event("input"));
  };
  const close = async () => {
    document.querySelector("[data-outbound-close]").click();
    await pause();
  };
  for (const engine of ["xray", "sing-box"]) {
    const test = await fixture(engine, {drift:true});
    const menu = action("add").closest("details");
    assert(menu.previousElementSibling.querySelector("[data-inbound-action]"), "outbound menu is not next to inbound operations");
    assert(["add", "bind", "modify", "delete"].every(kind=>action(kind).disabled), "outbound operation has no inbound scope");
    test.select("first");
    assert(!action("add").disabled && !action("bind").disabled && action("modify").disabled && action("delete").disabled,
      "unbound inbound must allow creation/binding, not editing another exit");
    const inboundMenu = menu.previousElementSibling;
    inboundMenu.querySelector("summary").click();
    menu.querySelector("summary").click();
    assert(!inboundMenu.open && menu.open && menu.querySelector("summary").getAttribute("aria-expanded") === "true",
      "opening outbound menu did not close inbound menu");
    const menuBounds = menu.querySelector('[role="menu"]').getBoundingClientRect();
    assert(menuBounds.left >= 0 && menuBounds.right <= innerWidth, "outbound menu exceeds viewport");
    menu.querySelector("summary").focus();
    menu.dispatchEvent(new KeyboardEvent("keydown", {key:"ArrowDown", bubbles:true}));
    assert(document.activeElement === action("add"), "outbound keyboard entry differs from inbound menu");
    menu.dispatchEvent(new KeyboardEvent("keydown", {key:"End", bubbles:true}));
    assert(document.activeElement === action("bind"), "keyboard focused disabled outbound action");
    menu.dispatchEvent(new KeyboardEvent("keydown", {key:"Escape", bubbles:true}));
    assert(!menu.open && document.activeElement === menu.querySelector("summary"), "Escape did not restore outbound menu focus");

    let release;
    test.workspaceGate = new Promise(resolve=>{release=resolve;});
    action("add").click();
    assert(document.querySelector('dialog[open] [role="status"]')?.textContent.includes("正在读取"), "outbound click gives no immediate loading feedback");
    await close();
    release(); test.workspaceGate = null; await pause();
    assert(!document.querySelector("dialog"), "late read reopened a canceled outbound dialog");
    await open("add");
    assert(document.querySelector("[data-bound-inbound]").textContent.includes("first") &&
      document.querySelector("[data-outbound-mark]").textContent.includes("0x51435209"), "binding identity or required mark missing");
    if (innerWidth <= 600) {
      const dialog = document.querySelector("dialog"), rect = dialog.getBoundingClientRect();
      assert(dialog.scrollWidth <= dialog.clientWidth + 1 && rect.left >= 0 && rect.right <= innerWidth,
        "mobile outbound dialog overflows");
      assert(document.querySelector('[data-intent="deploy"]').getBoundingClientRect().height >= 40, "outbound touch actions too small");
    }
    changeInput(input().value.replace("exit-21001", "exit-a"));
    assert(test.pages.configHasUnsavedChanges(), "outbound draft not protected during navigation");
    await close();
    assert(input()?.value.includes("exit-a"), "canceling close lost outbound draft");
    test.gate = new Promise(resolve=>{release=resolve;});
    save(); save(); await pause();
    assert(document.querySelector("dialog form").getAttribute("aria-busy") === "true" &&
      document.querySelector("[data-outbound-close]").disabled && input().disabled &&
      document.querySelector("[data-outbound-status]").textContent.includes("正在保存"), "pending outbound save has no consistent lock/status");
    document.querySelector("dialog").dispatchEvent(new Event("cancel", {cancelable:true}));
    assert(document.querySelector("dialog[open]"), "Escape closed an uncertain outbound write");
    release();
    await waitFor(()=>test.writes.length === 1 && !document.querySelector("dialog"), "outbound creation failed");
    test.gate = null;
    const routeKey = engine === "xray" ? "routing" : "route", targetKey = engine === "xray" ? "outboundTag" : "outbound";
    const binding = () => JSON.parse(test.saved().content)[routeKey].rules.find(rule=>rule[targetKey] === "exit-a");
    assert(test.writes[0].path.endsWith("/source") && binding() && !action("modify").disabled,
      "atomic outbound save lost binding or selected inbound");
    await open("modify");
    changeInput(input().value.replace("exit-a", "exit-b"));
    save();
    await waitFor(()=>test.writes.length === 2 && !document.querySelector("dialog"), "bound rename failed");
    assert(!binding() && JSON.parse(test.saved().content)[routeKey].rules.some(rule=>rule[targetKey] === "exit-b"),
      "renaming did not update inbound route");
    await open("bind");
    assert(input().readOnly, "binding an existing template must show a read-only preview");
    const select = document.querySelector('dialog select');
    select.value = "0"; select.dispatchEvent(new Event("change"));
    assert(input().value.includes("direct") && test.pages.configHasUnsavedChanges(), "existing outbound selection did not update/protect its preview");
    save(); await pause();
    assert(test.writes.length === 2 && document.querySelector("dialog"), "canceled rebinding wrote data");
    test.confirm = true;
    save();
    await waitFor(()=>test.writes.length === 3 && !document.querySelector("dialog"), "existing binding failed");
    await open("modify");
    save();
    await waitFor(()=>document.querySelector("[data-outbound-error]")?.textContent.includes("共享出口"), "default/shared exit was not protected");
    assert(test.writes.length === 3, "inbound edit altered global default");
    await close();
    await open("bind");
    const rebound = document.querySelector('dialog select');
    rebound.value = [...rebound.options].find(option=>option.textContent === "exit-b").value;
    rebound.dispatchEvent(new Event("change"));
    save();
    await waitFor(()=>test.writes.length === 4 && !document.querySelector("dialog"), "rebinding previous template failed");
    await open("delete");
    assert(input().readOnly && input().value.includes("exit-b") &&
      document.querySelector('[data-intent="deploy"]').classList.contains("danger"), "deletion lost readonly preview or danger style");
    test.confirm = false;
    save(); await pause();
    assert(test.writes.length === 4 && document.querySelector("dialog"), "canceled deletion wrote data");
    test.confirm = true;
    save();
    await waitFor(()=>test.writes.length === 5 && !document.querySelector("dialog"), "bound deletion failed");
    const final = JSON.parse(test.saved().content);
    assert(!final.outbounds.some(entry=>entry.tag === "exit-b") && !final[routeKey].rules.length &&
      final.inbounds.length === 2 && action("modify").disabled, "deletion left a binding or changed another inbound");
    test.dispose();
  }
  for (const status of [400, 409, 503, undefined]) {
    const test = await fixture("xray");
    test.select("first");
    await open("add");
    changeInput(input().value.replace("exit-21001", "retained-draft"));
    test.sourceFailure = {status};
    save();
    await waitFor(()=>!document.querySelector("[data-outbound-error]").hidden, "save error not visible");
    assert(input().value.includes("retained-draft") && !input().disabled && !test.writes.length, "save failure lost draft or left inputs locked");
    assert(document.querySelector('[data-intent="validate"]').disabled === (status !== 400), "uncertain/conflicting writes allow duplicate submission");
    test.confirm = true;
    await close();
    test.dispose();
  }
  const late = await fixture("xray");
  late.select("first");
  let release;
  late.workspaceGate = new Promise(resolve=>{release=resolve;});
  action("add").click();
  late.dispose();
  release(); await pause();
  assert(!document.querySelector("dialog") && !late.writes.length, "outbound read escaped its route");
  for (const options of [{readonly:true}, {noTasks:true}, {import:true}]) {
    const test = await fixture("xray", options);
    test.select("first");
    assert(["add", "bind", "modify", "delete"].every(kind=>action(kind).disabled), "outbound actions bypass permission/source gates");
    test.dispose();
  }
  await testOutboundPresetsRuntime();
}

async function testOutboundPresetsRuntime() {
  const change = (element, value) => {
    element.value = value;
    element.dispatchEvent(new Event(element.tagName === "SELECT" ? "change" : "input", {bubbles:true}));
  };
  const open = async () => {
    document.querySelector('[data-outbound-action="add"]').click();
    await waitFor(()=>document.querySelector("[data-outbound-mode]"), "outbound preset selector missing");
  };
  for (const engine of ["xray", "sing-box"]) {
    const test = await fixture(engine);
    test.select("first");
    await open();
    const field = key => document.querySelector(`[data-outbound-value="${key}"]`);
    const input = document.querySelector('textarea[aria-label="出站配置 JSON"]');
    const mode = document.querySelector("[data-outbound-mode]");
    assert(mode.value === "preset" && input.readOnly, "new outbound should start with a preset, not raw JSON");
    change(document.querySelector("[data-outbound-protocol]"), "vless");
    assert(document.querySelector("[data-outbound-credential-label]").textContent.includes("UUID"), "VLESS preset mislabels its credential as a password");
    document.querySelector('[data-intent="validate"]').click();
    await waitFor(()=>document.querySelector("[data-outbound-error]").textContent.includes("服务器地址"), "incomplete preset submitted");
    assert(!test.writes.length, "incomplete preset wrote source");
    change(field("server"), "edge.example");
    change(field("credential"), "123e4567-e89b-42d3-a456-426614174000");
    change(field("security"), "reality");
    assert(field("serverName").placeholder.includes("必填"), "Reality placeholder conflicts with required ServerName validation");
    change(field("serverName"), "tls.example");
    change(field("publicKey"), "public-only");
    change(field("shortId"), "aabb");
    assert(input.value.includes("public-only") && input.value.includes("tls.example"), "preset did not produce Reality parameters");
    change(mode, "json");
    assert(!input.readOnly && input.value.includes("public-only"), "switching to JSON lost preset output");
    input.value = input.value.replace('"tag":', '"large":9007199254740993,"tag":');
    input.dispatchEvent(new Event("input"));
    change(mode, "preset");
    change(mode, "json");
    assert(input.value.includes("9007199254740993"), "mode switch lost advanced JSON draft");
    test.confirm = true;
    document.querySelector('[data-outbound-close]').click();
    await waitFor(()=>!document.querySelector("dialog"), "preset draft did not close");
    await open();
    let release;
    test.peerGate = new Promise(resolve=>{release=resolve;});
    change(document.querySelector("[data-outbound-mode]"), "node");
    assert(document.querySelector("[data-outbound-peer-status]").textContent.includes("正在读取"), "peer picker gave no loading feedback");
    release();
    await waitFor(()=>document.querySelector('[data-outbound-node-select] option[value="peer-node"]'), "deployed peer listing missing");
    const node = document.querySelector("[data-outbound-node-select]");
    assert(![...node.options].some(option=>option.value === test.agent.id), "peer picker permits selecting this same node");
    change(node, "incompatible");
    assert(document.querySelector("[data-outbound-peer-status]").textContent.includes("Snell"), "unsupported peer has no explanation");
    document.querySelector('[data-intent="validate"]').click(); await pause();
    assert(!test.writes.length, "incompatible node silently reused another node's outbound");
    change(node, "peer-node");
    change(field("tag"), "node-exit");
    const preview = document.querySelector('textarea[aria-label="出站配置 JSON"]');
    assert(preview.readOnly && preview.value.includes("203.0.113.10") && preview.value.includes("peer.example"), "peer address or TLS not filled");
    const originalPeer = JSON.parse(preview.value);
    const peerSelect = document.querySelector("[data-outbound-peer-select]");
    test.peerEntries = [{agent_id:"peer-node", agent_name:"出口 · JP", engine:"xray", profiles:[
      {tag:"new-first", protocol:"VLESS", port:3443, outbound:{...originalPeer, tag:"new-first"}},
      {tag:"remote-inbound", protocol:"VLESS", port:2443, address:"203.0.113.10", outbound:originalPeer},
    ]}];
    document.querySelector("[data-outbound-peer-reload]").click();
    await waitFor(()=>!document.querySelector("[data-outbound-peer-reload]").disabled, "peer refresh did not finish");
    assert(peerSelect.selectedOptions[0].textContent.includes("remote-inbound"), "refresh silently switched to a different target inbound");
    test.peerEntries[0].profiles.pop();
    document.querySelector("[data-outbound-peer-reload]").click();
    await waitFor(()=>!document.querySelector("[data-outbound-peer-reload]").disabled, "peer removal refresh did not finish");
    assert(peerSelect.value === "" && preview.value === "" &&
      document.querySelector("[data-outbound-peer-status]").textContent.includes("不会自动"), "removed peer silently fell back to another target");
    document.querySelector('[data-intent="validate"]').click(); await pause();
    assert(!test.writes.length, "removed peer could still submit its stale credentials");
    test.peerEntries = undefined;
    document.querySelector("[data-outbound-peer-reload]").click();
    await waitFor(()=>!document.querySelector("[data-outbound-peer-reload]").disabled, "restored peer refresh did not finish");
    assert(peerSelect.value === "", "refresh selected an inbound without the user's choice");
    change(peerSelect, "0");
    document.querySelector('[data-intent="validate"]').click();
    await waitFor(()=>test.writes.length === 1 && !document.querySelector("dialog"), "peer outbound did not save");
    const saved = JSON.parse(test.saved().content), route = engine === "xray" ? saved.routing : saved.route;
    assert(saved.outbounds.some(entry=>entry.tag === "node-exit") &&
      route.rules.some(rule=>(rule.outboundTag || rule.outbound) === "node-exit"), "peer outbound lost local inbound binding");
    assert(test.calls.filter(call=>call.method !== "GET").every(call=>!call.path.includes("peer-node")), "peer selection modified target node");
    test.dispose();
  }
  const restricted = await fixture("xray", {noPeerRead:true});
  restricted.select("first");
  await open();
  assert(document.querySelector('[data-outbound-mode] option[value="node"]').disabled &&
    !restricted.calls.some(call=>call.path.startsWith("/client-access")), "peer selector bypassed client-access permission");
  restricted.dispose();
}

async function testCommonConfigRuntime() {
  const form = () => document.querySelector("#field-form");
  const selectField = async key => {
    const select = document.querySelector("[data-common-field]");
    assert([...select.options].some(option=>option.value === key), `common field ${key} is unavailable`);
    select.value = key;
    select.dispatchEvent(new Event("change", {bubbles:true}));
    await waitFor(()=>form() && document.querySelector("#common-options .field-editor-heading code")?.textContent === key,
      `common field ${key} did not load`);
  };
  const protectedStructure = test => {
    const saved = JSON.parse(test.saved().content);
    return JSON.stringify(Object.fromEntries(["inbounds", "outbounds", "listeners", "servers"].filter(key=>key in saved).map(key=>[key, saved[key]])));
  };
  for (const engine of ["xray", "sing-box", "mihomo", "ss-rust"]) {
    const test = await fixture(engine);
    const structure = protectedStructure(test);
    test.selectCommon();
    assert(!document.querySelector("dialog"), "selecting public config must not automatically open an editor");
    assert(document.querySelector("[data-config-operation-label]").textContent === "通用配置操作", "public selection did not change the operation menu");
    assert(!document.querySelector("[data-common-actions]").hidden &&
      document.querySelector('[data-inbound-action="modify"]').hidden, "public menu mixes common and inbound mutations");
    const add = document.querySelector('[data-inbound-action="add"]');
    assert(add && !add.hidden && !add.disabled && add.closest(".config-file-actions") &&
      !add.closest(".config-inbound-menu") && document.querySelectorAll('[data-inbound-action="add"]').length === 1,
      "add inbound must always be a single standalone source-toolbar action");
    const preview = document.querySelector("[data-config-preview]");
    if (preview) {
      assert(add.nextElementSibling === preview, "add inbound must be immediately before merged preview");
      const bounds = add.getBoundingClientRect(), previewBounds = preview.getBoundingClientRect();
      assert(bounds.right <= previewBounds.left && Math.abs(bounds.top - previewBounds.top) <= 1,
        "add inbound and merged preview must stay together on one row");
    }
    test.common("add");
    await waitFor(()=>form(), "common add form did not load");
    assert(document.querySelector("#config-inbound-title").textContent === "增加通用配置项", "common dialog title is incorrect");
    assert(form().elements.mutation.type === "hidden" && form().elements.mutation.value === "add", "common add mutation is not locked");
    let choices = [...document.querySelector("[data-common-field]").options].map(option=>option.value);
    assert(choices.join(",") === `dns,${test.extraField}`, "add choices contain existing or inbound-scoped fields");
    assert(!document.querySelector("dialog #server-plan-form, dialog #inbound-options, dialog .field-rail, dialog .config-source-studio"),
      "common editor contains unrelated inbound forms or a second source editor");
    assert(!test.calls.some(call=>call.path.endsWith("/plans")), "common editor generated unused inbound credentials");
    if (innerWidth <= 600) {
      const dialog = document.querySelector("dialog"), bounds = dialog.getBoundingClientRect();
      const field = form().elements.fragment.getBoundingClientRect();
      assert(dialog.scrollWidth <= dialog.clientWidth + 1 && field.left >= bounds.left && field.right <= bounds.right,
        "mobile common editor overflows its dialog");
      assert(form().querySelector("[data-field-intent=validate]").getBoundingClientRect().height >= 40,
        "mobile common actions are too small");
    }
    form().elements.fragment.value = '{"servers":["1.1.1.1"]}';
    await selectField(test.extraField);
    form().elements.fragment.value = '{"draft":true}';
    await selectField("dns");
    assert(form().elements.fragment.value === '{"servers":["1.1.1.1"]}', "switching common fields discarded the local draft");
    document.querySelector("[data-inbound-close]").click(); await pause();
    assert(document.querySelector("dialog[open]") && form().elements.fragment.value.includes("1.1.1.1"),
      "canceling common editor close discarded its draft");
    test.confirm = true;
    let release;
    test.gate = new Promise(resolve=>{release=resolve;});
    const submit = form().querySelector("[data-field-intent=validate]");
    submit.click(); submit.click(); await pause();
    assert(submit.disabled && document.querySelector("[data-common-field]").disabled &&
      form().getAttribute("aria-busy") === "true", "common save did not lock form and field switching");
    document.querySelector("[data-inbound-close]").click(); await pause();
    assert(document.querySelector("dialog[open]"), "common dialog closed during a pending write");
    release();
    await waitFor(()=>!document.querySelector("dialog"), "common add did not return to the source editor");
    test.gate = null;
    assert(test.writes.length === 1 && test.writes[0].path.endsWith("/fields/dns") &&
      test.writes[0].mutation === "add" && test.writes[0].expected_version === 1 &&
      !Object.hasOwn(test.writes[0], "install_if_missing"), "common add used wrong field, revision, or installation flow");
    assert(protectedStructure(test) === structure && JSON.parse(test.saved().content).dns.servers[0] === "1.1.1.1",
      "adding a common field altered inbounds or their paired exits");
    assert(document.querySelector("[data-config-operation-label]").textContent === "通用配置操作",
      "common save changed the current file selection");
    assert(test.confirmations.some(item=>item.title === "存在其他未保存修改"), "saving one common field silently discarded other field drafts");

    test.select("first");
    assert(document.querySelector("[data-common-actions]").hidden && !document.querySelector('[data-inbound-action="modify"]').hidden,
      "selecting an inbound did not restore inbound actions");
    assert(!document.querySelector('[data-inbound-action="add"]').hidden && !document.querySelector('[data-inbound-action="add"]').disabled,
      "selecting an existing inbound hid the standalone add action");
    test.common("delete"); await pause();
    assert(!document.querySelector("dialog"), "hidden common action can mutate the current inbound selection");
    const menu = document.querySelector(".config-inbound-menu");
    menu.querySelector("summary").focus();
    menu.dispatchEvent(new KeyboardEvent("keydown", {key:"ArrowDown", bubbles:true}));
    assert(document.activeElement.dataset.inboundAction === "modify", "keyboard menu navigation focused a hidden common action");
    menu.querySelector("summary").focus();
    menu.dispatchEvent(new KeyboardEvent("keydown", {key:"ArrowUp", bubbles:true}));
    assert(document.activeElement.dataset.inboundAction === "delete", "menu ArrowUp did not enter at the last visible item");
    menu.open = false;
    if (["xray", "sing-box"].includes(engine)) {
      document.querySelector("[data-config-preview]").click();
      assert(document.querySelector("[data-common-actions]").hidden &&
        document.querySelector('[data-common-action="modify"]').disabled, "merged preview is treated as common configuration");
      assert(!document.querySelector('[data-inbound-action="add"]').hidden, "merged preview hid the standalone add action");
      document.querySelector("[data-config-preview]").click();
    }
    test.selectCommon();
    assert(!document.querySelector("dialog"), "returning to common source opened a modal");
    const source = document.querySelector("[data-code-input]"), baseline = source.value;
    source.value += "\n "; source.dispatchEvent(new Event("input"));
    test.common("modify"); await pause();
    assert(!document.querySelector("dialog") && test.notices.some(message=>message.includes("未保存修改")),
      "common mutation ignored an unsaved source draft");
    source.value = baseline; source.dispatchEvent(new Event("input"));
    test.common("modify");
    await waitFor(()=>form(), "common modify form did not load");
    await selectField("dns");
    choices = [...document.querySelector("[data-common-field]").options].map(option=>option.value);
    assert(choices.includes(test.primaryField) && choices.includes(test.secondaryField) &&
      choices.includes("dns") && !choices.includes(test.extraField), "modify choices do not reflect saved field presence");
    assert(form().elements.fragment.value.includes("1.1.1.1") && form().elements.mutation.value === "modify",
      "modify did not load the saved field value and locked operation");
    form().elements.fragment.value = '{"servers":["9.9.9.9"]}';
    form().elements.mutation.value = "delete";
    form().querySelector("[data-field-intent=validate]").click(); await pause();
    assert(test.writes.length === 1 && test.notices.some(message=>message.includes("操作已变化")),
      "tampering with a hidden mutation escaped the common operation");
    form().elements.mutation.value = "modify";
    form().querySelector("[data-field-intent=validate]").click();
    await waitFor(()=>!document.querySelector("dialog"), "common modify did not finish");
    assert(test.writes.length === 2 && test.writes[1].mutation === "modify" &&
      test.writes[1].expected_version === 2 && protectedStructure(test) === structure, "common modify changed another target");
    const beforeDelete = JSON.parse(test.saved().content);
    test.common("delete");
    await waitFor(()=>form(), "common delete form did not load");
    await selectField("dns");
    assert(form().elements.fragment.readOnly && form().elements.fragment.value.includes("9.9.9.9") &&
      form().elements.mutation.value === "delete", "common deletion must show the selected saved value read-only");
    test.confirm = false;
    form().querySelector("[data-field-intent=deploy]").click(); await pause();
    assert(test.writes.length === 2 && document.querySelector("dialog[open]"), "canceling common deletion wrote data");
    assert(test.confirmations.at(-1).message.includes("dns") &&
      test.confirmations.at(-1).message.includes("重启内核"), "common delete confirmation omitted field identity or deploy impact");
    test.confirm = true;
    form().querySelector("[data-field-intent=validate]").click();
    await waitFor(()=>!document.querySelector("dialog"), "common delete did not finish");
    const afterDelete = JSON.parse(test.saved().content);
    delete beforeDelete.dns;
    assert(test.writes.length === 3 && test.writes[2].mutation === "delete" &&
      test.writes[2].expected_version === 3 && test.writes[2].fragment === "" &&
      JSON.stringify(afterDelete) === JSON.stringify(beforeDelete), "common deletion removed more than the selected field");
    test.common("add");
    await waitFor(()=>form(), "deleted common field cannot be added again");
    assert([...document.querySelector("[data-common-field]").options].some(option=>option.value === "dns"),
      "deleted common field remained in the wrong operation list");
    test.dispose();
  }
  for (const options of [{missing:true}, {emptyInbounds:true}]) {
    const test = await fixture("xray", options);
    const add = document.querySelector('[data-inbound-action="add"]');
    assert(add && !add.hidden && !add.disabled && add.closest(".config-file-actions") &&
      !add.closest(".config-inbound-menu"), "empty workspace lost the standalone add-inbound action");
    test.click("add");
    await waitFor(()=>document.querySelector("#server-plan-form"), "first inbound editor did not open");
    document.querySelector("[data-plan-intent=validate]").click();
    await waitFor(()=>test.writes.length === 1 && !document.querySelector("dialog"), "first inbound did not save");
    test.selectCommon();
    assert(!document.querySelector('[data-inbound-action="add"]').hidden &&
      !document.querySelector('.config-inbound-menu [data-inbound-action="add"]'),
      "creating the first inbound hid the persistent action or moved it into the common menu");
    test.dispose();
  }
  const nativeCommon = await fixture("mihomo", {native:true});
  assert(!document.querySelector('[data-inbound-action="add"]').hidden &&
    !document.querySelector('.config-inbound-menu [data-inbound-action="add"]'),
    "native non-preset listeners changed the standalone add action");
  nativeCommon.dispose();
  for (const options of [{readonly:true}, {noTasks:true}, {import:true}]) {
    const test = await fixture("xray", options);
    assert(!document.querySelector('[data-inbound-action="add"]').hidden, "permission or source state should disable, not hide, the add action");
    for (const operation of ["add", "modify", "delete"]) {
      test.common(operation); await pause();
      assert(!document.querySelector("dialog") && !test.writes.length,
        "read-only, denied, imported or drifted source permits common mutation");
    }
    test.dispose();
  }
  for (const options of [{missing:true}, {missing:true, savedMissing:true}, {offline:true}]) {
    const test = await fixture("ss-rust", options);
    test.common("add");
    if (options.missing && !options.savedMissing) {
      assert(!document.querySelector("dialog") && document.querySelector('[data-common-action="add"]').disabled,
        "common fields can be submitted before a configuration exists");
    } else {
      await waitFor(()=>form(), "unavailable core should still allow common field drafts");
      assert(form().querySelector("[data-field-intent=validate]").disabled &&
        form().querySelector("[data-field-intent=deploy]").disabled, "common field save bypassed runtime availability");
    }
    assert(!test.writes.length, "common configuration unexpectedly installed a missing core");
    test.dispose();
  }
  for (const options of [{noCommon:true}, {allCommon:true}]) {
    const test = await fixture("xray", options);
    test.common(options.noCommon ? "delete" : "add");
    await waitFor(()=>document.querySelector("#common-options"), "empty common choices did not finish loading");
    assert(!form() && document.querySelector("#common-options").textContent.includes(options.noCommon ? "尚无可操作" : "没有可增加"),
      "empty common choices exposed an accidental save target");
    test.dispose();
  }
  for (const kind of ["workspace", "field"]) {
    const test = await fixture("xray");
    if (kind === "workspace") test.stale = true;
    else test.staleField = true;
    test.common("modify");
    await waitFor(()=>document.querySelector("dialog")?.textContent.includes("配置版本已变化"), "common version conflict was not surfaced");
    assert(!form() && !test.writes.length, "common editor allows writes from a stale workspace/field read");
    test.dispose();
  }
  const isolated = await fixture("xray");
  isolated.common("modify");
  await waitFor(()=>form(), "common draft isolation form missing");
  form().elements.fragment.value = '{"level":"draft-only"}';
  await isolated.reenter();
  isolated.common("delete");
  await waitFor(()=>form(), "common deletion did not reopen after navigation");
  assert(form().elements.mutation.value === "delete" && form().elements.fragment.value === '{"level":"info"}',
    "a modify draft leaked into the delete operation");
  isolated.dispose();
  const conflict = await fixture("xray");
  conflict.common("modify");
  await waitFor(()=>form(), "common conflict form missing");
  form().elements.fragment.value = '{"level":"keep-this-draft"}';
  conflict.fail = true;
  form().querySelector("[data-field-intent=validate]").click();
  await waitFor(()=>document.querySelector("dialog [data-preset-status]")?.textContent.includes("草稿已保留"),
    "common save conflict did not preserve the draft");
  assert(form().elements.fragment.value.includes("keep-this-draft") && form().querySelector("[data-field-intent=validate]").disabled,
    "common conflict allowed stale resubmission or discarded inputs");
  conflict.dispose();
  const late = await fixture("xray");
  let release;
  late.fieldGate = new Promise(resolve=>{release=resolve;});
  late.common("modify");
  await waitFor(()=>document.querySelector("[data-common-field]"), "deferred common read did not mount its selector");
  late.dispose();
  release(); await pause();
  assert(!document.querySelector("dialog") && !late.writes.length, "late common field response escaped its disposed session");
}
