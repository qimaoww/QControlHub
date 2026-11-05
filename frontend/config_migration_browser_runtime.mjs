import { installConfigPages } from "./modules/configs.js";
import { bindConfigFiles } from "./modules/config-files.js";
import { reconcileView } from "./modules/refresh.js";

// Paired source files are saved through regular deployment. The dedicated
// bundle-generation button has been removed from the configuration page.
const assert = (condition, message) => { if (!condition) throw new Error(message); };
const pause = () => new Promise(resolve => setTimeout(resolve, 30));
const waitFor = async (condition, message) => {
  const deadline = Date.now() + 4000;
  while (!condition()) {
    if (Date.now() > deadline) throw new Error(message);
    await new Promise(resolve => setTimeout(resolve, 10));
  }
};

async function fixture(engine, readOnly = false, imported = false, preview = false, legacy = false, options = {}) {
  const content = engine === "mihomo" ? "listeners: []\n" : '{"inbounds":[{"tag":"a","port":1080,"listen_port":1080}],"outbounds":[{"tag":"direct"},{"tag":"qch-trf-1080-111111111111","custom_integer":9007199254740993}],"large":9007199254740993}';
  const sourceKey = `node|${engine}${imported ? "|import" : ""}`;
  const node = {id:"node",name:"Migration fixture",status:"online",os:"linux",arch:"amd64",features:legacy?["managed-config-read-v1","config-files-v1"]:["managed-config-read-v1","config-files-v1","config-files-paired-v1"],capabilities:[engine],runtime:{[engine]:{installed:true,existing_config_available:imported,version:"fixture"}}};
  if (options.multi) {
    const other = engine === "xray" ? "sing-box" : "xray";
    node.capabilities.push(other);
    node.runtime[other] = {installed:true};
    node.runtime[engine].existing_config_available = true;
  }
  const state = {route:"live-config",navigationEpoch:1,data:{liveAgent:"node",liveEngine:engine,liveConfigSource:imported?"import":"managed",
    liveSources:options.readFromAgent ? {} : {[sourceKey]:{content,agentContent:content}}}};
  const savedDate = version => `2026-11-02T04:05:${String(version).padStart(2,"0")}Z`;
  let saved = {id:"config",version:1,name:"fixture",content,updated_at:savedDate(1)};
  const test = {writes:[],tasks:[],reads:[],confirmations:[],notifications:[],accept:false,
    snapshotReads:0, taskPolls:0, missingSnapshots:options.missingSnapshots || 0, snapshotGate:options.snapshotGate};
  const api = async (path, options = {}) => {
    if (path === "/agents") return [node];
    if (path.endsWith("/workspace")) {
      if (test.workspaceError) throw test.workspaceError;
      if (test.refreshError && test.writes.length) throw test.refreshError;
      return {config:saved,inbound_targets:[{tag:"a",kind:"http",port:1080}]};
    }
    if (options.method === "POST" && path.endsWith("/source")) {
      const input = JSON.parse(options.body); test.writes.push(input);
      if (test.saveGate) await test.saveGate;
      if (test.saveError) throw test.saveError;
      if (test.taskError) throw test.taskError;
      saved = {...input,id:"config",version:saved.version+1,updated_at:savedDate(saved.version+1)};
      test.tasks.push({action:input.intent, expected_config_version:saved.version});
      const task = {id:options.headers["X-Config-Task-ID"], agent_id:"node", engine, config_id:saved.id, config_version:saved.version, action:input.intent, status:"pending"};
      test.receiptTask = task;
      if (test.supersedeLostSave) {
        saved = {...saved,version:saved.version+1,content:saved.content.replaceAll("9007199254740995", "9007199254740997"),updated_at:savedDate(saved.version+1)};
        throw TypeError("测试保存响应中断");
      }
      test.beforeSaveResponse?.();
      return {config:saved, task};
    }
    if (options.method === "PUT") {
      const input = JSON.parse(options.body); test.writes.push(input);
      if (test.saveGate) await test.saveGate;
      if (test.saveError) throw test.saveError;
      saved = {...input,id:"config",version:saved.version+1,updated_at:savedDate(saved.version+1)}; return saved;
    }
    if (options.method === "POST" && path === "/tasks") {
      const input = JSON.parse(options.body);
      test.reads.push(input);
      return {id:`read-${test.reads.length}`,action:input.action,status:"succeeded",
        reused:Boolean(input.prefer_cached),finished_at:new Date().toISOString()};
    }
    if (path.startsWith("/tasks/read-") && path.endsWith("/config-snapshot")) {
      test.snapshotReads++;
      if (test.snapshotGate) await test.snapshotGate;
      if (test.missingSnapshots-- > 0) throw Object.assign(new Error("snapshot retired"), {status:404});
      return {content};
    }
    if (path.startsWith("/tasks/")) {
      test.taskPolls++;
      return {...(path.split("?")[0] === `/tasks/${test.receiptTask?.id}` ? test.receiptTask : {}),status:"failed",error:"测试任务未执行"};
    }
    if (path === `/agents/node/configs/${engine}`) return saved;
    throw new Error(`unexpected API ${path}`);
  };
  const pages = installConfigPages({state,api,optionalAPI:api,engines:[engine],can:()=>!readOnly,esc:String,engineName:v=>v,conciseVersion:()=>"fixture",date:String,ago:String,bytes:String,
    confirmAction:async message=>{test.confirmations.push(message);return preview?window.confirm(message):test.accept;},
    notify:message=>test.notifications.push(message),shell:html=>{document.body.innerHTML=html;},bindCodeEditors:()=>{},
    submitTask:async value=>{
      test.tasks.push(value);
      if (test.taskError) throw test.taskError;
      return {id:`task-${test.tasks.length}`};
    },
  });
  await pages.liveConfig();
  Object.assign(test, {pages, state, saved:()=>saved, dispose:()=>{state.data = {};}});
  window.configMigrationFixture = test;
  return test;
}

