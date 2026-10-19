import {assert, waitFor, createConfigFixture} from "./config-fixture.mjs";

const change = (input, text) => { input.value = text; input.dispatchEvent(new Event("input", {bubbles:true})); };
const choose = file => {
  const transfer = new DataTransfer(); transfer.items.add(file);
  const picker = document.querySelector("[data-config-file-input]"); picker.files = transfer.files;
  picker.dispatchEvent(new Event("change", {bubbles:true}));
};

export async function testCompleteSourceRuntime() {
  const test = await createConfigFixture("xray", {noTasks:true});
  document.querySelector("[data-config-new]").click();
  const input = document.querySelector("[data-code-input]");
  await waitFor(()=>input.value === "", "blank complete draft did not open");
  assert(!input.readOnly && !test.writes.length, "starting a complete configuration performed a write");
  change(input, '{"inbounds":');
  assert(document.querySelector("[data-code-validation]").textContent.startsWith("JSON 格式错误") &&
    !document.querySelector("[data-workspace-result]").textContent,
    "source syntax error was duplicated in the operation feedback");
  document.querySelector("[data-config-preview]").click();
  assert(input.value === '{"inbounds":' && !input.readOnly, "invalid complete source was discarded by a view switch");
  const native = '{\n "custom":18446744073709551615,\n "inbounds":[{"tag":"own","protocol":"socks","port":23001,"settings":{"auth":"noauth"}}],\n "outbounds":[{"tag":"direct","protocol":"freedom"}]\n}\n';
  choose(new File([native], "mine.json", {type:"application/json"}));
  await waitFor(()=>test.confirmations.length === 1, "replacing a dirty draft did not request confirmation");
  assert(input.value === '{"inbounds":', "canceling import overwrote a dirty draft");
  test.confirm = true;
  choose(new File([native], "mine.json", {type:"application/json"}));
  await waitFor(()=>input.value === native, "local file did not become the complete editable draft");
  assert(!test.writes.length && !test.calls.some(call => call.path === "/tasks"), "file import saved or deployed without an explicit action");
  document.querySelector("[data-config-preview]").click();
  assert(input.readOnly && input.value === native, "preview did not include imported custom content");
  document.querySelector("[data-config-source]").click();
  assert(!input.readOnly && input.value === native, "returning from preview lost editable native content");
  choose(new File([new Uint8Array([0xff, 0xfe, 0])], "binary.json"));
  await waitFor(()=>test.notices.some(message => message.includes("UTF-8")), "invalid file encoding was not reported");
  assert(input.value === native, "failed file decoding destroyed the draft");
  let release;
  const old = new File(["{}"], "old.json");
  old.arrayBuffer = () => new Promise(resolve => { release = resolve; });
  choose(old);
  const newer = native.replace("23001", "23002");
  choose(new File([newer], "newer.json"));
  await waitFor(()=>input.value === newer, "newer local file read did not finish");
  release(new TextEncoder().encode("{}").buffer);
  await new Promise(resolve => setTimeout(resolve, 40));
  assert(input.value === newer, "an old file read overwrote the newer imported draft");
  await test.reenter();
  assert(document.querySelector("[data-code-input]").value === newer, "route return lost the complete draft");
  document.querySelector('[data-live-intent="save"]').click();
  await waitFor(()=>test.writes.length === 1 && document.querySelector("[data-workspace-save-state]")?.textContent === "已保存 v2", "complete source save did not finish");
  assert(test.saved().content === newer && test.saved().content.includes("18446744073709551615"), "save rewrote native content or rounded a custom number");
  assert(!test.calls.some(call => call.path === "/tasks"), "save-only complete source queued a task");
  test.dispose();
  const nativeTest = await createConfigFixture("mihomo", {native:true, noTasks:true});
  const nativeInput = document.querySelector("[data-code-input]");
  change(nativeInput, nativeInput.value.replace('"port": 21001', '"port": 23009'));
  nativeInput.focus(); nativeInput.setSelectionRange(8, 8);
  await waitFor(()=>document.querySelector('[data-access-inbound="first"]')?.textContent.includes("23009"), "native source edit did not update its object navigation");
  assert(nativeInput.selectionStart === 8 && !nativeTest.writes.length, "native object refresh reset the caret or saved the draft");
  nativeTest.dispose();
  const deployment = await createConfigFixture("xray");
  deployment.confirm = true;
  document.querySelector("[data-config-source]").click();
  change(document.querySelector("[data-code-input]"), native);
  deployment.taskFailure = true;
  let releaseTask;
  deployment.taskGate = new Promise(resolve => { releaseTask = resolve; });
  document.querySelector('[data-live-intent="deploy"]').click();
  await waitFor(()=>deployment.writes.length === 1 && document.querySelector("[data-workspace-save-state]").textContent === "已保存 v2",
    "saved baseline was delayed until task submission finished");
  assert([...document.querySelectorAll("#live-config-form button, #live-config-form textarea")].every(control => control.disabled),
    "updating the saved baseline unlocked controls during task submission");
  releaseTask(); deployment.taskGate = null;
  await waitFor(()=>document.querySelector("[data-workspace-result]")?.textContent === "fixture task unavailable", "task creation failure added redundant advice or hid the error");
  assert(deployment.saved().content === native && deployment.writes.length === 1 &&
    document.querySelector("[data-workspace-save-state]").textContent === "已保存 v2", "partial success lost the saved document or baseline");
  assert(!deployment.notices.length, "inline failure was duplicated in a notification");
  deployment.taskFailure = false;
  document.querySelector('[data-live-intent="deploy"]').click();
  await waitFor(()=>document.querySelector("[data-workspace-result]")?.firstChild?.textContent === "等待部署…", "queued deployment has no concise progress");
  const queuedTasks = deployment.calls.filter(call => call.path === "/tasks" && call.method === "POST").length;
  document.querySelector('[data-live-intent="deploy"]').click();
  await new Promise(resolve => setTimeout(resolve, 40));
  assert(document.querySelector("[data-workspace-result]")?.firstChild?.textContent === "等待部署…" &&
    deployment.calls.filter(call => call.path === "/tasks" && call.method === "POST").length === queuedTasks,
    "repeated deployment replaced its active feedback or submitted a duplicate task");
  deployment.taskError = "config.json: private_key: secret-material; listen 23001: address already in use";
  deployment.taskStatus = "failed";
  await waitFor(()=>document.querySelector("[data-workspace-result]")?.textContent === "config.json: private_key: [redacted]; listen 23001: address already in use", "deployment failure did not show only the redacted error");
  assert(!deployment.notices.length && document.querySelector("[data-workspace-deploy-state]").textContent.includes("v2 待部署"), "failed deployment duplicated feedback or advanced its deployed version");
  deployment.taskStatus = "succeeded";
  deployment.taskError = "";
  document.querySelector('[data-live-intent="deploy"]').click();
  await waitFor(()=>document.querySelector("[data-workspace-result]")?.firstChild?.textContent === "部署成功" &&
    document.querySelector("[data-workspace-deploy-state]")?.textContent === "已部署 v2", "retry did not report the actual successful deployment");
  assert(deployment.writes.length === 1 && deployment.saved().content === native, "deployment retry rewrote the saved draft");
  deployment.dispose();
}
