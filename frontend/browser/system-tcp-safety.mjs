import { assert, delay, waitFor } from "./assertions.mjs";

export async function testSystemTCPSafety({ card, editor, openEditor, refresh, testAPI }) {
  const agent = testAPI.agents.find((entry) => entry.id === "alpha");
  const parameters = agent.metrics.bbr.parameters;
  const preset = () => editor().querySelector("[data-tcp-preset]");
  const submit = () => editor().querySelector("form").requestSubmit();
  const confirmation = () => document.querySelector("[data-confirm-dialog][open]");
  const error = () => editor().querySelector("[data-tcp-error]");
  const before = testAPI.tcpMutations.length;
  if (!editor().open) openEditor();
  preset().click();

  const mtuKey = "net.ipv4.tcp_mtu_probing";
  const mtu = parameters[mtuKey];
  delete parameters[mtuKey];
  await refresh();
  submit();
  await delay(50);
  assert.equal(confirmation(), null, "预设参数失报后仍进入提交确认");
  assert.match(error().textContent, /未上报/, "失报参数没有在编辑器中提示");
  assert.equal(testAPI.tcpMutations.length, before, "失报参数仍提交了任务");
  const missingSelection = editor().querySelector(`[data-tcp-selected="${mtuKey}"]`);
  assert.ok(missingSelection.checked && !missingSelection.disabled, "失报参数的草稿必须保留并允许取消勾选");
  missingSelection.checked = false;
  missingSelection.dispatchEvent(new Event("change", { bubbles: true }));
  assert.ok(missingSelection.disabled, "未上报的参数允许重新勾选");
  assert.equal(editor().querySelectorAll("[data-tcp-selected]:checked").length, 7, "取消失报参数清除了其他草稿");
  parameters[mtuKey] = mtu;
  await refresh();
  preset().click();

  const bufferKey = "net.ipv4.tcp_rmem";
  const originalBuffer = parameters[bufferKey];
  submit();
  const changedConfirmation = await waitFor(confirmation, "参数变化测试未显示确认");
  const summary = changedConfirmation.textContent;
  parameters[bufferKey] = "8192 131072 67108864";
  await refresh();
  assert.equal(changedConfirmation.textContent, summary, "后台刷新改写了待确认的参数差异");
  changedConfirmation.querySelector("[data-confirm-accept]").click();
  await waitFor(() => !preset().disabled, "参数变化后编辑器未解锁");
  assert.equal(testAPI.tcpMutations.length, before, "确认期间参数变化仍按旧差异提交");
  assert.match(error().textContent, /参数已变化/, "确认期间参数变化没有提示重新核对");
  assert.equal(editor().querySelector(`[data-tcp-value="${bufferKey}"]`).value, "4096 65536 33554432", "参数变化覆盖了预设草稿");
  submit();
  const retry = await waitFor(confirmation, "参数变化后不能重新确认");
  assert.ok(retry.textContent.includes(`${bufferKey}: 8192 131072 67108864 → 4096 65536 33554432`), "重新确认仍使用旧参数基线");
  retry.querySelector("[data-confirm-cancel]").click();
  await waitFor(() => !preset().disabled, "取消重试后编辑器未解锁");
  parameters[bufferKey] = originalBuffer;
  await refresh();

  submit();
  const missingConfirmation = await waitFor(confirmation, "失报竞态测试未显示确认");
  delete parameters[mtuKey];
  await refresh();
  missingConfirmation.querySelector("[data-confirm-accept]").click();
  await waitFor(() => !editor().querySelector("[data-tcp-reset]").disabled, "确认期间失报后编辑器未解锁");
  assert.equal(testAPI.tcpMutations.length, before, "确认期间参数失报仍提交了任务");
  assert.match(error().textContent, /未上报/, "确认期间失报没有显示具体错误");
  assert.ok(editor().matches(":modal"), "失报错误关闭了编辑器");
  parameters[mtuKey] = mtu;
  await refresh();

  // A new heartbeat or a change outside the selected fields must not require
  // another confirmation or block a valid task.
  const ecnKey = "net.ipv4.tcp_ecn";
  const ecn = parameters[ecnKey];
  submit();
  const unrelatedConfirmation = await waitFor(confirmation, "未选参数变化测试未显示确认");
  parameters[ecnKey] = ecn === "0" ? "2" : "0";
  agent.metrics.bbr.collected_at = new Date().toISOString();
  await refresh();
  unrelatedConfirmation.querySelector("[data-confirm-accept]").click();
  await waitFor(() => testAPI.tcpMutations.length === before + 1 && !editor().open, "未选参数变化阻止了有效提交");
  assert.equal(Object.hasOwn(testAPI.tcpMutations.at(-1).tcp_settings, ecnKey), false, "未选参数进入了任务");
  parameters[ecnKey] = ecn;
  testAPI.tcpTasks[0].status = "succeeded";
  await refresh();
  testAPI.tcpTasks = [];
  await refresh();

  card().querySelector('[data-bbr-action="enable-bbr"]').click();
  const shortcutConfirmation = await waitFor(confirmation, "快捷操作没有请求确认");
  const qdisc = parameters["net.core.default_qdisc"];
  parameters["net.core.default_qdisc"] = "cake";
  await refresh();
  shortcutConfirmation.querySelector("[data-confirm-accept]").click();
  await waitFor(() => !card().querySelector("[data-bbr-action]").disabled, "快捷操作基线变化后仍锁定");
  assert.equal(testAPI.tcpMutations.length, before + 1, "快捷操作绕过了参数变化检查");
  parameters["net.core.default_qdisc"] = qdisc;
  await refresh();
  openEditor();
}