export async function testConfigMigrationRuntime(preview = false) {
  if (preview) {
    const params = new URLSearchParams(location.search);
    await fixture(params.get("engine")||"xray",params.has("readonly"),params.has("import"),true,params.has("legacy"));
    return;
  }
  testReconciledConfigFiles();
  {
    const test = await fixture("xray");
    test.supersedeLostSave = true;
    const input = document.querySelector("[data-code-input]");
    input.value = input.value.replaceAll("9007199254740993", "9007199254740995");
    input.dispatchEvent(new Event("input", {bubbles:true}));
    const draft = input.value;
    document.querySelector('[data-live-intent="validate"]').click();
    await waitFor(()=>document.querySelector("[data-live-save-status]")?.textContent.includes("之后又有新的配置保存"), "lost response associated a later revision with the original task");
    assert(input.isConnected && input.value === draft && document.querySelector('[data-live-intent="validate"]').disabled, "superseded receipt lost its draft or allowed duplicate writes");
    const operation = test.state.data.presetOperations?.["node|xray"];
    assert(!operation || operation.version === operation.task.config_version, "old task was labeled with another saved revision");
    assert(test.writes.length === 1 && test.tasks.length === 1 && test.saved().version === 3, "receipt recovery repeated the mutation");
    test.dispose();
  }
  for (const change of ["account", "newer-operation"]) {
    const test = await fixture("xray");
    test.accept = true;
    test.beforeSaveResponse = () => {
      if (change === "account") test.state.data = {...test.state.data, pendingDeployTasks:{}};
      else {
        test.state.data.presetOperations = {"node|xray":{version:3,task:{id:"newer-task"}}};
        test.state.data.pendingDeployTasks = {"node|xray":{taskId:"newer-task"}};
      }
    };
    document.querySelector('[data-live-intent="deploy"]').click();
    await waitFor(()=>test.writes.length === 1 && !document.querySelector("#live-config-form")?.hasAttribute("aria-busy"), "late source response did not release its editor");
    assert(test.state.data.pendingDeployTasks?.["node|xray"]?.taskId === (change === "account" ? undefined : "newer-task"), "late source response replaced another account or newer deployment");
    test.dispose();
  }
  for (const engine of ["xray","sing-box","mihomo","ss-rust"]) {
    for (const restricted of [false,true]) {
      const test = await fixture(engine, restricted);
      assert(!document.querySelector('[data-live-intent="migrate-files"]'), "removed bundle-generation action is still rendered");
      assert(!document.body.textContent.includes("生成入站与出口成套文件"), "removed bundle-generation label is still visible");
      const button = document.querySelector('[data-live-intent="deploy"]');
      assert(Boolean(button) === !restricted, `regular deployment permission changed: ${engine}/${restricted}`);
      const supported = ["xray","sing-box"].includes(engine) && !restricted;
      if (!supported) continue;
      button.click(); await pause();
      assert(test.writes.length===0 && test.tasks.length===0,"canceling source deployment wrote data");
      test.accept = true;
      const input = document.querySelector("[data-code-input]");
      const complete = input.value;
      const inbound = document.querySelector('[data-access-inbound="a"]');
      assert(inbound, "complete editor lost its inbound selection");
      inbound.click();
      assert(input.value === complete && JSON.parse(complete).outbounds?.some(value=>value.tag === "qch-trf-1080-111111111111"), "selection replaced the complete source or lost the dedicated exit");
      assert(!document.querySelector('[data-config-preview]'), "single-file editor retained merged preview");
      input.value = complete.replaceAll("1080","2080").replace('"a"','"VLESS-REALITY-443"');
      input.dispatchEvent(new Event("input", {bubbles:true}));
      document.querySelector('[data-live-intent="deploy"]').click(); await pause();
      assert(test.writes.length===1 && test.tasks.length===1,"source deployment did not save and submit exactly once");
      assert(test.reads.length===0,
        "source deployment required a duplicate Agent read");
      assert(test.tasks[0].action==="deploy","source save must use validated rollback-capable deployment");
      assert(test.tasks[0].expected_config_version===2,"source deployment must use the exact newly saved version");
      assert(test.writes[0].content.includes("9007199254740993") && test.writes[0].content.includes("2080"),"source deployment corrupted integer or discarded fragment draft");
      assert(test.confirmations.length===2,"source deployment presented duplicate confirmations");
    }
    await fixture(engine,false,true);
    assert(!document.querySelector('[data-live-intent="migrate-files"]'),"external service must not expose managed-file migration");
    assert(document.querySelector('[data-live-intent="import"]') && !document.querySelector('[data-live-intent="deploy"]'),
      "removing bundle generation changed explicit external-service import");
    if (["xray","sing-box"].includes(engine)) {
      const legacy = await fixture(engine,false,false,false,true);
      assert(!document.querySelector('[data-live-intent="migrate-files"]'), "legacy Agent still exposes the removed bundle action");
      const button = document.querySelector('[data-live-intent="deploy"]');
      assert(button && !button.disabled,"legacy Agent lost its regular deployment action");
      button.click(); await pause();
      assert(legacy.writes.length===0 && legacy.tasks.length===0,"canceling legacy source deployment wrote data");
      legacy.accept = true;
      button.click(); await pause();
      assert(legacy.writes.length===1 && legacy.tasks.length===1 && legacy.tasks[0].action==="deploy",
        "removing bundle generation blocked normal deployment to a legacy Agent");
      assert(legacy.reads.length===0,
        "legacy source deployment required a duplicate Agent read");
    }
  }
  await testSourceSaveRuntime();
  await testConfigReadCacheRuntime();
}

