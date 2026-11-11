import { readableConfigSource } from '../modules/code-format.js';
import { startAnalytics } from "../analytics/app.js";
import { assert, delay, waitFor } from "./assertions.mjs";

export async function testEditedRetryDialog(preview = false) {
  const nativeFetch = globalThis.fetch;
  const task = { id: "failed", agent_id: "node", engine: "sing-box", action: "deploy", config_id: "cfg", config_version: 2, status: "failed", attempt:1, error: "模拟部署失败\n" + "诊断内容，保留草稿后可重新校验。".repeat(80), created_at: "2026-11-04T10:00:00Z" };
  const originalTask = structuredClone(task);
  const config = { id: "cfg", name: "重试测试配置", agent_id: "node", engine: "sing-box", version: 5, content: '{"inbounds":[]}', updated_at: "2026-11-04T11:00:00Z" };
  const gate = {};
  gate.promise = new Promise(resolve => { gate.resolve = resolve; });
  let attempts = 0, reads = 0, session, lastPayload;
  const ordinaryRetries = [];
  globalThis.fetch = async (url, options = {}) => {
    if (!String(url).startsWith("/api/v1")) return nativeFetch(url, options);
    const path = new URL(String(url),location.href).pathname.slice(7), method = options.method || "GET";
    let value;
    if (path === "/auth/session") value = session;
    else if (path === "/tasks") { reads++; value = [task]; }
    else if (path === "/tasks/failures" && method === "POST") value = {id:"fixture-failure"};
    else if (path.endsWith("/diagnostics")) value = {source:"fixture",output:path.includes("/retry/") ? "部署完成" : "",error:path.includes("/retry/") ? "" : task.error};
    else if (path === "/tasks/failed") value = task;
    else if (path === "/tasks/failed/retry" && method === 'POST') {
      assert.equal(new Headers(options.headers).get('X-QControlHub-CSRF'), 'retry-fixture', 'ordinary retry missing CSRF');
      ordinaryRetries.push(JSON.parse(options.body));
      value = { ...task, id: 'ordinary-retry' };
    }
    else if (path === '/tasks/ordinary-retry') value = { ...task, id: 'ordinary-retry', status: 'succeeded', error: '' };
    else if (path === "/tasks/failed/retry-editor") value = { task, config };
    else if (path === "/tasks/failed/edit-retry" && method === "POST") {
      assert.equal(new Headers(options.headers).get("X-QControlHub-CSRF"), "retry-fixture", "retry missing CSRF");
      attempts++; lastPayload = JSON.parse(options.body);
      assert.equal(lastPayload.expected_version, 5, "retry submitted stale configuration version");
      assert.ok(lastPayload.content.includes('"log"'), "retry lost submitted source changes");
      if (attempts === 1) return new Response(JSON.stringify({error:"模拟配置校验失败"}), {status:400,headers:{"Content-Type":"application/json"}});
      await gate.promise;
      value = { config: {...config,version:6}, task: { ...task, id: "retry", config_version: 6, status: "pending" } };
    } else if (path === "/tasks/retry") value = { ...task, id: "retry", status: "succeeded", error: "", output: "部署完成" };
    else throw new Error(`Unexpected task retry API ${method} ${path}`);
    return new Response(JSON.stringify(value),{headers:{"Content-Type":"application/json"}});
  };
  const styles = Object.assign(document.createElement("link"), {rel:"stylesheet",href:"/assets/analytics/theme.css"});
  const loaded = new Promise((resolve,reject)=>{styles.onload=resolve;styles.onerror=reject;});document.head.append(styles);await loaded;
  const button = (label,scope=document)=>[...scope.querySelectorAll("button")].find(control=>control.textContent===label);
  const box = title=>[...document.querySelectorAll("dialog[open]")].find(dialog=>dialog.querySelector("h2")?.textContent===title);
  const settled = async()=>{await waitFor(()=>!document.querySelector('#retry-test[aria-busy="true"]'),"retry stayed pending");await delay(15);};
  const mount = async(permissions=["tasks.read","tasks.execute","configs.write"])=>{
    window.dispatchEvent(new Event("pagehide"));document.querySelectorAll("dialog").forEach(dialog=>dialog.close());
    session={user_id:"alice",username:"alice",role:"user",csrf_token:"retry-fixture",permissions};
    history.replaceState(null,"","#tasks");document.body.replaceChildren(Object.assign(document.createElement("div"),{id:"retry-test"}));
    startAnalytics({app:document.querySelector("#retry-test"),session});
    await waitFor(()=>document.querySelector('[aria-label="任务"] tbody tr'),"actual task list did not load");await settled();
  };
  const open = async()=>{button("编辑后重试").click();await waitFor(()=>box("编辑后重试"),"retry editor missing");await settled();return box("编辑后重试");};
  const confirm = async(accept)=>{
    const confirmation=await waitFor(()=>box("保存并重试"),"retry confirmation missing");button(accept?"保存并重试":"取消",confirmation).click();await waitFor(()=>!confirmation.open,"retry confirmation did not close");
  };
  await mount();
  if(preview){await open();return;}
  try{
    for (const status of ['failed', 'canceled']) for (const config_version of [undefined, null, 0]) {
      Object.assign(task, originalTask, { status, config_version });
      await mount();
      assert.equal(document.querySelector('[data-retry]'), null, 'historical task offers an unbound ordinary retry');
      assert.ok(document.querySelector('[aria-label="任务"]').textContent.includes('缺少配置历史关联'), 'historical task lacks association status');
      assert.ok(button('编辑后重试'), 'historical task lost its editable recovery path');
      button('详情').click();
      const detail = await waitFor(() => box('任务详情') && !box('任务详情').querySelector('[aria-busy=true]') && box('任务详情'), 'historical task detail missing');
      await waitFor(() => !detail.querySelector('[aria-busy=true]'), 'task detail data did not finish');
      assert.ok(detail.textContent.includes('缺少配置历史关联') && detail.textContent.includes('编辑后重试或重新部署'), 'historical detail lost recovery guidance');
      assert.ok(!/vundefined|vnull|版本 0/.test(detail.textContent), 'historical task exposes fabricated revisions');
      await settled(); detail.querySelector('[aria-label="关闭"]').click();
    }
    Object.assign(task, originalTask, { config_version: 3 }); await mount();
    assert.ok(button('重试配置') && !document.querySelector('[aria-label="任务"]').textContent.includes('缺少配置历史关联'), 'versioned configuration retry is unavailable');
    assert.equal(ordinaryRetries.length, 0, 'historical task submitted an ordinary retry');
    button('重试配置').click();
    let retryConfirmation = await waitFor(() => box('重试任务'), 'configuration retry confirmation missing');
    button('重试任务', retryConfirmation).click();
    await waitFor(() => box('任务详情') && !box('任务详情').querySelector('[aria-busy=true]') && box('任务详情'), 'configuration retry receipt missing'); await settled();
    assert.equal(ordinaryRetries.length, 1); assert.equal(ordinaryRetries[0].expected_config_version, 3, 'ordinary retry lost its exact configuration association');
    box('任务详情').querySelector('[aria-label="关闭"]').click();
    Object.assign(task, originalTask, { action: 'status', config_id: '', config_version: undefined }); await mount();
    assert.ok(button('重试任务') && !button('编辑后重试'), 'operational retry incorrectly requires configuration history');
    button('重试任务').click(); retryConfirmation = await waitFor(() => box('重试任务'), 'operational retry confirmation missing');
    button('重试任务', retryConfirmation).click();
    await waitFor(() => box('任务详情') && !box('任务详情').querySelector('[aria-busy=true]') && box('任务详情'), 'operational retry receipt missing'); await settled();
    assert.equal(ordinaryRetries.length, 2); assert.equal(JSON.stringify(ordinaryRetries[1]), '{}', 'operational retry fabricated configuration history');
    box('任务详情').querySelector('[aria-label="关闭"]').click();
    Object.assign(task, originalTask, { config_version: undefined, error: 'Caddy rollback failed token=fixture-secret' }); await mount();
    button('详情').click();
    const rollback = await waitFor(() => box('任务详情') && !box('任务详情').querySelector('[aria-busy=true]') && box('任务详情'), 'rollback task detail missing');
    assert.ok(rollback.textContent.includes('回滚失败：需检查节点最终服务与路由状态') && rollback.textContent.includes('缺少配置历史关联'), 'rollback guidance or missing-history recovery was discarded');
    assert.ok(!rollback.textContent.includes('fixture-secret'), 'task diagnostic exposes credentials');
    await settled(); rollback.querySelector('[aria-label="关闭"]').click();
    Object.assign(task, originalTask); await mount();
    let dialog=await open(),form=dialog.querySelector("form"),source=form.elements.content;
    assert.ok(dialog.textContent.includes("当前配置")&&dialog.textContent.includes(config.name)&&dialog.textContent.includes("sing-box"),"retry lacks configuration and engine identity");
    assert.ok(!/当前 v5|原任务 v2|版本 5/.test(dialog.textContent),"retry exposes internal revisions");
    const failure=dialog.querySelector('[role="alert"]'),bounds=source.getBoundingClientRect();
    assert.ok(failure.getBoundingClientRect().width>=bounds.width*.9,"failure is squeezed beside source");
    assert.ok(failure.clientHeight<=Math.min(280,innerHeight*.36)+1,"long failure pushes source out of the task dialog");
    assert.ok(button("格式化源码",form).getBoundingClientRect().bottom<=bounds.top,"source tools are below editor");
    assert.equal(source.getAttribute("style"),null,"source uses inline styling");
    source.value="invalid JSON";source.dispatchEvent(new Event("input",{bubbles:true}));
    assert.ok(form.querySelector('[type="submit"]').disabled&&attempts===0,"invalid retry source can submit");
    source.value='{"inbounds":[],"log":{"level":"info"},"exact":9007199254740993}';source.dispatchEvent(new Event("input",{bubbles:true}));button("格式化源码",form).click();
    assert.ok(source.value.includes("\n")&&source.value.includes("9007199254740993")&&!form.querySelector('[type="submit"]').disabled,"format corrupts source or keeps save disabled");
    button("重置源码",form).click();assert.equal(source.value,readableConfigSource(config.content,config.engine),"reset did not restore saved source");
    source.value='{"inbounds":[],"log":{"level":"info"}}';source.dispatchEvent(new Event("input",{bubbles:true}));
    dialog.querySelector('[aria-label="关闭"]').click();const discard=await waitFor(()=>box("放弃更改"),"dirty close has no confirmation");button("取消",discard).click();assert.ok(dialog.open,"cancel discarded retry draft");
    form.requestSubmit();await confirm(false);await settled();assert.equal(attempts,0,"canceled retry wrote configuration");assert.ok(dialog.open&&source.value.includes('"log"'),"canceled retry lost draft");
    form.requestSubmit();await confirm(true);await settled();assert.equal(attempts,1);assert.ok(dialog.textContent.includes("模拟配置校验失败"),"failure has no inline feedback");assert.ok(source.value.includes('"log"'),"failed retry lost source");
    // Refreshing the underlying task query must leave the independently mounted editor intact.
    const before=reads,input=document.querySelector('.a-main [name="agent_id"]');input.value="node";input.dispatchEvent(new Event("input",{bubbles:true}));input.dispatchEvent(new Event("change",{bubbles:true}));
    source.focus();source.setSelectionRange(2,8);await waitFor(()=>reads>before,"task filter did not reload list");await delay(50);
    assert.ok(dialog.open&&dialog.querySelector('textarea')===source&&document.activeElement===source,"task filtering remounted or blurred retry editor");assert.equal(source.selectionStart,2);assert.equal(source.selectionEnd,8);
    form.requestSubmit();await confirm(true);await waitFor(()=>attempts===2,"second retry did not reach API");
    assert.ok(form.querySelector('fieldset').disabled&&button("格式化源码",form).disabled&&button("重置源码",form).disabled,"pending source tools remain editable");
    const submitted=source.value;form.requestSubmit();button("格式化源码",form).click();source.dispatchEvent(new KeyboardEvent("keydown",{key:"Tab",bubbles:true,cancelable:true}));
    dialog.querySelector('[aria-label="关闭"]').click();dialog.dispatchEvent(new Event("cancel",{cancelable:true}));
    assert.ok(dialog.open&&attempts===2&&source.value===submitted,"pending retry closed, duplicated or mutated");
    assert.equal(lastPayload.content,submitted,"retry payload differs from accepted draft");
    gate.resolve();await waitFor(()=>!dialog.open&&box("任务详情"),"successful retry has no task receipt");await settled();
    assert.ok(box("任务详情").textContent.includes("成功")&&box("任务详情").textContent.includes("部署完成"),"retry result does not follow actual task");box("任务详情").close();
    dialog=await open();location.hash="#themes";await waitFor(()=>!dialog.open,"navigation leaked retry editor");
    for(const permissions of [["tasks.read"],["tasks.read","configs.write"],["tasks.read","tasks.execute"]]){
      await mount(permissions);assert.equal(button("编辑后重试"),undefined,"retry bypasses execute or configuration-write permission");
      assert.equal(Boolean(button('重试配置')), permissions.includes('tasks.execute'), 'ordinary retry bypasses task execution permission');
      task.config_version = undefined; await mount(permissions);
      assert.equal(button('重试配置'), undefined, 'permission combination allows an unbound historical retry');
      Object.assign(task, originalTask);
    }
    assert.ok(document.documentElement.scrollWidth<=innerWidth+1,"task layout exceeds viewport");
  }finally{gate.resolve();window.dispatchEvent(new Event("pagehide"));document.querySelectorAll("dialog").forEach(dialog=>dialog.close());globalThis.fetch=nativeFetch;styles.remove();}
}
