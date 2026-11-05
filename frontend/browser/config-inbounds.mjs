import { assert, pause, waitFor, createConfigFixture as fixture } from "./config-fixture.mjs";
import { testConfigOutboundsRuntime } from "./config-outbounds.mjs";
import { testCommonConfigRuntime } from "./config-common.mjs";
import { testWireGuardConfigRuntime } from "./config-wireguard.mjs";
import { testPresetFieldLayoutRuntime } from "./preset-layout.mjs";

export async function testConfigInboundsRuntime(preview = false) {
  if (preview) {
    const params = new URLSearchParams(location.search);
    window.inboundFixture = await fixture(params.get("engine") || "xray", {missing:params.has("missing"), multi:params.has("multi"), protocol:params.get("protocol")});
    if (params.has("common")) window.inboundFixture.common(params.get("common") || "modify");
    else if (params.has("outbound")) {
      window.inboundFixture.select("first");
      document.querySelector(`[data-outbound-action="${params.get("outbound") || "add"}"]`).click();
    }
    else if (params.has("modify")) {
      window.inboundFixture.select("second");
      window.inboundFixture.click("modify");
    }
    else if (params.has("modal")) window.inboundFixture.click("add");
    return;
  }
  await testWireGuardConfigRuntime();
  await testPresetFieldLayoutRuntime();
  await testNodeConfigHistory();
  for (const engine of ["xray", "sing-box", "mihomo", "ss-rust"]) {
    const test = await fixture(engine);
    test.select("second");
    const source = document.querySelector("[data-code-input]").value;
    for (const [index, label] of ["停用入站", "启用入站"].entries()) {
      const trigger = document.querySelector('[data-inbound-action="toggle"]');
      assert(!trigger.disabled && trigger.textContent === label, "state action does not reflect selected inbound");
      trigger.click();
      await waitFor(()=>document.querySelector('[data-management-intent="validate"]'), "state confirmation missing");
      assert(document.querySelector("dialog").textContent.includes("second"), "state dialog targets another inbound");
      const submit = document.querySelector('[data-management-intent="validate"]');
      submit.click(); submit.click();
      await waitFor(()=>!document.querySelector("dialog[open]"), "state save did not complete");
      assert(test.writes.length === index+1, "state operation submitted twice");
      assert(test.writes[index].version === index+1 && test.writes[index].disabled.length === (index === 0 ? 1 : 0), "state revision or target lost");
      assert(document.querySelector("[data-code-input]").value === source, "state save discarded source parameters");
    }
  }
  for (const engine of ["xray", "sing-box", "mihomo", "ss-rust"]) {
    const test = await fixture(engine);
    const action = kind => document.querySelector(`[data-inbound-action="${kind}"]`);
    assert(!document.querySelector('[data-live-intent="migrate-files"]'), "configuration page still renders the removed bundle action");
    assert(document.querySelector(".config-inbound-menu").previousElementSibling?.matches("[data-config-access-open]"), "inbound menu must be beside restrictions");
    assert(action("modify").disabled && action("delete").disabled, "public config allows inbound mutation");
    assert(document.querySelector("[data-config-access-open]").hidden, "public config exposes an irrelevant restriction action");
    test.select("second");
    assert(!document.querySelector("[data-config-access-open]").hidden, "selected inbound lost its restriction action");
    assert(!action("modify").disabled && !action("delete").disabled, "selected inbound actions disabled");
    test.click("modify");
    await waitFor(()=>document.querySelector("#server-plan-form"), "modify editor did not load");
    let form = document.querySelector("#server-plan-form");
    assert(form.elements.tag.value === "second" && form.elements.port.value === "21002", "modify opened the wrong inbound");
    assert(form.elements.operation.value === "modify" && form.elements.operation.type === "hidden", "operation can escape its selected target");
    assert(!document.querySelector("dialog .inbound-browser, dialog .config-source-studio, dialog [data-engine-select]"), "modal duplicated the page layout");
    if (innerWidth <= 600) {
      form.querySelector('[data-builder-step="identity"]').click();
      const section = form.querySelector("#identity").getBoundingClientRect();
      const tabs = form.querySelector(".builder-index").getBoundingClientRect();
      assert(tabs.bottom <= section.top + 1 && section.width > form.clientWidth * .85, "mobile tabs squeeze the authentication form into a narrow column");
      form.querySelector('[data-builder-step="listen"]').click();
    }
    form.elements.tag.value = "renamed";
    form.elements.port.value = "23002";
    document.querySelector("[data-inbound-close]").click(); await pause();
    assert(document.querySelector("dialog[open]") && form.elements.tag.value === "renamed", "cancel close lost dirty inputs");
    form.querySelector("[data-plan-intent=validate]").click();
    await waitFor(()=>!document.querySelector("dialog[open]"), "modify save did not close modal");
    assert(test.writes.length === 1 && test.writes[0].original_tag === "second" && test.writes[0].expected_version === 1 &&
      test.writes[0].input.tag === "renamed" && !test.writes[0].install_if_missing, "modify identity/version/installation regression");
    assert(document.querySelector("#live-config-form"), "save left the configuration page");
    test.click("add");
    await waitFor(()=>document.querySelector("#server-plan-form"), "add editor did not load");
    form = document.querySelector("#server-plan-form");
    assert(form.elements.operation.value === "add" && form.elements.tag.value !== "renamed", "add reused saved identity");
    let release;
    test.gate = new Promise(resolve=>{release=resolve;});
    const button = form.querySelector("[data-plan-intent=validate]");
    button.click(); button.click(); await pause();
    assert(button.disabled && form.getAttribute("aria-busy") === "true", "saving did not lock controls");
    document.querySelector("[data-inbound-close]").click(); await pause();
    assert(document.querySelector("dialog[open]"), "closed modal during an uncertain write");
    release();
    await waitFor(()=>!document.querySelector("dialog[open]"), "add did not finish");
    assert(test.writes.length === 2 && test.writes[1].operation === "add" && test.writes[1].original_tag === "", "duplicate add or copied original tag");
    test.gate = null;
    test.click("delete");
    await waitFor(()=>document.querySelector("[data-delete-intent]"), "delete confirmation absent");
    assert(document.querySelector("dialog").textContent.includes(test.writes[1].input.tag), "delete confirmation names wrong inbound");
    document.querySelector("[data-inbound-close]").click(); await pause();
    assert(test.writes.length === 2, "canceling deletion wrote data");
    test.click("delete");
    await waitFor(()=>document.querySelector("[data-delete-intent=validate]"), "delete reopened incorrectly");
    document.querySelector("[data-delete-intent=validate]").click();
    await waitFor(()=>!document.querySelector("dialog[open]"), "delete did not return to editor");
    assert(test.writes.length === 3 && test.writes[2].operation === "delete" && test.writes[2].original_tag === test.writes[1].input.tag, "wrong deletion target");
    assert(action("modify").disabled && action("delete").disabled, "deletion retained removed selection");
    assert(!document.querySelector("[data-config-preview]"), "single-file editor still offers a redundant merged preview");
    const input = document.querySelector("[data-code-input]");
    const original = input.value;
    input.value += "\n "; input.dispatchEvent(new Event("input"));
    test.click("add"); await pause();
    assert(!document.querySelector("dialog[open]") && test.notices.some(message=>message.includes("未保存修改")), "dirty source was overwritten");
    input.value = original; input.dispatchEvent(new Event("input"));
    test.click("advanced");
    await waitFor(()=>document.querySelector("#field-form"), "advanced field editor missing");
    assert(!document.querySelector("dialog #server-plan-form"), "advanced fields unnecessarily mounted preset form");
    document.querySelector("#field-form textarea").value = '{"level":"debug"}';
    document.querySelector("#field-form [data-field-intent=validate]").click();
    await waitFor(()=>!document.querySelector("dialog[open]"), "field save did not refresh source page");
    test.click("history");
    await waitFor(()=>document.querySelector("#revisions [data-revision-body] nav"), "history not accessible");
    assert(!document.querySelector("dialog #server-plan-form"), "history generated an unused credential form");
    document.querySelector("[data-inbound-close]").click(); await pause();
    test.click("diff");
    await waitFor(()=>document.querySelector("dialog .config-diff"), "deployment diff unavailable");
    document.querySelector("[data-inbound-close]").click(); await pause();
    test.click("add");
    await waitFor(()=>document.querySelector("#server-plan-form"), "conflict form missing");
    form = document.querySelector("#server-plan-form");
    form.elements.tag.value = "keep-my-draft";
    test.fail = true;
    form.querySelector("[data-plan-intent=validate]").click();
    await waitFor(()=>document.querySelector("dialog [data-preset-status]")?.textContent.includes("草稿已保留"), "conflict did not preserve draft");
    assert(form.elements.tag.value === "keep-my-draft" && form.querySelector("[data-plan-intent=validate]").disabled, "conflict allowed stale resubmission");
    test.dispose();
    assert(!document.querySelector("dialog"), "route abort left an orphan modal");
  }
  for (const status of [200, 408]) {
    for (const kind of ["modify", "delete"]) {
      const test = await fixture("xray");
      test.select("first");
      test.click(kind);
      const selector = kind === "delete" ? "[data-delete-intent=validate]" : "[data-plan-intent=validate]";
      await waitFor(()=>document.querySelector(selector), "interrupted inbound form did not open");
      const form = document.querySelector("#server-plan-form");
      if (form) form.elements.tag.value = "interrupted-draft";
      test.mutationFailure = {status};
      document.querySelector(selector).click();
      await waitFor(()=>document.querySelector(kind === "delete" ? "[data-inbound-error]" : "dialog [data-preset-status]")?.textContent.includes("确认"), "interrupted inbound result was not shown as unknown");
      assert(document.querySelector(selector).disabled && !test.writes.length, "interrupted inbound response permits duplicate writes");
      assert(!form || form.elements.tag.value === "interrupted-draft", "interrupted inbound response lost its draft");
      test.dispose();
    }
  }
  for (const options of [{missing:true}, {missing:true, legacy:true}, {missing:true, shared:true}, {missing:true, offline:true}]) {
    const test = await fixture("xray", options);
    test.click("add");
    await waitFor(()=>document.querySelector("#server-plan-form"), "missing-core editor not available");
    const submit = document.querySelector("[data-plan-intent=validate]");
    const allowed = !options.legacy && !options.shared && !options.offline;
    assert(submit.disabled !== allowed, "auto-install permission/capability/offline gate incorrect");
    if (allowed) {
      submit.click();
      await waitFor(()=>test.writes.length === 1 && !document.querySelector("dialog"), "auto-install submission failed");
      assert(test.writes[0].install_if_missing && test.writes[0].intent === "validate", "missing core did not opt into stable installation");
      assert(!test.calls.some(call=>call.path === "/tasks"), "installation requires an open frontend for a second task");
      assert(document.body.textContent.includes("稳定版安装"), "installation progress missing");
      const runtimeReads = test.calls.filter(call=>call.path === "/agents").length;
      test.agent.runtime.xray.installed = true;
      test.agent.runtime.xray.version = "stable-installed";
      test.taskStatus = "succeeded";
      await waitFor(()=>test.calls.filter(call=>call.path === "/agents").length > runtimeReads &&
        !document.querySelector("[data-live-intent=validate]")?.disabled &&
        document.body.textContent.includes("stable-installed"), "terminal auto-install task did not refresh installed runtime");
    }
    test.dispose();
  }
  for (const options of [{readonly:true}]) {
    const test = await fixture("xray", options);
    test.click("add"); await pause();
    assert(!document.querySelector("dialog"), "read-only or diverged snapshot permitted mutation");
    test.dispose();
  }
  const stopped = await fixture("xray", {serviceStatus:"inactive"});
  let stoppedTab = document.querySelector('[data-live-engine="xray"]');
  assert(stoppedTab.querySelector("small").textContent === "已停止" && stoppedTab.querySelector("small").classList.contains("stopped"), "stopped core still looks installed or running");
  assert(stoppedTab.getAttribute("aria-label").includes("已停止") && stoppedTab.title === "已停止", "stopped state missing from accessible engine label");
  stopped.agent.runtime.xray.service_status = "unknown";
  await stopped.reenter();
  stoppedTab = document.querySelector('[data-live-engine="xray"]');
  assert(stoppedTab.querySelector("small").textContent === "状态未知", "unknown service state was presented as stopped");
  stopped.dispose();
  const offline = await fixture("xray", {offline:true, serviceStatus:"active"});
  const offlineTab = document.querySelector('[data-live-engine="xray"]');
  assert(offlineTab.querySelector("small").textContent === "状态未知" &&
    offlineTab.getAttribute("aria-label").includes("状态未知"), "offline node presented cached running state as current");
  offline.dispose();
  const shared = await fixture("xray", {shared:true});
  shared.agent.runtime.xray.instance = {saved_config_id:"cfg", saved_version:2, deployed_config_id:"cfg", deployed_version:1};
  await shared.reenter();
  const sharedTab = () => document.querySelector('[data-live-engine="xray"]');
  assert(sharedTab().querySelector(".live-engine-status").textContent === "待部署", "共享配置状态控件缺失或使用了服务状态");
  assert(sharedTab().title === "待部署" && sharedTab().getAttribute("aria-label").includes("待部署"), "共享配置状态缺少无障碍说明");
  assert(document.querySelector('.config-tools-menu a[href="#settings-node-node"]'), "共享配置没有返回节点管理的入口");
  assert(!document.querySelector("[data-task-action], [data-instance-version]"), "实例启停或运行状态被放回了配置页");
  shared.agent.runtime.xray.instance.saved_version = 1;
  shared.agent.runtime.xray.service_status = "inactive";
  await shared.reenter();
  assert(sharedTab().querySelector("small").textContent === "已部署", "停止实例丢失了文件的部署状态");
  shared.dispose();
  const switcher = await fixture("xray", {multi:true});
  assert(switcher.state.data.liveEngine === "xray", "default selected an uninstalled core");
  assert(document.querySelector('[data-live-engine="xray"] small').textContent === "正在运行", "running core status not visible");
  assert(document.querySelector('[data-live-engine="mihomo"] small').textContent === "未安装", "missing status not visible");
  let releaseSwitch;
  switcher.workspaceGate = new Promise(resolve=>{releaseSwitch=resolve;});
  switcher.failSwitch = true;
  const missingTab = document.querySelector('[data-live-engine="mihomo"]');
  missingTab.click();
  await waitFor(()=>document.querySelector(".live-engine-loading"), "switch did not acknowledge input");
  assert(document.querySelector("#live-config-form").inert, "old source remains editable while switching engines");
  missingTab.click();
  const switchReads = switcher.calls.filter(call=>call.path.includes("/mihomo/")).length;
  assert(switchReads === 1, "repeated click duplicated switch request");
  releaseSwitch();
  await waitFor(()=>switcher.notices.some(message=>message.includes("切换内核失败")), "failed switch lost feedback");
  assert(switcher.state.data.liveEngine === "xray" && !missingTab.disabled, "failed switch did not restore usable previous editor");
  assert(!document.querySelector("#live-config-form").inert, "failed switch left the source inert");
  switcher.workspaceGate = new Promise(resolve=>{releaseSwitch=resolve;});
  missingTab.click();
  await waitFor(()=>document.querySelector(".live-engine-loading"), "second switch not started");
  document.querySelector('[data-live-engine="xray"]').click();
  await waitFor(()=>switcher.state.data.liveEngine === "xray", "latest selection was blocked by previous request");
  releaseSwitch();
  await waitFor(()=>!document.querySelector(".live-engine-loading"), "latest switch did not finish");
  assert(document.querySelector('[data-live-engine="xray"]').classList.contains("active"), "old response replaced latest selection");
  switcher.dispose();
  const drift = await fixture("xray", {drift:true});
  drift.click("add");
  await waitFor(()=>document.querySelector("#server-plan-form"), "node snapshot drift blocked saved config editing");
  assert(drift.writes.length === 0, "opening drifted config unexpectedly saved or deployed");
  drift.dispose();
  await testConfigOutboundsRuntime();
  const background = await fixture("xray");
  background.confirm = true;
  background.click("add");
  await waitFor(()=>document.querySelector("#server-plan-form"), "background fixture did not open");
  document.querySelector("[data-plan-intent=deploy]").click();
  await waitFor(()=>background.writes.length === 1 && !document.querySelector("dialog"), "background deployment not submitted");
  assert(background.readRequests.length === 0,
    "deployment added a redundant Agent read");
  background.select("first"); background.click("delete");
  await waitFor(()=>document.querySelector("[data-delete-intent]"), "delete fixture did not open");
  const workspaceReads = background.calls.filter(call=>call.path.endsWith("/workspace")).length;
  background.taskStatus = "succeeded";
  await new Promise(resolve=>setTimeout(resolve, 1800));
  assert(background.calls.filter(call=>call.path.endsWith("/workspace")).length === workspaceReads,
    "background deployment invalidated the open delete dialog");
  background.dispose();
  const deployConflict = await fixture("xray");
  deployConflict.confirm = true;
  deployConflict.click("add");
  await waitFor(()=>document.querySelector("#server-plan-form"), "deploy-conflict fixture did not open");
  const conflictForm = document.querySelector("#server-plan-form");
  conflictForm.elements.tag.value = "keep-conflicting-draft";
  deployConflict.setAgentContent(deployConflict.saved().content + "\n");
  conflictForm.querySelector("[data-plan-intent=deploy]").click();
  await waitFor(()=>deployConflict.writes.length === 1 && !document.querySelector("dialog"),
    "runtime snapshot drift blocked a valid saved-revision deployment");
  assert(deployConflict.readRequests.length === 0, "deployment required an unrelated runtime snapshot");
  deployConflict.dispose();
  const stale = await fixture("xray");
  let release;
  stale.workspaceGate = new Promise(resolve=>{release=resolve;});
  stale.click("add");
  stale.dispose();
  release(); await pause();
  assert(!document.querySelector("dialog") && !stale.writes.length, "late response escaped its session");
  const narrow = await fixture("xray", {noFleet:true});
  assert(document.querySelector("#live-config-form") && !narrow.calls.some(call=>call.path === "/agents"), "config-only deep link requires fleet permission");
  narrow.dispose();
  const native = await fixture("mihomo", {native:true});
  native.select("first");
  assert(document.querySelector('[data-inbound-action="modify"]').disabled &&
    !document.querySelector('[data-inbound-action="delete"]').disabled &&
    !document.querySelector("[data-config-access-open]").disabled, "native non-preset listener lost restriction/delete selection");
  native.click("delete");
  await waitFor(()=>document.querySelector("[data-delete-intent=validate]"), "native deletion did not open");
  document.querySelector("[data-delete-intent=validate]").click();
  await waitFor(()=>native.writes.length === 1 && !document.querySelector("dialog"), "native deletion failed");
  assert(native.writes[0].original_tag === "first", "native deletion targeted another inbound");
  native.dispose();
  const protocolDraft = await fixture("xray");
  protocolDraft.click("add");
  await waitFor(()=>document.querySelector("#server-plan-form"), "protocol draft form missing");
  let draftForm = document.querySelector("#server-plan-form");
  const selector = document.querySelector("[data-preset-protocol]");
  const originalProtocol = selector.value, otherProtocol = selector.options[1].value;
  draftForm.elements.tag.value = "protocol-specific-draft";
  draftForm.elements.tag.dispatchEvent(new Event("input", {bubbles:true}));
  selector.value = otherProtocol; selector.dispatchEvent(new Event("change", {bubbles:true}));
  await waitFor(()=>document.querySelector("#server-plan-form") !== draftForm, "protocol selection did not update the embedded form");
  draftForm = document.querySelector("#server-plan-form");
  const restoredSelector = document.querySelector("[data-preset-protocol]");
  restoredSelector.value = originalProtocol; restoredSelector.dispatchEvent(new Event("change", {bubbles:true}));
  await waitFor(()=>document.querySelector("#server-plan-form") !== draftForm, "protocol return did not load");
  assert(document.querySelector("#server-plan-form").elements.tag.value === "protocol-specific-draft", "protocol switch lost its local draft");
  protocolDraft.dispose();
  const refreshFailure = await fixture("xray");
  refreshFailure.click("add");
  await waitFor(()=>document.querySelector("#server-plan-form"), "refresh-failure form missing");
  refreshFailure.failRefresh = true;
  document.querySelector("[data-plan-intent=validate]").click();
  await waitFor(()=>!document.querySelector("dialog") &&
    document.querySelector("[data-preset-status]")?.textContent.includes("已保存且任务已提交，页面刷新失败"), "committed save was silently lost after parent refresh failed");
  assert(refreshFailure.writes.length === 1 && refreshFailure.notices.some(message=>message.includes("已保存且任务已提交")), "refresh failure misreported the durable mutation");
  refreshFailure.failRefresh = false;
  document.querySelector("[data-preset-status] button").click();
  await waitFor(()=>document.querySelector('.live-config-version')?.textContent === `已保存 · ${refreshFailure.saved().updated_at}`, "refresh recovery did not bind the persisted save date");
  assert(refreshFailure.writes.length === 1, "reload repeated the inbound mutation");
  refreshFailure.dispose();
  await testCommonConfigRuntime();
}

