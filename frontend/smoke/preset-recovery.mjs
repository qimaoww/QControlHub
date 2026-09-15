import assert from "node:assert/strict";

import { installConfigPages } from "../modules/configs.js";

export async function testSamePageRecovery({ FakeForm, AGENT_ID, ENGINE, makeAgent, workspaceWithConfig, buildContext, installForms }) {
  // Failed same-page refreshes must preserve the draft and rebind controls
  // to the new route epoch. A lost save response must require reconciliation.
  {
    const state={route:"agent-config",navigationEpoch:1,data:{agents:[makeAgent()],agentId:AGENT_ID,engine:ENGINE,inboundTag:"old-inbound"}};
    let failRead=false, failSave=false, savedVersion=1, allowDiscard=true;
    const planForm=new FakeForm({operation:"modify",tag:"old-inbound",credential:"saved"});
    const fieldForm=new FakeForm({mutation:"modify",fragment:"{}"});
    const {ctx,markup}=buildContext(state,{
      "GET /agents/test-agent/configs/xray/workspace":()=>{if(failRead)throw Error("workspace unavailable");return workspaceWithConfig({version:savedVersion});},
      "GET /agents/test-agent/configs/xray/fields/log":{present:true,fragment:"{}",version:1},
      "POST /agents/test-agent/configs/xray/fields/log":()=>{if(failSave)throw TypeError("response lost");return {config:{version:2},task:{}};},
    });
    ctx.confirmAction=async()=>allowDiscard;
    const pages=installForms(ctx,{"#server-plan-form":planForm,"#field-form":fieldForm});
    await pages.agentConfig();
    planForm.elements.namedItem("credential").value="retained-draft";
    state.navigationEpoch++;failRead=true;
    await assert.rejects(pages.agentConfig(),/workspace unavailable/);
    assert.equal(planForm.inert,false,"refresh error froze the old form");
    assert.equal(state.data.presetOperation.reload,true);
    assert.equal(pages.presetHasUnsavedChanges(),true);
    assert.equal(planForm.elements.namedItem("credential").value,"retained-draft");
    failRead=false;await pages.agentConfig();
    assert.equal(state.data.presetOperation.reload,false,"successful recovery failed to release submission lock");
    failSave=true;
    await fieldForm.dispatchSubmit({fieldIntent:"deploy"});
    assert.equal(state.data.presetOperation.reload,true,"unknown commit outcome allowed blind resubmission");
    assert.ok(state.data.presetOperation.message.includes("未能确认保存结果"));
    savedVersion=2;allowDiscard=false;
    await pages.agentConfig();
    assert.equal(state.data.presetOperation.reload,true,"canceling a version replacement must keep the old draft locked against stale saves");
    assert.ok(markup().includes("当前 v1"),"canceling external changes must render the old revision rather than hide its draft");
    assert.equal(planForm.elements.namedItem("credential").value,"retained-draft");
    allowDiscard=true;
    await pages.agentConfig();
    assert.equal(state.data.presetOperation.reload,false,"confirmed new revision did not recover the editor");
    assert.ok(markup().includes("当前 v2"));
  }

}

export async function testRapidNavigation({ FakeForm, deferred, AGENT_ID, makeAgent, workspaceWithConfig, emptyWorkspace, buildContext, installForms, drain }) {
  // Rapid navigation uses the latest click, shares reads and restores the
  // visible editor on failure. This is a Node event harness, not a browser.
  {
    const agent = {...makeAgent(), capabilities:["xray","mihomo","sing-box"],runtime:{xray:{installed:true},mihomo:{installed:true},"sing-box":{installed:true}}};
    const state = {route:"agent-config",navigationEpoch:1,data:{agentId:AGENT_ID,engine:"xray",inboundTag:"old-inbound"}};
    const initial = {...workspaceWithConfig(),agent};
    initial.catalog.fields.push({key:"dns",label:"DNS",kind:"object"});
    const logGate = deferred(), engineGate = deferred();
    let failEngine = false;
    const fields = ["log","dns"].map(configField=>({dataset:{configField}}));
    const engineLinks = ["xray","mihomo","sing-box"].map(engineSelect=>({dataset:{engineSelect}}));
    const planForm = new FakeForm({operation:"modify",tag:"old-inbound",credential:"unchanged"});
    const {ctx,apiLog,markup} = buildContext(state,{
      "GET /agents/test-agent/configs/xray/workspace":initial,
      "GET /agents/test-agent/configs/xray/fields/log":()=>logGate.promise,
      "GET /agents/test-agent/configs/xray/fields/dns":{present:true,fragment:"{}",version:1},
      "GET /agents/test-agent/configs/mihomo/workspace":()=>failEngine?Promise.reject(Error("switch failed")):engineGate.promise,
      "GET /agents/test-agent/configs/sing-box/workspace":{...emptyWorkspace,agent},
      "POST /agents/test-agent/configs/sing-box/plans":{tag:"sing-box-new",protocol:"ss2022",port:23001},
    });
    const pages = installForms(ctx,{"#server-plan-form":planForm},{"[data-config-field]":fields,"[data-engine-select]":engineLinks});
    await pages.agentConfig();
    assert.ok(!apiLog.some(call=>call.path==="/agents"),"a direct workspace load must not read the whole fleet");
    assert.equal(state.data.agents,undefined,"single node workspace must not masquerade as a complete fleet cache");
    planForm.elements.namedItem("credential").value="unsaved";
    fields[1].onclick({preventDefault(){}});await drain();
    fields[0].onclick({preventDefault(){}});await drain();
    logGate.resolve({present:true,fragment:"{}",version:1});await drain();
    assert.equal(apiLog.filter(call=>call.path.endsWith("/fields/log")).length,1,"field switches duplicated the slow read");
    assert.equal(planForm.elements.namedItem("credential").value,"unsaved","field navigation lost the primary draft");
    const before = apiLog.length;
    fields[0].onclick({preventDefault(){}});await drain();
    assert.equal(apiLog.length,before,"clicking the selected field should do nothing");
    engineLinks[1].onclick({preventDefault(){}});
    engineLinks[2].onclick({preventDefault(){}});
    await drain();
    assert.equal(state.data.engine,"sing-box","rapid engine switch dropped the final click");
    engineGate.resolve({...emptyWorkspace,agent});await drain();
    assert.equal(state.data.engine,"sing-box","late engine response overwrote the latest selection");
    assert.ok(markup().includes("sing-box-new"));
    failEngine=true;
    engineLinks[1].onclick({preventDefault(){}});await drain();
    assert.equal(state.data.engine,"sing-box","failed switch did not restore the prior engine");
    assert.equal(planForm.inert,false,"failed switch left the editor locked");
    const oldMarkup=markup(), stale=deferred();
    const oldAPI=ctx.api;
    ctx.api = async ()=>stale.promise;
    // A separate installer captures the delayed reader, like a new route.
    const latePages=installConfigPages(ctx);
    const late=latePages.agentConfig();
    state.navigationEpoch++;
    stale.resolve({...initial,agent});await late;
    assert.equal(markup(),oldMarkup,"a result from an older route epoch rendered after returning");
    ctx.api=oldAPI;
  }

}
