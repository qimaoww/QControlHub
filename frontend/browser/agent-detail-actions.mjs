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
  const alpha = testAPI.agents.find((item) => item.id === "alpha");
  const uninstallButton = () => document.querySelector('.service-mihomo [data-core-uninstall]');
  assert.ok(uninstallButton() && !uninstallButton().disabled, "已安装内核缺少可用的卸载按钮");
  assert.equal(document.querySelector('.service-sing-box [data-core-uninstall]'), null, "未安装内核不应提供卸载按钮");
  const uninstallTasksBefore = testAPI.submittedTasks.filter((task) => task.action === "uninstall").length;
  uninstallButton().click();
  let confirm = await waitFor(() => document.querySelector('[data-confirm-dialog][open]'), "卸载内核没有确认弹窗");
  assert.match(confirm.textContent, /停止.*删除.*保留/);
  confirm.querySelector('[data-confirm-cancel]').click();
  await waitFor(() => !uninstallButton().disabled, "取消卸载后按钮未恢复");
  assert.equal(testAPI.submittedTasks.filter((task) => task.action === "uninstall").length, uninstallTasksBefore, "取消卸载仍提交了任务");
  uninstallButton().click();
  confirm = await waitFor(() => document.querySelector('[data-confirm-dialog][open]'), "状态变化测试没有卸载确认弹窗");
  alpha.status = "offline";
  document.querySelector('[data-agent-refresh]').click();
  await waitFor(() => document.querySelector('[data-agent-status-label]')?.textContent === "离线", "确认期间离线状态未刷新");
  assert.equal(document.querySelector('.node-live-state [data-agent-presence]')?.dataset.agentPresence, "offline", "离线文字与胶囊视觉状态必须同步刷新");
  assert.equal(getComputedStyle(document.querySelector('[data-agent-status-dot]')).backgroundColor, "rgba(0, 0, 0, 0)", "离线指示点应为空心");
  confirm.querySelector('[data-confirm-accept]').click();
  await waitFor(() => uninstallButton()?.disabled, "离线后卸载按钮未禁用");
  assert.equal(testAPI.submittedTasks.filter((task) => task.action === "uninstall").length, uninstallTasksBefore, "确认期间节点离线仍提交卸载");
  alpha.status = "online";
  document.querySelector('[data-agent-refresh]').click();
  await waitFor(() => !uninstallButton().disabled, "节点上线后卸载按钮未恢复");
  assert.equal(document.querySelector('.node-live-state [data-agent-presence]')?.dataset.agentPresence, "online", "重新上线后胶囊应恢复在线样式");
  assert.equal(document.querySelector('[data-agent-status-label]')?.textContent, "在线", "重新上线后胶囊应恢复在线文字");
  uninstallButton().click();
  confirm = await waitFor(() => document.querySelector('[data-confirm-dialog][open]'), "再次卸载没有确认弹窗");
  confirm.querySelector('[data-confirm-accept]').click();
  await waitFor(() => testAPI.submittedTasks.filter((task) => task.action === "uninstall").length === uninstallTasksBefore + 1, "确认卸载未提交任务");
  assert.equal(JSON.stringify(testAPI.submittedTasks.filter((task) => task.action === "uninstall").at(-1)),
    JSON.stringify({ agent_id: "alpha", engine: "mihomo", action: "uninstall" }),
    "卸载任务参数不正确");
  const firstRequest = testAPI.pendingTasks.filter((task) => task.payload.action === "uninstall").at(-1);
  firstRequest.ok({ id: "uninstall-alpha-first" });
  await waitFor(() => testAPI.calls.some((call) => call.path === "/tasks/uninstall-alpha-first"), "卸载等待状态未开始跟踪任务");
  assert.equal(uninstallButton()?.disabled, true, "卸载任务等待期间按钮未禁用");
  uninstallButton().click();
  assert.equal(testAPI.submittedTasks.filter((task) => task.action === "uninstall").length, uninstallTasksBefore + 1, "等待期间重复提交卸载");
  testAPI.taskStates.set("uninstall-alpha-first", { id: "uninstall-alpha-first", status: "failed" });
  document.querySelector('[data-agent-refresh]').click();
  await waitFor(() => uninstallButton() && !uninstallButton().disabled, "卸载失败后按钮未恢复重试");
  uninstallButton().click();
  confirm = await waitFor(() => document.querySelector('[data-confirm-dialog][open]'), "卸载重试没有确认弹窗");
  confirm.querySelector('[data-confirm-accept]').click();
  await waitFor(() => testAPI.submittedTasks.filter((task) => task.action === "uninstall").length === uninstallTasksBefore + 2, "卸载失败后不能重试");
  const secondRequest = testAPI.pendingTasks.filter((task) => task.payload.action === "uninstall").at(-1);
  secondRequest.ok({ id: "uninstall-alpha-second" });
  await waitFor(() => testAPI.calls.some((call) => call.path === "/tasks/uninstall-alpha-second"), "重试卸载未开始跟踪任务");
  testAPI.taskStates.set("uninstall-alpha-second", { id: "uninstall-alpha-second", status: "succeeded" });
  alpha.runtime.mihomo.installed = false;
  alpha.runtime.mihomo.service_status = "inactive";
  document.querySelector('[data-agent-refresh]').click();
  await waitFor(() => document.querySelector('.service-mihomo[data-core-installed="0"]') && !uninstallButton(), "卸载完成后按钮未消失");
  alpha.runtime.mihomo.installed = true;
  alpha.runtime.mihomo.service_status = "running";
  document.querySelector('[data-agent-refresh]').click();
  await waitFor(() => uninstallButton() && !uninstallButton().disabled, "重新安装后卸载按钮未恢复");
  location.hash = "#settings-node-delta";
  await waitFor(() => document.querySelector('.node-operations-workspace[data-agent-node="delta"]'), "旧版 Agent 详情未渲染");
  assert.equal(document.querySelector('.service-mihomo [data-core-uninstall]')?.disabled, true, "旧版 Agent 卸载按钮应禁用");
  location.hash = "#settings-node-alpha";
  await waitFor(() => document.querySelector('.node-operations-workspace[data-agent-node="alpha"]'), "返回原节点失败");
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
  const inactiveCore = await waitFor(() => document.querySelector('.service-mihomo[data-capability-enabled="0"]'), "能力关闭后已安装内核管理卡片消失");
  assert.ok(inactiveCore.querySelector('[data-core-uninstall]') && !inactiveCore.querySelector('[data-core-uninstall]').disabled,
    "能力关闭后仍需允许卸载已安装内核");
  assert.equal(inactiveCore.querySelector('[data-open-version-form]').disabled, true, "能力关闭后仍可切换内核版本");
  assert.ok([...inactiveCore.querySelectorAll('[data-service-action]')].every((button) => button.disabled), "能力关闭后仍可执行服务操作");
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

