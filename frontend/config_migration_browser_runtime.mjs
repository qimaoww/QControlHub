import { installConfigPages } from "./modules/configs.js";

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
  const state = {route:"live-config",navigationEpoch:1,data:{liveAgent:"node",liveEngine:engine,liveConfigSource:imported?"import":"managed",liveSources:{[sourceKey]:{content}}}};
  let saved = {id:"config",version:1,name:"fixture",content};
  const test = {writes:[],tasks:[],confirmations:[],notifications:[],accept:false};
  const api = async (path, options = {}) => {
    if (path === "/agents") return [node];
    if (path.endsWith("/workspace")) {
      if (test.refreshError && test.writes.length) throw test.refreshError;
      return {config:saved};
    }
    if (options.method === "PUT") {
      const input = JSON.parse(options.body); test.writes.push(input);
      if (test.saveGate) await test.saveGate;
      if (test.saveError) throw test.saveError;
      saved = {...input,id:"config",version:saved.version+1}; return saved;
    }
    if (path.startsWith("/tasks/")) return {status:"failed",error:"Fixture: node execution is intentionally not performed"};
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
      const buttons = document.querySelectorAll("[data-config-file]");
      assert(buttons[1].querySelector("b").textContent === "a", "filename must follow the preset tag, not the server name");
      buttons[1].click();
      const input = document.querySelector("[data-code-input]");
      assert(JSON.parse(input.value).outbounds?.[0]?.tag === "qch-trf-1080-111111111111", "inbound editor did not include its dedicated exit");
      assert(!document.querySelector('optgroup[label="出站"]'), "legacy outbound file group remains visible");
      input.value = input.value.replaceAll("1080","2080").replace('"a"','"VLESS-REALITY-443"');
      buttons[0].click();
      assert(buttons[1].querySelector("b").textContent === "VLESS-REALITY-443", "renaming the preset did not refresh its filename");
      document.querySelector('[data-live-intent="deploy"]').click(); await pause();
      assert(test.writes.length===1 && test.tasks.length===1,"source deployment did not save and submit exactly once");
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
    }
  }
  await testSourceSaveRuntime();
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
    await waitFor(()=>document.querySelector(".editor-toolbar-state b")?.textContent === "v2", "source save did not load its saved revision");
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
    } else {
      assert(document.querySelector("[data-live-save-status]").textContent.includes("配置 v2 已保存"), "partial source success misreported durable configuration as unsaved");
      assert(button.disabled, "partial source success allows uncertain resubmission");
      form.dispatchEvent(new SubmitEvent("submit", {cancelable:true, submitter:button}));
      assert(test.writes.length === 1 && test.tasks.length === 1, "partial source failure repeated a committed mutation");
    }
    test.dispose();
  }
}
