import { installConfigPages } from "./modules/configs.js";
import { sameConfigContent } from "./modules/config-inbounds.js";

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
  const controller = new AbortController();
  const agent = {id:"node", name:"香港 · HK-01", os:"linux", arch:"amd64", status:options.offline ? "offline" : "online",
    can_manage:options.shared !== true, capabilities:[engine], features:["managed-config-read-v1", "independent-egress-v1",
      ...(options.legacy ? [] : ["preset-auto-install-v1"])],
    runtime:{[engine]:{installed:!options.missing, version:"test-development"}}};
  const test = {writes:[], calls:[], notices:[], confirm:false, serial:0, fail:false, gate:null, taskStatus:"pending"};
  let inbounds = options.missing ? [] : ["first", "second"].map((tag, index) => ({...basePlan, tag, port:21001+index}));
  const content = () => JSON.stringify(engine === "mihomo"
    ? {listeners:inbounds.map(item => ({name:item.tag, port:item.port, type:"socks"})), rules:["MATCH,DIRECT"]}
    : engine === "ss-rust" ? {servers:inbounds.map(item => ({remarks:item.tag, server_port:item.port, method:item.method, password:item.credential}))}
    : {inbounds:inbounds.map(item => ({tag:item.tag, port:item.port, listen_port:item.port, protocol:"shadowsocks", type:"shadowsocks"}))}, null, 2);
  let saved = options.missing ? null : {id:"cfg", agent_id:agent.id, engine, name:"配置", version:1, content:content()};
  const state = {route:"live-config", navigationEpoch:1, routeSignal:controller.signal,
    session:{role:options.shared ? "user" : "admin"}, data:{liveAgent:agent.id, liveEngine:engine,
      liveSources:saved ? {[`node|${engine}`]:{content:options.drift ? saved.content + "\n# node-only" : saved.content}} : {}}};
  const tasks = new Map();
  const workspace = () => ({agent:structuredClone(agent), config:saved && {...saved, version:saved.version+(test.stale?1:0)},
    engine, inbounds:structuredClone(options.native ? [] : inbounds),
    inbound_targets:inbounds.map(item=>({tag:item.tag, port:item.port, kind:options.native ? "socks" : item.protocol})),
    protocols:entries.map(entry => entry.protocol), reality_presets:["www.amazon.com"],
    catalog:{name:engine, format:engine === "mihomo" ? "YAML" : "JSON", fields:[
      {key:"log", label:"日志", kind:"object", scope:"global"},
      {key:"mode", label:"转发模式", kind:"string", scope:"override"},
    ], topic_groups:[]}, present_fields:{}});
  const api = async (path, request = {}) => {
    test.calls.push({path, method:request.method || "GET"});
    const body = request.body && JSON.parse(request.body);
    if (path === "/agents") return [structuredClone(agent)];
    if (path.endsWith("/workspace")) {
      if (test.workspaceGate) await test.workspaceGate;
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
    if (request.method === "POST" && (path.endsWith("/server-inbounds") || path.includes("/fields/"))) {
      if (test.gate) await test.gate;
      if (test.fail) throw Object.assign(new Error("fixture version conflict"), {status:409});
      assert(body.expected_version === (saved?.version || 0), "inbound request lost optimistic version");
      test.writes.push({path, ...body});
      if (path.endsWith("/server-inbounds")) {
        if (body.operation === "delete") assert(Object.keys(body.input).sort().join(",") === "port,tag", "delete leaked selection metadata into the strict input schema");
        if (body.operation !== "add") inbounds = inbounds.filter(item => item.tag !== body.original_tag);
        if (body.operation !== "delete") inbounds.push(body.input);
      }
      saved = {id:"cfg", agent_id:agent.id, engine, name:"配置", version:(saved?.version || 0)+1, content:content()};
      const task = {id:`task-${saved.version}`, action:body.intent, config_version:saved.version, status:test.taskStatus,
        install_if_missing:Boolean(body.install_if_missing && !agent.runtime[engine].installed)};
      tasks.set(task.id, task);
      return {config:saved, task};
    }
    if (path.includes("/fields/")) return {version:saved.version, present:true, fragment:"{}"};
    if (path.startsWith("/tasks/")) {
      const task = tasks.get(path.split("/")[2].split("?")[0]);
      return {...task, status:test.taskStatus};
    }
    throw new Error(`Unexpected fixture API: ${path}`);
  };
  const pages = installConfigPages({state, api, optionalAPI:api, engines:[engine], esc, engineName:value=>value,
    conciseVersion:(_engine, version)=>version, date:String, ago:()=> "刚刚", bytes:String,
    can:permission=>!options.readonly && !(options.noFleet && permission === "agents.read"), confirmAction:async()=>test.confirm, notify:message=>test.notices.push(message),
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
  const dispose = () => { controller.abort(); state.data = {}; };
  Object.assign(test, {state, agent, pages, click, select, dispose, saved:()=>saved});
  return test;
}

export async function testConfigInboundsRuntime(preview = false) {
  assert(sameConfigContent('{"n":9007199254740993,"a":[1]}', '{ "a":[1], "n":9007199254740993 }'), "format-only changes block presets");
  assert(!sameConfigContent('{"n":9007199254740993}', '{"n":9007199254740992}'), "snapshot comparison rounded a large integer");
  if (preview) {
    const params = new URLSearchParams(location.search);
    window.inboundFixture = await fixture(params.get("engine") || "xray", {missing:params.has("missing")});
    if (params.has("modal")) window.inboundFixture.click("add");
    return;
  }
  for (const engine of ["xray", "sing-box", "mihomo", "ss-rust"]) {
    const test = await fixture(engine);
    const action = kind => document.querySelector(`[data-inbound-action="${kind}"]`);
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
      document.querySelector(".config-file-navigation button").click();
      assert(action("modify").disabled && action("delete").disabled, "merged preview allows mutation");
      document.querySelector(".config-file-navigation button").click();
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
  for (const options of [{readonly:true}, {drift:true}]) {
    const test = await fixture("xray", options);
    test.click("add"); await pause();
    assert(!document.querySelector("dialog"), "read-only or diverged snapshot permitted mutation");
    if (options.drift) assert(test.notices.some(message=>message.includes("快照")), "snapshot drift not explained");
    test.dispose();
  }
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
}
