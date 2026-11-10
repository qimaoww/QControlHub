import { startAnalytics } from "./analytics/app.js";
import { accountStorage, setStorageAccount } from "./modules/account-storage.js";
import { nodeCardOrderKey } from "./modules/node-order.js";
import { subStoreSelectionPayload } from "./modules/substore-model.js";
import { testSubStoreCardLayout } from "./browser/substore-layout.mjs";

const assert = (value, message) => { if (!value) throw new Error(message); };
const esc = value => String(value ?? "").replace(/[&<>"']/g, char => ({
  "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;",
})[char]);
const waitFor = async (condition, message) => {
  const deadline = performance.now() + 3000;
  while (!condition()) {
    if (performance.now() > deadline) throw new Error(message);
    await new Promise(resolve => setTimeout(resolve, 10));
  }
};

async function configFixture({ writable = true, executable = false, installed = true, saved = true, owned = false } = {}) {
  const content = "mixed-port: 21001\n# alice-private-workspace\n";
  const agent = { id: "shared", name: "共享主机", status: "online", os: "linux", arch: "amd64",
    can_manage: owned,
    shared_allocation: owned ? undefined : { ports:[21001], limit_bytes:1024, used_bytes:0 },
    capabilities: ["mihomo"], features: ["managed-config-read-v1", "independent-egress-v1"],
    runtime: { mihomo: { installed, existing_config_available: true, existing_sources: [
      {id:"a".repeat(64),service:"private-host-source.service",config_path:"/srv/private-host/config.yaml",status:"active"},
    ] } } };
  const state = { route: "live-config", navigationEpoch: 1, session: { role: "user", user_id: "alice" },
    data: { liveAgent: agent.id, liveEngine: "mihomo", liveSources: {
      "shared|mihomo": { content: "mixed-port: 21002\n# other-user-runtime-secret\n" },
    } } };
  let workspace = saved ? { id: "cfg_alice", version: 1, name: "Alice", engine: "mihomo", content } : null;
  const records = { calls: [], writes: [], tasks: [], notifications: [] };
  const archives = [1, 2].map(index => ({ id: `archive_${index}`, version: 1, name: `方案 ${index}`,
    engine: "mihomo", content: `mixed-port: ${22000 + index}\n` }));
  const permissions = new Set(["agents.read", "agent-config.read", "configs.read", ...(writable ? ["agent-config.write", "configs.write"] : []), ...(executable ? ["tasks.execute"] : [])]);
  const api = async (path, options = {}) => {
    records.calls.push(path);
    if (path === "/agents") return [agent];
    if (path.endsWith("/workspace")) return { agent, config: workspace, deployment:{state:"pending",service_status:"unknown"} };
    if (path === "/configs") return archives;
    if (path.includes("/revisions")) return [];
    if (options.method === "PUT") {
      const input = JSON.parse(options.body);
      records.writes.push(input);
      workspace = { ...input, id: "cfg_alice", version: (workspace?.version || 0) + 1 };
      return workspace;
    }
    throw new Error(`unexpected configuration API: ${path}`);
  };
  permissions.add("tasks.read");
  if (owned && executable) permissions.add("host.manage");
  const session = {...state.session,username:"alice",permissions:[...permissions],csrf_token:"fixture"};
  const nativeFetch = globalThis.fetch;
  globalThis.fetch = async (url, request = {}) => {
    if (!String(url).startsWith("/api/v1")) return nativeFetch(url,request);
    const path = String(url).slice("/api/v1".length);
    const value = path === "/auth/session" ? session : await api(path,request);
    return new Response(JSON.stringify(value),{status:200,headers:{"Content-Type":"application/json"}});
  };
  const styles = document.createElement("link"); styles.rel="stylesheet";styles.href="/assets/analytics/theme.css";
  const loaded = new Promise((resolve,reject)=>{styles.onload=resolve;styles.onerror=reject;});document.head.append(styles);await loaded;
  const mount = async hash => {
    window.dispatchEvent(new Event("pagehide"));
    history.replaceState(null,"",hash); document.body.replaceChildren(Object.assign(document.createElement("div"),{id:"config-scope-test"}));
    startAnalytics({app:document.querySelector("#config-scope-test"),session});
    await waitFor(()=>document.querySelector(".a-main textarea[name=content],.a-main [aria-label=配置档案]")&&!document.querySelector(".a-main [aria-busy=true]"),"actual analytics workspace did not load");
  };
  const pages = {liveConfig:(source="")=>mount(`#live-config?agent=shared&engine=mihomo${source?`&source=${source}`:""}`),archiveConfigs:()=>mount("#archive-config")};
  await pages.liveConfig();
  const dispose = () => {window.dispatchEvent(new Event("pagehide"));document.querySelectorAll("dialog.a-dialog").forEach(box=>box.close());styles.remove();globalThis.fetch=nativeFetch;};
  return { state, pages, records, archives, dispose };
}

