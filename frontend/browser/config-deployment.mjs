import { assert, pause, waitFor, createConfigFixture } from "./config-fixture.mjs";

const failureButton = test => test.row("second")?.querySelector('button[aria-label*="部署失败"]');

export async function testInboundDeploymentRuntime() {
  await testSupersededDeploymentReceipts();
  for (const engine of ["xray", "sing-box", "mihomo", "ss-rust"]) for (const shared of [false, true]) {
    const test = await createConfigFixture(engine, {shared});
    test.confirm = true; test.taskStatus = "failed";
    test.taskError = "部署失败：端口已被其他服务占用，请检查监听地址。";
    test.taskOutput = "部署中止，已保留原运行配置。";
    try {
      test.select("second"); test.click("modify");
      await waitFor(()=>test.topDialog()?.querySelector('form [name=port]'), "deployment editor did not open");
      const form = test.topDialog().querySelector("form");
      assert(form.elements.intent.value === "deploy" && !form.querySelector('select[name=intent]') && form.querySelector('[type=submit]').textContent === "保存并部署", "editor retained validation-only/save-only choices");
      test.submit("deploy"); await test.settle(1);
      assert(test.sawTaskDetails, "saving no longer automatically opens task details");
      await waitFor(()=>failureButton(test) && !failureButton(test).hidden, "failed deployment was not shown in the inbound status column");
      assert(![...test.root.querySelectorAll("button")].some(button=>button.textContent === "部署任务详情") && !test.root.textContent.includes("保存并校验"), "removed task-details or validation entry remains");
      const region = test.root.querySelector("[data-deployment-status]");
      assert(region.textContent.includes("内核运行") && region.closest(".a-card-head")?.querySelector("h2")?.textContent === "入站配置", "runtime state is detached from the inbound heading");
      const row = test.row("second");
      assert(row.cells[0].textContent === "21002" && row.cells[3].contains(failureButton(test)) && row.cells[3].innerText === "部署失败", "failed deployment is outside the status column or still claims enabled");
      assert(failureButton(test).getBoundingClientRect().width < 140, "failure button stretches across the status column");
      failureButton(test).click();
      await waitFor(()=>test.topDialog()?.textContent.includes(test.taskError), "clicking failed port did not read its actual failure reason");
      assert(test.topDialog().textContent.includes(engine) && test.topDialog().textContent.includes(shared ? "共享实例" : "主实例") && test.topDialog().textContent.includes(test.agent.name), "failure reason lacks node/core/instance identity");
      assert(test.topDialog().scrollWidth <= test.topDialog().clientWidth + 1, "failure details overflow the viewport");
      test.topDialog().querySelector('[aria-label="关闭"]').click(); await pause();
      test.taskReadFailure = 403; failureButton(test).click();
      await waitFor(()=>!test.root.hasAttribute("aria-busy"), "revoked task read did not finish");
      assert(!test.topDialog() && !failureButton(test).hidden, "revoked task read disclosed details or cleared the known failure");
      test.taskReadFailure = null;
      for (const taskOverride of [{config_version:99}, {engine:"another-core"}, {shared_instance:!shared}, {agent_id:"another-node"}]) {
        test.taskOverride = taskOverride; failureButton(test).click();
        await waitFor(()=>!test.root.hasAttribute("aria-busy"), "mismatched task response did not finish");
        assert(!test.topDialog(), "another target's task entered the failure dialog");
      }
      assert(test.writes.length === 1 && test.writes[0].intent === "deploy", "reading failure details repeated or changed the deployment");
      if (engine === "xray") {
        const anchor = (cell,index) => cell.getBoundingClientRect()[index === 3 && matchMedia("(max-width: 48rem)").matches ? "right" : "left"];
        const positions = [...row.cells].map(anchor);
        for (const [status, label] of [["pending","等待执行"], ["running","部署中"], ["canceled","部署已取消"], ["succeeded","启用"]]) {
          test.taskStatus = status;
          await waitFor(()=>row.cells[3].innerText === label, `deployment transition to ${label} did not replace the old status`);
          assert(![...region.querySelectorAll(".a-badge")].some(item=>!item.closest("[hidden]") && item.textContent === "已部署"), "successful deployment retained a duplicate applied badge");
          assert([...row.cells].every((cell,index)=>Math.abs(anchor(cell,index)-positions[index])<1), "deployment state changes shift the inbound columns");
        }
        test.deploymentState = "unknown";
        await waitFor(()=>row.cells[3].innerText === "部署结果待确认", "unknown result falsely claims enabled");
      }
    } finally { test.dispose(); }
  }
}

async function testSupersededDeploymentReceipts() {
  for (const shared of [false,true]) {
    const test=await createConfigFixture('xray',{shared});
    const source=()=>test.root.querySelector('form:has([name=content])');
    let release;
    test.confirm=true;
    try {
      source().elements.content.value+='\n ';source().elements.content.dispatchEvent(new Event('input',{bubbles:true}));
      source().requestSubmit();await test.settle(1);
      const firstSource=source().elements.content.value;
      location.hash='#tasks';await waitFor(()=>!source()&&test.calls.some(call=>call.path.startsWith('/tasks?'))&&!test.root.querySelector('[aria-busy=true]'),'pending deployment did not leave for tasks');
      location.hash='#live-config?agent=node&engine=xray';
      await waitFor(()=>source()&&!test.root.querySelector('[aria-busy=true]'),'pending deployment did not recover on return');
      assert(source().elements.content.value===firstSource&&test.row('first').cells[3].innerText==='等待执行','route return lost pending deployment or saved baseline');
      const oldReceipt=test.workspace();oldReceipt.deployment={...oldReceipt.deployment,state:'applied',task_status:'succeeded'};
      test.workspaceResponses=[new Promise(resolve=>{release=()=>resolve(oldReceipt);})];
      await waitFor(()=>test.workspaceResponses.length===0,'old deployment poll did not begin');
      const form=source();form.elements.content.value+='\n  ';form.elements.content.dispatchEvent(new Event('input',{bubbles:true}));
      const latest=form.elements.content.value;form.requestSubmit();await test.settle(2);
      assert(test.saved().version===3&&form.elements.content.value===latest,'new source submission lost current revision');
      release();release=null;await pause();
      assert(source()===form&&form.elements.content.value===latest&&test.row('first').cells[3].innerText==='等待执行','old successful receipt overwrote newer pending configuration');
      test.taskStatus='failed';
      await waitFor(()=>test.row('first').cells[3].innerText==='部署失败','latest failure did not supersede older success');
      assert(form.elements.content.value===latest&&test.saved().version===3&&test.writes.length===2,'failed deployment cleared baseline or repeated write');
      test.taskStatus='succeeded';
      await waitFor(()=>test.row('first').cells[3].innerText==='启用','latest deployment did not converge after recovery');
      assert(source()===form&&form.elements.content.value===latest&&!test.readRequests.length&&test.writes.length===2,'status convergence replaced source or created redundant read/write tasks');
    } finally {release?.();test.dispose();}
  }
}
