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
    release({status:"succeeded"});
    // A response arriving after an account change cannot call onTask.
    lost = false;
    const oldData = state.data;
    const late = createConfigMutations({state, api:async (_path, options)=>{
      id = options.headers["X-Config-Task-ID"];
      state.data = {};
      return {config:{id:"cfg",version:4}, task:{id}};
    }}, {onTask:()=>assert.fail("old account response applied"), waitForTerminal:()=>completion});
    await late.request(path,{method:"POST",body});
    assert.notEqual(state.data,oldData);
  } finally {
    setStorageAccount(null);
    globalThis.localStorage = originalStorage;
  }
}