async function testNodeConfigHistory() {
  for (const engine of ["xray", "sing-box", "mihomo", "ss-rust"]) {
    const test = await fixture(engine, {history:true, shared:true});
    test.click("history");
    await waitFor(() => document.querySelector('[data-revision="1"]'), "node history not selectable");
    document.querySelector('[data-revision="1"]').click();
    await waitFor(() => document.querySelector('[data-restore-revision]'), "history preview has no restore action");
    assert(document.querySelector('[aria-label="历史配置内容"]').readOnly, "history preview is editable");
    if (innerWidth <= 600) assert(document.querySelector('[data-history-preview]').getBoundingClientRect().width >
      document.querySelector('.timeline-body').getBoundingClientRect().width * .9, "narrow history preview is squeezed beside the record list");
    document.querySelector('[data-restore-revision]').click();
    await pause();
    assert(test.restores.length === 0, "canceling restore wrote a configuration");
    test.confirm = true;
    test.restoreError = 409;
    document.querySelector('[data-restore-revision]').click();
    await waitFor(() => document.querySelector('[data-restore-error]').textContent.includes("配置已变化"), "restore conflict is hidden");
    assert(test.saved().version === 2, "conflict changed the configuration");
    test.restoreError = 403;
    document.querySelector('[data-restore-revision]').click();
    await waitFor(() => document.querySelector('[data-restore-error]').textContent.includes("fixture restore rejected"), "restore authorization failure is hidden");
    test.restoreError = null;
    let release;
    test.restoreGate = new Promise(resolve => { release = resolve; });
    document.querySelector('[data-restore-revision]').click();
    document.querySelector('[data-restore-revision]').click();
    await pause();
    assert(document.querySelector('[data-restore-revision]').disabled, "restore allowed duplicate commits");
    document.querySelector('[data-inbound-close]').click();
    await pause();
    assert(document.querySelector('dialog[open]'), "restore modal closed during an uncertain commit");
    release();
    await waitFor(() => !document.querySelector('dialog[open]'), "restored page did not refresh");
    assert(test.restores.length === 1 && test.restores[0].expected_version === 2, "restore used the historical version for CAS");
    assert(test.saved().version === 3 && test.revisions.size === 3, "restore replaced an archive");
    assert(document.body.textContent.includes("尚未部署"), "saved restore was reported as deployed");
    assert(!test.calls.some(call => call.path === "/tasks"), "restore queued an implicit deployment");
    test.click("history");
    await waitFor(() => document.querySelector('[data-revision="3"]'), "restore did not refresh history");
    document.querySelector('[data-revision="3"]').click();
    await waitFor(() => document.querySelector('[aria-label="历史配置内容"]'), "current archive has no preview");
    assert(!document.querySelector('[data-restore-revision]'), "current archive offers a duplicate restore");
    test.dispose();
  }
  const readonly = await fixture("xray", {history:true, readonly:true});
  readonly.click("history");
  await waitFor(() => document.querySelector('[data-revision="1"]'), "read-only history unavailable");
  document.querySelector('[data-revision="1"]').click();
  await waitFor(() => document.querySelector('[aria-label="历史配置内容"]'), "read-only preview unavailable");
  assert(!document.querySelector('[data-restore-revision]'), "read-only user can restore");
  readonly.dispose();
  const stale = await fixture("xray", {history:true});
  stale.click("history");
  await waitFor(() => document.querySelector('[data-revision="1"]'), "stale history unavailable");
  let release;
  stale.revisionGate = new Promise(resolve => { release = resolve; });
  document.querySelector('[data-revision="1"]').click();
  stale.dispose();
  await fixture("sing-box");
  release();
  await pause();
  assert(!document.querySelector('[data-restore-revision]'), "old account preview reached a new target");
}