export async function testConfigScopeRuntime(preview = false) {
  const editor = () => document.querySelector('.a-main textarea[name="content"]');
  const form = () => editor().form;
  const button = (label,scope=document) => [...scope.querySelectorAll("button")].find(control=>control.textContent===label);
  if (preview) {await configFixture();return;}
  for (const options of [{}, {installed:false,saved:false}]) {
    const test = await configFixture(options);
    assert(editor() && !editor().readOnly,"private workspace must remain editable without task permission or an installed core");
    assert(!editor().value.includes("other-user-runtime-secret"),"private editor inherited a shared-host snapshot");
    assert(![...document.querySelector('.a-target-form [name="source"]').options].some(option=>option.value!=="personal"),"borrower can read shared-host configuration");
    assert(form().elements.intent.type==="hidden"&&form().elements.intent.value==="deploy"&&form().querySelector('[type=submit]').disabled,"user without execution permission was offered deployment");
    editor().value="mixed-port: 23001\n# saved-private-draft\n";editor().dispatchEvent(new Event("input",{bubbles:true}));form().requestSubmit();
    await new Promise(resolve=>setTimeout(resolve,30));
    assert(test.records.tasks.length===0&&test.records.writes.length===0,"user without execution permission submitted a save or task");
    assert(editor().value.includes("saved-private-draft")&&!form().textContent.includes("部署成功"),"blocked deployment lost draft or claimed success");test.dispose();
  }
  const shared=await configFixture({executable:true});
  assert(form().elements.intent.type==="hidden"&&form().elements.intent.value==="deploy"&&form().querySelector('[type=submit]').textContent==="保存并部署"&&!form().querySelector('[type=submit]').disabled,"authorized recipient cannot save and deploy its own configuration");
  assert(document.querySelector('.a-main').textContent.includes("本人端口 · 21001"),"shared workspace lacks recipient port scope");
  assert(document.querySelector('a[href="#settings-node-shared"]')?.textContent==="实例管理","shared workspace lacks instance management");
  assert(!button("读取托管配置")&&!button("读取系统配置"),"shared borrower was offered host snapshots");
  assert(!button("启动")&&!button("重启"),"lifecycle controls leaked into source editing");
  await shared.pages.liveConfig("import");
  assert([...document.querySelector('.a-target-form [name="source"]').options].every(option=>option.value==="personal"),"shared recipient can select host discovery through a direct link");
  assert(!document.querySelector('.a-main').textContent.includes("private-host-source")&&!document.querySelector('.a-main').textContent.includes("/srv/private-host"),"shared page exposes host discovery details");
  assert(!shared.records.tasks.length&&!shared.records.calls.includes("/tasks"),"shared page created a host discovery task");shared.dispose();
  const owner=await configFixture({owned:true,executable:true});
  assert([...document.querySelector('.a-target-form [name="source"]').options].some(option=>option.value==="managed")&&[...document.querySelector('.a-target-form [name="source"]').options].some(option=>option.value==="import"),"owner cannot explicitly select authorized host sources");
  assert(!owner.records.tasks.length&&!owner.records.calls.includes("/tasks"),"personal source auto-read a host snapshot");owner.dispose();
  const readonly=await configFixture({writable:false});assert(editor().readOnly&&!form().querySelector('button[type="submit"]'),"read-only workspace is writable");readonly.dispose();
  const archive=await configFixture();archive.state.data.archiveConfigId="foreign-stale-selection";await archive.pages.archiveConfigs();
  const rows=[...document.querySelectorAll('[aria-label="配置档案"] tbody tr')];
  assert(rows.length===2&&!document.querySelector('textarea[name="content"]'),"stale archive selection opened a foreign source");
  assert(!archive.records.calls.includes("/templates"),"archive reader requested unauthorized templates");
  button("查看与编辑",rows[1]).click();await waitFor(()=>document.querySelector('dialog textarea[name="content"]'),"second personal archive did not open");
  assert(document.querySelector('dialog textarea[name="content"]').value.includes("22002"),"archive selection loaded the wrong personal source");archive.dispose();
}

