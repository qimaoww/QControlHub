import { installConfigPages } from "./modules/configs.js";

const assert = (condition, message) => { if (!condition) throw new Error(message); };
const pause = () => new Promise(resolve => setTimeout(resolve, 30));

async function fixture(engine, readOnly = false, imported = false, preview = false, legacy = false) {
  const content = engine === "mihomo" ? "listeners: []\n" : '{"inbounds":[{"tag":"a","port":1080,"listen_port":1080}],"outbounds":[{"tag":"direct"},{"tag":"qch-trf-1080-111111111111","custom_integer":9007199254740993}],"large":9007199254740993}';
  const sourceKey = `node|${engine}${imported ? "|import" : ""}`;
  const node = {id:"node",name:"Migration fixture",status:"online",os:"linux",arch:"amd64",features:legacy?["managed-config-read-v1","config-files-v1"]:["managed-config-read-v1","config-files-v1","config-files-paired-v1"],capabilities:[engine],runtime:{[engine]:{installed:true,existing_config_available:imported,version:"fixture"}}};
  const state = {route:"live-config",navigationEpoch:1,data:{liveAgent:"node",liveEngine:engine,liveConfigSource:imported?"import":"managed",liveSources:{[sourceKey]:{content}}}};
  let saved = {id:"config",version:1,name:"fixture",content};
  const test = {writes:[],tasks:[],confirmations:[],notifications:[],accept:false};
  const api = async (path, options = {}) => {
    if (path === "/agents") return [node];
    if (path.endsWith("/workspace")) return {config:saved};
    if (options.method === "PUT") {
      const input = JSON.parse(options.body); test.writes.push(input);
      saved = {...input,id:"config",version:saved.version+1}; return saved;
    }
    if (path.startsWith("/tasks/")) return {status:"failed",error:"Fixture: node execution is intentionally not performed"};
    throw new Error(`unexpected API ${path}`);
  };
  const pages = installConfigPages({state,api,optionalAPI:api,engines:[engine],can:()=>!readOnly,esc:String,engineName:v=>v,conciseVersion:()=>"fixture",date:String,ago:String,bytes:String,
    confirmAction:async message=>{test.confirmations.push(message);return preview?window.confirm(message):test.accept;},
    notify:message=>test.notifications.push(message),shell:html=>{document.body.innerHTML=html;},bindCodeEditors:()=>{},
    submitTask:async value=>{test.tasks.push(value);return {id:`task-${test.tasks.length}`};},
  });
  await pages.liveConfig();
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
      const button = document.querySelector('[data-live-intent="migrate-files"]');
      const supported = ["xray","sing-box"].includes(engine) && !restricted;
      assert(Boolean(button) === supported, `incorrect migration permission/engine visibility: ${engine}/${restricted}`);
      if (!supported) continue;
      button.click(); await pause();
      assert(test.writes.length===0 && test.tasks.length===0,"canceling migration wrote data");
      test.accept = true;
      const select = document.querySelector('select[aria-label="选择入站与独立出口或公共配置文件"]');
      assert(select.options[1].textContent === "a.json", "filename must follow the preset tag, not the server name");
      select.value = "1"; select.dispatchEvent(new Event("change"));
      const input = document.querySelector("[data-code-input]");
      assert(JSON.parse(input.value).outbounds?.[0]?.tag === "qch-trf-1080-111111111111", "inbound editor did not include its dedicated exit");
      assert(!document.querySelector('optgroup[label="出站"]'), "legacy outbound file group remains visible");
      input.value = input.value.replaceAll("1080","2080").replace('"a"','"VLESS-REALITY-443"');
      select.value = "0"; select.dispatchEvent(new Event("change"));
      assert(select.options[1].textContent === "VLESS-REALITY-443.json", "renaming the preset did not refresh its filename");
      document.querySelector('[data-live-intent="migrate-files"]').click(); await pause();
      assert(test.writes.length===1 && test.tasks.length===1,"migration did not save and submit exactly once");
      assert(test.tasks[0].action==="deploy","migration must use validated rollback-capable deployment");
      assert(test.tasks[0].expected_config_version===2,"migration must deploy the exact newly saved version");
      assert(test.writes[0].content.includes("9007199254740993") && test.writes[0].content.includes("2080"),"migration corrupted integer or discarded fragment draft");
      assert(test.confirmations.length===2,"migration presented duplicate confirmations");
    }
    await fixture(engine,false,true);
    assert(!document.querySelector('[data-live-intent="migrate-files"]'),"external service must not expose managed-file migration");
    if (["xray","sing-box"].includes(engine)) {
      const legacy = await fixture(engine,false,false,false,true);
      const button = document.querySelector('[data-live-intent="migrate-files"]');
      assert(button?.disabled,"legacy Agent must not offer unsupported migration");
      button.click(); await pause();
      assert(legacy.writes.length===0 && legacy.tasks.length===0,"legacy migration wrote data");
    }
  }
}