function testReconciledConfigFiles() {
  for (const dirty of [false, true]) {
    const baseline = JSON.stringify({inbounds:[{tag:"alice",port:24101},{tag:"second",port:24105}],outbounds:[{tag:"direct"}]});
    const markup = () => {
      const form = document.createElement("form");
      form.innerHTML = '<section data-code-editor><header class="code-editor-toolbar"></header><textarea data-code-input></textarea></section>';
      form.querySelector("textarea").textContent = baseline;
      return form;
    };
    const form = markup(); document.body.replaceChildren(form);
    assert(bindConfigFiles(form, "xray", () => {}) === null, "single-file source unexpectedly has a fragment controller");
    if (dirty) form.querySelector("textarea").value = baseline.replace("24105","24106");
    reconcileView(form, markup());
    assert(bindConfigFiles(form, "xray", () => {}) === null, "refresh reintroduced fragments");
    assert(form.querySelectorAll(".config-file-buttons").length === 0, "source binding added fragment navigation");
    const complete = JSON.parse(form.querySelector("textarea").value);
    assert(complete.inbounds.length === 2 && complete.outbounds.length === 1, "refresh lost complete configuration arrays");
    assert(complete.inbounds[1].port === (dirty ? 24106 : 24105), "refresh discarded the complete source draft");
  }
}