async function subStoreFixture(manage = true) {
  const state = { route: "substore-sync", navigationEpoch: 1,
    data: { agents: [{ id: "shared", labels: { region_code: "HK" } }, { id:"alpha" }, { id:"charlie" }] },
    session: { role: "user", user_id: "alice" } };
  setStorageAccount(state.session);
  accountStorage.setItem(nodeCardOrderKey, JSON.stringify(["shared", "charlie", "alpha"]));
  const targets = [
    { id: "url-group", display_name: "我的 URL 组", subscription_name: "Alice URL", sync_format: "url", sync_mode: "incremental" },
    { id: "mihomo-group", display_name: "我的 Mihomo 组", subscription_name: "Alice Mihomo", sync_format: "mihomo", sync_mode: "incremental" },
  ];
  const active = { agent_id: "shared", agent_name: "共享主机", agent_status: "online", config_id: "cfg_current",
    engine: "mihomo", profile_tag: "same-tag", protocol: "Shadowsocks 2022", port: 21001, default_name: "我的节点",
    available: true, addresses: [{ family: "ipv4", address: "198.51.100.10" }, { family: "ipv6", address: "2001:db8::10" }] };
  const passive = [
    { ...active, agent_id: "alpha", agent_name: "Alpha 主机", config_id: "cfg_alpha", profile_tag: "alpha-tag", default_name: "Alpha 主机 · alpha-tag" },
    { ...active, agent_id: "charlie", agent_name: "Charlie 主机", config_id: "cfg_charlie", engine: "ss-rust", profile_tag: "charlie-tag", default_name: "Charlie 节点" },
  ];
  const stale = { ...active, config_id: "cfg_previous", default_name: "失效的旧配置", available: false, addresses: [] };
  const selections = new Map([["url-group", [{ ...stale, custom_name: stale.default_name, selected: true }]], ["mihomo-group", []]]);
  const calls = [], test = {};
  const api = async (path, options = {}) => {
    const input = options.body ? JSON.parse(options.body) : null;
    calls.push({ path, method: options.method || "GET", input });
    if (path === "/auth/session") return session;
    if (path === "/agents") return state.data.agents;
    if (path === "/preferences/node-order") return {ids:["shared","charlie","alpha"],revision:1};
    if (path === "/substore-sync/run") {const target=targets.find(item=>item.id===input.target_id);target.last_synced_at="2026-11-06T00:00:00Z";return {node_count:(selections.get(target.id)||[]).reduce((n,p)=>n+(p.address_mode==="both"?2:1),0)};}
    if (path === "/agents/alpha/region") return { country_code: "SG" };
    if (path === "/agents/charlie/region") return { country_code: "US" };
    if (path === "/substore-sync/remote-targets") return [];
    if (path === "/substore-sync/selections") {
      if(test.selectionGate)await test.selectionGate;
      if(test.selectionFailure)throw Object.assign(new Error("同步选择保存暂不可用"),{status:503});
      selections.set(input.target_id, input.selections);
      return input.selections;
    }
    if (path.startsWith("/substore-sync/targets")) {
      const target = targets.find(item => path.endsWith(`/${item.id}`));
      if (target) {
        Object.assign(target, input);
        return { ...target };
      }
      const created = { ...input, id: `group-${targets.length}`, subscription_name: input.display_name };
      targets.push(created);
      selections.set(created.id, []);
      return created;
    }
    if (path.startsWith("/substore-sync")) {
      const id = new URL(path, location.origin).searchParams.get("target_id") || targets[0].id;
      const selected = selections.get(id) || [];
      const profiles = [active, ...passive, ...(selected.some(item => item.config_id === stale.config_id) ? [stale] : [])].map(profile => {
        const selection = selected.find(item => item.config_id === profile.config_id);
        return { ...profile, ...selection, selected: Boolean(selection) };
      });
      return structuredClone({ settings: { configured: true, endpoint_hint: "https://substore.example/••••••" },
        targets, target_id: id, profiles, selections: subStoreSelectionPayload(profiles) });
    }
    throw new Error(`unexpected Sub-Store API ${path}`);
  };
  const session={...state.session,username:"alice",csrf_token:"fixture",permissions:["client-access.read","agents.read",...(manage?["settings.manage"]:[])]};
  const nativeFetch=globalThis.fetch;
  globalThis.fetch=async(url,request={})=>{
    if(!String(url).startsWith("/api/v1"))return nativeFetch(url,request);
    if(String(url)==="/api/v1/tasks/failures"&&request.method==="POST") {
      assert(new Headers(request.headers).get("X-QControlHub-CSRF")===session.csrf_token,"failure retention missing CSRF");
      return new Response(JSON.stringify({id:"fixture-failure"}),{headers:{"Content-Type":"application/json"}});
    }
    try {
      const value=await api(String(url).slice("/api/v1".length),request);
      return new Response(JSON.stringify(value),{status:200,headers:{"Content-Type":"application/json"}});
    } catch(error) {if(!error.status)throw error;return new Response(JSON.stringify({error:error.message}),{status:error.status,headers:{"Content-Type":"application/json"}});}
  };
  const styles=document.createElement("link");styles.rel="stylesheet";styles.href="/assets/analytics/theme.css";
  const loaded=new Promise((resolve,reject)=>{styles.onload=resolve;styles.onerror=reject;});document.head.append(styles);await loaded;
  const render=async()=>{
    window.dispatchEvent(new Event("pagehide"));history.replaceState(null,"","#substore-sync");
    document.body.replaceChildren(Object.assign(document.createElement("div"),{id:"substore-scope-test"}));
    startAnalytics({app:document.querySelector("#substore-scope-test"),session});
    await waitFor(()=>document.querySelector('[aria-label=同步节点] [data-refresh-key]')&&!document.querySelector('.a-main [aria-busy=true]'),"analytics Sub-Store did not load");
  };
  await render();
  const dispose=()=>{window.dispatchEvent(new Event("pagehide"));document.querySelectorAll("dialog.a-dialog").forEach(box=>box.close());styles.remove();globalThis.fetch=nativeFetch;};
  return {state,render,targets,calls,active,selections,test,dispose};
}

