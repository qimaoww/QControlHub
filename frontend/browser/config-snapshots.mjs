import { requestConfigSnapshot } from "../modules/config-snapshot.js";
import { assert, pause, waitFor, createConfigFixture } from "./config-fixture.mjs";

const deferred = () => {
  let resolve;
  const promise = new Promise(done => { resolve = done; });
  return {promise, resolve};
};

// The same reader serves both themes. Check its cache provenance and bounded
// fallback directly, then exercise the native page's explicit read controls.
async function checkSnapshotReader() {
  for (const missing of [0, 1, 2]) {
    const reads = [];
    let snapshots = 0, polls = 0, error, result;
    const api = async (path, options = {}) => {
      if (path === "/tasks") {
        const body = JSON.parse(options.body); reads.push(body);
        return {id:`read-${reads.length}`, status:"succeeded", reused:Boolean(body.prefer_cached), finished_at:"2026-11-02T04:05:06Z"};
      }
      assert(path.endsWith("/config-snapshot"), "snapshot reader requested unrelated data");
      if (++snapshots <= missing) throw Object.assign(new Error("retired snapshot"), {status:404});
      return {content:'{"large":9007199254740993}'};
    };
    try {
      result = await requestConfigSnapshot({api, waitForTask:()=>{polls++;}}, {id:"node"}, "xray", "read-managed-config", {preferCached:true});
    } catch (failure) { error = failure; }
    assert(reads.length === (missing ? 2 : 1) && snapshots === reads.length, "cache retirement did not stop after one fresh fallback");
    assert(reads[0].prefer_cached === true && !polls, "cached terminal read lost preference or redundantly polled");
    if (missing) assert(reads[1].prefer_cached === undefined, "fallback reused retired cache");
    if (missing === 2) assert(error?.status === 404 && !result, "exhausted snapshot failure was hidden");
    else assert(!error && result.cached === !missing && result.content.includes("9007199254740993") && result.readAt === Date.parse("2026-11-02T04:05:06Z"), "cache provenance, original source or sample time was lost");
  }
  for (const retirement of ["account", "route", "epoch", "source"]) {
    const gate = deferred();
    let current = true, reads = 0, snapshots = 0;
    const newerRead = {reading:true, taskId:"newer-read"};
    let visibleRead = newerRead;
    const pending = requestConfigSnapshot({
      api:async path => {
        if (path === "/tasks") { reads++; return {id:"old-read", status:"succeeded"}; }
        snapshots++; await gate.promise;
        throw Object.assign(new Error("retired snapshot"), {status:404});
      },
      waitForTask:()=>{throw new Error("terminal read was polled");},
    }, {id:"node"}, "xray", "read-managed-config", {preferCached:true, isCurrent:()=>current});
    await waitFor(()=>snapshots === 1, "late snapshot did not start");
    current = false; gate.resolve();
    const result = await pending;
    if (result) visibleRead = result;
    assert(result === null && reads === 1 && visibleRead === newerRead, `${retirement} invalidation allowed fallback or replaced a newer read`);
  }
}