async function testConfigReadCacheRuntime() {
  const key = "node|xray";
  for (const missingSnapshots of [0, 1, 2]) {
    const test = await fixture("xray", false, false, false, false, {readFromAgent:true, missingSnapshots});
    await waitFor(()=>test.state.data.liveSources[key] && !test.state.data.liveSources[key].reading,
      "automatic cached read did not finish");
    assert(test.reads.length === (missingSnapshots ? 2 : 1), "snapshot recovery must issue at most one forced read");
    assert(test.reads[0].prefer_cached === true, "automatic read lost its cache preference");
    assert(test.taskPolls === 0, "already terminal cached reads still poll task status");
    if (missingSnapshots) assert(test.reads[1].prefer_cached === undefined, "snapshot recovery reused the failed cache");
    if (missingSnapshots === 2) {
      assert(test.state.data.liveSources[key].error, "exhausted snapshot recovery hid its error");
    } else {
      assert(Boolean(test.state.data.liveSources[key].cached) === !missingSnapshots, "cache provenance is incorrect");
      assert(test.state.data.liveSources[key].agentContent === test.state.data.liveSources[key].content, "Agent baseline was lost");
      const previousReads = test.reads.length;
      await waitFor(()=>document.querySelector("[data-config-refresh]"), "configuration refresh control is missing");
      document.querySelector("[data-config-refresh]").click();
      await waitFor(()=>test.reads.length === previousReads + 1 && test.state.data.liveSources[key]?.taskId,
        "explicit refresh did not finish");
      assert(test.reads.at(-1).prefer_cached === undefined && !test.state.data.liveSources[key].cached,
        "explicit refresh reused the display cache");
    }
    test.dispose();
  }

  // A fresh read can also be retired by another tab before its snapshot GET.
  const deploy = await fixture("xray", false, false, false, false, {missingSnapshots:1});
  deploy.accept = true;
  document.querySelector('[data-live-intent="deploy"]').click();
  await waitFor(()=>deploy.tasks.length === 1, "fresh preflight did not recover a concurrent snapshot retirement");
  assert(deploy.reads.length === 0,
    "fresh preflight recovery used a cached read");
  assert(deploy.writes.length === 1, "preflight recovery duplicated configuration persistence");
  await pause();
  deploy.dispose();

  for (const change of ["account", "route", "epoch", "source"]) {
    let release;
    const snapshotGate = new Promise(resolve=>{release=resolve;});
    const test = await fixture("xray", false, false, false, false, {readFromAgent:true, missingSnapshots:1, snapshotGate});
    await waitFor(()=>test.snapshotReads === 1, "late-snapshot fixture did not start");
    if (change === "account") test.state.data = {...test.state.data, liveSources:{}};
    if (change === "route") test.state.route = "tasks";
    if (change === "epoch") test.state.navigationEpoch++;
    if (change === "source") test.state.data.liveConfigSource = "import";
    const newerRead = {reading:true, pendingTaskId:"newer-read"};
    test.state.data.liveSources[key] = newerRead;
    release();
    await pause();
    assert(test.reads.length === 1, `${change} change allowed an abandoned cache fallback to create a task`);
    assert(test.state.data.liveSources[key] === newerRead, `${change} change let an old read clear the newer read state`);
    test.dispose();
  }
}