export async function testSubStoreScopeRuntime(preview = false) {
  const fixture=await subStoreFixture();
  const button=(text,scope=document)=>[...scope.querySelectorAll("button")].find(control=>control.textContent===text);
  const row=id=>[...document.querySelectorAll("[aria-label=同步节点] [data-refresh-key]")].find(row=>JSON.parse(row.dataset.refreshKey)[3]===id);
  const target=()=>document.querySelector('.a-main [name="target"]');
  const change=(control,value)=>{control.value=value;control.dispatchEvent(new Event("change",{bubbles:true}));};
  const settled=()=>waitFor(()=>!document.querySelector('#substore-scope-test[aria-busy=true]'),"Sub-Store operation did not settle");
  const openSettings=async()=>{button("组设置").click();await waitFor(()=>document.querySelector('dialog[open] form [name="rename_remote"]'),"group editor did not open");return document.querySelector('dialog[open] form');};
  assert(document.querySelector('[data-substore-region="shared"] img[src="/api/v1/region-flags/hk"]'),"saved node flag missing");
  await waitFor(()=>document.querySelector('[data-substore-region="alpha"] img[src="/api/v1/region-flags/sg"]'),"automatic flag missing");
  assert(row("cfg_charlie").textContent.includes("Charlie 节点")&&row("cfg_charlie").textContent.includes("charlie-tag"),"SS-Rust client name or inbound identity missing");
  assert([...new Set([...document.querySelectorAll('[aria-label=同步节点] [data-refresh-key]')].map(row=>JSON.parse(row.dataset.refreshKey)[0]))].join(",")==="shared,charlie,alpha","saved node order was lost");
  if(preview){await openSettings();return;}
  testSubStoreCardLayout();
  const surface = row("cfg_current"), pick = surface.querySelector('[name="selected"]'), initial = pick.checked;
  surface.cells[1].click(); assert(pick.checked !== initial, "clicking the subscription node did not toggle selection");
  surface.focus(); surface.dispatchEvent(new KeyboardEvent('keydown', {key:' ',bubbles:true}));
  assert(pick.checked === initial, "keyboard selection did not restore the subscription choice");
  if (!pick.checked) surface.click();
  const editing = surface.querySelector('[name="name"]'); editing.click();
  assert(pick.checked && document.contains(editing), "editing a subscription name toggled its selection");
  if (!initial) surface.click();
  const unavailablePick = row("cfg_previous").querySelector('[name="selected"]'), unavailableBefore = unavailablePick.checked;
  row("cfg_previous").click(); assert(unavailablePick.checked === unavailableBefore, "unavailable subscription source was toggled");
  assert(!document.querySelector('.a-main h1'),"subscription page adds a duplicate title hero");
  assert(document.querySelector('.a-main').textContent.includes("https://substore.example/••••••"),"connection hint missing");
  button("连接设置").click();await waitFor(()=>document.querySelector('dialog [name="endpoint_url"]'),"connection settings did not open");
  const endpoint=document.querySelector('dialog [name="endpoint_url"]');
  assert(endpoint.type==="password"&&endpoint.value===""&&!endpoint.required,"saved endpoint secret was exposed or cannot be preserved");
  document.querySelector('dialog[open]').close();
  button("新建同步组").click();await waitFor(()=>document.querySelector('dialog [name="display_name"]'),"new group form did not open");
  assert(!document.querySelector('dialog [name="rename_remote"]'),"new group offers an unrelated remote rename");
  document.querySelector('dialog[open]').close();
  const search=document.querySelector('.a-main [name="search"]'),agent=document.querySelector('.a-main [name="agent"]');
  const readsBefore=fixture.calls.length;
  search.value="charlie-tag";search.dispatchEvent(new Event("input",{bubbles:true}));
  assert(row("cfg_charlie")&&!row("cfg_current"),"inbound keyword search failed");
  change(agent,"shared");assert(!row("cfg_charlie")&&!row("cfg_current"),"node and keyword filters do not compose");
  search.value="21001";search.dispatchEvent(new Event("input",{bubbles:true}));
  assert(row("cfg_current")&&!row("cfg_charlie"),"port search or node filter failed");
  assert(fixture.calls.length===readsBefore,"local filters triggered API reads");
  search.value="";search.dispatchEvent(new Event("input",{bubbles:true}));change(agent,"");
  assert(row("cfg_previous").querySelector('[name="selected"]').disabled,"stale source remains selectable");
  assert(button("立即同步").disabled,"stale selected source remains syncable");
  button("移除选择",row("cfg_previous")).click();button("保存所选节点").click();await settled();
  assert(!row("cfg_previous"),"stale selection was not removed");
  // Checkbox changes carry checked state, rather than their string value.
  const selected=row("cfg_current").querySelector('[name="selected"]');selected.checked=true;selected.dispatchEvent(new Event("change",{bubbles:true}));
  const name=row("cfg_current").querySelector('[name="name"]');name.value="我的双栈节点";name.dispatchEvent(new Event("input",{bubbles:true}));
  change(row("cfg_current").querySelector('[name="address"]'),"both");
  button("组设置").click();assert(!document.querySelector('dialog[open]'),"group editor can discard pending selection changes");
  fixture.test.selectionFailure=true;button("保存所选节点").click();await settled();
  assert(row("cfg_current").querySelector('[name="name"]').value==="我的双栈节点"&&row("cfg_current").querySelector('[name="selected"]').checked&&!button("保存所选节点").disabled,"save failure lost draft or prevented retry");
  fixture.test.selectionFailure=false;
  let finish;fixture.test.selectionGate=new Promise(resolve=>{finish=resolve;});
  const before=fixture.calls.filter(call=>call.path==="/substore-sync/selections").length;
  button("保存所选节点").click();await waitFor(()=>fixture.calls.filter(call=>call.path==="/substore-sync/selections").length===before+1,"selection retry not submitted");
  assert(row("cfg_current").querySelector('[name="selected"]').matches(":disabled"),"pending selection can still be edited");
  row("cfg_current").cells[1].click(); assert(row("cfg_current").querySelector('[name="selected"]').checked, "pending row click changed selection");
  button("保存所选节点").click();assert(fixture.calls.filter(call=>call.path==="/substore-sync/selections").length===before+1,"duplicate selection write");
  finish();fixture.test.selectionGate=null;await settled();
  const saved=fixture.calls.filter(call=>call.path==="/substore-sync/selections").at(-1).input.selections;
  assert(saved.length===1&&saved[0].config_id==="cfg_current"&&saved[0].address_mode==="both"&&saved[0].custom_name==="我的双栈节点","old/new same-tag sources were mixed or dual-stack choice was lost");
  assert(button("保存所选节点").disabled&&!button("立即同步").disabled,"saved selection still dirty or cannot sync");
  const unlinkCalls=fixture.calls.length;
  button("移除关联").click();await waitFor(()=>document.querySelector('dialog[open]'),"unlink confirmation did not open");
  assert(document.querySelector('dialog[open]').textContent.includes("Sub-Store 远端组会保留"),"unlink confirmation omits remote preservation");
  button("取消",document.querySelector('dialog[open]')).click();await settled();
  assert(fixture.calls.length===unlinkCalls&&target().value==="url-group","cancelling unlink changed the association");
  button("关联远端现有组").click();await settled();
  const remote=document.querySelector('dialog[open] [name="subscription_name"]');
  assert(remote&&remote.required&&button("更换关联组",document.querySelector('dialog[open]')),"existing remote group selector missing");
  document.querySelector('dialog[open]').close();
  let form=await openSettings();
  assert(form.elements.sync_format.value==="url","URL format was not restored");
  assert(!form.elements.rename_remote.checked,"editing panel name silently renames the remote group");
  assert([...form.elements.sync_mode.options].map(option=>option.value).join(",")==="incremental,managed","incremental or managed sync choice missing");
  assert(form.elements.sync_mode.value==="incremental","existing sync mode was not restored");
  assert(button("关联远端现有组")&&button("移除关联"),"remote group association or unlink entry missing");
  for(const control of [form.elements.sync_format,form.querySelector('button[type="submit"]')]) {const r=control.getBoundingClientRect();assert(r.left>=0&&r.right<=innerWidth&&r.bottom<=innerHeight,"group settings or save control clipped");}
  change(form.elements.sync_format,"mihomo");form.requestSubmit();await settled();
  assert(fixture.targets[0].sync_format==="mihomo"&&!document.querySelector('dialog[open]'),"Mihomo format did not persist");
  change(target(),"mihomo-group");await settled();assert(target().value==="mihomo-group","second group did not load");
  form=await openSettings();assert(form.elements.sync_format.value==="mihomo","per-group format was not restored");change(form.elements.sync_format,"url");form.requestSubmit();await settled();
  assert(fixture.targets[1].sync_format==="url"&&fixture.targets[0].sync_format==="mihomo","format edit affected another group");
  change(target(),"url-group");await settled();
  fixture.active.mihomo_error="Mihomo 不支持此安全选项，请选择 URL 格式";await fixture.render();
  assert(row("cfg_current").querySelector('[name="selected"]').disabled&&button("立即同步").disabled,"unsupported Mihomo security options remain syncable");
  assert(row("cfg_current").textContent.includes("请选择 URL"),"unsupported format reason hidden");fixture.dispose();
  const readonly=await subStoreFixture(false);
  assert(!button("组设置")&&!button("移除选择")&&!button("保存所选节点"),"read-only user can edit sync groups");
  assert([...document.querySelectorAll('[name="selected"],[name="name"],[name="address"]')].every(control=>control.disabled),"read-only controls enabled");
  assert(readonly.calls.every(call=>call.method==="GET"),"read-only rendering wrote settings");readonly.dispose();
}
