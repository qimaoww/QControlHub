import { assert, waitFor } from "./assertions.mjs";

export async function testAgentDeleteActions({ testAPI }) {
  // Offline removal is a control-plane action and must not need a heartbeat.
  testAPI.agents.find((agent) => agent.id === "alpha").status = "offline";
  location.hash = "#settings-node-alpha";
  await waitFor(() => document.querySelector('[data-delete="alpha"]'), "删除测试节点未渲染");
  document.querySelector('[data-node-tab="agent"]').click();
  assert.equal(document.querySelector("[data-agent-status-label]").textContent, "离线");
  const button = () => document.querySelector('[data-delete="alpha"]');
  const calls = () => testAPI.calls.filter((call) => call.method === "DELETE" && call.path === "/agents/alpha").length;
  const notice = () => document.querySelector("[data-spa-notice]")?.textContent || "";
  const confirm = async () => {
    button().click();
    const dialog = await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "删除确认弹窗未打开");
    assert.match(dialog.textContent, /QAgent 不会被远程卸载/);
    return dialog;
  };
  const before = calls();
  (await confirm()).querySelector("[data-confirm-cancel]").click();
  await waitFor(() => !button().disabled, "取消删除后按钮未恢复");
  assert.equal(calls(), before, "取消删除仍然发送请求");

  testAPI.deleteFailure = true;
  (await confirm()).querySelector("[data-confirm-accept]").click();
  await waitFor(() => /服务暂不可用/.test(notice()), "删除失败未显示错误提示");
  assert.ok(!button().disabled, "删除失败后应允许重试");
  assert.ok(testAPI.agents.some((agent) => agent.id === "alpha"), "失败时不应移除节点");

  let finishDelete;
  testAPI.deleteGate = new Promise((resolve) => { finishDelete = resolve; });
  const dialog = await confirm();
  button().dispatchEvent(new MouseEvent("click"));
  dialog.querySelector("[data-confirm-accept]").click();
  await waitFor(() => calls() === before + 2, "重试删除未发送请求");
  assert.equal(button().disabled, true, "删除期间按钮应禁用");
  assert.equal(button().getAttribute("aria-busy"), "true");
  assert.match(button().textContent, /正在删除/);

  // A render while DELETE is pending must retain the lock on the new button.
  location.hash = "#node-settings";
  await waitFor(() => document.querySelector(".node-card-grid"), "节点列表未渲染");
  location.hash = "#settings-node-alpha";
  await waitFor(() => button(), "返回详情后删除按钮未渲染");
  assert.equal(button().disabled, true, "重新渲染丢失删除锁");
  button().dispatchEvent(new MouseEvent("click"));
  assert.equal(document.querySelector("[data-confirm-dialog]").open, false, "重复点击不应打开第二个确认框");
  assert.equal(calls(), before + 2, "重复点击发送了额外删除请求");
  finishDelete();
  await waitFor(() => !button().disabled && /服务暂不可用/.test(notice()), "迟到的失败未恢复当前按钮");
  testAPI.deleteGate = null;
  testAPI.deleteFailure = false;

  let finishRefresh;
  testAPI.agentsGate = new Promise((resolve) => { finishRefresh = resolve; });
  (await confirm()).querySelector("[data-confirm-accept]").click();
  await waitFor(() => /节点已删除/.test(notice()), "列表刷新阻塞时未立即显示删除成功");
  assert.equal(button().textContent, "已删除");
  assert.equal(button().disabled, true, "成功后刷新期间不能再次删除");
  assert.equal(button().hasAttribute("aria-busy"), false, "删除成功后仍在显示进行中");
  finishRefresh();
  testAPI.agentsGate = null;
  await waitFor(() => document.querySelector("[data-node-missing]") && /节点已删除/.test(notice()), "删除成功后未刷新详情并提示结果");
  assert.equal(calls(), before + 3);
  location.hash = "#node-settings";
  await waitFor(() => document.querySelector(".node-card-grid"), "删除后节点列表未渲染");
  assert.equal(document.querySelector('[data-agent-node="alpha"]'), null, "已删除节点仍出现在列表");

  location.hash = "#settings-node-bravo";
  const remaining = await waitFor(() => document.querySelector('[data-delete="bravo"]'), "刷新失败测试节点未渲染");
  document.querySelector('[data-node-tab="agent"]').click();
  // Use the browser's real abort/TimeoutError behavior with an accelerated clock.
  const timeout = AbortSignal.timeout;
  testAPI.deleteHang = true;
  let deleteTimeout;
  AbortSignal.timeout = (milliseconds) => {
    deleteTimeout = milliseconds;
    return timeout.call(AbortSignal, 50);
  };
  try {
    remaining.click();
    const pendingDialog = await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "超时测试未打开确认框");
    pendingDialog.querySelector("[data-confirm-accept]").click();
    await waitFor(() => /请求超时/.test(notice()) && !remaining.disabled, "无响应的删除请求未超时或未恢复按钮");
    assert.ok(deleteTimeout > 30000 && deleteTimeout < 60000, "客户端等待应长于后端删除期限，并短于连接写入期限");
    assert.match(notice(), /刷新确认结果/);
    assert.ok(testAPI.agents.some((agent) => agent.id === "bravo"), "超时不能当作删除成功");
  } finally {
    AbortSignal.timeout = timeout;
    testAPI.deleteHang = false;
  }
  testAPI.agentsFailure = true;
  remaining.click();
  const lastDialog = await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "刷新失败测试未打开确认框");
  lastDialog.querySelector("[data-confirm-accept]").click();
  await waitFor(() => /节点已删除，但列表刷新失败/.test(notice()), "刷新失败不应掩盖删除已成功");
  assert.equal(remaining.textContent, "已删除");
  assert.equal(remaining.disabled, true, "刷新失败不能恢复已删除节点的按钮");
  assert.equal(testAPI.agents.some((agent) => agent.id === "bravo"), false);
  testAPI.agentsFailure = false;
}