export async function testNativeConfigReadCacheRuntime() {
  await checkSnapshotReader();
  for (const engine of ["xray","sing-box","mihomo","ss-rust"]) for (const shared of [false,true]) {
    const test=await createConfigFixture(engine,{shared,deploymentState:"applied"});
    try {
      test.clickButton("读取托管配置");
      await waitFor(()=>test.snapshotReads===1&&!test.root.hasAttribute("aria-busy"),"unchanged reverse read did not finish");
      const row=test.row("first");
      assert(row.textContent.includes("启用")&&!row.textContent.includes("待部署"),"unchanged reverse read reset a deployed inbound");
      assert(!test.writes.length,"unchanged reverse read saved or deployed again");
    } finally {test.dispose();}
  }
  for (const engine of ["xray", "sing-box", "mihomo", "ss-rust"]) for (const imported of [false, true]) {
    for (const missing of [0, 1, 2]) {
      const test = await createConfigFixture(engine, {import:imported});
      try {
        test.missingSnapshots = missing;
        const form = test.root.querySelector('form:has([name=content])'), input = form.elements.content;
        const original = input.value, content = test.saved().content.replaceAll("21001", "23101");
        test.setAgentContent(content);
        const read = test.clickButton(imported ? "读取系统配置" : "读取托管配置");
        assert(read, "native configuration snapshot read control is missing");
        await waitFor(()=>test.snapshotReads === (missing ? 2 : 1) && !test.root.hasAttribute("aria-busy"), "native snapshot read did not finish");
        assert(test.readRequests.length === (missing ? 2 : 1) && test.readRequests.every(body=>body.prefer_cached === undefined && body.agent_id === "node" && body.engine === engine && body.action === (imported ? "read-config" : "read-managed-config")), "explicit read reused cache or targeted another source/engine");
        assert(!test.calls.some(call=>/^\/tasks\/read-\d+\?/.test(call.path)), "native terminal read redundantly polled");
        if (missing === 2) assert(input.value === original && document.body.textContent.includes("配置快照已失效"), "failed native read cleared source or hid error");
        else assert(input.value === content && input.defaultValue === content && form.textContent.includes("已读取"), "native read lost full source or baseline");
        assert(test.writes.length === 0 && test.saved().version === 1, "reading a snapshot saved or deployed configuration");
        if (!missing) {
          const reads = test.readRequests.length;
          read.click();
          await waitFor(()=>test.readRequests.length === reads + 1 && !test.root.hasAttribute("aria-busy"), "explicit reread did not finish");
          assert(test.readRequests.at(-1).prefer_cached === undefined, "explicit reread used display cache");
        }
      } finally { test.dispose(); }
    }
  }
  for (const changed of ["content", "name", "description"]) {
    const test = await createConfigFixture("xray");
    const gate = deferred(); test.snapshotGate = gate.promise;
    try {
      const form = test.root.querySelector('form:has([name=content])'), input = form.elements[changed];
      const source = form.elements.content.value;
      test.setAgentContent(source.replaceAll("21001", "23101"));
      test.clickButton("读取托管配置");
      await waitFor(()=>test.snapshotReads === 1, "native delayed snapshot did not start");
      input.value += changed === "content" ? "\n " : "（未保存）";
      input.dispatchEvent(new Event("input", {bubbles:true})); input.focus(); input.setSelectionRange(2, 7);
      const draft = input.value;
      const selectionEnd = input.selectionEnd;
      gate.resolve();
      await waitFor(()=>!test.root.hasAttribute("aria-busy"), "draft-protected snapshot did not release read control");
      assert(input.value === draft && document.activeElement === input && input.selectionStart === 2 && input.selectionEnd === selectionEnd, `${changed}: snapshot response overwrote or interrupted a newer draft`);
      assert(form.elements.content.value === (changed === "content" ? draft : source) && document.body.textContent.includes("读取期间配置已修改"), "draft conflict was not explained or source was replaced");
      const reset = [...test.root.querySelectorAll("button")].find(button=>button.textContent === "重置修改");
      assert(reset?.disabled === false && !test.writes.length, "snapshot read lost dirty state or wrote configuration");
    } finally { gate.resolve(); test.dispose(); }
  }
  for (const options of [{readonly:true}, {noTasks:true}, {offline:true}]) {
    const test = await createConfigFixture("xray", options);
    try { assert(!test.clickButton("读取托管配置") && !test.readRequests.length, "unauthorized or offline account can read host configuration"); }
    finally { test.dispose(); }
  }
  {
    const test = await createConfigFixture("xray");
    try {
      const form = test.root.querySelector('form:has([name=content])');
      form.elements.name.value += "（未保存）";
      form.elements.name.dispatchEvent(new Event("input", {bubbles:true}));
      const name = form.elements.name.value;
      test.clickButton("读取托管配置");
      await waitFor(()=>test.snapshotReads === 1 && !test.root.hasAttribute("aria-busy"), "read with an existing name draft did not finish");
      const reset = [...test.root.querySelectorAll("button")].find(button=>button.textContent === "重置修改");
      assert(form.elements.name.value === name && reset?.disabled === false, "successful source read cleared a separate name draft's dirty state");
      const confirmations = test.confirmations.length;
      location.hash = "#tasks";
      await waitFor(()=>test.confirmations.length > confirmations, "successful source read removed unsaved navigation protection");
      await pause();
      assert(form.isConnected && form.elements.name.value === name && !test.writes.length, "canceling navigation lost the retained name draft");
    } finally { test.dispose(); }
  }
  const test = await createConfigFixture("xray"), gate = deferred();
  try {
    test.snapshotGate = gate.promise; test.missingSnapshots = 1;
    test.clickButton("读取托管配置");
    await waitFor(()=>test.snapshotReads === 1, "abandoned native snapshot did not start");
    window.dispatchEvent(new Event("pagehide")); gate.resolve(); await pause();
    assert(test.readRequests.length === 1 && !test.writes.length, "disposed native page created a fallback task");
  } finally { gate.resolve(); test.dispose(); }
}
