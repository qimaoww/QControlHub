import { testBatchCoreUpdates } from "./agent-batch-core.mjs";
import { assert, delay, waitFor } from "./assertions.mjs";
export async function testAgentBatchActions({ testAPI, onlineAgent }) {
  document.querySelector(".enrollment-dialog [data-close]")?.click();
  const batchLauncher = document.querySelector("[data-node-batch-toggle]");
  batchLauncher.click();
  await waitFor(() => document.querySelector("#batch-form"), "顶栏批量操作没有打开选择模式");
  assert.ok(document.querySelector(".node-batch-bar"), "选择模式没有渲染底部操作栏");
  assert.equal(document.querySelectorAll("[data-node-batch-card] [data-batch-checkbox]").length, 4);
  let form = document.querySelector("#batch-form");
  let all = form.querySelector("[data-batch-select-all]");
  let inputs = [...form.querySelectorAll("[data-batch-checkbox]")];
  let [alpha, bravo, charlie, delta] = inputs;
  let count = form.querySelector("[data-batch-count]");
  let submit = form.querySelector('button[type="submit"]');
  const readCurrentBatchDOM = () => {
    form = document.querySelector("#batch-form");
    all = form.querySelector("[data-batch-select-all]");
    inputs = [...form.querySelectorAll("[data-batch-checkbox]")];
    [alpha, bravo, charlie, delta] = inputs;
    count = form.querySelector("[data-batch-count]");
    submit = form.querySelector('button[type="submit"]');
  };
  const replaceAgent = (agentID, update) => {
    testAPI.agents = testAPI.agents.map((agent) =>
      agent.id === agentID ? update(agent) : agent,
    );
  };
  const refreshAgents = async (predicate, message) => {
    document.querySelector("[data-agent-refresh]")?.click();
    await waitFor(predicate, message);
    await delay(50);
    if (document.querySelector("#batch-form") !== form) {
      readCurrentBatchDOM();
      await waitFor(predicate, message);
    }
  };
  assert.equal(charlie.disabled, true);
  assert.match(charlie.closest(".node-card-select").title, /离线/);
  assert.equal(delta.disabled, true);
  assert.match(delta.closest(".node-card-select").title, /旧版 Agent/);

  all.click();
  assert.equal(alpha.checked, true);
  assert.equal(bravo.checked, true);
  assert.equal(count.textContent, "已选择 2 个节点 · 当前可选 2 个");
  assert.equal(all.checked, true);
  assert.equal(all.indeterminate, false);
  assert.equal(all.getAttribute("aria-checked"), "true");
  bravo.click();
  assert.equal(all.checked, false);
  assert.equal(all.indeterminate, true);
  assert.equal(all.getAttribute("aria-checked"), "mixed");
  all.click();
  assert.equal(alpha.checked && bravo.checked, true);
  all.click();
  assert.equal(alpha.checked || bravo.checked, false, "取消全选未清空合格节点");
  all.click();

  testAPI.taskMode = "deferred";

  replaceAgent("alpha", (agent) => ({ ...agent, status: "offline" }));
  await refreshAgents(
    () => alpha.disabled && !alpha.checked,
    "刷新后没有撤销刚变离线的节点",
  );
  assert.equal(bravo.checked, true, "刷新不应清除仍合格节点的选择");
  assert.equal(count.textContent, "已选择 1 个节点 · 当前可选 1 个");
  assert.equal(all.checked, true);
  assert.equal(all.indeterminate, false);
  assert.equal(all.getAttribute("aria-checked"), "true");
  replaceAgent("alpha", () => onlineAgent("alpha"));
  await refreshAgents(() => !alpha.disabled, "恢复在线快照后节点仍不可选");

  bravo.click();
  alpha.click();
  form.requestSubmit(submit);
  let confirmDialog = await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "离线二次校验没有进入确认流程");
  replaceAgent("alpha", (agent) => ({ ...agent, status: "offline" }));
  await refreshAgents(
    () => alpha.disabled && !alpha.checked && /离线/.test(alpha.closest(".node-card-select").title),
    "刷新后没有立即撤销已离线节点的选择",
  );
  confirmDialog.querySelector("[data-confirm-accept]").click();
  await delay(30);
  assert.equal(testAPI.pendingTasks.length, 0, "确认后仍向已离线节点提交任务");

  replaceAgent("alpha", () => onlineAgent("alpha"));
  await refreshAgents(() => !alpha.disabled, "恢复在线快照后节点仍不可选");
  bravo.click();
  form.requestSubmit(submit);
  confirmDialog = await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "feature 二次校验没有进入确认流程");
  replaceAgent("bravo", (agent) => ({ ...agent, features: [] }));
  await refreshAgents(
    () => bravo.disabled && !bravo.checked && /旧版 Agent/.test(bravo.closest(".node-card-select").title),
    "刷新后没有立即撤销缺少升级 feature 的节点选择",
  );
  confirmDialog.querySelector("[data-confirm-accept]").click();
  await delay(30);
  assert.equal(testAPI.pendingTasks.length, 0, "确认后仍向缺少升级 feature 的节点提交任务");

  replaceAgent("bravo", () => onlineAgent("bravo"));
  await refreshAgents(() => !bravo.disabled, "恢复 feature 后节点仍不可选");
  form.elements.action.value = "restart";
  form.elements.action.dispatchEvent(new Event("change", { bubbles: true }));
  alpha.click();
  form.requestSubmit(submit);
  confirmDialog = await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "runtime 二次校验没有进入确认流程");
  replaceAgent("alpha", (agent) => ({
    ...agent,
    runtime: { ...agent.runtime, mihomo: { installed: false, service_status: "stopped" } },
  }));
  await refreshAgents(
    () => alpha.disabled && !alpha.checked && /未安装/.test(alpha.closest(".node-card-select").title),
    "刷新后没有立即撤销 runtime 不可用节点的选择",
  );
  confirmDialog.querySelector("[data-confirm-accept]").click();
  await delay(30);
  assert.equal(testAPI.pendingTasks.length, 0, "确认后仍向 runtime 不可用节点提交任务");

  replaceAgent("alpha", () => onlineAgent("alpha"));
  await refreshAgents(() => !alpha.disabled, "恢复 runtime 后节点仍不可选");
  form.elements.action.value = "upgrade-agent";
  form.elements.action.dispatchEvent(new Event("change", { bubbles: true }));
  all.click();
  form.requestSubmit(submit);
  form.requestSubmit(submit);
  confirmDialog = await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "批量提交没有进入确认流程");
  assert.equal(testAPI.pendingTasks.length, 0, "确认前不应提交任务");
  confirmDialog.querySelector("[data-confirm-accept]").click();
  await waitFor(() => testAPI.pendingTasks.length === 1, "首个串行任务未提交");
  assert.equal(testAPI.pendingTasks.length, 1, "确认防重入产生了重复任务");
  assert.equal(form.querySelector(".batch-progress").max, 2);
  assert.match(form.querySelector("[data-batch-result-summary]").textContent, /已处理 0\/2/);
  assert.equal(document.querySelector("[data-node-batch-toggle]").disabled, true);
  assert.equal(alpha.checked && bravo.checked, true, "busy 不应清除选中状态");
  assert.equal(alpha.disabled && bravo.disabled, true, "busy 应锁定节点选择控件");
  assert.equal(all.disabled, true, "busy 应锁定全选控件");
  assert.equal(form.querySelector("[data-batch-clear]").disabled, true, "busy 应锁定清空控件");
  assert.equal(submit.disabled, true, "busy 应锁定提交控件");
  assert.equal(form.elements.action.disabled, true, "busy 应锁定动作控件");
  assert.equal(form.elements.engine.disabled, true, "busy 应锁定内核控件");
  assert.equal(count.textContent, "已选择 2 个节点 · 当前可选 2 个");
  assert.equal(all.checked, true);
  assert.equal(all.indeterminate, false);
  assert.equal(all.getAttribute("aria-checked"), "true");

  testAPI.pendingTasks[0].ok({ id: "task-alpha" });
  await waitFor(() => testAPI.pendingTasks.length === 2, "第二个任务没有在首个完成后串行提交");
  assert.equal(testAPI.pendingTasks[1].payload.agent_id, "bravo");
  assert.match(form.querySelector("[data-batch-result-summary]").textContent, /已处理 1\/2/);
  testAPI.pendingTasks[1].fail("bravo temporary failure");
  await waitFor(() => form.dataset.busy !== "1", "部分失败后 busy 未恢复");
  let rows = [...form.querySelectorAll(".batch-result-row")];
  assert.equal(rows.length, 2);
  assert.equal(rows.filter((row) => row.classList.contains("ok")).length, 1);
  assert.equal(rows.filter((row) => row.classList.contains("error")).length, 1);
  assert.equal(form.querySelector("[data-batch-result-summary]").textContent, "1 个已提交 · 1 个失败");
  const succeededRow = form.querySelector(".batch-result-row.ok");
  assert.equal(getComputedStyle(succeededRow).display, "none", "部分失败应先展示失败节点");
  form.querySelector('[data-batch-result-filter="all"]').click();
  assert.notEqual(getComputedStyle(succeededRow).display, "none", "全部筛选没有恢复成功节点");
  form.querySelector('[data-batch-result-filter="error"]').click();
  const resultsToggle = form.querySelector("[data-batch-results-toggle]");
  resultsToggle.click();
  assert.equal(form.querySelector("[data-batch-result-details]").hidden, true);
  assert.equal(resultsToggle.getAttribute("aria-expanded"), "false");
  resultsToggle.click();
  assert.equal(form.querySelector("[data-batch-result-details]").hidden, false);

  const failureNotice = document.querySelector("[data-spa-notice]");
  assert.equal(failureNotice.getAttribute("role"), "alert");
  failureNotice.querySelector(".notice-close").click();
  assert.equal(document.querySelector("[data-spa-notice]"), null, "错误提示必须支持关闭");
  let retry = form.querySelector("[data-batch-retry]");
  assert.equal(retry.dataset.batchRetry, "bravo", "部分失败只应重试失败节点");
  retry.click();
  await waitFor(() => testAPI.pendingTasks.length === 3, "部分失败项重试未提交");
  testAPI.pendingTasks[2].ok({ id: "task-bravo-retry" });
  await waitFor(() => !form.querySelector("[data-batch-retry]"), "成功重试后仍残留重试入口");
  assert.equal(form.querySelector("[data-batch-result-summary]").textContent, "2 个已提交 · 0 个失败", "重试成功后汇总没有更新");
  assert.equal(form.querySelector("[data-batch-result-filters]").hidden, true, "没有失败项时应隐藏筛选");
  assert.notEqual(getComputedStyle(succeededRow).display, "none", "最后一次重试成功后应展示全部节点");
  assert.equal(document.querySelector("[data-spa-notice]").getAttribute("role"), "status");

  form.requestSubmit(submit);
  confirmDialog = await waitFor(() => document.querySelector("[data-confirm-dialog][open]"), "第二轮批量提交没有进入确认流程");
  confirmDialog.querySelector("[data-confirm-accept]").click();
  await waitFor(() => testAPI.pendingTasks.length === 4, "第二轮首个任务未提交");
  testAPI.pendingTasks[3].fail("alpha temporary failure");
  await waitFor(() => testAPI.pendingTasks.length === 5, "第二轮任务没有串行提交");
  testAPI.pendingTasks[4].fail("bravo temporary failure");
  await waitFor(() => form.dataset.busy !== "1", "全部失败后 busy 未恢复");
  rows = [...form.querySelectorAll(".batch-result-row")];
  assert.equal(rows.filter((row) => row.classList.contains("error")).length, 2);
  let retries = [...form.querySelectorAll("[data-batch-retry]")];
  assert.equal(retries.length, 2, "两个失败节点均应保留重试入口");
  const batchBar = form.querySelector(".node-batch-bar");
  const aggregateWorkspace = document.querySelector(".workspace-main");
  const aggregateHash = location.hash;
  aggregateWorkspace.scrollTop = 43;
  const aggregateScrollTop = aggregateWorkspace.scrollTop;
  const callsFor = (path) =>
    testAPI.calls.filter((call) => call.method === "GET" && call.path === path)
      .length;
  const agentPollsBefore = callsFor("/agents");
  const overviewCallsBefore = callsFor("/overview");
  const enrollmentCallsBefore = callsFor("/enrollment-tokens");
  const komariReadsBefore = callsFor("/agents/alpha/komari");
  const alphaCard = document.querySelector('[data-agent-metrics="alpha"]');
  const singBoxChip = alphaCard.querySelector(".service-sing-box");
  const singBoxService = singBoxChip.querySelector('[data-core-service="sing-box"]');
  const installedSummary = alphaCard.querySelector(
    "[data-core-installed-summary]",
  );
  assert.equal(singBoxChip.dataset.coreInstalled, "0");
  assert.equal(singBoxService.textContent, "未安装");
  assert.equal(
    singBoxService.closest(".engine-state").classList.contains("muted"),
    true,
  );
  assert.match(installedSummary.textContent, /1\/2 内核已安装/);
  replaceAgent("alpha", (agent) => ({
    ...agent,
    runtime: {
      ...agent.runtime,
      "sing-box": { installed: true, service_status: "active" },
    },
  }));
  await delay(6500);
  assert.ok(
    callsFor("/agents") >= agentPollsBefore + 3,
    "批量失败结果未经历三轮连续指标 poll",
  );
  assert.ok(
    callsFor("/agents") <= agentPollsBefore + 4,
    "连续指标 poll 请求失去边界",
  );
  assert.equal(
    callsFor("/overview"),
    overviewCallsBefore,
    "指标 poll 不应重复加载 overview",
  );
  assert.equal(
    callsFor("/enrollment-tokens"),
    enrollmentCallsBefore,
    "指标 poll 不应重复加载 enrollment history",
  );
  assert.equal(
    callsFor("/agents/alpha/komari"),
    komariReadsBefore,
    "重建节点卡片不应重复读取 Komari 月流量",
  );
  assert.equal(
    document.querySelector("#batch-form"),
    form,
    "聚合 core chip 的连续 poll 不应替换批量表单",
  );
  assert.equal(batchBar.isConnected, true, "连续 poll 不应关闭底部批量操作栏");
  assert.equal(location.hash, aggregateHash, "连续 poll 不应改变 route");
  assert.equal(
    aggregateWorkspace.scrollTop,
    aggregateScrollTop,
    "连续 poll 不应改变聚合页滚动位置",
  );
  assert.equal(
    form.querySelector("[data-batch-results]").hidden,
    false,
    "连续 poll 不应隐藏批量结果",
  );
  assert.equal(
    form.querySelectorAll(".batch-result-row.error").length,
    2,
    "连续 poll 后两个失败结果必须保留",
  );
  retries = [...form.querySelectorAll("[data-batch-retry]")];
  assert.equal(retries.length, 2, "连续 poll 后两个 retry 必须保留");
  assert.equal(count.textContent, "已选择 2 个节点 · 当前可选 2 个");
  assert.equal(all.checked, true);
  assert.equal(all.indeterminate, false);
  assert.equal(all.getAttribute("aria-checked"), "true");
  assert.equal(singBoxChip.dataset.coreInstalled, "1");
  assert.equal(singBoxService.textContent, "运行中");
  assert.equal(
    singBoxService.closest(".engine-state").classList.contains("ok"),
    true,
  );
  assert.match(installedSummary.textContent, /2\/2 内核已安装/);
  replaceAgent("alpha", (agent) => ({
    ...agent,
    runtime: {
      ...agent.runtime,
      "sing-box": { installed: false, service_status: "unknown" },
    },
  }));
  await waitFor(
    () => singBoxChip.dataset.coreInstalled === "0",
    "compact chip 没有原位同步卸载转换",
  );
  assert.equal(document.querySelector("#batch-form"), form);
  assert.equal(form.querySelectorAll(".batch-result-row.error").length, 2);
  assert.equal(form.querySelectorAll("[data-batch-retry]").length, 2);
  assert.equal(singBoxService.textContent, "未安装");
  assert.equal(
    singBoxService.closest(".engine-state").classList.contains("muted"),
    true,
  );
  assert.match(installedSummary.textContent, /1\/2 内核已安装/);
  assert.equal(batchBar.isConnected, true);
  assert.equal(location.hash, aggregateHash);
  assert.equal(aggregateWorkspace.scrollTop, aggregateScrollTop);
  assert.equal(count.textContent, "已选择 2 个节点 · 当前可选 2 个");
  assert.equal(all.checked, true);
  assert.equal(all.indeterminate, false);
  assert.equal(all.getAttribute("aria-checked"), "true");
  retries[0].click();
  retries[1].click();
  form.requestSubmit(submit);
  alpha.click();
  await waitFor(() => testAPI.pendingTasks.length === 6, "失败项重试未提交");
  await delay(30);
  assert.equal(testAPI.pendingTasks.length, 6, "共享 busy 未阻止并行 retry 或主提交");
  assert.equal(retries.every((button) => button.disabled), true, "retry busy 未锁定全部重试控件");
  assert.equal(alpha.disabled && bravo.disabled && all.disabled, true, "retry busy 未锁定选择控件");
  assert.equal(form.querySelector("[data-batch-clear]").disabled, true, "retry busy 未锁定清空控件");
  assert.equal(submit.disabled && form.elements.action.disabled && form.elements.engine.disabled, true, "retry busy 未锁定动作控件");
  assert.equal(count.textContent, "已选择 2 个节点 · 当前可选 2 个");
  assert.equal(all.checked, true);
  assert.equal(all.getAttribute("aria-checked"), "true");
  testAPI.pendingTasks[5].fail("alpha retry still failing");
  await waitFor(() => form.dataset.busy !== "1", "retry 失败后共享 busy 未释放");
  retries = [...form.querySelectorAll("[data-batch-retry]")];
  assert.equal(retries.every((button) => !button.disabled), true, "retry 失败后控件未恢复");

  retries[0].click();
  await waitFor(() => testAPI.pendingTasks.length === 7, "失败 retry 未允许再次重试");
  testAPI.pendingTasks[6].ok({ id: "task-alpha-retry" });
  await waitFor(() => form.querySelectorAll("[data-batch-retry]").length === 1, "成功 retry 后失败项未原位更新");

  const remainingRetry = form.querySelector("[data-batch-retry]");
  replaceAgent("bravo", (agent) => ({ ...agent, status: "offline" }));
  await refreshAgents(() => remainingRetry.disabled, "刷新后不合格 retry 未禁用");
  const pendingBeforeRetry = testAPI.pendingTasks.length;
  await remainingRetry.onclick();
  assert.equal(testAPI.pendingTasks.length, pendingBeforeRetry, "retry 实际 POST 前未使用最新离线快照 fail closed");

  await testBatchCoreUpdates({ testAPI });

}