export async function testWriteOnlyCoreUninstall({ testAPI }) {
  location.hash = "#settings-node-alpha";
  const button = await waitFor(
    () => document.querySelector('.service-mihomo [data-core-uninstall]:not(:disabled)'),
    "只有执行权的节点所有者不能卸载内核",
  );
  testAPI.taskMode = "deferred";
  button.click();
  const confirm = await waitFor(
    () => document.querySelector('[data-confirm-dialog][open]'),
    "卸载确认弹窗未出现",
  );
  confirm.querySelector('[data-confirm-accept]').click();
  const request = await waitFor(
    () => testAPI.pendingTasks.find((item) => item.payload.action === "uninstall"),
    "没有提交卸载任务",
  );
  assert.equal(button.disabled, true, "卸载请求尚未返回时按钮没有锁定");
  request.ok({ id: "uninstall-writeonly-task" });
  await waitFor(() => !button.disabled, "无法读取任务状态后卸载按钮没有恢复");
  document.querySelector('[data-agent-refresh]').click();
  await waitFor(() => testAPI.calls.filter((call) => call.path === "/agents").length > 1,
    "节点状态没有刷新");
  assert.equal(button.disabled, false, "刷新后卸载按钮再次卡在等待状态");
  assert.equal(testAPI.calls.some((call) => call.path === "/tasks/uninstall-writeonly-task"), false,
    "没有 tasks.read 权限时仍轮询受保护的任务详情");
}
