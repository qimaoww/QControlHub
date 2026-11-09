import assert from "node:assert/strict";
import { createConfigMutations } from "../modules/config-mutations.js";
import { setStorageAccount } from "../modules/account-storage.js";

export async function run() {
  const originalStorage = globalThis.localStorage;
  const storage = new Map();
  globalThis.localStorage = {getItem:key=>storage.get(key), setItem:(key,value)=>storage.set(key,value)};
  setStorageAccount({user_id:"mutation-alice"});
  try {
    const state = {data:{}};
    const body = JSON.stringify({intent:"deploy", version:3, content:"private-draft"});
    const path = "/agents/node/configs/xray/source";
    let writes = 0, id, lost = true, release;
    const completion = new Promise(resolve=>{release=resolve;});
    const seen = [];
    const api = async (path, options = {}) => {
      if (options.method === "POST") {
        writes++;
        id = options.headers["X-Config-Task-ID"];
        assert.match(id, /^tsk_[0-9a-f]{16}$/);
        if (lost) throw TypeError("response lost after commit");
      }
      if (path.startsWith("/tasks/")) return {id, agent_id:"node", engine:"xray", action:"deploy", config_id:"cfg", config_version:4, status:"running"};
      if (path === "/agents/node/configs/xray") return {id:"cfg", version:4, content:"saved"};
      throw Error(`unexpected request ${path}`);
    };
    const make = () => createConfigMutations({api,state}, {onTask:result=>seen.push(result), waitForTerminal:()=>completion});
    const mutation = make();
    const result = await mutation.request(path, {method:"POST",body});
    assert.equal(writes, 1, "lost response must never resend a mutation");
    assert.equal(result.config.content, "saved");
    assert.equal(seen.length, 1);
    assert(![...storage.values()].join("").includes("private-draft"), "tracking storage must contain no configuration secrets");
    state.data = {}; // Browser reopen in the same account.
    await make().recover("node", "sing-box");
    assert.equal(seen.length, 1, "another instance recovered this task");
    await make().recover("node", "xray");
    assert.equal(seen.length, 2, "accepted task was lost on reopen");
    setStorageAccount({user_id:"mutation-bob"});
    state.data = {};
    await make().recover("node", "xray");
    assert.equal(seen.length, 2, "another user inherited the task");
    const delayedState = {data:{}};
    let available = false, delayedWrites = 0;
    const delayed = createConfigMutations({state:delayedState, api:async (url, options = {})=>{
      if (options.method === "POST") {
        delayedWrites++;
        id = options.headers["X-Config-Task-ID"];
        throw Object.assign(Error("incomplete response"), {status:200});
      }
      if (!available) throw Object.assign(Error("not found yet"), {status:404});
      return api(url, options);
    }}, {onTask:result=>seen.push(result), waitForTerminal:()=>completion});
    let interrupted;
    try { await delayed.request(path,{method:"POST",body}); } catch (error) { interrupted = error; }
    assert.equal(typeof interrupted?.recoverConfigSubmission, "function", "unknown save cannot be reconciled in the same page");
    available = true;
    const recovered = await interrupted.recoverConfigSubmission();
    assert.equal(recovered.config.version,4);
    assert.equal(delayedWrites,1,"same-page recovery replayed an interrupted write");
    let receiptInstance = "old-share", instanceWrites = 0, instanceCallbacks = 0;
    const instance = createConfigMutations({state:{data:{}}, api:async (url, options = {})=>{
      if (options.method === "POST") {
        instanceWrites++; id = options.headers["X-Config-Task-ID"];
        throw TypeError("response lost");
      }
      const value = await api(url, options);
      return url.startsWith("/tasks/") ? {...value, shared_instance:true, shared_traffic_id:receiptInstance} : value;
    }}, {onTask:()=>instanceCallbacks++, waitForTerminal:()=>completion});
    let unknownInstance;
    try { await instance.request(path,{method:"POST",body:JSON.stringify({...JSON.parse(body),expected_instance_id:"share"})}); }
    catch (error) { unknownInstance = error; }
    assert.equal(instanceCallbacks,0,"foreign instance receipt was applied");
    receiptInstance = "share";
    await instance.recover("node","xray","another-share");
    assert.equal(instanceCallbacks,0,"previous instance recovered into current page");
    await unknownInstance.recoverConfigSubmission();
    assert.equal(instanceCallbacks,1);
    assert.equal(instanceWrites,1,"instance reconciliation replayed a write");
    let correctionWrites = 0;
    const correction = createConfigMutations({state:{data:{}}, api:async (_url, options)=>{
      correctionWrites++;
      if (correctionWrites === 1) throw Object.assign(Error("invalid source"),{status:400});
      assert.equal(JSON.parse(options.body).content,"corrected");
      return {config:{id:"cfg",version:4},task:{id:options.headers["X-Config-Task-ID"],config_version:4,status:"succeeded"}};
    }}, {onTask:()=>{},waitForTerminal:()=>completion});
    await assert.rejects(correction.request(path,{method:"POST",body}),{status:400});
    await correction.request(path,{method:"POST",body:JSON.stringify({...JSON.parse(body),content:"corrected"})});
    assert.equal(correctionWrites,2,"business rejection blocked corrected content");
    const superseded = createConfigMutations({state:{data:{}}, api:async (url, options = {})=>{
      if (options.method === "POST") {
        id = options.headers["X-Config-Task-ID"];
        throw TypeError("response lost after commit");
      }
      const value = await api(url, options);
      return url.startsWith("/tasks/") ? value : {...value, version:5, content:"another saved edit"};
    }}, {onTask:()=>{}, waitForTerminal:()=>completion});
    await assert.rejects(superseded.request(path,{method:"POST",body}), error=>
      error.status === 409 && error.code === "config_submission_superseded" && error.message.includes("任务已提交"),
    "reconciliation returned a later edit as the submitted task's configuration");
    // Every task-producing editor uses the same read-only receipt recovery.
    for (const [endpoint, method] of [["server-inbounds","POST"],["outbounds","POST"],["management","PUT"],["fields/route","POST"]]) {
      let attempts=0, receiptID;
      const editor=createConfigMutations({state:{data:{}},api:async (url,options={})=>{
        if(options.method===method){attempts++;receiptID=options.headers["X-Config-Task-ID"];throw TypeError("committed response lost");}
        if(url.startsWith("/tasks/"))return {id:receiptID,agent_id:"node",engine:"xray",action:"deploy",config_id:"cfg",config_version:4,status:"succeeded"};
        return {id:"cfg",version:4};
      }},{onTask:()=>{},waitForTerminal:()=>completion});
      const recovered=await editor.request(`/agents/node/configs/xray/${endpoint}`,{method,body});
      assert.equal(recovered.config.version,4);
      assert.equal(attempts,1,`${endpoint} replayed a committed write`);
    }
    release({status:"succeeded"});
    // A response arriving after an account change cannot reach task callbacks
    // or the caller's subsequent save/deploy handlers.
    lost = false;
    const oldData = state.data;
    const late = createConfigMutations({state, api:async (_path, options)=>{
      id = options.headers["X-Config-Task-ID"];
      state.data = {};
      return {config:{id:"cfg",version:4}, task:{id}};
    }}, {onTask:()=>assert.fail("old account response applied"), waitForTerminal:()=>completion});
    await assert.rejects(late.request(path,{method:"POST",body}), {name:"AbortError"});
    assert.notEqual(state.data,oldData);
  } finally {
    setStorageAccount(null);
    globalThis.localStorage = originalStorage;
  }
}
