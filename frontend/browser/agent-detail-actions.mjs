import { assert, waitFor } from "./assertions.mjs";
export async function testAgentDetailActions({ testAPI }) {
  location.hash = "#settings-node-alpha";
  await waitFor(
    () => document.querySelector('.node-operations-workspace[data-agent-node="alpha"]'),
    "单节点详情没有完成渲染",
  );
  assert.equal(document.querySelector("[data-open-enrollment]"), null, "单节点详情不得渲染添加节点入口");
  assert.equal(document.querySelector("[data-node-batch-toggle]"), null, "单节点详情不得渲染批量操作入口");
  assert.equal(document.querySelector("#batch-form"), null, "单节点详情不得渲染批量操作表单");
  assert.equal(document.querySelector(".node-batch-bar"), null, "单节点详情不得保留批量操作栏");
  const commandButton = document.querySelector('[data-view-enrollment-command="alpha"]');
  assert.ok(commandButton, "有权限的单节点详情缺少查看已有部署命令入口");
  const commandReadsBefore = testAPI.calls.filter(
    (call) => call.method === "POST" && call.path === "/agents/alpha/enrollment-command",
  ).length;
  commandButton.click();
  const firstCommandDialog = await waitFor(
    () => document.querySelector(".deploy-command-modal"),
    "单节点详情无法打开已有部署命令",
  );
  const firstCommand = firstCommandDialog.querySelector("[data-command]").value;
  assert.match(firstCommand, /browser-test-enrollment/);
  firstCommandDialog.querySelector("[data-close]").click();
  commandButton.click();
  const secondCommandDialog = await waitFor(
    () => document.querySelector(".deploy-command-modal"),
    "单节点详情无法重复打开已有部署命令",
  );
  assert.equal(secondCommandDialog.querySelector("[data-command]").value, firstCommand);
  secondCommandDialog.querySelector("[data-close]").click();
  const commandReadsAfter = testAPI.calls.filter(
    (call) => call.method === "POST" && call.path === "/agents/alpha/enrollment-command",
  ).length;
  assert.equal(commandReadsAfter, commandReadsBefore + 2, "重复查看部署命令应只读取而不创建凭据");
  assert.equal(
    testAPI.calls.some((call) => call.method === "POST" && /\/enrollment-token$/.test(call.path)),
    false,
    "单节点详情不得调用创建 enrollment credential 的接口",
  );
  document.querySelector('[data-node-tab="cores"]').click();
  assert.equal(document.querySelector('[data-node-panel="cores"] [data-node-capabilities]'), null, "内核页不应包含 Agent 能力设置");
  document.querySelector('[data-node-tab="agent"]').click();
  assert.ok(document.querySelector('[data-node-panel="agent"] [data-node-capabilities]'), "能力设置应位于 Agent 页");
  const capability = () => document.querySelector('[data-engine-capability="xray"]');
  assert.equal(capability()?.getAttribute("role"), "switch", "能力使用语义化开关");
  assert.ok(capability() && !capability().checked && !capability().disabled, "默认关闭的 Xray 必须可以单独开启");
  capability().click();
  await waitFor(() => capability()?.checked && !capability()?.disabled && document.querySelector('[data-version-engine="xray"]'), "开启后应显示 Xray 内核管理卡片");
  testAPI.capabilityFailure = true;
  capability().click();
  await waitFor(() => capability()?.checked && !capability()?.disabled, "保存失败应恢复开关");
  testAPI.capabilityFailure = false;
  capability().click();
  await waitFor(() => !capability()?.checked && !capability()?.disabled && !document.querySelector('[data-version-engine="xray"]'), "关闭后应移除 Xray 管理入口但保留能力开关");
  const installedCapability = () => document.querySelector('[data-engine-capability="mihomo"]');
  const transition = () => testAPI.agents.find((item) => item.id === "alpha").capability_transitions?.mihomo;
  const completeTransition = async (success) => {
    const agent = testAPI.agents.find((item) => item.id === "alpha");
    transition().status = success ? "succeeded" : "failed";
    if (success) {
      agent.capabilities = agent.capabilities.filter((engine) => engine !== "mihomo");
      if (transition().enabled) agent.capabilities.push("mihomo");
      agent.runtime.mihomo.service_status = transition().enabled ? "running" : "inactive";
    }
    document.querySelector("[data-agent-refresh]").click();
    await waitFor(() => installedCapability() && !installedCapability().disabled && installedCapability().checked === agent.capabilities.includes("mihomo"), "启停完成后开关没有更新并解除等待");
  };
  installedCapability().click();
  await waitFor(() => transition()?.status === "pending" && installedCapability()?.checked && installedCapability()?.disabled, "停止任务未确认前不能提前关闭能力");
  await completeTransition(false);
  assert.equal(installedCapability().checked, true, "停止失败须保留能力");
  assert.match(document.querySelector("[data-node-capabilities]").textContent, /失败或取消/, "失败状态必须可见");
  installedCapability().click();
  await waitFor(() => transition()?.status === "pending" && installedCapability()?.disabled, "未排队重试停止");
  await completeTransition(true);
  assert.equal(installedCapability().checked, false, "停止成功才关闭能力");
  installedCapability().click();
  await waitFor(() => transition()?.enabled && transition()?.status === "pending" && !installedCapability()?.checked && installedCapability()?.disabled, "启动任务未确认前不能提前开启能力");
  await completeTransition(false);
  assert.equal(installedCapability().checked, false, "启动失败须保持关闭");
  installedCapability().click();
  await waitFor(() => transition()?.status === "pending" && installedCapability()?.disabled, "未排队重试启动");
  await completeTransition(true);
  assert.equal(installedCapability().checked, true, "启动成功恢复管理能力");
  for (const tab of ["cores", "metrics", "agent"]) {
    document.querySelector(`[data-node-tab="${tab}"]`).click();
    await waitFor(
      () => document.querySelector(`[data-node-panel="${tab}"]:not([hidden])`),
      `${tab} 标签页没有完成切换`,
    );
    assert.equal(document.querySelector("[data-open-enrollment]"), null, `${tab} 标签页不得渲染添加节点入口`);
    assert.equal(document.querySelector("#batch-form"), null, `${tab} 标签页不得渲染批量操作`);
    assert.equal(document.querySelector(".node-batch-bar"), null, `${tab} 标签页不得保留批量操作栏`);
  }

}
