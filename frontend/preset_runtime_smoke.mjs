import assert from "node:assert/strict";
import {createPresetReads, renderPresetIdentity} from "./modules/preset-runtime.js";
import {createTaskMonitor} from "./modules/task-monitor.js";

const deferred = () => {
  let resolve, reject;
  return {promise:new Promise((yes,no) => {resolve=yes;reject=no;}),resolve,reject};
};
const reads = createPresetReads(), workspace = {}, gate = deferred();
let count = 0;
const read = () => {count++;return gate.promise;};
const entry = reads(workspace,"field",read);
for (let i=0;i<100;i++) assert.equal(reads(workspace,"field",read),entry);
await Promise.resolve();
assert.equal(count,1,"rapid switches must share the in-flight read");
gate.resolve({version:1,fragment:"draft-baseline"});
await entry.promise;
assert.equal(reads(workspace,"field",read).value.fragment,"draft-baseline","switching back must render without a loading flash");
await reads({},"field",read).promise;
assert.equal(count,2,"a new workspace revision must not reuse old fields");
await assert.rejects(reads(workspace,"failed",() => {throw Error("offline");}).promise,/offline/);
assert.equal(await reads(workspace,"failed",() => "recovered").promise,"recovered","failed reads must be retryable");

const markup = '<a href="#identity" data-builder-step="identity"><b>02</b><strong>认证</strong></a><section class="builder-section" id="identity"><header>认证</header><div><input name="credential"></div></section><section id="security"></section>';
const options = '<details><input name="sudoku_client_key"></details>';
const rendered = renderPresetIdentity(markup,options);
assert.ok(rendered.includes(`<input name="credential">${options}</div></section>`));
assert.equal(rendered.split('name="sudoku_client_key"').length,2,"options must be in the render tree exactly once");
assert.ok(renderPresetIdentity(markup,'<input value="$&$`$\'">').includes('<input value="$&$`$\'">'),"passwords must not be interpreted as string replacement tokens");
const forwarded = renderPresetIdentity(markup,options,'<section id="target">target</section>');
assert.ok(forwarded.includes('data-builder-step="target"'));
assert.ok(!forwarded.includes('name="credential"') && !forwarded.includes("sudoku_client_key"));

let owner = {}, polls = 0;
const steps = [], pending = deferred();
const monitor = createTaskMonitor({session:()=>owner,read:async () => {polls++;return pending.promise;},sleep:async ms=>steps.push(ms)});
const first = monitor("task"), same = monitor("task");
assert.equal(first,same,"preset and live recovery must share one poller");
pending.resolve({id:"task",status:"succeeded"});
assert.equal((await first).status,"succeeded");
assert.equal(polls,1);

const progress = [], delays = [];
let attempt = 0;
const retry = createTaskMonitor({session:()=>owner,hidden:()=>false,sleep:async ms=>delays.push(ms),read:async () => {
  attempt++;
  if (attempt===1) throw new DOMException("route changed","AbortError");
  if (attempt===2) throw new TypeError("network unavailable");
  return {status:attempt===3?"running":"succeeded"};
}});
assert.equal((await retry("retry",(task,error)=>progress.push(task?.status || error.name))).status,"succeeded");
assert.deepEqual(progress,["AbortError","TypeError","running"]);
assert.deepEqual(delays,[1000,2000,500],"temporary failures back off and recover responsively");
for(const status of [401,403,404]) {
  let calls=0;
  const denied=createTaskMonitor({session:()=>owner,read:()=>{calls++;throw Object.assign(Error("denied"),{status});},sleep:async()=>{}});
  await assert.rejects(denied("denied"),/denied/);
  assert.equal(calls,1,"hard permission/deletion errors must not loop");
}
const logoutGate=deferred();
const logout=createTaskMonitor({session:()=>owner,read:()=>logoutGate.promise,sleep:async()=>{}});
const oldSession=logout("logout");
owner={};logoutGate.resolve({status:"succeeded"});
await assert.rejects(oldSession,{name:"AbortError"},"old login results must not affect a new session");
let slowCalls=0;
const bounded=createTaskMonitor({session:()=>owner,attempts:3,hidden:()=>true,read:async()=>{slowCalls++;return {status:"running"};},sleep:async ms=>assert.equal(ms,5000)});
await assert.rejects(bounded("timeout"),/超时/);
assert.equal(slowCalls,3);
console.log("Preset reads, render tree and task monitor smoke passed");