async function testSourceSaveRuntime() {
  for (const engine of ["xray", "sing-box"]) {
    const test = await fixture(engine, false, false, false, false, {multi:true});
    let release;
    test.saveGate = new Promise(resolve => { release = resolve; });
    const form = document.querySelector("#live-config-form");
    const button = document.querySelector('[data-live-intent="validate"]');
    button.click();
    await waitFor(()=>test.writes.length === 1, "source save did not start");
    assert(form.dataset.saving === "1" && form.getAttribute("aria-busy") === "true" &&
      document.querySelector("[data-live-save-status]").textContent.includes("正在保存"), "pending source save has no status");
    assert([...document.querySelectorAll(".live-config-workspace button, .live-config-workspace input, .live-config-workspace select, .live-config-workspace textarea")]
      .every(element=>element.disabled), "source save left mutable controls active");
    assert(test.pages.configHasUnsavedChanges(), "pending source save lost navigation protection");
    form.dispatchEvent(new SubmitEvent("submit", {cancelable:true, submitter:button}));
    const otherEngine = [...document.querySelectorAll("[data-live-engine]")].find(tab=>tab.dataset.liveEngine !== engine);
    await otherEngine.onclick({preventDefault(){}});
    await document.querySelector('[data-live-source="import"]').onclick();
    assert(test.state.data.liveEngine === engine && test.state.data.liveConfigSource === "managed", "pending source save allowed target switching");
    assert(test.writes.length === 1 && !test.tasks.length, "source save submitted twice or created a task before persistence");
    release();
    await waitFor(()=>test.saved().version === 2 &&
      document.querySelector(".live-config-version")?.textContent === `已保存 · ${test.saved().updated_at}` &&
      !document.querySelector("[data-live-intent]").disabled, "source save did not load its persisted save date");
    assert(test.writes.length === 1 && test.tasks.length === 1 && test.tasks[0].expected_config_version === 2, "source save lost exact-once revision/task submission");
    assert(!document.querySelector("[data-live-intent]").disabled, "source save did not unlock fresh controls");
    test.dispose();
  }

  for (const failure of ["save", "task", "refresh"]) {
    const test = await fixture("xray");
    if (failure === "save") test.saveError = Object.assign(new Error("source rejected"), {status:422});
    if (failure === "task") test.taskError = Object.assign(new Error("task unavailable"), {status:503});
    if (failure === "refresh") test.refreshError = Object.assign(new Error("refresh unavailable"), {status:503});
    const input = document.querySelector("[data-code-input]");
    input.value = input.value.replace("9007199254740993", "9007199254740995");
    input.dispatchEvent(new Event("input", {bubbles:true}));
    const draft = input.value, form = document.querySelector("#live-config-form");
    const button = document.querySelector('[data-live-intent="validate"]');
    const baseline = [...document.querySelectorAll(".live-config-workspace button, .live-config-workspace textarea")].map(element=>[element, element.disabled]);
    button.click();
    await waitFor(()=>!form.dataset.saving && document.querySelector("[data-live-save-status]")?.getAttribute("role") === "alert", `${failure} failure lost source feedback`);
    assert(input.value === draft && !input.disabled, `${failure} failure discarded or locked the source draft`);
    if (failure === "save") {
      assert(baseline.every(([element, disabled])=>element.disabled === disabled), "rejected source save changed preexisting disabled states");
      test.saveError = null;
      button.click();
      await waitFor(()=>test.tasks.length === 1, "correctable source failure could not retry");
      assert(test.writes.length === 2, "correctable source failure duplicated its retry");
    } else if (failure === "task") {
      assert(test.saved().version === 1 && !test.tasks.length, "task creation failure partially committed the save");
      assert(button.disabled, "unknown transaction outcome allows blind resubmission");
    } else {
      assert(document.querySelector("[data-live-save-status]").textContent.includes("配置已保存"), "partial source success misreported durable configuration as unsaved");
      assert(button.disabled, "partial source success allows uncertain resubmission");
      form.dispatchEvent(new SubmitEvent("submit", {cancelable:true, submitter:button}));
      assert(test.writes.length === 1 && test.tasks.length === 1, "partial source failure repeated a committed mutation");
      test.refreshError = null;
      const retry = document.querySelector("[data-live-save-status] button");
      assert(retry?.textContent === "重新加载配置", "accepted source save has no same-page recovery");
      retry.click();
      await waitFor(()=>!document.querySelector('[data-live-intent="validate"]').disabled,
        "read-only page recovery did not unlock deployment");
      assert(test.writes.length === 1 && test.tasks.length === 1, "page recovery submitted another mutation");
    }
    test.dispose();
  }

  const test = await fixture("xray", false, false, false, false, {multi:true});
  test.accept = true;
  const workspace = document.querySelector(".live-config-workspace");
  const input = document.querySelector("[data-code-input]");
  input.value = input.value.replace("9007199254740993", "9007199254740995");
  const draft = input.value;
  test.workspaceError = Object.assign(Error("temporary workspace read failure"), {status:503});
  const other = document.querySelector('[data-live-engine="sing-box"]');
  await other.onclick({preventDefault(){}});
  assert(test.state.data.liveEngine === "xray" && workspace.isConnected && !workspace.inert &&
    input.value === draft, "failed engine switch lost its original workspace or draft");
  test.workspaceError = null;
  document.querySelector('[data-live-intent="deploy"]').click();
  await waitFor(()=>test.tasks.length === 1, "failed engine switch poisoned the original deployment guard");
  assert(test.writes.length === 1 && test.tasks[0].action === "deploy", "same-page deployment repeated its write");
  test.dispose();
}
